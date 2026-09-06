#!/usr/bin/env python3
"""Short, isolated geth/BTC/Ord/indexer fault and canonical replay matrix."""

import argparse
from contextlib import closing
import base64
import hashlib
import json
import os
from pathlib import Path
import signal
import socket
import sqlite3
import subprocess
import sys
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib import request

from configure_usdb_pow_calibration_genesis import configure_genesis
from verify_usdb_profile_e2e import decode_selector, SYSTEM_STATE_SLOTS, USDB_SYSTEM_STATE_ADDRESS


SCHEMA = "usdb-independent-upstream-matrix:v2"
CASES = ("baseline", "indexer-crash", "crash-recovery", "balance-crash", "balance-recovery",
         "ord-outage", "ord-recovery", "ord-source-outage", "ord-source-recovery",
         "stable-fork", "recovery-interrupted", "recovery-reinterrupted", "fork-recovery", "fresh-replay")
FAULT_CODES = {"indexer-crash": {-32098}, "balance-crash": {-32041},
               "ord-source-outage": {-32040, -32041}, "stable-fork": {-32042, -32043, -32045, -32046}}
RECOVERY_ENV = ("USDB_INDEXER_INJECT_REORG_RECOVERY_ENERGY_FAILURES",
                "USDB_INDEXER_INJECT_REORG_RECOVERY_TRANSFER_RELOAD_FAILURES")
SERVICES = ("bitcoin", "btc-p2p", "ord", "balance-history", "usdb-indexer", "geth", "geth-p2p", "auth", "audit")
MINER = "0x1111111111111111111111111111111111111111"


def require(condition, message):
    if not condition:
        raise ValueError(f"upstream matrix: {message}")


def ports(base, index):
    result = dict(zip(SERVICES, range(base + index * 20, base + index * 20 + len(SERVICES))))
    # Core also binds P2P+1 for inbound onion traffic, even on regtest.
    result["btc-p2p"] = base + index * 20 + 10
    result["btc-onion"] = base + index * 20 + 11
    return result


def probe_ports(base):
    """Fail before starting services; never attach to a pre-existing listener."""
    require(1024 <= base and base + 51 < 32768, "ports must stay below Linux ephemeral range")
    for index in range(3):
        for port in ports(base, index).values():
            with socket.socket() as probe:
                probe.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
                probe.bind(("127.0.0.1", port))


def validate_fault(evidence, kind):
    """A quiet validator is meaningful only if it attempted the new healthy chain."""
    require(evidence["healthy_after"] > evidence["before"], "healthy miner did not progress")
    require(evidence["validator_after"] == evidence["before"], "faulty validator imported new blocks")
    require(evidence["new_anchor"] > evidence["old_anchor"], "fault only exercised a cached anchor")
    require(evidence["observed_seconds"] >= 4, "fault observation too short")
    require(evidence["profile_errors"] > 0, "no real validator profile rejection observed")
    expected_codes = FAULT_CODES[kind]
    require(expected_codes.intersection(evidence["profile_error_codes"]), "rejection did not match injected fault")
    if kind in {"balance-crash", "ord-source-outage"}:
        validate_not_ready(evidence["readiness"],
                           "UpstreamReadinessUnknown" if kind == "balance-crash" else "CatchingUp")
    if kind == "stable-fork":
        require(evidence["fork_depth"] > 10, "fork did not cross stable frontier")
        require(evidence["canonical_hash"] != evidence["fork_hash"], "no actual BTC fork")
        require(evidence["canonical_balance"] - evidence["fork_balance"] == 100_000_000,
                "fork did not remove the confirmed one-BTC top-up")


def validate_not_ready(readiness, blocker):
    require(readiness["rpc_alive"] is True, "indexer RPC died instead of reporting not ready")
    require(readiness["consensus_ready"] is False and blocker in readiness["blockers"],
            f"missing readiness blocker: {blocker}")


def validate_ord_independence(evidence):
    require(evidence["source"] == "bitcoind", "Ord-independent case requires Core inscription source")
    require(evidence["ord_exit_code"] == -signal.SIGKILL, "Ord outage was not injected")
    require(evidence["btc_after"] > evidence["btc_before"], "Ord missed no new BTC blocks")
    require(evidence["after"] > evidence["before"] and evidence["validator_after"] == evidence["after"],
            "Core-based consensus did not progress during Ord outage")
    require(evidence["profile_successes"] > 0 and evidence["readiness"]["consensus_ready"] is True,
            "Core-based validator did not actually validate while Ord was offline")


def validate_interrupted_recovery(evidence):
    validate_not_ready(evidence["readiness"], "ReorgRecoveryPending")
    require(evidence["readiness"]["query_ready"] is False, "pending recovery exposed query-ready state")
    require(type(evidence["pending_before"]) is int and evidence["pending_before"] > 0,
            "recovery was not durably pending")
    require(evidence["pending_before"] == evidence["pending_after"] == evidence["readiness"]["synced_block_height"],
            "crash lost or changed durable recovery marker")
    require(evidence["epoch"] > evidence["fork_epoch"] and evidence["hook_hits"] > 0,
            "canonical recovery phase was not actually reached")
    require(evidence["profile_errors"] > 0 and evidence["validator_after"] == evidence["validator_before"],
            "validator did not reject while recovery was pending")
    require(evidence["exit_code"] == -signal.SIGKILL, "recovery process was not interrupted")


