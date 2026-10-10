#!/usr/bin/env python3
"""P0 process recovery against independent real Core/BH/indexer/geth databases.

Run with MATRIX_SCENARIO=restart via run_usdb_upstream_fault_matrix.sh.
This models process restarts and a controlled temporary Core tip regression;
it does not simulate power loss, fsync failure or loadtxoutset dual chainstates.
"""

import hashlib
import hmac
import json
from pathlib import Path
import signal
import shutil
import subprocess
import sys
import time

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "scripts/usdb"))
from upstream_fault_matrix import Matrix, Node, RPC, configure_genesis, main, require
from verify_usdb_profile_e2e import decode_selector


P0_CASES = ("group-restart", "core-target-regression", "service-crashes", "downstream-first")


class RestartNode(Node):
    """Give optional Ord stable test credentials; BH/indexer still use rotating cookies."""

    def start(self, service, command, *, env=None):
        command = list(command)
        if service == "bitcoin" and not any(a.startswith("-rpcauth=") for a in command):
            digest = hmac.new(b"restart-fixture-salt", b"restart-fixture-password", "sha256").hexdigest()
            command.append("-rpcauth=restart-fixture:restart-fixture-salt$" + digest)
        if service == "ord":
            # CI pins Ord 0.23.3, whose client caches the cookie for its lifetime.
            # Auth rotation is a separate optional-service test, not the P0 Core/BH recovery contract.
            cookie = self.root / "bitcoin/ord-rpc.cookie"
            cookie.write_text("restart-fixture:restart-fixture-password")
            cookie.chmod(0o600)
            command[command.index("--cookie-file") + 1] = str(cookie)
        super().start(service, command, env=env)
        self.matrix.record("process-start", service=service, node=self.name, pid=self.processes[service].pid)

    def stop(self, service, *, crash=False):
        pid = self.processes[service].pid
        result = super().stop(service, crash=crash)
        self.matrix.record("process-stop", service=service, node=self.name, pid=pid,
                           requested_signal="SIGKILL" if crash else "SIGTERM", exit_code=result)
        return result


