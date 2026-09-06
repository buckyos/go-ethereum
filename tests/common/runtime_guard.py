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
    daemon_threads = True
    epoch = 0
    fault = ""
    requests = 0
    failures = 0


class ReadinessHandler(BaseHTTPRequestHandler):
    def do_POST(self) -> None:  # noqa: N802
        length = int(self.headers.get("content-length", "0"))
        request = json.loads(self.rfile.read(length))
        if request.get("method") != "get_readiness":
            self.send_error(404)
            return
        self.server.requests += 1
        fault = self.server.fault
        if fault:
            self.server.failures += 1
        if fault == "http":
            self.send_error(503)
            return
        if fault == "timeout":
            self.server.release_request.wait(15)
        result = {
            "jsonrpc": "2.0",
            "id": request.get("id"),
            "result": {
                "service": "usdb-indexer",
                "consensus_ready": True,
                "upstream_reorg_epoch": self.server.epoch,
            },
        }
        payload = b"invalid json" if fault == "json" else json.dumps(result).encode()
        try:
            self.send_response(200)
            self.send_header("content-type", "application/json")
            self.send_header("content-length", str(len(payload)))
            self.end_headers()
            self.wfile.write(payload)
        except (BrokenPipeError, ConnectionResetError):
            pass

    def log_message(self, _format: str, *_args: object) -> None:
        return


class RuntimeGuardFixture(unittest.TestCase):
    def setUp(self) -> None:
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        self.server = ReadinessServer(("127.0.0.1", 0), ReadinessHandler)
        self.server.epoch = 0
        self.server.release_request = threading.Event()
        self.server_thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.server_thread.start()
        self.processes: list[subprocess.Popen[str]] = []
        self.starts = self.root / "geth-starts.log"
        self.stops = self.root / "geth-stops.log"
        self.events = self.root / "geth-events.log"
        self.validator = self.write_executable("validator.py", "#!/usr/bin/env python3\n")
        self.geth = self.write_executable(
            "fake-geth.py",
            """#!/usr/bin/env python3
import os
import json
import signal
import sys
import time

starts = os.environ["FAKE_GETH_STARTS"]
stops = os.environ["FAKE_GETH_STOPS"]
events = os.environ["FAKE_GETH_EVENTS"]

def stop(_signum, _frame):
    time.sleep(float(os.environ.get("FAKE_GETH_STOP_DELAY", "0")))
    with open(events, "a", encoding="utf-8") as output:
        output.write(json.dumps({"action": "stop", "pid": os.getpid()}) + "\\n")
    with open(stops, "a", encoding="utf-8") as output:
        output.write(f"{os.getpid()}\\n")
    raise SystemExit(0)

signal.signal(signal.SIGTERM, stop)
signal.signal(signal.SIGINT, stop)
with open(events, "a", encoding="utf-8") as output:
    output.write(json.dumps({"action": "start", "pid": os.getpid(), "argv": sys.argv[1:]}) + "\\n")
with open(starts, "a", encoding="utf-8") as output:
    output.write(f"{os.getpid()}\\n")
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
        self.server.release_request.set()
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

    def start_runtime(self, data_dir: Path, **overrides: str) -> subprocess.Popen[str]:
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
                "USDB_HTTP_PORT": str(self.server.server_port),
                "USDB_DEEP_REORG_GUARD_ENABLED": "1",
                "USDB_DEEP_REORG_GUARD_SCRIPT": str(GUARD),
                "USDB_DEEP_REORG_GUARD_POLL_INTERVAL_SECS": "0.1",
                "USDB_DEEP_REORG_GUARD_REQUEST_TIMEOUT_SECS": "0.2",
                "USDB_DEEP_REORG_GUARD_MAX_CONSECUTIVE_ERRORS": "2",
                "FAKE_GETH_STARTS": str(self.starts),
                "FAKE_GETH_STOPS": str(self.stops),
                "FAKE_GETH_EVENTS": str(self.events),
            }
        )
        env.update(overrides)
        # Files avoid pipe backpressure while repeated outage checks are logged.
        output_path = self.root / f"runtime-{len(self.processes)}.log"
        output = output_path.open("w")
        process = subprocess.Popen(
            ["bash", str(RUNTIME)],
            env=env,
            stdout=output,
            stderr=subprocess.STDOUT,
            text=True,
            start_new_session=True,
        )
        output.close()
        process.output_path = output_path
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
            output = process.output_path.read_text()
            self.fail(f"{label} exited with status {status}:\n{output}")

    def assert_group_stopped(self, process: subprocess.Popen[str]) -> None:
        """Signal PID 1 only, as Docker does, and require all children to be reaped."""
        process.send_signal(signal.SIGTERM)
        self.assertEqual(process.wait(timeout=3), 0)
        with self.assertRaises(ProcessLookupError):
            os.killpg(process.pid, 0)