def compare_chain(expected, actual):
    require(len(expected) > 1 and len(expected) == len(actual), "incomplete block replay")
    for number, (left, right) in enumerate(zip(expected, actual)):
        require(left["number"] == right["number"] == hex(number), f"missing block {number}")
        for field in ("hash", "parentHash", "stateRoot", "receiptsRoot", "transactionsRoot", "extraData"):
            require(left[field] == right[field], f"block {number} {field} mismatch")


class RPCError(ValueError):
    def __init__(self, method, error):
        self.code, self.message = error.get("code"), error.get("message")
        super().__init__(f"{method}: {error}")


class RPC:
    def __init__(self, url, cookie=None):
        self.url, self.cookie = url, cookie

    def __call__(self, method, params=None):
        headers = {"Content-Type": "application/json"}
        if self.cookie:
            headers["Authorization"] = "Basic " + base64.b64encode(self.cookie.read_bytes().strip()).decode()
        payload = json.dumps({"jsonrpc": "2.0", "id": 1, "method": method, "params": params or []}).encode()
        with request.urlopen(request.Request(self.url, data=payload, headers=headers), timeout=5) as response:
            value = json.load(response)
        if value.get("error"):
            raise RPCError(method, value["error"])
        require("result" in value, f"missing RPC result: {method}")
        return value["result"]


class AuditProxy:
    """Forward to this node's own indexer and record actual geth validation calls."""
    def __init__(self, port, upstream, output):
        self.events, self.lock = [], threading.Lock()
        self.phase = "startup"
        self.stream = output.open("w")
        proxy = self

        class Handler(BaseHTTPRequestHandler):
            def do_POST(self):
                payload = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
                event = {"phase": proxy.phase, "method": payload["method"], "params": payload.get("params", [])}
                try:
                    result = RPC(upstream)(payload["method"], payload.get("params", []))
                    reply = {"jsonrpc": "2.0", "id": payload["id"], "result": result}
                except (OSError, ValueError) as error:
                    event["error"] = {"code": getattr(error, "code", -32098), "message": str(error)}
                    reply = {"jsonrpc": "2.0", "id": payload["id"], "error": event["error"]}
                with proxy.lock:
                    proxy.events.append(event)
                    proxy.stream.write(json.dumps(event) + "\n")
                    proxy.stream.flush()
                body = json.dumps(reply).encode()
                try:
                    self.send_response(200)
                    self.send_header("Content-Type", "application/json")
                    self.send_header("Content-Length", str(len(body)))
                    self.end_headers()
                    self.wfile.write(body)
                except (BrokenPipeError, ConnectionResetError):
                    pass

            def log_message(self, *args):
                pass

        class Server(ThreadingHTTPServer):
            # Header verification is parallel. The default backlog of five
            # causes proxy-induced connect timeouts during otherwise healthy sync.
            request_queue_size = 128
            daemon_threads = False

        self.server = Server(("127.0.0.1", port), Handler)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()

    def profile_calls(self, phase, *, errors=False):
        with self.lock:
            return [e for e in self.events if e["phase"] == phase and e["method"] == "get_pass_economic_profile"
                    and (not errors or "error" in e)]

    def close(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=6)
        self.stream.close()