class RestartMatrix(Matrix):
    """Keep the healthy miner as an independent state and execution oracle."""

    def phase(self, name):
        super().phase(name)
        self.record("phase", phase=name)

    def record(self, kind, **fields):
        event = {"elapsed_seconds": round(time.monotonic() - self.started, 3), "event": kind, **fields}
        with (self.args.output_dir / "timeline.jsonl").open("a") as stream:
            stream.write(json.dumps(event) + "\n")

    def identities(self):
        return {name: {"pid": process.pid, "start_ticks": Path(f"/proc/{process.pid}/stat").read_text(
        ).rsplit(")", 1)[1].split()[19]} for name, process in self.b.processes.items()}

    def guard(self, expected=0):
        """Exercise the shipped guard with actual indexer readiness, not a fake epoch."""
        path = Path(__file__).resolve().parents[1] / "scripts/usdb/docker/usdb_deep_reorg_guard.py"
        result = subprocess.run([sys.executable, str(path), "check", "--state-dir", str(self.guard_dir),
                                 "--indexer-rpc-url", self.b.url("usdb-indexer"),
                                 "--chain-rpc-url", self.b.url("geth"), "--request-timeout-secs", "2"],
                                capture_output=True, text=True, timeout=10)
        with (self.args.output_dir / "guard.log").open("a") as log:
            log.write(f"phase={self.report.get('active_case')} exit={result.returncode}\n")
            log.write(result.stdout + result.stderr)
        require(result.returncode == expected, f"guard exit {result.returncode}, expected {expected}: {result.stderr}")
        require(not (self.guard_dir / "halted.json").exists(), "normal restart created a durable incident")
        return result.returncode

    def checkpoint(self):
        height, block_hash = self.frontier(self.b)
        result = {"height": height, "block_hash": block_hash,
                "state": self.state(self.b, height),
                "bh_commit": self.b.rpc("balance-history")("get_readiness")["latest_block_commit"],
                "epoch": self.b.rpc("usdb-indexer")("get_readiness")["upstream_reorg_epoch"],
                "identities": self.identities(),
                "config_hashes": self.config_hashes()}
        self.write_json(self.args.output_dir / f"{self.report['active_case']['name']}-checkpoint.json", result)
        return result

    def config_hashes(self):
        paths = [self.b.root / "balance-history/config.toml", self.b.root / "usdb-indexer/config.json",
                 self.b.root / "geth/geth/nodekey", self.b.root / "geth/geth/static-nodes.json"]
        return {str(p.relative_to(self.b.root)): hashlib.sha256(p.read_bytes()).hexdigest() for p in paths}

    def preserved(self, before):
        require(self.b.rpc("bitcoin")("getblockhash", [before["height"]]) == before["block_hash"],
                "restart changed historical Bitcoin hash")
        actual = self.state(self.b, before["height"])
        difference = self.difference(before["state"], actual)
        require(difference is None, f"restart changed committed historical state: {difference}")
        require(self.b.rpc("usdb-indexer")("get_readiness")["upstream_reorg_epoch"] == before["epoch"],
                "restart changed reorg epoch")
        require(self.config_hashes() == before["config_hashes"], "restart changed configuration or node identity")
        self.guard()

    def fresh_work(self, name):
        """Confirm a new owner top-up and execute new USDB blocks after recovery."""
        before = self.state(self.a)
        old_height = self.b.height()
        old_anchor = decode_selector(self.a.block())["btc_height"]
        wallet = RPC(self.a.url("bitcoin") + "/wallet/upstream-matrix", self.a.root / "bitcoin/regtest/.cookie")
        txid = wallet("sendtoaddress", [self.args.owner_address, 0.001])
        self.mine_btc(12)
        self.wait_btc(self.b)
        for node in (self.a, self.b):
            self.wait_upstream(node)
        after = self.state(self.b)
        require(after["owners"][self.owner]["balance"][-1]["balance"] ==
                before["owners"][self.owner]["balance"][-1]["balance"] + 100_000,
                "recovered node did not account for the exact new top-up")
        self.prepare_miner()
        self.mine_usdb()
        self.converge_geth(self.b)
        anchor = decode_selector(self.a.block())["btc_height"]
        calls = [c for c in self.b.proxy.profile_calls(name) if "error" not in c
                 and c["params"][0]["block_height"] == anchor]
        require(self.b.height() > old_height and anchor > old_anchor and calls,
                "recovery did not validate and execute fresh anchored work")
        return {"topup_txid": txid, "topup_sats": 100_000, "old_chain_height": old_height,
                "chain_height": self.b.height(), "old_anchor": old_anchor, "anchor": anchor,
                "profile_successes": len(calls), "state_sha256": self.compare_states(name, [self.b])}

    def stop_group(self):
        self.b.pinned_geth = None
        self.saved_commands = {s: p.args for s, p in self.b.processes.items()}
        for service in ("geth", "usdb-indexer", "balance-history", "ord", "bitcoin"):
            self.b.stop(service)

    def start_core(self, *, offline=False):
        # The offline flag belongs only to the controlled tip-regression phase;
        # do not inherit it when a later case reuses the same durable database.
        command = [arg for arg in self.saved_commands["bitcoin"] if not arg.startswith("-networkactive=")]
        command += ["-networkactive=0"] if offline else []
        self.b.start("bitcoin", command)
        self.wait("restarted Core RPC", lambda: self.b.rpc("bitcoin")("getblockchaininfo")["chain"] == "regtest")
        if not offline:
            self.b.rpc("bitcoin")("addnode", [f"127.0.0.1:{self.a.ports['btc-p2p']}", "add"])
            self.wait_btc(self.b)

    def start_validator(self):
        self.b.start_geth()
        # From this point convergence must use the persisted peer configuration.
        self.b.pinned_geth = self.b.processes["geth"]

    def group_restart(self):
        self.phase("group-restart")
        before = self.checkpoint()
        self.stop_group()
        self.start_core()
        self.b.start_ord()
        self.b.start_service("balance-history")
        self.b.start_indexer()
        self.wait_upstream(self.b)
        self.start_validator()
        self.converge_geth(self.b)
        self.preserved(before)
        require(all(self.identities()[s] != identity for s, identity in before["identities"].items()),
                "whole-group case did not restart every process")
        self.passed("group-restart", before=before["identities"], after=self.identities(),
                    **self.fresh_work("group-restart"))

    def core_regression(self):
        self.phase("core-target-regression")
        before = self.checkpoint()
        tip = self.b.rpc("bitcoin")("getbestblockhash")
        self.saved_commands = {"bitcoin": self.b.processes["bitcoin"].args}
        self.b.stop("bitcoin")
        self.start_core(offline=True)
        # Remove only an unconfirmed tip. The BH committed height/hash still exists.
        self.b.rpc("bitcoin")("invalidateblock", [tip])
        def lower_target_observed():
            bh = self.b.rpc("balance-history")("get_readiness")
            # Cookie rotation can also set the pending flag. Require a successful
            # Core observation of the lower target, not merely any transient RPC error.
            return ("UpstreamRecoveryPending" in bh["blockers"] and bh["total"] == before["height"] - 1
                    and "Waiting for Bitcoin confirmation target" in bh["message"])
        self.wait("BH actually observes the lower confirmation target", lower_target_observed)
        observations = []
        for _ in range(4):
            bh = self.b.rpc("balance-history")("get_readiness")
            indexer = self.b.rpc("usdb-indexer")("get_readiness")
            require(bh["stable_height"] == before["height"] and bh["query_ready"] is False
                    and bh["stable_block_hash"] == before["block_hash"]
                    and bh["latest_block_commit"] == before["bh_commit"] and bh["total"] == before["height"] - 1,
                    "temporary target regression discarded committed BH history")
            require(indexer["upstream_reorg_epoch"] == before["epoch"], "false reorg during Core recovery")
            observations.append({"bh": bh, "indexer": indexer})
            time.sleep(1)
        self.wait("indexer rejects pending upstream recovery", lambda:
                  self.b.rpc("usdb-indexer")("get_readiness")["consensus_ready"] is False)
        self.guard()
        self.b.rpc("bitcoin")("reconsiderblock", [tip])
        self.b.rpc("bitcoin")("setnetworkactive", [True])
        self.b.rpc("bitcoin")("addnode", [f"127.0.0.1:{self.a.ports['btc-p2p']}", "add"])
        self.wait_upstream(self.b)
        self.preserved(before)
        require(all(self.identities()[s] == identity for s, identity in before["identities"].items()
                    if s != "bitcoin"), "Core recovery restarted a downstream process")
        self.write_json(self.args.output_dir / "core-recovery-observations.json", observations)
        self.passed("core-target-regression", observations_file="core-recovery-observations.json", guard_unchanged_epoch_exit=0,
                    **self.fresh_work("core-target-regression"))

    def service_crashes(self):
        evidence = []
        for service in ("balance-history", "usdb-indexer"):
            name = f"{service}-sigkill"
            self.phase(name)
            before = self.checkpoint()
            require(self.b.stop(service, crash=True) == -signal.SIGKILL, "SIGKILL was not injected")
            self.mine_btc(3)
            self.wait_btc(self.b)
            self.wait_upstream(self.a)
            self.prepare_miner()
            self.mine_usdb()
            self.wait("validator actually rejects unavailable upstream", lambda:
                      bool(self.b.proxy.profile_calls(name, errors=True)))
            require(self.b.height() < self.a.height(), "validator imported while upstream was unavailable")
            self.b.start_service(service)
            self.wait_upstream(self.b)
            self.converge_geth(self.b)
            self.preserved(before)
            require(all(self.identities()[s] == identity for s, identity in before["identities"].items()
                        if s != service), f"{service} recovery restarted another process")
            evidence.append({"service": service, "signal": "SIGKILL", "identities_before": before["identities"],
                             "identities_after": self.identities(), **self.fresh_work(name)})
        self.passed("service-crashes", recoveries=evidence)

    def downstream_first(self):
        self.phase("downstream-first")
        before = self.checkpoint()
        self.stop_group()
        self.start_validator()
        self.b.start_indexer()
        # Match the deployed BH entrypoint's real TCP gate. A bare BH binary
        # performs startup preflight and may exit before Core is available.
        # exec preserves the waiting process identity; the harness never retries it.
        helper = self.args.usdb_repo / "docker/scripts/helpers/wait_for_tcp.sh"
        self.b.start("balance-history", ["bash", "-ec", '"$1" 127.0.0.1 "$2" 180; exec "$3" --root-dir "$4" --skip-process-lock',
                    "bh-startup", str(helper), str(self.b.ports["bitcoin"]), str(self.args.balance_history),
                    str(self.b.root / "balance-history")])
        started = self.identities()
        self.mine_btc(3)
        self.wait_upstream(self.a)
        self.prepare_miner()
        self.mine_usdb()
        self.wait("downstream observes absent upstream", lambda:
                  bool(self.b.proxy.profile_calls("downstream-first", errors=True)))
        # The guard reads the durable epoch even when consensus readiness is false.
        self.guard()
        self.start_core()
        self.b.start_ord()
        self.wait_upstream(self.b)
        self.converge_geth(self.b)
        self.preserved(before)
        require(all(self.identities()[s] == identity for s, identity in started.items()),
                "downstream-first required an extra restart")
        self.passed("downstream-first", startup_order=["geth", "usdb-indexer", "balance-history-tcp-wait", "bitcoin", "ord"],
                    downstream_before=started, downstream_after=self.identities(), **self.fresh_work("downstream-first"))

    def run(self):
        self.report["schema"] = "usdb-node-restart:v1"
        self.b = RestartNode(self, "b", 1)
        self.nodes = [self.a, self.b]
        self.report["topology"] = self.report["topology"][:2]
        self.wait_upstream(self.a)
        genesis = subprocess.check_output([str(self.args.geth), "dumpgenesis", "--usdb"], timeout=30)
        self.write_json(self.genesis, configure_genesis(json.loads(genesis), 256, 256))
        self.command([sys.executable, str(self.args.usdb_repo / "tests/common/miner_pass_regtest.py"),
                      "configure-genesis", str(self.genesis)], "configure-genesis.log")
        self.a.init_geth()
        self.a.start_geth()
        self.mine_usdb()
        self.b.fresh_upstream()
        self.b.init_geth()
        enode = self.a.rpc("geth")("admin_nodeInfo")["enode"].split("@")[0]
        self.write_json(self.b.root / "geth/geth/static-nodes.json", [enode + f"@127.0.0.1:{self.a.ports['geth-p2p']}"])
        self.start_validator()
        self.converge_geth(self.b)
        self.guard_dir = self.b.root / "geth/recovery/deep-btc-reorg"
        self.guard()
        self.compare_states("baseline", [self.b])
        self.group_restart()
        self.core_regression()
        self.service_crashes()
        self.downstream_first()
        require([c["name"] for c in self.report["cases"]] == list(P0_CASES), "P0 coverage incomplete")
        self.check_alive()

    def close(self):
        try:
            super().close()
        finally:
            # Native A upstreams belong to the shell harness; collect their logs
            # without publishing cookies, wallets or configuration secrets.
            for node in (self.a, self.b):
                for relative in ("bitcoin/regtest/debug.log", "balance-history.log", "usdb-indexer.log", "ord-server.log"):
                    source = node.root / relative
                    if source.is_file():
                        shutil.copyfile(source, self.args.output_dir / f"{node.name}-{relative.replace('/', '-')}")
                for service in ("balance-history", "usdb-indexer"):
                    for source in (node.root / service / "logs").glob("*.log"):
                        shutil.copyfile(source, self.args.output_dir / f"{node.name}-{service}-{source.name}")


if __name__ == "__main__":
    main(RestartMatrix)
