#!/usr/bin/env python3
"""P1 chain recovery through the shipped supervisor, guard and real geth/upstreams."""
import hashlib
import json
import os
from pathlib import Path
import signal

from node_restart_acceptance import RestartMatrix, RestartNode, main, require

ROOT = Path(__file__).resolve().parents[1]
P1_CASES = ("chain-graceful-restart", "chain-geth-crash")


class RuntimeNode(RestartNode):
    def init_geth(self):
        super().init_geth()
        digest = hashlib.sha256(self.matrix.genesis.read_bytes()).hexdigest()
        identity = dict(chain_id=20260323, network_id=20260323)
        self.manifest = self.root / "genesis.manifest.json"
        self.matrix.write_json(self.manifest, dict(file_sha256=digest, **identity))
        marker = self.root / "geth/bootstrap/usdb-init.done.json"
        marker.parent.mkdir()
        # Match the init entrypoint's field order, which the runtime marker check expects.
        marker.write_text(json.dumps(dict(**identity, genesis_sha256=digest), indent=2) + "\n")

    def start_geth(self):
        env = {**os.environ,
               "USDB_CHAIN_DATA_DIR": str(self.root / "geth"), "USDB_GENESIS_FILE": str(self.matrix.genesis),
               "USDB_GENESIS_MANIFEST_FILE": str(self.manifest),
               "USDB_GENESIS_VALIDATOR": str(ROOT / "scripts/usdb/docker/validate_usdb_genesis.py"),
               "USDB_GETH_BIN": str(self.matrix.args.geth), "USDB_CHAIN_ID": "20260323", "USDB_NETWORK_ID": "20260323",
               "USDB_NODE_ROLE": "full", "USDB_CHAIN_GCMODE": "archive", "USDB_INDEXER_RPC_URL": self.url("audit"),
               "USDB_MINER_ANCHOR_GAP": str(self.anchor_gap),
               "USDB_HTTP_ADDR": "127.0.0.1", "USDB_HTTP_PORT": str(self.ports["geth"]),
               "USDB_WS_ADDR": "127.0.0.1", "USDB_WS_PORT": str(self.ports["geth-ws"]),
               "USDB_AUTHRPC_ADDR": "127.0.0.1", "USDB_AUTHRPC_PORT": str(self.ports["auth"]),
               "USDB_P2P_PORT": str(self.ports["geth-p2p"]), "USDB_MAX_PEERS": "4", "USDB_BOOTNODES": "",
               "USDB_NAT": "none", "USDB_DEEP_REORG_GUARD_ENABLED": "1",
               "USDB_DEEP_REORG_GUARD_SCRIPT": str(ROOT / "scripts/usdb/docker/usdb_deep_reorg_guard.py"),
               "USDB_DEEP_REORG_GUARD_POLL_INTERVAL_SECS": "0.2", "USDB_DEEP_REORG_GUARD_REQUEST_TIMEOUT_SECS": "2",
               "USDB_CHAIN_EXTRA_ARGS": f"--cache 128 --ipcdisable --ethash.dagdir {self.root / 'dag'} "
                                        f"--ethash.dagsondisk 1 --ethash.cachedir {self.root / 'ethash'}"}
        self.start("geth", ["bash", str(ROOT / "scripts/usdb/docker/usdb_runtime_node.sh")], env=env)
        self.matrix.wait("runtime geth RPC", lambda: self.rpc("geth")("eth_chainId") == hex(20260323))

    def geth_child(self):
        supervisor = self.processes["geth"].pid
        children = Path(f"/proc/{supervisor}/task/{supervisor}/children").read_text().split()
        for pid in children:
            try:
                args = Path(f"/proc/{pid}/cmdline").read_bytes().split(b"\0")
            except FileNotFoundError:
                continue
            if args[0] == os.fsencode(self.matrix.args.geth):
                return int(pid)
        raise AssertionError("Supervisor has no real geth child")


class ProcessRestartMatrix(RestartMatrix):
    node_type = RuntimeNode

    def run_cases(self):
        self.report["schema"] = "usdb-node-restart-p1:v1"
        for name in P1_CASES:
            self.phase(name)
            before = self.checkpoint()
            require(self.b.rpc("geth")("eth_mining") is False, "full node unexpectedly mines")
            baseline = (self.guard_dir / "baseline.json").read_bytes()
            child = self.b.geth_child()
            self.b.pinned_geth = None
            if name == "chain-graceful-restart":
                # Docker signals the entrypoint only. Its handler must reap geth and the guard.
                process = self.b.processes.pop("geth")
                try:
                    process.send_signal(signal.SIGTERM)
                    require(process.wait(timeout=30) == 0, "graceful runtime shutdown failed")
                finally:
                    if process.poll() is None:
                        self.b.processes["geth"] = process
            else:
                process = self.b.processes.pop("geth")
                try:
                    os.kill(child, signal.SIGKILL)
                    require(process.wait(timeout=15) != 0, "geth crash was hidden by supervisor")
                finally:
                    if process.poll() is None:
                        self.b.processes["geth"] = process
            try:
                os.killpg(process.pid, 0)
            except ProcessLookupError:
                pass
            else:
                os.killpg(process.pid, signal.SIGKILL)
                raise AssertionError("Runtime leaked children after shutdown")
            self.record("runtime-stopped", case=name, supervisor=process.pid, geth=child, exit=process.returncode)
            self.start_validator()
            self.converge_geth(self.b)
            self.preserved(before)
            require(self.b.rpc("geth")("eth_mining") is False, "restart changed the full-node role")
            require((self.guard_dir / "baseline.json").read_bytes() == baseline, "guard baseline changed")
            after = self.identities()
            require(after["geth"] != before["identities"]["geth"], "chain runtime was not replaced")
            require(all(after[s] == identity for s, identity in before["identities"].items() if s != "geth"),
                    "chain restart disrupted an upstream service")
            self.passed(name, before=before["identities"], after=after, **self.fresh_work(name))
        require([c["name"] for c in self.report["cases"]] == list(P1_CASES), "P1 chain coverage incomplete")


if __name__ == "__main__":
    main(ProcessRestartMatrix)
