#!/usr/bin/env python3
"""Real independent BH/indexer acceptance with two miners and a late validator.

Run through run_usdb_upstream_fault_matrix.sh with MATRIX_SCENARIO=multi-miner.
Only seal computation is replaced by delayed fake PoW. Selector validation,
fork choice, execution, rewards, durable upstreams and P2P are real.
"""

import argparse
import hashlib
import json
from pathlib import Path
import re
import signal
import socket
import subprocess
import sys
import time

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "scripts/usdb"))
from upstream_fault_matrix import (Matrix, Node, RPCError, MINER, compare_chain, require,
                                   validate_auto_recovery)
from configure_usdb_anchor_max_age_genesis import configure_anchor_max_age
from verify_usdb_profile_e2e import decode_selector, SYSTEM_STATE_SLOTS, USDB_SYSTEM_STATE_ADDRESS
from common.rpc_delay import BlockDelayProxy


SECOND_MINER = "0x2222222222222222222222222222222222222222"
ANCHOR_MAX_AGE = 24
RETRY_BUDGET_SECONDS = {"downloader": 120, "fetcher": 1800}


def expiry_path(kind, cycle, mode):
    if cycle == 1 and mode != "none":
        if kind == "usdb-indexer":
            return "downloader"
        if kind == "balance-history" and mode == "all":
            return "fetcher"
    return None


def required_cases(cycles, retry_expiry="none"):
    cases = ["baseline"]
    for cycle in range(1, cycles + 1):
        for fault in ("balance-history-delay", "usdb-indexer-delay", "rpc-outage"):
            cases.append(f"{fault}-{cycle}")
            if expiry_path(fault.removesuffix("-delay"), cycle, retry_expiry):
                cases.append(f"{fault}-{cycle}-expiry")
            cases.append(f"{fault}-{cycle}-recovery")
        cases.extend([f"competing-miners-{cycle}", f"competing-delay-{cycle}",
                      f"competing-delay-{cycle}-recovery", f"competing-delay-{cycle}-fork-choice"])
    return cases + ["anchor-exhaustion", "invalid-block", "final-state"]


def expiry_log_evidence(text):
    exhausted = "External state wait budget exhausted"
    after_exhaustion = text.partition(exhausted)[2]
    return {"exhausted_sessions": text.count(exhausted),
            "new_session_after_exhaustion": "Chain validation waiting for external state" in after_exhaustion,
            "expired_block_numbers": sorted({int(n) for n in re.findall(
                r"Expired propagated block awaiting (?:validation|external state).*?number=(\d+)", text)})}


def validate_retry_expiry(evidence):
    """Require production expiry evidence and an unchanged, already existing tip."""
    path = evidence["path"]
    require(evidence["observed_seconds"] >= RETRY_BUDGET_SECONDS[path], "retry boundary was not crossed")
    require(evidence["target_hash_before"] == evidence["target_hash_after"], "healthy tip moved during expiry")
    require(evidence["validator_height"] < evidence["target_height"], "validator did not remain behind")
    require(evidence["profile_errors"] > 0, "missing actual failed validation")
    if path == "fetcher":
        require(evidence["validator_height"] == evidence["target_height"] - 1,
                "gossip expiry must exercise a single missing tip")
        require(evidence["target_height"] in evidence["expired_block_numbers"], "gossip tip did not expire")
    else:
        require(evidence["exhausted_sessions"] > 0, "missing actual session expiry")
        require(evidence["new_session_after_exhaustion"], "no automatic downloader session after expiry")


def validate_chain(blocks, expected_passes):
    """Independently check monotonic anchors and pass-to-beneficiary binding."""
    require(len(blocks) > 1, "no executed chain")
    last = None
    authors = set()
    for number, block in enumerate(blocks[1:], 1):
        require(block["number"] == hex(number) and block["parentHash"] == blocks[number - 1]["hash"],
                f"discontinuous canonical chain at {number}")
        selector = decode_selector(block)
        author = block["miner"].lower()
        require(expected_passes.get(author) == selector["pass_id"], f"wrong miner/pass at {number}")
        age = 0
        if last:
            require(selector["btc_height"] >= last["btc_height"], f"anchor regressed at {number}")
            if selector["btc_height"] == last["btc_height"]:
                require(selector["snapshot_id"] == last["snapshot_id"]
                        and selector["system_state_id"] == last["system_state_id"],
                        f"same-height identity changed at {number}")
                age = last["btc_anchor_age_blocks"] + 1
        require(selector["btc_anchor_age_blocks"] == age <= ANCHOR_MAX_AGE,
                f"invalid anchor age at {number}: {selector}")
        authors.add(author)
        last = selector
    return sorted(authors)


