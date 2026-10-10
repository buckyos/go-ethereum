#!/usr/bin/env python3
"""Run real usdb-node down/up on an explicitly designated deployment fixture.

Read-only planning is the default. --execute authorizes stopping this fixture.
The node must already be READY with a real validated release and configured peers.
This does not create a mainnet snapshot or weaken any release/readiness checks.
"""

import argparse
import hashlib
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import time
from urllib import request
from urllib.parse import urlparse

from common.deployment_fixture import read_fixture, require, validate_progress, validate_recovery, verify_mount_isolation


class Acceptance:
    def __init__(self, fixture, output, timeout):
        self.fixture, self.output = fixture, output
        self.deadline = time.monotonic() + timeout
        self.cli = [fixture["launcher"], "--kit-root", fixture["kit_root"], "--node-env", fixture["node_env"]]
        self.report = {"schema": "usdb-node-deployment-restart:v1", "status": "running", "steps": []}
        self.sequence = 0

    def save(self):
        (self.output / "summary.json").write_text(json.dumps(self.report, indent=2) + "\n")

    def command(self, args, label, *, allowed=(0,)):
        remaining = self.deadline - time.monotonic()
        require(remaining > 0, "deployment acceptance time budget exhausted")
        started = time.monotonic()
        self.sequence += 1
        path = self.output / f"{self.sequence:03d}-{label}.log"
        # Non-interactive CI must have its required Docker/sudo access prepared.
        # Kill only the CLI wait on timeout; never force-kill any node container.
        with path.open("w") as log:
            process = subprocess.Popen(args, stdin=subprocess.DEVNULL, stdout=log,
                                       stderr=subprocess.STDOUT, start_new_session=True)
            try:
                process.wait(timeout=min(remaining, 120 if label.startswith("probe-") else remaining))
            except BaseException:
                if process.poll() is None:
                    os.killpg(process.pid, signal.SIGTERM)
                    try:
                        process.wait(timeout=10)
                    except subprocess.TimeoutExpired:
                        os.killpg(process.pid, signal.SIGKILL)
                        process.wait()
                raise
        self.report["steps"].append({"command": label, "exit": process.returncode,
                                     "seconds": round(time.monotonic() - started, 2), "log": path.name})
        self.save()
        require(process.returncode in allowed, f"{label} failed ({process.returncode}); inspect {path}")
        return path.read_text()

    def cli_json(self, *args):
        text = self.command(self.cli + list(args), "probe-" + args[0], allowed=(0, 1))
        # Artifact verification diagnostics can precede the JSON envelope.
        start = next((i for i, line in enumerate(text.splitlines()) if line.startswith("{")), None)
        require(start is not None, "CLI did not emit a JSON report")
        return json.loads("\n".join(text.splitlines()[start:]))

    def containers(self):
        ids = self.command(["docker", "ps", "-aq"], "probe-docker-list").split()
        require(ids, "no Docker containers")
        # Project labels, mounts and lifecycle only; never read/log container environments.
        template = ('{"Id":{{json .Id}},"Name":{{json .Name}},"State":{"Running":{{json .State.Running}}},'
                    '"Mounts":{{json .Mounts}},"Config":{"Labels":{"com.docker.compose.project":'
                    '{{json (index .Config.Labels "com.docker.compose.project")}}}}}')
        raw = self.command(["docker", "inspect", "--format", template, *ids], "probe-docker-inspect")
        all_containers = [json.loads(line) for line in raw.splitlines()]
        return verify_mount_isolation(all_containers, self.projects, self.fixture)

    def rpc(self, service, method, params=None):
        payload = json.dumps({"jsonrpc": "2.0", "id": 1, "method": method, "params": params or []}).encode()
        req = request.Request(self.fixture[service + "_rpc"], payload, {"Content-Type": "application/json"})
        with request.urlopen(req, timeout=15) as response:
            result = json.load(response)
        require(not result.get("error"), f"{service}/{method}: {result.get('error')}")
        return result["result"]

    def evidence(self, baseline=None):
        indexer = self.rpc("indexer", "get_readiness")
        bh = self.rpc("balance_history", "get_readiness")
        require(indexer["consensus_ready"] and bh["consensus_ready"], "live upstream is not ready")
        stable = indexer["synced_block_height"]
        height = baseline["stable_height"] if baseline else stable
        block = self.rpc("chain", "eth_getBlockByNumber", ["latest", False])
        tag = hex(baseline["chain_height"]) if baseline else block["number"]
        files = [self.fixture["node_env"], *self.fixture["identity_files"]]
        containers = self.containers()
        return {"chain_id": self.rpc("chain", "eth_chainId"),
                "genesis_hash": self.rpc("chain", "eth_getBlockByNumber", ["0x0", False])["hash"],
                "chain_height": int(block["number"], 16), "stable_height": stable,
                "historical_chain_hash": self.rpc("chain", "eth_getBlockByNumber", [tag, False])["hash"],
                "balance_ref": self.rpc("balance_history", "get_state_ref_at_height", [{"block_height": height}]),
                "indexer_ref": self.rpc("indexer", "get_state_ref_at_height", [{"block_height": height}]),
                "epoch": indexer["upstream_reorg_epoch"],
                "identities": [hashlib.sha256(Path(p).read_bytes()).hexdigest() for p in files],
                "containers": {c["Name"]: c["Id"] for c in containers if c["State"]["Running"]}}

    def run(self):
        version = self.cli_json("version", "--json")
        bundle = version["network"]["bundle_id"]
        self.projects = {bundle, bundle + "-bitcoin"}
        config = self.cli_json("config", "--json")
        values = {s["key"]: s["value"] for g in config["groups"] for s in g["settings"] if s["source"] == "node.env"}
        require(values.get("USDB_NODE_ROLE") == "full", "deployment fixture must be a full node")
        from common.deployment_fixture import inside
        for key in ("BTC_NODE_DATA_HOST_DIR", "BH_DATA_HOST_DIR", "USDB_INDEXER_DATA_HOST_DIR", "USDB_CHAIN_DATA_HOST_DIR"):
            require(values.get(key) and inside(values[key], self.fixture["data_root"]), f"{key} escapes fixture")
        for service, key in (("balance_history", "BH_BIND_PORT"), ("indexer", "USDB_INDEXER_BIND_PORT"),
                             ("chain", "USDB_HTTP_BIND_PORT")):
            require(str(urlparse(self.fixture[service + "_rpc"]).port) == str(values.get(key)),
                    f"{service} RPC does not match this node's saved port")
        require(self.cli_json("status", "--json")["overall_state"] == "READY", "fixture must start READY")
        before = self.evidence()
        self.report.update(release_id=version["release_id"], network=bundle, before=before)
        self.save()
        restart_started = time.time()
        self.command(self.cli + ["down"], "down")
        # Compose normally removes runtime containers; no selected project may remain running.
        running = self.command(["docker", "ps", "--format", "{{.Label \"com.docker.compose.project\"}}"], "probe-stopped")
        require(not self.projects.intersection(running.splitlines()), "down left a node service running")
        self.command(self.cli + ["up", "--no-watch"], "up")
        consecutive = 0
        while time.monotonic() < self.deadline:
            status = self.cli_json("status", "--json")
            if status["overall_state"] == "READY":
                consecutive += 1
                if consecutive >= 2:
                    after = self.evidence(before)
                    if after["stable_height"] > before["stable_height"] and after["chain_height"] > before["chain_height"]:
                        validate_recovery(before, after)
                        progress = self.cli_json("status", "--progress-json")
                        validate_progress(progress, restart_started)
                        self.report.update(status="ok", after=after, progress=progress)
                        self.save()
                        return
            else:
                consecutive = 0
            time.sleep(min(15, max(0, self.deadline - time.monotonic())))
        raise TimeoutError("node did not recover and process fresh BTC/USDB work within the budget")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--fixture", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--timeout-secs", type=int, default=5400)
    parser.add_argument("--execute", action="store_true")
    args = parser.parse_args()
    fixture = read_fixture(args.fixture)
    require(args.timeout_secs > 0, "timeout must be positive")
    if not args.execute:
        print(json.dumps({"mode": "plan", "hostname": fixture["hostname"], "kit": fixture["kit_root"],
                          "steps": ["verify isolation and READY", "usdb-node down", "verify stopped", "usdb-node up",
                                    "verify preserved history, identity and fresh work"],
                          "execute": "Use --execute only on the dedicated test fixture."}, indent=2))
        return
    args.output.mkdir(parents=True, exist_ok=False)
    args.output.chmod(0o700)
    runner = Acceptance(fixture, args.output, args.timeout_secs)
    try:
        runner.run()
    except BaseException as error:
        runner.report.update(status="failed", error=str(error),
                             recovery="Inspect the fixture before retrying. No data reset or forced container stop was performed.")
        runner.save()
        raise


if __name__ == "__main__":
    main()
