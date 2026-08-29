#!/usr/bin/env python3

from __future__ import annotations

import hashlib
import json
import os
import signal
import subprocess
import tempfile
import threading
import time
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import Callable


ROOT = Path(__file__).resolve().parents[2]
RUNTIME = ROOT / "scripts" / "usdb" / "docker" / "usdb_runtime_node.sh"
GUARD = ROOT / "scripts" / "usdb" / "docker" / "usdb_deep_reorg_guard.py"


class ReadinessServer(ThreadingHTTPServer):
    epoch = 0


class ReadinessHandler(BaseHTTPRequestHandler):
    def do_POST(self) -> None:  # noqa: N802
        length = int(self.headers.get("content-length", "0"))
        request = json.loads(self.rfile.read(length))
        if request.get("method") != "get_readiness":
            self.send_error(404)
            return
        result = {
            "jsonrpc": "2.0",
            "id": request.get("id"),
            "result": {
                "service": "usdb-indexer",
                "consensus_ready": True,
                "upstream_reorg_epoch": self.server.epoch,
            },
        }
        payload = json.dumps(result).encode()
        self.send_response(200)
        self.send_header("content-type", "application/json")
        self.send_header("content-length", str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)

    def log_message(self, _format: str, *_args: object) -> None:
        return


class RuntimeDeepReorgTest(unittest.TestCase):
    def setUp(self) -> None:
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        self.server = ReadinessServer(("127.0.0.1", 0), ReadinessHandler)
        self.server.epoch = 0
        self.server_thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.server_thread.start()
        self.processes: list[subprocess.Popen[str]] = []
        self.starts = self.root / "geth-starts.log"
        self.stops = self.root / "geth-stops.log"
        self.validator = self.write_executable("validator.py", "#!/usr/bin/env python3\n")
        self.geth = self.write_executable(
            "fake-geth.py",
            """#!/usr/bin/env python3
import os
import signal
import time

starts = os.environ["FAKE_GETH_STARTS"]
stops = os.environ["FAKE_GETH_STOPS"]
with open(starts, "a", encoding="utf-8") as output:
    output.write(f"{os.getpid()}\\n")

def stop(_signum, _frame):
    with open(stops, "a", encoding="utf-8") as output:
        output.write(f"{os.getpid()}\\n")
    raise SystemExit(0)

signal.signal(signal.SIGTERM, stop)
signal.signal(signal.SIGINT, stop)
while True:
    time.sleep(0.1)
""",
        )
        self.genesis = self.root / "genesis.json"
        self.manifest = self.root / "manifest.json"
        self.genesis.write_text("{}\n", encoding="utf-8")
        self.manifest.write_text("{}\n", encoding="utf-8")

    def tearDown(self) -> None:
        for process in self.processes:
            self.stop_process(process)
        self.server.shutdown()
        self.server.server_close()
        self.server_thread.join(timeout=2)
        self.temp.cleanup()

    def write_executable(self, name: str, content: str) -> Path:
        path = self.root / name
        path.write_text(content, encoding="utf-8")
        path.chmod(0o755)
        return path

    def prepare_data_dir(self, name: str) -> Path:
        data_dir = self.root / name
        (data_dir / "geth" / "chaindata").mkdir(parents=True)
        (data_dir / "geth" / "chaindata" / "CURRENT").write_text("fixture\n")
        marker = data_dir / "bootstrap" / "usdb-init.done.json"
        marker.parent.mkdir(parents=True)
        genesis_sha = hashlib.sha256(self.genesis.read_bytes()).hexdigest()
        marker.write_text(
            json.dumps(
                {
                    "genesis_sha256": genesis_sha,
                    "chain_id": 9001,
                    "network_id": 9001,
                    "test_fixture": True,
                },
                indent=2,
            )
            + "\n",
            encoding="utf-8",
        )
        return data_dir

    def start_runtime(self, data_dir: Path) -> subprocess.Popen[str]:
        env = os.environ.copy()
        env.update(
            {
                "USDB_CHAIN_DATA_DIR": str(data_dir),
                "USDB_GENESIS_FILE": str(self.genesis),
                "USDB_GENESIS_MANIFEST_FILE": str(self.manifest),
                "USDB_GENESIS_VALIDATOR": str(self.validator),
                "USDB_GETH_BIN": str(self.geth),
                "USDB_CHAIN_ID": "9001",
                "USDB_NETWORK_ID": "9001",
                "USDB_INDEXER_RPC_URL": f"http://127.0.0.1:{self.server.server_port}",
                "USDB_DEEP_REORG_GUARD_ENABLED": "1",
                "USDB_DEEP_REORG_GUARD_SCRIPT": str(GUARD),
                "USDB_DEEP_REORG_GUARD_POLL_INTERVAL_SECS": "0.1",
                "USDB_DEEP_REORG_GUARD_REQUEST_TIMEOUT_SECS": "0.2",
                "USDB_DEEP_REORG_GUARD_MAX_CONSECUTIVE_ERRORS": "2",
                "FAKE_GETH_STARTS": str(self.starts),
                "FAKE_GETH_STOPS": str(self.stops),
            }
        )
        process = subprocess.Popen(
            ["bash", str(RUNTIME)],
            env=env,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
            start_new_session=True,
        )
        self.processes.append(process)
        return process

    def stop_process(self, process: subprocess.Popen[str]) -> None:
        if process.poll() is None:
            os.killpg(process.pid, signal.SIGTERM)
            try:
                process.wait(timeout=3)
            except subprocess.TimeoutExpired:
                os.killpg(process.pid, signal.SIGKILL)
                process.wait(timeout=3)
        if process.stdout is not None:
            process.stdout.close()

    def wait_for(self, predicate: Callable[[], bool], message: str, timeout: float = 8.0) -> None:
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            if predicate():
                return
            time.sleep(0.05)
        self.fail(message)

    def line_count(self, path: Path) -> int:
        return len(path.read_text(encoding="utf-8").splitlines()) if path.exists() else 0

    def assert_running(self, process: subprocess.Popen[str], label: str) -> None:
        status = process.poll()
        if status is not None:
            output = process.communicate(timeout=1)[0]
            self.fail(f"{label} exited with status {status}:\n{output}")

    def test_halts_restart_and_accepts_empty_generation(self) -> None:
        old_data = self.prepare_data_dir("old-generation")
        old_runtime = self.start_runtime(old_data)
        time.sleep(0.2)
        self.assert_running(old_runtime, "old generation runtime")
        baseline = old_data / "recovery" / "deep-btc-reorg" / "baseline.json"
        incident = old_data / "recovery" / "deep-btc-reorg" / "halted.json"
        self.wait_for(
            lambda: baseline.exists() and self.line_count(self.starts) == 1,
            "old generation did not start",
        )

        self.server.epoch = 1
        self.wait_for(lambda: incident.exists(), "deep reorg incident was not persisted")
        self.wait_for(lambda: self.line_count(self.stops) == 1, "old geth process was not stopped")
        self.assertIsNone(old_runtime.poll(), "halted runtime must stay alive for inspection")
        self.stop_process(old_runtime)

        restarted = self.start_runtime(old_data)
        time.sleep(0.5)
        self.assertEqual(self.line_count(self.starts), 1, "restart bypassed the incident latch")
        self.assertIsNone(restarted.poll(), "latched restart must remain halted")
        self.stop_process(restarted)

        new_data = self.prepare_data_dir("new-generation")
        new_runtime = self.start_runtime(new_data)
        new_baseline = new_data / "recovery" / "deep-btc-reorg" / "baseline.json"
        self.wait_for(
            lambda: new_baseline.exists() and self.line_count(self.starts) == 2,
            "empty network generation did not start",
        )
        self.assertFalse((new_baseline.parent / "halted.json").exists())
        baseline_value = json.loads(new_baseline.read_text(encoding="utf-8"))
        self.assertEqual(baseline_value["upstream_reorg_epoch"], 1)
        self.stop_process(new_runtime)


if __name__ == "__main__":
    unittest.main()
