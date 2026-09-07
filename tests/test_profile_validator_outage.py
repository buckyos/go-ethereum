#!/usr/bin/env python3
"""Exercise the profile outage gate and the shell's recovery ordering."""

import io
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "scripts/usdb"))
import verify_profile_validator_outage as gate


UPSTREAM = "http://127.0.0.1:28320"
FAILURE = ('WARN [09-07|07:54:18.900] Invalid header encountered number=1 '
           'err="failed to resolve usdb difficulty profile: failed to call get_pass_economic_profile: '
           r'usdb-indexer rpc get_pass_economic_profile failed for \"http://127.0.0.1:28320\": connection refused"' + "\n")


class ValidatorOutageTests(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.root = Path(directory.name)
        self.log = self.root / "validator.log"
        self.log.write_text("")
        self.now = 0.0

    def run_gate(self, on_poll=None, **overrides):
        def sleep(seconds):
            self.now += seconds

        def height(*args):
            return on_poll() if on_poll else 0

        args = dict(log=self.log, offset=0, pid=os.getpid(), rpc="http://validator",
                    upstream=UPSTREAM, timeout=25, observe_seconds=4)
        args.update(overrides)
        with patch.object(gate.time, "monotonic", side_effect=lambda: self.now), \
                patch.object(gate.time, "sleep", side_effect=sleep), \
                patch.object(gate, "validator_height", side_effect=height):
            return gate.wait_for_failure(**args)

    def test_waits_for_delayed_real_query_and_full_observation(self):
        def poll():
            if self.now >= 12 and self.log.stat().st_size == 0:
                self.log.write_text(FAILURE)
            return 0

        result = self.run_gate(poll)
        self.assertGreaterEqual(result["failure_observed_after_seconds"], 12)
        self.assertGreaterEqual(result["observed_seconds"], 4)
        self.assertEqual(result["height"], 0)
        self.assertIn("get_pass_economic_profile", result["failure_evidence"])

    def test_quiet_validator_and_stale_log_cannot_pass(self):
        for stale in (False, True):
            with self.subTest(stale=stale):
                self.now = 0
                self.log.write_text(FAILURE if stale else "Starting peer-to-peer node\n")
                with self.assertRaisesRegex(ValueError, "timed out"):
                    self.run_gate(offset=self.log.stat().st_size if stale else 0)

    def test_failure_near_deadline_does_not_shorten_observation(self):
        def poll():
            if self.now >= 23:
                self.log.write_text(FAILURE)
            return 0

        with self.assertRaisesRegex(ValueError, "full observation window"):
            self.run_gate(poll)

    def test_rejects_imports_before_and_after_failed_query(self):
        for after_failure in (False, True):
            with self.subTest(after_failure=after_failure):
                self.now = 0
                self.log.write_text(FAILURE if after_failure else "")
                with self.assertRaisesRegex(ValueError, "imported blocks"):
                    self.run_gate(lambda: int(not after_failure or self.now >= 2))

    def test_rejects_process_exit_replacement_and_log_truncation(self):
        with patch.object(gate, "process_identity", side_effect=OSError("process exited")):
            with self.assertRaises(OSError):
                self.run_gate()
        with patch.object(gate, "process_identity", side_effect=[{"pid": 1}, {"pid": 2}]):
            with self.assertRaisesRegex(ValueError, "process changed"):
                self.run_gate()
        self.log.write_text(FAILURE)
        with self.assertRaisesRegex(ValueError, "truncated"):
            self.run_gate(lambda: self.log.write_text("") or 0, offset=len(FAILURE.encode()))

    def test_rejects_unrelated_errors_and_wrong_endpoint(self):
        self.assertTrue(gate.is_profile_transport_failure(FAILURE, UPSTREAM))
        for line in (
            FAILURE.replace("28320", "28321"),
            FAILURE.replace("get_pass_economic_profile", "get_system_state_info"),
            FAILURE.replace("Invalid header encountered", "USDB sealing work unavailable"),
            FAILURE.replace("connection refused", "request canceled"),
            'WARN Invalid header encountered err="SNAPSHOT_ID_MISMATCH"',
            "Starting peer-to-peer node",
        ):
            with self.subTest(line=line):
                self.assertFalse(gate.is_profile_transport_failure(line, UPSTREAM))

    def test_rpc_errors_and_missing_height_do_not_mean_genesis(self):
        for response in ({"error": {"code": -32603}}, {}, {"result": None}, {"result": ""}, [], {"result": 0}):
            with self.subTest(response=response):
                with patch.object(gate.urllib.request, "urlopen", return_value=io.BytesIO(json.dumps(response).encode())):
                    with self.assertRaises(ValueError):
                        gate.validator_height("http://validator", 1)
        with patch.object(gate.urllib.request, "urlopen", return_value=io.BytesIO(b'{"result":"0x0"}')):
            self.assertEqual(gate.validator_height("http://validator", 1), 0)

    def test_shell_recovery_requires_successful_gate(self):
        source = (ROOT / "scripts/usdb/run_usdb_profile_e2e.sh").read_text()
        function = re.search(r"(?ms)^run_indexer_outage_recovery_check\(\) \{\n.*?^\}", source).group()
        for gate_exit in (1, 0):
            with self.subTest(gate_exit=gate_exit):
                self.log.write_text("")
                prelude = r'''
set -euo pipefail
events="$1"
VALIDATOR_LOG_FILE="$2"
ROOT_DIR=unused VALIDATOR_PID=123 VALIDATOR_HTTP_ADDR=127.0.0.1 VALIDATOR_HTTP_PORT=1234
USDB_INDEXER_RPC_PORT=28320 RPC_WAIT_SECONDS=90 OUTAGE_OBSERVE_SECONDS=4 USDB_CHAIN_WORK_DIR=unused
usdb_chain_log() { :; }
regtest_log() { :; }
usdb_chain_stop_mining() { :; }
usdb_chain_start_mining() { :; }
regtest_stop_usdb_indexer() { echo stop-upstream >>"$events"; }
usdb_chain_start_validator() { :; }
usdb_chain_wait_rpc_url_ready() { :; }
usdb_chain_connect_validator() { :; }
usdb_chain_current_height() { echo 0; }
usdb_chain_wait_block_height() { echo 2; }
usdb_chain_assert_validator_synced() { :; }
usdb_chain_stop_validator() { :; }
regtest_wait_usdb_rpc_ready() { :; }
regtest_wait_usdb_consensus_ready() { :; }
regtest_start_usdb_indexer() { echo restore-upstream >>"$events"; }
sleep() { :; }
python3() { echo gate >>"$events"; return "$3"; }
'''.replace('return "$3"', f"return {gate_exit}")
                events = self.root / f"events-{gate_exit}"
                result = subprocess.run(["bash", "-c", prelude + function + "\nrun_indexer_outage_recovery_check\n",
                                         "test", str(events), str(self.log)], capture_output=True, text=True)
                self.assertEqual(result.returncode, gate_exit, result.stderr)
                self.assertEqual(events.read_text().splitlines(),
                                 ["stop-upstream", "gate"] + ([] if gate_exit else ["restore-upstream"]))

    def test_validator_launch_explicitly_selects_full(self):
        source = (ROOT / "scripts/usdb/run_usdb_profile_e2e.sh").read_text()
        function = re.search(r"(?ms)^usdb_chain_start_validator\(\) \{\n.*?^\}", source).group()
        # Capture the actual exec arguments without starting geth.
        prelude = r'''
set -euo pipefail
ROOT_DIR="$1" VALIDATOR_LOG_FILE="$1/launch.log" VALIDATOR_DATADIR="$1/data"
NETWORK_ID=1 USDB_CHAIN_GCMODE=archive VALIDATOR_HTTP_ADDR=127.0.0.1
VALIDATOR_HTTP_PORT=1 VALIDATOR_AUTHRPC_PORT=2 VALIDATOR_P2P_PORT=3 USDB_INDEXER_RPC_PORT=4 USDB_QUERY_TIMEOUT=1s
POST_ACTIVATION_GETH_CMD=(/usr/bin/printf '%s\n')
'''
        subprocess.run(["bash", "-c", prelude + function + '\nusdb_chain_start_validator\nwait "$VALIDATOR_PID"\n',
                        "test", str(self.root)], check=True)
        args = (self.root / "launch.log").read_text().splitlines()
        self.assertEqual(args[args.index("--syncmode") + 1], "full")
        self.assertEqual(args.count("--syncmode"), 1)


if __name__ == "__main__":
    unittest.main()