class Node:
    def __init__(self, matrix, name, index):
        self.matrix, self.name = matrix, name
        self.root = matrix.args.work_dir / name
        self.ports = ports(matrix.args.port_base, index)
        self.processes = {}
        self.proxy = None

    def url(self, service):
        return f"http://127.0.0.1:{self.ports[service]}"

    def rpc(self, service):
        if service == "bitcoin":
            return RPC(self.url(service), self.root / "bitcoin/regtest/.cookie")
        return RPC(self.url(service))

    def start(self, service, command, *, env=None):
        require(service not in self.processes, f"{self.name}/{service} already managed")
        log = (self.matrix.args.output_dir / f"{self.name}-{service}.log").open("a")
        try:
            self.processes[service] = subprocess.Popen(command, stdout=log, stderr=subprocess.STDOUT,
                                                       start_new_session=True, env=env)
        finally:
            log.close()

    def stop(self, service, *, crash=False):
        process = self.processes.pop(service)
        require(process.poll() is None, f"{self.name}/{service} exited before requested fault/shutdown")
        os.killpg(process.pid, signal.SIGKILL if crash else signal.SIGTERM)
        try:
            process.wait(timeout=12)
        except subprocess.TimeoutExpired:
            os.killpg(process.pid, signal.SIGKILL)
            process.wait(timeout=5)
            require(False, f"{self.name}/{service} failed graceful shutdown")
        return process.returncode

    def start_indexer(self, recovery_stage=None):
        self.start_service("usdb-indexer", recovery_stage=recovery_stage)

    def start_service(self, package, recovery_stage=None):
        binary = self.matrix.args.balance_history if package == "balance-history" else self.matrix.args.indexer
        env = {key: value for key, value in os.environ.items() if key not in RECOVERY_ENV}
        if recovery_stage is not None:
            require(package == "usdb-indexer" and recovery_stage in ("energy", "transfer"), "invalid recovery hook")
            # Existing regtest hooks keep the persisted recovery phase pending;
            # the matrix then kills the real process at that observed boundary.
            env[RECOVERY_ENV[0 if recovery_stage == "energy" else 1]] = "100000"
        self.start(package, [str(binary),
                            "--root-dir", str(self.root / package), "--skip-process-lock"], env=env)

    def start_ord(self):
        self.start("ord", [str(self.matrix.args.ord), "--regtest", "--bitcoin-rpc-url", self.url("bitcoin"),
                           "--cookie-file", str(self.root / "bitcoin/regtest/.cookie"),
                           "--bitcoin-data-dir", str(self.root / "bitcoin"), "--data-dir", str(self.root / "ord"),
                           "--savepoint-interval", "1", "--max-savepoints", "64", "--index-addresses",
                           "--index-transactions", "server", "--address", "127.0.0.1", "--http",
                           "--http-port", str(self.ports["ord"])])

    def inscription_source(self):
        return json.loads((self.root / "usdb-indexer/config.json").read_text())["usdb"]["inscription_source"]

    def set_inscription_source(self, source):
        require(source in {"bitcoind", "ord"}, "unsupported matrix inscription source")
        self.stop("usdb-indexer")
        path = self.root / "usdb-indexer/config.json"
        config = json.loads(path.read_text())
        config["usdb"]["inscription_source"] = source
        self.matrix.write_json(path, config)
        self.start_indexer()
        self.matrix.wait_upstream(self)

    def fresh_upstream(self):
        # mkdir(exist_ok=False) is the proof that no BTC, Ord or indexer state
        # can be inherited from another node or an earlier attempt.
        self.root.mkdir()
        for service in ("bitcoin", "ord", "balance-history", "usdb-indexer"):
            (self.root / service).mkdir()
        self.start("bitcoin", [str(self.matrix.args.bitcoin), "-regtest", "-server=1", "-txindex=1",
                               f"-datadir={self.root / 'bitcoin'}", f"-rpcport={self.ports['bitcoin']}",
                               f"-port={self.ports['btc-p2p']}", "-listen=1", "-dnsseed=0", "-discover=0"])
        self.matrix.wait(f"{self.name} Bitcoin RPC", lambda: self.rpc("bitcoin")("getblockchaininfo")["chain"] == "regtest")
        self.rpc("bitcoin")("addnode", [f"127.0.0.1:{self.matrix.a.ports['btc-p2p']}", "add"])
        self.matrix.wait_btc(self)
        self.start_ord()
        self.matrix.wait_ord(self)
        balance = (self.matrix.a.root / "balance-history/config.toml").read_text()
        balance = balance.replace(str(self.matrix.a.root), str(self.root))
        balance = balance.replace(self.matrix.a.url("bitcoin"), self.url("bitcoin"))
        balance = balance.replace(f"port = {self.matrix.a.ports['balance-history']}", f"port = {self.ports['balance-history']}")
        (self.root / "balance-history/config.toml").write_text(balance)
        config = json.loads((self.matrix.a.root / "usdb-indexer/config.json").read_text())
        config["bitcoin"].update(data_dir=str(self.root / "bitcoin/regtest"), rpc_url=self.url("bitcoin"))
        config["ordinals"]["rpc_url"] = self.url("ord")
        config["balance_history"]["rpc_url"] = self.url("balance-history")
        config["usdb"]["rpc_server_port"] = self.ports["usdb-indexer"]
        (self.root / "usdb-indexer/config.json").write_text(json.dumps(config, indent=2) + "\n")
        self.start_service("balance-history")
        self.matrix.wait(f"{self.name} balance RPC", lambda: self.rpc("balance-history")("get_network_type") == "regtest")
        self.start_indexer()
        self.matrix.wait_upstream(self)

    def init_geth(self):
        (self.root / "geth").mkdir()
        self.matrix.command([str(self.matrix.args.geth), "init", "--datadir", str(self.root / "geth"),
                             str(self.matrix.genesis)], f"{self.name}-init.log")
        self.proxy = AuditProxy(self.ports["audit"], self.url("usdb-indexer"),
                                self.matrix.args.output_dir / f"{self.name}-rpc.jsonl")

    def start_geth(self):
        self.start("geth", [str(self.matrix.args.geth), "--datadir", str(self.root / "geth"),
                            "--networkid", "20260323", "--syncmode", "full", "--gcmode", "archive",
                            "--cache", "128", "--http", "--http.addr", "127.0.0.1", "--http.port", str(self.ports["geth"]),
                            "--http.api", "eth,net,web3,admin,miner", "--authrpc.addr", "127.0.0.1",
                            "--authrpc.port", str(self.ports["auth"]), "--port", str(self.ports["geth-p2p"]),
                            "--nodiscover", "--maxpeers", "4", "--ipcdisable",
                            "--ethash.dagdir", str(self.root / "dag"), "--ethash.dagsondisk", "1",
                            "--ethash.cachedir", str(self.root / "ethash"),
                            "--miner.etherbase", MINER, "--miner.usdb-indexer.rpcurl", self.url("audit"),
                            "--miner.usdb-indexer.timeout", "3s", "--ethash.usdb-indexer.rpcurl", self.url("audit"),
                            "--ethash.usdb-indexer.timeout", "3s"])
        self.matrix.wait(f"{self.name} geth RPC", lambda: self.rpc("geth")("eth_chainId") == hex(20260323))

    def height(self):
        return int(self.rpc("geth")("eth_blockNumber"), 16)

    def block(self, number="latest"):
        return self.rpc("geth")("eth_getBlockByNumber", [hex(number) if isinstance(number, int) else number, False])


