#!/usr/bin/env python3
"""Exercise timeout evidence and the profile runners' strict sync assertions."""

import contextlib
from http.client import IncompleteRead
import io
import json
from pathlib import Path
import re
import shlex
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[1]
SCRIPTS = ROOT / "scripts/usdb"
sys.path.insert(0, str(SCRIPTS))
import collect_sync_diagnostics as diagnostics


HASH_A = "0x" + "a" * 64
HASH_B = "0x" + "b" * 64


def shell_function(script: str, name: str) -> str:
    source = (SCRIPTS / script).read_text()
    return re.search(rf"(?ms)^{name}\(\) \{{\n.*?^\}}", source).group()


class SyncDiagnosticsTests(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.root = Path(directory.name)
        self.output = self.root / "geth/sync-diagnostics.json"
        self.nodes = [("miner", "http://miner"), ("validator", "http://validator")]

    def collect(self):
        return diagnostics.collect(self.output, self.nodes, "validator height timeout", 6, HASH_A)

    def test_captures_both_local_heads_peer_td_and_syncing_without_mutations(self):
        requests = []
        td = 2**120

        def rpc(request, timeout):
            self.assertEqual(timeout, 1)
            payload = json.loads(request.data)
            method = payload["method"]
            miner = request.full_url == "http://miner"
            requests.append((request.full_url, method))
            responses = {
                "eth_getBlockByNumber": {"number": "0x6" if miner else "0x5",
                                         "hash": HASH_A if miner else HASH_B, "totalDifficulty": hex(td)},
                "eth_syncing": False if miner else {"currentBlock": "0x5", "highestBlock": "0x6"},
                "admin_peers": [{"id": "other-node", "protocols": {"eth": {"head": HASH_B, "difficulty": td}}}],
                "admin_nodeInfo": {"id": "self", "protocols": {"eth": {"head": HASH_A, "difficulty": td + 1}}},
                "net_peerCount": "0x1",
            }
            if method == "eth_getBlockByNumber":
                self.assertEqual(payload["params"], ["latest", False])
            else:
                self.assertEqual(payload["params"], [])
            return io.BytesIO(json.dumps({"result": responses[method]}).encode())

        stdout, stderr = io.StringIO(), io.StringIO()
        with patch.object(diagnostics.urllib.request, "urlopen", side_effect=rpc), \
                contextlib.redirect_stdout(stdout), contextlib.redirect_stderr(stderr):
            report = self.collect()
        self.assertEqual(json.loads(self.output.read_text()), report)
        self.assertEqual(report["status"], "complete")
        self.assertEqual(report["expected_height"], 6)
        self.assertEqual(report["expected_hash"], HASH_A)
        self.assertEqual(report["nodes"]["miner"]["head"]["result"]["hash"], HASH_A)
        self.assertEqual(report["nodes"]["validator"]["head"]["result"]["number"], "0x5")
        self.assertIs(report["nodes"]["miner"]["syncing"]["result"], False)
        self.assertEqual(report["nodes"]["validator"]["syncing"]["result"]["highestBlock"], "0x6")
        self.assertEqual(report["nodes"]["validator"]["peers"]["result"][0]["protocols"]["eth"]["difficulty"], td)
        self.assertEqual(report["nodes"]["validator"]["peer_count"]["result"], "0x1")
        self.assertEqual(requests[:2], [(url, "eth_getBlockByNumber") for _, url in self.nodes])
        self.assertEqual(len(requests), 10)
        self.assertEqual(stdout.getvalue(), "")
        self.assertIn(HASH_A, stderr.getvalue())
        self.assertIn(str(td), stderr.getvalue())

    def test_unavailable_node_does_not_hide_the_other_node(self):
        def rpc(request, timeout):
            if request.full_url == "http://validator":
                raise ConnectionRefusedError("upstream unavailable")
            return io.BytesIO(b'{"result":false}')

        with patch.object(diagnostics.urllib.request, "urlopen", side_effect=rpc), \
                contextlib.redirect_stderr(io.StringIO()):
            report = self.collect()
        for field, _, _ in diagnostics.QUERIES:
            self.assertIn("ConnectionRefusedError", report["nodes"]["validator"][field]["error"])
            self.assertNotIn("result", report["nodes"]["validator"][field])
            self.assertIs(report["nodes"]["miner"][field]["result"], False)

    def test_bad_rpc_responses_are_errors_not_zero_or_synced(self):
        for response in (b'not json', b'[]', b'{}', b'{"error":{"code":-32601,"message":"unavailable"}}'):
            with self.subTest(response=response), \
                    patch.object(diagnostics.urllib.request, "urlopen", return_value=io.BytesIO(response)):
                result = diagnostics.query("http://validator", "eth_syncing", [])
                self.assertIn("error", result)
                self.assertNotIn("result", result)
        for error in (TimeoutError("slow RPC"), IncompleteRead(b'{"result"')):
            with self.subTest(error=error), patch.object(diagnostics.urllib.request, "urlopen", side_effect=error):
                self.assertIn(type(error).__name__, diagnostics.query("http://validator", "admin_peers", [])["error"])

    def test_interrupted_collection_preserves_partial_json(self):
        with patch.object(diagnostics, "query", side_effect=[{"result": {"hash": HASH_A}}, KeyboardInterrupt]), \
                contextlib.redirect_stderr(io.StringIO()), self.assertRaises(KeyboardInterrupt):
            self.collect()
        report = json.loads(self.output.read_text())
        self.assertEqual(report["status"], "collecting")
        self.assertEqual(report["nodes"]["miner"]["head"]["result"]["hash"], HASH_A)
        self.assertNotIn("head", report["nodes"]["validator"])

    def test_long_ci_collects_the_json_artifact(self):
        self.output.parent.mkdir(parents=True)
        self.output.write_text('{"schema":"usdb-sync-diagnostics:v1"}\n')
        output_root = self.root / "ci-output"
        result = subprocess.run(["bash", "-c", '''
set -euo pipefail
source "$1/scripts/usdb/run_long_ci.sh"
WORK_ROOT="$2" OUTPUT_ROOT="$3"
collect_diagnostics
''', "test", str(ROOT), str(self.root / "geth"), str(output_root)],
                                capture_output=True, text=True, timeout=5)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual((output_root / "diagnostics/sync-diagnostics.json").read_text(), self.output.read_text())

    def run_shell(self, body, collector_exit=0):
        # Run the actual shell wrappers, but replace only the bounded collector
        # process. Its output must stay on stderr and never change test status.
        prelude = f'''
set -euo pipefail
ROOT_DIR={shlex.quote(str(ROOT))}
USDB_CHAIN_WORK_DIR={shlex.quote(str(self.root))}
HTTP_ADDR=127.0.0.1 HTTP_PORT=1 VALIDATOR_HTTP_ADDR=127.0.0.1 VALIDATOR_HTTP_PORT=2
NODE1_HTTP_ADDR=127.0.0.1 NODE1_HTTP_PORT=1 NODE2_HTTP_ADDR=127.0.0.1 NODE2_HTTP_PORT=2
NETWORK_ID=20260323
source "$ROOT_DIR/scripts/usdb/lib/sync_diagnostics.sh"
timeout() {{ printf 'collector %s\\n' "$*"; return {collector_exit}; }}
'''
        return subprocess.run(["bash", "-c", prelude + body], capture_output=True, text=True, timeout=5)

    def test_timeout_paths_keep_failure_and_capture_both_nodes(self):
        cases = (
            ("run_usdb_profile_e2e.sh", "usdb_chain_wait_rpc_ready", ""),
            ("run_usdb_profile_e2e.sh", "usdb_chain_wait_rpc_url_ready", "http://validator"),
            ("run_usdb_profile_e2e.sh", "usdb_chain_fetch_enode", "http://miner"),
            ("run_usdb_profile_e2e.sh", "usdb_chain_wait_block_height", "6"),
            ("run_usdb_profile_e2e.sh", "usdb_chain_wait_block_height_url", "http://validator 6"),
            ("run_usdb_profile_historical_stability_e2e.sh", "wait_chain_id", "http://validator"),
            ("run_usdb_profile_historical_stability_e2e.sh", "fetch_enode", "http://miner"),
            ("run_usdb_profile_historical_stability_e2e.sh", "wait_block_height", "http://validator 6"),
            ("run_usdb_profile_historical_stability_e2e.sh", "wait_peers", "http://validator 1"),
        )
        for script, function, args in cases:
            for collector_exit in (0, 124):
                with self.subTest(function=function, collector_exit=collector_exit):
                    body = shell_function(script, "usdb_chain_dump_sync_diagnostics") + "\n"
                    body += shell_function(script, function) + f'\nBLOCK_WAIT_SECONDS=0 RPC_WAIT_SECONDS=0\nvalue="$({function} {args})"\n'
                    result = self.run_shell(body, collector_exit)
                    self.assertEqual(result.returncode, 1, result.stderr)
                    self.assertEqual(result.stdout, "")
                    self.assertIn("Timed out waiting", result.stderr)
                    self.assertIn("--node miner http://127.0.0.1:1 --node validator http://127.0.0.1:2", result.stderr)
                    self.assertIn("--kill-after=1s 12s python3", result.stderr)
                    if "height" in function:
                        self.assertIn("--expected-height 6", result.stderr)

    def test_final_hash_and_height_assertions_remain_strict(self):
        for script in ("run_usdb_profile_e2e.sh", "run_usdb_profile_historical_stability_e2e.sh"):
            wrapper = shell_function(script, "usdb_chain_dump_sync_diagnostics")
            if script == "run_usdb_profile_e2e.sh":
                assertion = shell_function(script, "usdb_chain_assert_validator_synced") + "\nusdb_chain_assert_validator_synced 6\n"
            else:
                # Exercise the historical runner's exact final assertion block.
                source = (SCRIPTS / script).read_text()
                assertion = source.split('    if [[ ! "$node1_tip_hash"', 1)[1].split('    blocks_file=', 1)[0]
                assertion = '    if [[ ! "$node1_tip_hash"' + assertion
            for miner_hash, validator_hash, height, valid in (
                (HASH_A, HASH_A, 6, True),
                (HASH_A, HASH_B, 6, False),
                (HASH_A, HASH_A, 7, False),
                ("", "", 6, False),
            ):
                with self.subTest(script=script, validator_hash=validator_hash, height=height):
                    body = f'''
node1_tip_hash={shlex.quote(miner_hash)} node2_tip_hash={shlex.quote(validator_hash)}
node1_tip_height=6 node2_tip_height={height}
usdb_chain_wait_block_height_url() {{ echo "$node2_tip_height"; }}
usdb_chain_head_hash_at() {{
  if [[ "$1" == http://127.0.0.1:1 ]]; then echo "$node1_tip_hash"; else echo "$node2_tip_hash"; fi
}}
'''
                    result = self.run_shell(body + wrapper + "\n" + assertion, collector_exit=124)
                    self.assertEqual(result.returncode, 0 if valid else 1, result.stderr)
                    self.assertEqual(result.stdout, "")
                    self.assertEqual("collector" in result.stderr, not valid)


if __name__ == "__main__":
    unittest.main()