def common_head(nodes):
    """Sample a shared head without pinning an obsolete tip before convergence."""
    blocks = [node.block() for node in nodes]
    return blocks[0] if blocks and all(b["hash"] == blocks[0]["hash"] for b in blocks) else None


def validate_lag(evidence):
    require(evidence["held_reads"] > 0, "delay did not reach a real sync read")
    require(evidence["old_anchor"] < evidence["target_anchor"], "no new BTC state exercised")
    require(evidence["indexer_height"] == evidence["old_anchor"], "indexer did not remain behind")
    require(evidence["historical_query_succeeded"] and evidence["historical_profile_matches"],
            "committed prefix was unavailable or disagreed with the healthy peer")
    require(evidence["readiness"]["consensus_ready"] is False, "no global catch-up state exercised")
    if evidence["kind"] == "balance-history":
        require(evidence["balance_height"] == evidence["old_anchor"], "BH did not remain behind")
    else:
        require(evidence["balance_height"] == evidence["target_anchor"], "indexer delay also delayed BH")


class MiningNode(Node):
    def __init__(self, matrix, name, index):
        super().__init__(matrix, name, index)
        self.gates = {}
        self.beneficiary = SECOND_MINER if name == "b" else MINER
        self.ports.update(bh_gate=self.ports["bitcoin"] + 13, indexer_gate=self.ports["bitcoin"] + 14)

    def start_service(self, package, recovery_stage=None):
        # Gate only the sync worker's Core requests, not the geth/indexer RPC.
        # Thus all successful profile responses still come from the real store.
        if package not in self.gates:
            port = self.ports["bh_gate" if package == "balance-history" else "indexer_gate"]
            self.gates[package] = BlockDelayProxy(port, self.url("bitcoin"),
                                                 self.matrix.args.output_dir / f"{self.name}-{package}-delay.jsonl")
            replacement = f"http://127.0.0.1:{port}"
            if package == "balance-history":
                path = self.root / package / "config.toml"
                path.write_text(path.read_text().replace(self.url("bitcoin"), replacement))
            else:
                path = self.root / package / "config.json"
                config = json.loads(path.read_text())
                config["bitcoin"]["rpc_url"] = replacement
                self.matrix.write_json(path, config)
        super().start_service(package, recovery_stage)

    def start(self, service, command, *, env=None):
        if service == "geth":
            command = list(command)
            command[command.index("--miner.etherbase") + 1] = self.beneficiary
            command[command.index("--http.api") + 1] += ",debug"
            command.extend(["--fakepow", "--fakepow.delay", "1200ms", "--verbosity", "4"])
        super().start(service, command, env=env)