class Matrix:
    def __init__(self, args):
        self.args = args
        self.started, self.deadline = time.monotonic(), time.monotonic() + args.timeout_sec
        self.a, self.b, self.c = (Node(self, name, index) for index, name in enumerate("abc"))
        self.nodes = [self.a, self.b, self.c]
        self.genesis = args.work_dir / "genesis.json"
        self.report = {"schema": SCHEMA, "status": "running", "work_dir": str(args.work_dir), "cases": [],
                       "topology": [{"node": n.name, "root": str(n.root), "ports": n.ports} for n in self.nodes]}
        sys.path.insert(0, str(args.usdb_repo / "src/btc/usdb-indexer/scripts"))
        from world_replay_state import capture_state, first_difference, digest, write_json
        from compare_world_replay import wait_historical_refs, ReplayRPCError
        from regtest_world_simulator import RegtestWorldSimulator
        self.capture, self.difference, self.digest, self.write_json = capture_state, first_difference, digest, write_json
        self.wait_history, self.history_error = wait_historical_refs, ReplayRPCError
        self.context = RegtestWorldSimulator.build_consensus_context_from_state_ref
        script = self.a.rpc("bitcoin")("validateaddress", [args.owner_address])["scriptPubKey"]
        self.owner = hashlib.sha256(bytes.fromhex(script)).digest()[::-1].hex()

    def log(self, message):
        print(f"[upstream-matrix] {message}", flush=True)

    def check_alive(self):
        require(time.monotonic() < self.deadline, "matrix time budget exceeded")
        for node in self.nodes:
            for service, process in node.processes.items():
                require(process.poll() is None, f"unexpected exit: {node.name}/{service}: {process.returncode}")

    def wait(self, label, predicate, seconds=180, interval=0.25):
        deadline, next_log, last = min(self.deadline, time.monotonic() + seconds), 0, "not ready"
        while time.monotonic() < deadline:
            self.check_alive()
            try:
                if predicate():
                    return
            except RPCError as error:
                # Startup/warmup may be retried; selector mismatches and unknown
                # RPC methods are permanent failures, not readiness delays.
                if error.code not in {-28, -32040, -32041, -32049}:
                    raise
                last = str(error)
            except OSError as error:
                last = str(error)
            if time.monotonic() >= next_log:
                self.log(f"Waiting: {label} ({last})")
                next_log = time.monotonic() + 20
            time.sleep(interval)
        raise ValueError(f"upstream matrix: timeout: {label}: {last}")

    def command(self, command, log_name):
        with (self.args.output_dir / log_name).open("a") as log:
            subprocess.run(command, stdout=log, stderr=subprocess.STDOUT, check=True,
                           timeout=max(1, self.deadline - time.monotonic()))

    def phase(self, name):
        self.log(f"START {name}")
        self.report["active_case"] = {"name": name}
        for node in self.nodes:
            if node.proxy:
                node.proxy.phase = name

    def passed(self, name, **evidence):
        self.report["cases"].append({"name": name, "status": "ok", **evidence})
        self.report.pop("active_case", None)
        self.write_json(self.args.output_dir / "summary.json", self.report)
        self.log(f"PASS {name}: {evidence}")

    def frontier(self, node):
        height = node.rpc("bitcoin")("getblockcount") - 10
        return height, node.rpc("bitcoin")("getblockhash", [height])

    def wait_btc(self, node):
        target = self.a.rpc("bitcoin")("getbestblockhash")
        self.wait(f"{node.name} canonical BTC and txindex", lambda:
                  node.rpc("bitcoin")("getbestblockhash") == target
                  and node.rpc("bitcoin")("getindexinfo").get("txindex", {}).get("synced") is True)

    def wait_ord(self, node):
        height = node.rpc("bitcoin")("getblockcount")
        block_hash = node.rpc("bitcoin")("getblockhash", [height])

        def ready():
            with request.urlopen(node.url("ord") + "/blockcount", timeout=3) as response:
                count = int(response.read())
            with request.urlopen(node.url("ord") + f"/blockhash/{height}", timeout=3) as response:
                actual_hash = response.read().decode().strip()
            return count == height + 1 and actual_hash == block_hash
        self.wait(f"{node.name} Ord exact height/hash", ready)

    def wait_upstream(self, node):
        height, block_hash = self.frontier(node)

        def ready():
            for service in ("balance-history", "usdb-indexer"):
                rpc = node.rpc(service)
                if rpc("get_readiness").get("consensus_ready") is not True:
                    return False
                snapshot = rpc("get_snapshot_info")
                if snapshot.get("stable_height", snapshot.get("balance_history_stable_height")) != height:
                    return False
                if snapshot.get("stable_block_hash") != block_hash:
                    return False
            return node.rpc("usdb-indexer")("get_synced_block_height") == height
        self.wait(f"{node.name} upstream exact stable height/hash={height}/{block_hash}", ready)

    def history_rpc(self, node):
        def call(method, params):
            self.check_alive()
            try:
                return node.rpc("usdb-indexer")(method, params)
            except RPCError as error:
                raise self.history_error(method, {"code": error.code, "message": error.message}) from error
        return call

    def state(self, node, height=None):
        final_height, _ = self.frontier(node)
        height = final_height if height is None else height
        block_hash = node.rpc("bitcoin")("getblockhash", [height])
        self.wait_history(self.history_rpc(node), [{"height": height, "block_hash": block_hash}],
                          final_height, min(self.deadline, time.monotonic() + 180))
        return self.capture(node.rpc("usdb-indexer"), node.rpc("balance-history"), height, block_hash,
                            [self.owner], self.context, history=True)

    def compare_states(self, label, nodes, *, include_ord=True):
        expected = self.state(self.a)
        require(self.args.pass_id in expected["passes"], "minted pass missing from state audit")
        self.write_json(self.args.output_dir / f"{label}-a-state.json", expected)
        for node in nodes:
            actual = self.state(node)
            self.write_json(self.args.output_dir / f"{label}-{node.name}-state.json", actual)
            difference = self.difference(expected, actual)
            require(difference is None, f"{label}/{node.name} upstream mismatch: {difference}")
        if not include_ord:
            require(label == "ord-outage", "Ord comparison can only be deferred during the explicit outage")
            return self.digest(expected)
        # A and C scan inscriptions through Core; B also exercises Ord as its
        # source. Compare independently rebuilt Ord ownership and content too.
        inscriptions = {}
        for node in [self.a, *nodes]:
            self.wait_ord(node)
            req = request.Request(node.url("ord") + f"/inscription/{self.args.pass_id}",
                                  headers={"Accept": "application/json"})
            with request.urlopen(req, timeout=5) as response:
                inscription = json.load(response)
            require(inscription["address"] == self.args.owner_address, f"{node.name} Ord owner mismatch")
            with request.urlopen(node.url("ord") + f"/content/{self.args.pass_id}", timeout=5) as response:
                content = json.load(response)
            require(content == json.loads((self.a.root / "mint.json").read_text()), f"{node.name} Ord mint content mismatch")
            inscriptions[node.name] = {"inscription": inscription, "content": content}
            require(inscriptions[node.name] == inscriptions["a"], f"{node.name} Ord inscription state mismatch")
        self.write_json(self.args.output_dir / f"{label}-ord.json", inscriptions)
        return self.digest(expected)

    def connect_geth(self, node):
        enode = self.a.rpc("geth")("admin_nodeInfo")["enode"]
        # Discovery is disabled; the explicit loopback enode prevents accidental
        # external peers and keeps every validator attached to the healthy miner.
        enode = enode.split("@")[0] + f"@127.0.0.1:{self.a.ports['geth-p2p']}"
        require(node.rpc("geth")("admin_addPeer", [enode]) is True, "admin_addPeer rejected")

    def converge_geth(self, node, *, restart=False):
        if restart:
            node.stop("geth")
            node.start_geth()
        self.connect_geth(node)
        target = self.a.block()
        self.wait(f"{node.name} geth canonical head {target['number']}", lambda: node.block()["hash"] == target["hash"])
        expected = [self.a.block(i) for i in range(self.a.height() + 1)]
        actual = [node.block(i) for i in range(node.height() + 1)]
        compare_chain(expected, actual)
        # Check state RPCs as well as headers: a node with headers but missing
        # execution state must not qualify as a completed full replay.
        for block in expected:
            tag = block["number"]
            for slot in SYSTEM_STATE_SLOTS.values():
                params = [USDB_SYSTEM_STATE_ADDRESS, slot, tag]
                require(self.a.rpc("geth")("eth_getStorageAt", params) == node.rpc("geth")("eth_getStorageAt", params),
                        f"{node.name} historical system storage mismatch at {tag}/{slot}")
            require(self.a.rpc("geth")("eth_getBalance", [MINER, tag]) == node.rpc("geth")("eth_getBalance", [MINER, tag]),
                    f"{node.name} historical miner balance mismatch at {tag}")
        self.write_json(self.args.output_dir / f"{node.name}-blocks.json", actual)
        return len(expected) - 1

    def mine_usdb(self, count=2):
        start = self.a.height()
        self.a.rpc("geth")("miner_start", [1])
        try:
            self.wait(f"healthy miner advances from {start}", lambda: self.a.height() >= start + count, interval=0.01)
        finally:
            self.a.rpc("geth")("miner_stop")
        time.sleep(1)

    def mine_btc(self, count):
        self.a.rpc("bitcoin")("generatetoaddress", [count, self.args.miner_address])

    def prepare_miner(self, *, wait_for_peer=True):
        # miner_stop preserves pending sealing work. Restart the healthy miner
        # after advancing BTC so no old-anchor work can race the fault's first
        # block. Live miner refresh has its own dedicated E2E coverage.
        self.a.stop("geth")
        self.a.start_geth()
        self.connect_geth(self.b)
        if wait_for_peer:
            self.wait("validator attached before fault injection", lambda: bool(self.b.rpc("geth")("admin_peers")))

    def readiness_blocked(self, blocker):
        evidence = {}
        def blocked():
            evidence.update(self.b.rpc("usdb-indexer")("get_readiness"))
            return evidence["rpc_alive"] is True and evidence["consensus_ready"] is False and blocker in evidence["blockers"]
        self.wait(f"B readiness reports {blocker}", blocked, seconds=45)
        validate_not_ready(evidence, blocker)
        return evidence

    def profile_errors(self, phase, codes, anchor=None):
        return [event for event in self.b.proxy.profile_calls(phase, errors=True)
                if event["error"]["code"] in codes
                and (anchor is None or event["params"][0]["block_height"] == anchor)]

    def fault_observation(self, name, before, old_anchor, **extra):
        self.mine_usdb()
        started = time.monotonic()
        new_anchor = decode_selector(self.a.block())["btc_height"]
        def rejected():
            require(self.b.height() == before, f"{name}: faulty validator imported new blocks")
            return time.monotonic() - started >= 5 and bool(self.profile_errors(
                name, FAULT_CODES[name], new_anchor if name == "stable-fork" else None))
        self.wait(f"{name}: actual validator rejection", rejected, seconds=45)
        errors = self.profile_errors(name, FAULT_CODES[name])
        # An unavailable indexer can reject the already-known parent before the
        # validator reaches the new header. A divergent but ready indexer must
        # reject the new anchor specifically, with a native selector error.
        if name == "stable-fork":
            errors = [e for e in errors if e["params"][0]["block_height"] == new_anchor]
        if name in {"balance-crash", "ord-source-outage"}:
            extra["readiness"] = self.b.rpc("usdb-indexer")("get_readiness")
        evidence = {"before": before, "healthy_after": self.a.height(), "validator_after": self.b.height(),
                    "old_anchor": old_anchor, "new_anchor": new_anchor,
                    "observed_seconds": round(time.monotonic() - started, 2), "profile_errors": len(errors),
                    "profile_error_codes": sorted({e["error"]["code"] for e in errors}),
                    "rejected_anchors": sorted({e["params"][0]["block_height"] for e in errors}), **extra}
        self.report["active_case"] = {"name": name, **evidence}
        validate_fault(evidence, name)
        self.passed(name, **evidence)

    def run_balance_outage(self):
        self.phase("balance-crash")
        before, old_anchor = self.a.height(), decode_selector(self.a.block())["btc_height"]
        self.mine_btc(3)
        self.wait_btc(self.b)
        for node in (self.a, self.b):
            self.wait_upstream(node)
        self.prepare_miner()
        indexer_pid = self.b.processes["usdb-indexer"].pid
        require(self.b.stop("balance-history", crash=True) == -signal.SIGKILL, "balance-history crash was not injected")
        readiness = self.readiness_blocked("UpstreamReadinessUnknown")
        self.fault_observation("balance-crash", before, old_anchor, readiness=readiness)

        self.phase("balance-recovery")
        self.b.start_service("balance-history")
        self.wait_upstream(self.b)
        require(self.b.processes["usdb-indexer"].pid == indexer_pid, "balance recovery restarted downstream indexer")
        blocks = self.converge_geth(self.b, restart=True)
        self.passed("balance-recovery", blocks=blocks, indexer_restarted=False, balance_database_reused=True,
                    state_sha256=self.compare_states("balance-recovery", [self.b]))

    def run_ord_outages(self):
        self.phase("ord-outage")
        require(self.b.inscription_source() == "bitcoind", "unexpected initial B source")
        before = self.a.height()
        btc_before = self.b.rpc("bitcoin")("getblockcount")
        exit_code = self.b.stop("ord", crash=True)
        self.mine_btc(3)
        self.wait_btc(self.b)
        for node in (self.a, self.b):
            self.wait_upstream(node)
        self.prepare_miner()
        self.mine_usdb()
        blocks = self.converge_geth(self.b)
        anchor = decode_selector(self.a.block())["btc_height"]
        calls = [e for e in self.b.proxy.profile_calls("ord-outage")
                 if "error" not in e and e["params"][0]["block_height"] == anchor]
        evidence = {"source": self.b.inscription_source(), "ord_exit_code": exit_code,
                    "btc_before": btc_before, "btc_after": self.b.rpc("bitcoin")("getblockcount"),
                    "before": before, "after": blocks, "validator_after": self.b.height(),
                    "profile_successes": len(calls), "readiness": self.b.rpc("usdb-indexer")("get_readiness")}
        validate_ord_independence(evidence)
        self.passed("ord-outage", **evidence, state_sha256=self.compare_states("ord-outage", [self.b], include_ord=False))

        self.phase("ord-recovery")
        self.b.start_ord()
        self.wait_ord(self.b)
        self.passed("ord-recovery", ord_database_reused=True, state_sha256=self.compare_states("ord-recovery", [self.b]))

        self.phase("ord-source-outage")
        self.b.set_inscription_source("ord")
        before, old_anchor = self.a.height(), decode_selector(self.a.block())["btc_height"]
        require(self.b.stop("ord", crash=True) == -signal.SIGKILL, "required Ord crash was not injected")
        self.mine_btc(3)
        self.wait_btc(self.b)
        self.wait_upstream(self.a)
        readiness = self.readiness_blocked("CatchingUp")
        require(readiness["synced_block_height"] < self.frontier(self.a)[0], "Ord-dependent indexer did not stall")
        self.prepare_miner(wait_for_peer=False)
        self.fault_observation("ord-source-outage", before, old_anchor, readiness=readiness)

        self.phase("ord-source-recovery")
        self.b.start_ord()
        self.wait_ord(self.b)
        self.wait_upstream(self.b)
        blocks = self.converge_geth(self.b, restart=True)
        self.passed("ord-source-recovery", blocks=blocks, source=self.b.inscription_source(),
                    state_sha256=self.compare_states("ord-source-recovery", [self.b]))

    def pending_recovery_height(self):
        path = self.b.root / "usdb-indexer/data/miner_pass.db"
        # Read-only SQLite observes the durable marker without changing it or
        # relying exclusively on the RPC's in-memory readiness flags.
        with closing(sqlite3.connect(path.as_uri() + "?mode=ro", uri=True, timeout=3)) as conn:
            row = conn.execute("SELECT value FROM state WHERE name = 'upstream_reorg_recovery_pending_height'").fetchone()
        return None if row is None else int(row[0])

    def recovery_hook_hits(self, stage, height):
        token = "energy" if stage == "energy" else "transfer reload"
        text = f"Injected reorg recovery {token} failure: target_height={height},"
        return sum(path.read_text().count(text) for path in (self.b.root / "usdb-indexer/logs").glob("*.log"))

    def interrupt_pending_recovery(self, phase, stage, fork_epoch, expected_height):
        self.phase(phase)
        readiness = self.readiness_blocked("ReorgRecoveryPending")
        pending = self.pending_recovery_height()
        require(pending == expected_height, f"unexpected recovery rollback target: {pending}, expected {expected_height}")
        self.wait(f"{stage} recovery hook reached", lambda: self.recovery_hook_hits(stage, pending) > 0, seconds=30)
        before = self.b.height()
        self.b.stop("geth")
        self.b.start_geth()
        self.connect_geth(self.b)
        def rejected():
            require(self.b.height() == before, "validator imported blocks while reorg recovery was pending")
            return bool(self.profile_errors(phase, {-32041}))
        self.wait(f"{phase}: validator refuses incomplete recovery", rejected, seconds=45)
        evidence = {"stage": stage, "readiness": readiness, "pending_before": pending,
                    "epoch": readiness["upstream_reorg_epoch"], "fork_epoch": fork_epoch,
                    "hook_hits": self.recovery_hook_hits(stage, pending),
                    "profile_errors": len(self.profile_errors(phase, {-32041})),
                    "validator_before": before, "validator_after": self.b.height(),
                    "exit_code": self.b.stop("usdb-indexer", crash=True), "pending_after": self.pending_recovery_height()}
        self.report["active_case"] = {"name": phase, **evidence}
        validate_interrupted_recovery(evidence)
        self.passed(phase, **evidence)
        return evidence["epoch"]

    def run(self):
        self.phase("baseline")
        self.wait_upstream(self.a)
        genesis = subprocess.check_output([str(self.args.geth), "dumpgenesis", "--usdb"], timeout=30)
        # Low positive difficulty shortens mining while retaining real PoW and
        # all USDB validation. It is confined to this temporary test genesis.
        self.write_json(self.genesis, configure_genesis(json.loads(genesis), 256, 256))
        self.a.init_geth()
        self.a.start_geth()
        self.mine_usdb()
        self.b.fresh_upstream()
        self.b.init_geth()
        self.b.start_geth()
        self.b.proxy.phase = "baseline"
        blocks = self.converge_geth(self.b)
        state_digest = self.compare_states("baseline", [self.b])
        require(self.b.proxy.profile_calls("baseline"), "baseline did not validate historical profiles")
        self.passed("baseline", blocks=blocks, state_sha256=state_digest)

        self.phase("indexer-crash")
        before, old_anchor = self.a.height(), decode_selector(self.a.block())["btc_height"]
        self.mine_btc(3)
        self.wait_btc(self.b)
        self.wait_upstream(self.a)
        self.wait_upstream(self.b)
        self.prepare_miner()
        require(self.b.stop("usdb-indexer", crash=True) == -signal.SIGKILL, "indexer crash was not injected")
        self.fault_observation("indexer-crash", before, old_anchor)

        self.phase("crash-recovery")
        self.b.start_indexer()
        self.wait_upstream(self.b)
        self.wait_ord(self.b)
        blocks = self.converge_geth(self.b, restart=True)
        self.passed("crash-recovery", blocks=blocks, state_sha256=self.compare_states("crash-recovery", [self.b]),
                    indexer_database_reused=True, validator_restarted=True)

        self.run_balance_outage()
        self.run_ord_outages()

        self.phase("stable-fork")
        before, old_anchor = self.a.height(), decode_selector(self.a.block())["btc_height"]
        fork_height = self.frontier(self.a)[0] + 1
        wallet = RPC(self.a.url("bitcoin") + "/wallet/upstream-matrix", self.a.root / "bitcoin/regtest/.cookie")
        txid = wallet("sendtoaddress", [self.args.owner_address, 1.0])
        self.mine_btc(13)
        self.wait_btc(self.b)
        self.wait_ord(self.b)
        self.wait_upstream(self.a)
        self.wait_upstream(self.b)
        self.prepare_miner()
        canonical_height, canonical_hash = self.frontier(self.a)
        canonical = self.state(self.a, canonical_height)
        require(self.owner in canonical["owners"], "owner missing after confirmed top-up")
        canonical_balance = canonical["owners"][self.owner]["balance"][-1]["balance"]
        btc = self.b.rpc("bitcoin")
        old_tip = btc("getblockcount")
        original = btc("getblockhash", [fork_height])
        btc("setnetworkactive", [False])
        btc("invalidateblock", [original])
        fork_address = wallet("getnewaddress")
        replacements = []
        # Exclude the reverted top-up from replacement blocks. A final block
        # beyond the old tip triggers Ord 0.23.3's reorg detection.
        for _ in range(old_tip - fork_height + 2):
            replacements.append(btc("generateblock", [fork_address, []])["hash"])
        self.wait_ord(self.b)
        self.wait_upstream(self.b)
        fork = self.state(self.b, canonical_height)
        fork_balance = fork["owners"][self.owner]["balance"][-1]["balance"]
        self.write_json(self.args.output_dir / "divergent-b-state.json", fork)
        self.fault_observation("stable-fork", before, old_anchor, fork_depth=old_tip - fork_height + 1,
                               canonical_hash=canonical_hash, fork_hash=btc("getblockhash", [canonical_height]),
                               canonical_balance=canonical_balance, fork_balance=fork_balance, removed_txid=txid)

        fork_epoch = self.b.rpc("usdb-indexer")("get_readiness")["upstream_reorg_epoch"]
        self.b.stop("usdb-indexer")
        self.b.start_indexer(recovery_stage="energy")
        self.wait_upstream(self.b)
        self.phase("recovery-interrupted")
        # Adopt one settled replacement chain. The intermediate tip between
        # invalidateblock and reconsiderblock is an orchestration artifact and
        # must not determine which durable recovery boundary gets interrupted.
        self.b.stop("balance-history")
        btc("invalidateblock", [replacements[0]])
        btc("reconsiderblock", [original])
        # A must exceed B's old fork tip so Ord sees a new trigger block after
        # reconnecting. No database is copied or reset on the recovered node.
        self.mine_btc(2)
        btc("setnetworkactive", [True])
        btc("addnode", [f"127.0.0.1:{self.a.ports['btc-p2p']}", "onetry"])
        self.wait_btc(self.b)
        for node in (self.a, self.b):
            self.wait_ord(node)
        self.wait_upstream(self.a)
        self.b.start_service("balance-history")
        self.wait("B balance-history adopted canonical fork", lambda:
                  self.b.rpc("balance-history")("get_snapshot_info")["stable_block_hash"] == self.frontier(self.a)[1])
        epoch = self.interrupt_pending_recovery("recovery-interrupted", "energy", fork_epoch, fork_height - 1)
        self.b.start_indexer(recovery_stage="transfer")
        resumed_epoch = self.interrupt_pending_recovery("recovery-reinterrupted", "transfer", fork_epoch, fork_height - 1)
        require(resumed_epoch == epoch, "resuming the same recovery incremented reorg epoch")

        self.phase("fork-recovery")
        self.b.start_indexer()
        self.wait_upstream(self.b)
        require(self.pending_recovery_height() is None, "successful recovery retained pending marker")
        readiness = self.b.rpc("usdb-indexer")("get_readiness")
        require(readiness["upstream_reorg_epoch"] == epoch, "final recovery changed reorg epoch")
        blocks = self.converge_geth(self.b, restart=True)
        self.passed("fork-recovery", blocks=blocks, state_sha256=self.compare_states("fork-recovery", [self.b]),
                    original_databases_reused=True, validator_restarted=True, interruptions=2, reorg_epoch=epoch)

        self.phase("fresh-replay")
        require(not self.c.root.exists(), "fresh replay root already exists")
        self.c.fresh_upstream()
        self.c.init_geth()
        self.c.proxy.phase = "fresh-replay"
        self.c.start_geth()
        require(self.c.height() == 0, "fresh validator did not start at genesis")
        # Head readiness precedes historical-anchor backfill during full catch-up.
        # Wait for every actual USDB payload anchor before permitting P2P import.
        anchors = sorted({decode_selector(self.a.block(i))["btc_height"] for i in range(1, self.a.height() + 1)})
        for height in anchors:
            self.state(self.c, height)
        blocks = self.converge_geth(self.c)
        calls = self.c.proxy.profile_calls("fresh-replay")
        successes = [e for e in calls if "error" not in e]
        queried = {e["params"][0]["block_height"] for e in successes}
        require(set(anchors) <= queried, f"full replay skipped historical anchors: {set(anchors) - queried}")
        self.passed("fresh-replay", blocks=blocks, fresh_databases=True, historical_anchors=anchors,
                    profile_successes=len(successes), state_sha256=self.compare_states("fresh-replay", [self.b, self.c]))
        require([case["name"] for case in self.report["cases"]] == list(CASES), "matrix coverage incomplete")
        self.check_alive()

    def close(self):
        errors = []
        for node in reversed(self.nodes):
            for service in reversed(list(node.processes)):
                try:
                    node.stop(service)
                except (OSError, ValueError, subprocess.SubprocessError) as error:
                    errors.append(str(error))
            if node.proxy:
                node.proxy.close()
        require(not errors, f"cleanup failures: {errors}")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--probe-ports", type=int)
    for name in ("work-dir", "output-dir", "usdb-repo", "geth", "bitcoin", "ord", "balance-history", "indexer"):
        parser.add_argument("--" + name, type=Path)
    for name in ("miner-address", "owner-address", "pass-id"):
        parser.add_argument("--" + name)
    parser.add_argument("--port-base", type=int, default=22400)
    parser.add_argument("--timeout-sec", type=int, default=900)
    args = parser.parse_args()
    if args.probe_ports is not None:
        probe_ports(args.probe_ports)
        return
    for name in ("work_dir", "output_dir", "usdb_repo", "geth", "bitcoin", "ord", "balance_history", "indexer", "miner_address", "owner_address", "pass_id"):
        require(getattr(args, name) is not None, f"missing --{name.replace('_', '-')}")
    args.output_dir.mkdir(parents=True, exist_ok=True)
    matrix = Matrix(args)
    def interrupted(signum, _frame):
        raise RuntimeError(f"matrix interrupted by signal {signum}")
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