class MultiMinerMatrix(Matrix):
    def __init__(self, args):
        super().__init__(args)
        self.a, self.b, self.c = (MiningNode(self, name, i) for i, name in enumerate("abc"))
        self.nodes = [self.a, self.b, self.c]
        for node in self.nodes:
            for name in ("bh_gate", "indexer_gate"):
                with socket.socket() as probe:
                    probe.bind(("127.0.0.1", node.ports[name]))
        script = self.a.rpc("bitcoin")("validateaddress", [args.second_owner])["scriptPubKey"]
        self.owners.append(hashlib.sha256(bytes.fromhex(script)).digest()[::-1].hex())
        self.expected_passes = {MINER: args.pass_id, SECOND_MINER: args.second_pass}
        self.identities = {}
        self.expected_bad_blocks = {n.name: set() for n in self.nodes}
        self.connected = []
        self.report.update(schema="usdb-multi-miner-delay:v1", seal_mode="fakepow-delay-1200ms",
                           cycles=args.cycles, retry_expiry=args.retry_expiry,
                           retry_budgets_seconds=RETRY_BUDGET_SECONDS,
                           topology=[{"node": n.name, "root": str(n.root),
                           "ports": n.ports, "miner": n.beneficiary if n.name != "c" else None} for n in self.nodes])

    def connect_once(self, node):
        require(node.name not in self.connected, "manual peer reconnection forbidden")
        self.connect_geth(node)
        self.wait(f"{node.name} initial peer", lambda: len(node.rpc("geth")("admin_peers")) == 1)
        self.connected.append(node.name)
        node.pinned_geth = node.processes["geth"]
        self.identities[node.name] = node.validator_identity()

    def log_text(self, node):
        return (self.args.output_dir / f"{node.name}-geth.log").read_text()

    def continuity(self):
        for node in self.nodes:
            if node.pinned_geth:
                require(node.validator_identity() == self.identities[node.name], "geth process continuity lost")
            if node.name in self.connected:
                require(len(node.rpc("geth")("admin_peers")) == 1, f"{node.name}: healthy peer lost")
                require("Removing p2p peer" not in self.log_text(node), f"{node.name}: peer disconnected during faults")
            bad = node.rpc("geth")("debug_getBadBlocks") or []
            require({b["hash"] for b in bad} == self.expected_bad_blocks[node.name],
                    f"{node.name}: dependency delay polluted bad-block records")

    def advance_btc(self, count=2):
        self.mine_btc(count)
        for node in self.nodes:
            self.wait_btc(node)

    def audit_chain(self, nodes):
        target = {}

        def converged():
            head = common_head([self.a, *nodes])
            if head:
                target.update(head)
                return True
            return False

        self.wait("executed canonical convergence", converged, seconds=90)
        height = int(target["number"], 16)
        expected = [self.a.block(i) for i in range(height + 1)]
        authors = validate_chain(expected, self.expected_passes)
        for node in nodes:
            actual = [node.block(i) for i in range(height + 1)]
            compare_chain(expected, actual)
            for block in expected:
                tag = block["number"]
                for slot in SYSTEM_STATE_SLOTS.values():
                    query = [USDB_SYSTEM_STATE_ADDRESS, slot, tag]
                    require(self.a.rpc("geth")("eth_getStorageAt", query) == node.rpc("geth")("eth_getStorageAt", query),
                            f"{node.name}: system execution state mismatch at {tag}")
                for author in self.expected_passes:
                    query = [author, tag]
                    require(self.a.rpc("geth")("eth_getBalance", query) == node.rpc("geth")("eth_getBalance", query),
                            f"{node.name}: payout mismatch at {tag}/{author}")
        for author in authors:
            require(int(self.a.rpc("geth")("eth_getBalance", [author, "latest"]), 16) > 0, "mined beneficiary received no rewards")
        self.write_json(self.args.output_dir / "canonical-blocks.json", expected)
        self.continuity()
        return {"height": height, "hash": target["hash"], "state_root": target["stateRoot"], "authors": authors}

    def mine(self, node, count):
        initial = node.height()
        log_offset = len(self.log_text(node))
        node.rpc("geth")("miner_start", [1])
        try:
            if count == 1:
                # In the delayed fake-PoW fixture, stop new work while the
                # first seal is still in flight. Stopping after its import can
                # already leave a second task running and break the lone-tip case.
                self.wait(f"miner {node.name}: single seal submitted", lambda:
                          "Commit new sealing work" in self.log_text(node)[log_offset:], interval=0.05)
            else:
                self.wait(f"miner {node.name}: {count} new blocks", lambda: node.height() >= initial + count, interval=0.05)
        finally:
            node.rpc("geth")("miner_stop")
        if count == 1:
            self.wait(f"miner {node.name}: single seal imported", lambda: node.height() >= initial + 1, interval=0.05)
        # Stopping mining does not cancel an already submitted seal. Drain it
        # before another miner starts or a test records the stationary parent,
        # otherwise a valid old-anchor block can race the unavailable tip.
        stopped = time.monotonic()
        self.wait(f"miner {node.name}: in-flight seal drains", lambda: time.monotonic() - stopped >= 4, seconds=8)
        if count == 1:
            require(node.height() == initial + 1, "single-block fixture produced extra sealing work")

    def baseline(self):
        self.phase("baseline")
        self.wait_upstream(self.a)
        genesis = json.loads(subprocess.check_output([str(self.args.geth), "dumpgenesis", "--usdb"], timeout=30))
        self.write_json(self.genesis, configure_anchor_max_age(genesis, ANCHOR_MAX_AGE))
        self.command(["python3", str(self.args.usdb_repo / "tests/common/miner_pass_regtest.py"),
                      "configure-genesis", str(self.genesis)], "configure-v2-genesis.log")
        for node in (self.b, self.c):
            node.fresh_upstream()
        states = [self.state(n) for n in self.nodes]
        require(all(self.difference(states[0], s) is None for s in states[1:]), "independent upstreams disagree at baseline")
        for pass_id in self.expected_passes.values():
            profile = states[0]["passes"][pass_id]["profile"]["pass"]
            require(profile["state"] == "active" and int(profile["effective_energy"]) > 0,
                    f"mining fixture must have an active, funded pass: {pass_id}")
        for node in self.nodes:
            node.init_geth()
            node.start_geth()
        self.a.pinned_geth = self.a.processes["geth"]
        self.identities["a"] = self.a.validator_identity()
        self.connect_once(self.b)
        self.mine(self.a, 3)
        self.audit_chain([self.b])
        self.mine(self.b, 3)
        self.wait("A receives B's blocks", lambda: self.a.block()["hash"] == self.b.block()["hash"])
        chain = self.audit_chain([self.b])
        require(chain["authors"] == sorted(self.expected_passes), "both distinct miners must mine canonical blocks")
        self.passed("baseline", **chain, independent_databases=True, pass_ids=self.expected_passes,
                    upstream_sha256=self.digest(states[0]))

    def hold_sync(self, node, kind):
        old = node.rpc("usdb-indexer")("get_synced_block_height")
        # Select already-known unstable blocks before advancing the stable tip,
        # so there is no race between mining and installing the gate.
        hashes = [node.rpc("bitcoin")("getblockhash", [h]) for h in range(old + 1, old + 3)]
        gate = node.gates[kind]
        gate.hold(hashes)
        self.advance_btc()
        self.wait_upstream(self.a)
        self.wait(f"{node.name}/{kind} real sync read held", lambda: bool(gate.events), seconds=35)
        if kind == "usdb-indexer":
            self.wait(f"{node.name} BH independently catches up", lambda:
                      node.rpc("balance-history")("get_snapshot_info")["stable_height"] == old + 2)
        # The sync read and the downstream readiness sampler run independently.
        # Wait for the actual observation rather than asserting on a cached
        # readiness value from just before the gate was entered.
        self.wait(f"{node.name} observes global catch-up", lambda:
                  node.rpc("usdb-indexer")("get_readiness")["consensus_ready"] is False, seconds=20)
        query = {"view_version": "uip-0006-usdb-economic-state-view:v1", "pass_id": self.args.pass_id, "block_height": old}
        profile = node.rpc("usdb-indexer")("get_pass_economic_profile", [query])
        require(profile["pass"]["state"] == "active", "historical pass unexpectedly inactive")
        expected_profile = self.a.rpc("usdb-indexer")("get_pass_economic_profile", [query])
        evidence = {"kind": kind, "old_anchor": old, "target_anchor": old + 2,
                    "held_reads": len(gate.events), "historical_query_succeeded": True,
                    "historical_profile_matches": profile == expected_profile,
                    "balance_height": node.rpc("balance-history")("get_snapshot_info")["stable_height"],
                    "indexer_height": node.rpc("usdb-indexer")("get_synced_block_height"),
                    "readiness": node.rpc("usdb-indexer")("get_readiness")}
        validate_lag(evidence)
        return evidence

    def recover(self, node, fault, failures, restore, stalled_height, frozen_target=None):
        target = frozen_target or self.a.block()
        recovery = {"validator_before": node.validator_identity(), "stalled_height": stalled_height,
                    "target_hash": target["hash"], "target_anchor": decode_selector(target)["btc_height"],
                    "target_height": int(target["number"], 16), "budget_seconds": 90,
                    "failed_profile_attempts": len(failures)}
        name = fault + "-recovery"
        self.phase(name)
        started = time.monotonic()
        restore()
        # No peer RPC, miner_start, or geth restart may appear between restore
        # and the target import. Only the failed upstream is repaired.
        try:
            self.wait(f"{node.name} automatic import after {fault}", lambda:
                      (node.block(recovery["target_height"]) or {}).get("hash") == target["hash"], seconds=90)
        except ValueError:
            # Preserve dependency readiness and the quiet chain's head on
            # failure, so a lost sync trigger is distinguishable from slow BH.
            observation = {}
            for service, method in (("balance-history", "get_snapshot_info"),
                                    ("usdb-indexer", "get_readiness"), ("geth", "eth_blockNumber"),
                                    ("geth", "eth_syncing"), ("geth", "admin_peers")):
                try:
                    observation[method] = node.rpc(service)(method)
                except (OSError, ValueError) as error:
                    observation[method] = {"error": str(error)}
            self.report["recovery_failure"] = {**recovery, "observation": observation,
                                               "elapsed_seconds": round(time.monotonic() - started, 3)}
            self.write_json(self.args.output_dir / f"{name}-failure.json", self.report["recovery_failure"])
            raise
        recovery.update(validator_after=node.validator_identity(), blocks=node.height(), head_hash=node.block()["hash"],
                        canonical_target_hash=node.block(recovery["target_height"])["hash"],
                        elapsed_seconds=round(time.monotonic() - started, 3))
        recovery["retried_profiles"] = validate_auto_recovery(recovery, failures, node.proxy.profile_calls(name))
        if frozen_target:
            require(self.a.block()["hash"] == target["hash"], "healthy tip moved during recovery")
            require(node.block()["hash"] == target["hash"], "recovery did not reach the frozen tip")
            recovery.update(healthy_tip_unchanged=True, mining_during_recovery=False)
        self.wait_upstream(node)
        chain = self.audit_chain([n for n in (self.b, self.c) if n.name in self.connected])
        self.passed(name, **recovery, canonical=chain, peer_disconnects=0, manual_reconnections=0)

    def wait_retry_expiry(self, node, kind, name, log_offset, started):
        path = expiry_path(kind, 1, self.args.retry_expiry)
        # miner_stop may leave an in-flight seal. Freeze only after it drains,
        # then prohibit any new blocks throughout expiry and recovery.
        stopped = time.monotonic()
        self.wait("last seal drains before freezing expiry target", lambda: time.monotonic() - stopped >= 4, seconds=8)
        target = self.a.block()
        target_height = int(target["number"], 16)
        budget = RETRY_BUDGET_SECONDS[path]
        evidence = {}

        def expired():
            self.continuity()
            require(self.a.block()["hash"] == target["hash"], "healthy miner advanced during retry expiry")
            evidence.update(expiry_log_evidence(self.log_text(node)[log_offset:]))
            boundary_crossed = (target_height in evidence["expired_block_numbers"] if path == "fetcher"
                                else evidence["new_session_after_exhaustion"])
            return boundary_crossed and time.monotonic() - started >= budget

        # Keep the original fault phase for RPC audits; record expiry separately
        # without discarding the selectors that failed before the first timeout.
        self.report["active_case"] = {"name": name + "-expiry", "budget_seconds": budget}
        self.write_json(self.args.output_dir / "summary.json", self.report)
        self.wait(f"{node.name} {path} production retry expiry ({budget}s)", expired, seconds=budget + 60, interval=1)
        evidence.update(path=path, observed_seconds=round(time.monotonic() - started, 3),
                        target_height=target_height, target_hash_before=target["hash"],
                        target_hash_after=self.a.block()["hash"], validator_height=node.height(),
                        profile_errors=len(node.proxy.profile_calls(name, errors=True)))
        validate_retry_expiry(evidence)
        self.passed(name + "-expiry", **evidence)
        return target

    def delay_case(self, node, kind, cycle, *, late_join=False):
        name = f"{kind}-delay-{cycle}"
        self.phase(name)
        log_offset = len(self.log_text(node))
        before = node.height()
        evidence = self.hold_sync(node, kind)
        if kind == "balance-history":
            # While globally catching up, the lagging miner must still build on
            # its safe committed prefix. A healthy peer validates those blocks.
            self.mine(node, 2)
            self.wait("healthy peer validates lagging miner", lambda: self.a.block()["hash"] == node.block()["hash"])
            require(decode_selector(node.block())["btc_height"] == evidence["old_anchor"], "lagging miner used unavailable state")
            evidence["lagging_miner_blocks"] = node.height() - before
            before = node.height()
        fault_started = time.monotonic()
        # A lone tip is the hard expiry case: its advertised parent may already
        # equal the validator's head, so a higher-TD sync cannot mask lost gossip.
        single_tip = expiry_path(kind, cycle, self.args.retry_expiry) == "fetcher"
        self.mine(self.a, 1 if single_tip else 6)
        if single_tip:
            require(self.a.height() == before + 1, "expected exactly one unavailable gossip tip")
        if late_join:
            self.connect_once(node)
        self.wait(f"{node.name} rejects unavailable selector", lambda: bool(node.proxy.profile_calls(name, errors=True)), seconds=30)
        failures = node.proxy.profile_calls(name, errors=True)
        require(any(e["params"][0]["block_height"] == evidence["target_anchor"] for e in failures), "only cached anchors exercised")
        if not late_join:
            require(node.height() == before, "lagging validator imported unavailable state")
        else:
            require(node.height() < self.a.height(), "late validator did not wait")
        expected = "Chain validation waiting for external state" if late_join else "Propagated block waiting for external state"
        # Parallel header checks can publish the RPC audit event before the
        # downloader has consumed the batch result and entered its retry loop.
        self.wait(f"{node.name} {'downloader' if late_join else 'gossip'} enters retry", lambda:
                  expected in self.log_text(node)[log_offset:], seconds=15)
        self.passed(name, **evidence, validator_before=before, validator_after=node.height(), healthy_after=self.a.height(),
                    profile_errors=len(failures), path="downloader" if late_join else "gossip")
        target = None
        if expiry_path(kind, cycle, self.args.retry_expiry):
            target = self.wait_retry_expiry(node, kind, name, log_offset, fault_started)
            failures = node.proxy.profile_calls(name, errors=True)
        self.recover(node, name, failures, node.gates[kind].restore, node.height(), target)

    def transport_case(self, cycle):
        name = f"rpc-outage-{cycle}"
        self.phase(name)
        self.advance_btc()
        for node in self.nodes:
            self.wait_upstream(node)
        before = self.b.height()
        require(self.b.stop("usdb-indexer", crash=True) == -signal.SIGKILL, "indexer outage not injected")
        self.mine(self.a, 5)
        self.wait("transport failure observed by validator", lambda: any(
            e.get("transport_closed") for e in self.b.proxy.profile_calls(name, errors=True)), seconds=30)
        failures = self.b.proxy.profile_calls(name, errors=True)
        require(self.b.height() == before, "unavailable validator advanced")
        self.passed(name, validator_height=before, healthy_height=self.a.height(), profile_errors=len(failures))
        self.recover(self.b, name, failures, self.b.start_indexer, before)

    def competing_miners(self, cycle):
        name = f"competing-miners-{cycle}"
        self.phase(name)
        self.advance_btc()
        for node in self.nodes:
            self.wait_upstream(node)
        before = self.a.height()
        offsets = {n.name: len(self.log_text(n)) for n in (self.a, self.b)}
        for node in (self.a, self.b):
            node.rpc("geth")("miner_start", [1])
        try:
            self.wait("both active miners produce blocks", lambda: self.a.height() >= before + 6 and all(
                "Successfully sealed new block" in self.log_text(n)[offsets[n.name]:] for n in (self.a, self.b)), seconds=40)
        finally:
            for node in (self.a, self.b):
                node.rpc("geth")("miner_stop")
        # Equal-work tips can legitimately remain split until another block.
        # Produce a tie breaker through normal mining, never force a head.
        self.mine(self.a, 2)
        self.wait("competing branches converge", lambda: self.a.block()["hash"] == self.b.block()["hash"], seconds=45)
        chain = self.audit_chain([self.b, self.c])
        self.passed(name, **chain, produced_by_both=True, blocks=chain["height"] - before,
                    local_seals={n.name: self.log_text(n)[offsets[n.name]:].count("Successfully sealed new block") for n in (self.a, self.b)})

    def competing_delay(self, cycle):
        name = f"competing-delay-{cycle}"
        self.phase(name)
        before = self.a.height()
        evidence = self.hold_sync(self.b, "balance-history")
        self.a.rpc("geth")("miner_start", [1])
        # Establish an unavailable new-anchor branch before B starts sealing.
        # Different head hashes alone would also match a harmless ancestor/tip
        # pair; that would not exercise reorganization on recovery.
        self.wait("healthy miner establishes new-anchor branch", lambda:
                  self.a.height() >= before + 3
                  and decode_selector(self.a.block())["btc_height"] == evidence["target_anchor"], seconds=20)
        self.b.rpc("geth")("miner_start", [1])
        try:
            self.wait("different-anchor branches from both miners", lambda:
                      self.a.height() >= before + 4 and self.b.height() >= before + 2
                      and (self.a.block(self.b.height()) or {}).get("hash") != self.b.block()["hash"]
                      and bool(self.b.proxy.profile_calls(name, errors=True)), seconds=35)
        finally:
            self.b.rpc("geth")("miner_stop")
        try:
            # Give the healthy branch strictly more work before restoring B;
            # this avoids assuming how equal-work forks break ties.
            self.wait("healthy branch gains a recovery target", lambda: self.a.height() >= self.b.height() + 4, seconds=20)
        finally:
            self.a.rpc("geth")("miner_stop")
        branch = [self.b.block(h) for h in range(before + 1, self.b.height() + 1)]
        require(all(decode_selector(block)["btc_height"] == evidence["old_anchor"] for block in branch),
                "lagging miner escaped the committed anchor")
        require(int(self.a.block()["totalDifficulty"], 16) > int(self.b.block()["totalDifficulty"], 16),
                "healthy recovery target does not have greater total difficulty")
        require(self.a.block(self.b.height())["hash"] != self.b.block()["hash"],
                "competing fixture produced a common prefix instead of competing branches")
        self.write_json(self.args.output_dir / f"{name}-lagging-branch.json", branch)
        failures = self.b.proxy.profile_calls(name, errors=True)
        self.passed(name, **evidence, competing_blocks=len(branch), lagging_head=self.b.block()["hash"],
                    healthy_head=self.a.block()["hash"], profile_errors=len(failures))
        self.recover(self.b, name, failures, self.b.gates["balance-history"].restore, self.b.height())
        orphans = [b["hash"] for b in branch if self.b.block(int(b["number"], 16))["hash"] != b["hash"]]
        require(orphans, "competing branch never underwent canonical reorganization")
        self.passed(name + "-fork-choice", replaced_blocks=len(orphans), orphan_hashes=orphans)

    def anchor_exhaustion(self):
        self.phase("anchor-exhaustion")
        self.advance_btc()
        for node in self.nodes:
            self.wait_upstream(node)
        before = self.a.height()
        self.a.rpc("geth")("miner_start", [1])
        try:
            self.wait("anchor reuse reaches its bound", lambda:
                      decode_selector(self.a.block())["btc_anchor_age_blocks"] == ANCHOR_MAX_AGE, seconds=65)
            exhausted = self.a.block()
            started = time.monotonic()
            self.wait("exhausted anchor stays stopped", lambda: time.monotonic() - started >= 4, seconds=8)
            require(self.a.block()["hash"] == exhausted["hash"], "miner exceeded anchor reuse bound")
            self.advance_btc(1)
            self.wait("active miner automatically resumes on a new anchor", lambda: self.a.height() > int(exhausted["number"], 16), seconds=45)
        finally:
            self.a.rpc("geth")("miner_stop")
        chain = self.audit_chain([self.b, self.c])
        resumed = self.a.block(int(exhausted["number"], 16) + 1)
        require(decode_selector(resumed)["btc_anchor_age_blocks"] == 0, "new anchor did not reset age")
        self.passed("anchor-exhaustion", **chain, before=before, exhausted_height=int(exhausted["number"], 16),
                    max_age=ANCHOR_MAX_AGE, miner_restarted=False)

    def invalid_block(self):
        self.phase("invalid-block")
        target = self.a.block()
        raw = self.a.rpc("geth")("debug_getBlockRlp", [int(target["number"], 16)])
        source, invalid = self.args.work_dir / "block.hex", self.args.work_dir / "invalid-block.rlp"
        source.write_text(raw)
        fixture = json.loads(subprocess.check_output([str(self.args.invalidblock), "--input", str(source),
                                                      "--output", str(invalid)], timeout=10))
        try:
            self.c.rpc("geth")("admin_importChain", [str(invalid)])
        except RPCError as error:
            require("invalid gasUsed" in error.message, f"unexpected invalid-block rejection: {error}")
        else:
            raise ValueError("invalid gas header was accepted")
        require(self.c.block()["hash"] == target["hash"], "invalid block changed the canonical head")
        self.expected_bad_blocks["c"].add(fixture["hash"])
        self.continuity()
        self.mine(self.a, 2)
        self.passed("invalid-block", rejected_hash=fixture["hash"], rejection="invalid gasUsed",
                    **self.audit_chain([self.b, self.c]))

    def run(self):
        self.baseline()
        for cycle in range(1, self.args.cycles + 1):
            self.delay_case(self.b, "balance-history", cycle)
            self.delay_case(self.c, "usdb-indexer", cycle, late_join=cycle == 1)
            self.transport_case(cycle)
            self.competing_miners(cycle)
            self.competing_delay(cycle)
        self.anchor_exhaustion()
        self.invalid_block()
        self.phase("final-state")
        states = [self.state(n) for n in self.nodes]
        require(all(self.difference(states[0], s) is None for s in states[1:]), "independent stores disagree after recovery")
        for node, state in zip(self.nodes, states):
            self.write_json(self.args.output_dir / f"final-{node.name}-state.json", state)
        self.passed("final-state", upstream_sha256=self.digest(states[0]), identities=self.identities,
                    **self.audit_chain([self.b, self.c]))
        require([case["name"] for case in self.report["cases"]] == required_cases(self.args.cycles, self.args.retry_expiry),
                "multi-miner acceptance coverage incomplete")

    def close(self):
        # Release held HTTP workers before signalling services. No suspended
        # process or listener survives a failed assertion or the outer timeout.
        for node in self.nodes:
            for gate in node.gates.values():
                gate.restore()
        try:
            super().close()
        finally:
            for node in self.nodes:
                for gate in node.gates.values():
                    gate.close()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("work-dir", "output-dir", "usdb-repo", "geth", "bitcoin", "ord", "balance-history", "indexer", "invalidblock"):
        parser.add_argument("--" + name, type=Path, required=True)
    for name in ("miner-address", "owner-address", "pass-id", "second-owner", "second-pass"):
        parser.add_argument("--" + name, required=True)
    parser.add_argument("--port-base", type=int, default=22400)
    parser.add_argument("--timeout-sec", type=int, default=900)
    parser.add_argument("--cycles", type=int, choices=range(1, 6), default=1)
    parser.add_argument("--retry-expiry", choices=("none", "downloader", "all"), default="none",
                        help="Cross actual 2-minute downloader and optionally 30-minute fetcher limits once")
    args = parser.parse_args()
    matrix = MultiMinerMatrix(args)

    def interrupted(signum, _frame):
        raise RuntimeError(f"multi-miner matrix interrupted by signal {signum}")

    for signum in (signal.SIGINT, signal.SIGTERM):
        signal.signal(signum, interrupted)
    try:
        matrix.run()
        matrix.report["status"] = "ok"
    except BaseException as error:
        matrix.report.update(status="failed", error=str(error))
        raise
    finally:
        try:
            matrix.close()
        except BaseException as error:
            matrix.report.update(status="failed", cleanup_error=str(error))
            raise
        finally:
            matrix.report["elapsed_seconds"] = round(time.monotonic() - matrix.started, 2)
            matrix.write_json(args.output_dir / "summary.json", matrix.report)


if __name__ == "__main__":
    main()
