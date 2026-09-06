#!/usr/bin/env python3
"""Exercise the real chain entrypoint and guard against controlled upstream faults."""
import json
import os
import signal
import time
import unittest

from common.runtime_guard import GUARD, RuntimeGuardFixture


class RuntimeDeepReorgTest(RuntimeGuardFixture):
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

    def test_startup_retries_unavailable_and_malformed_rpc(self) -> None:
        for fault in ("http", "json", "timeout"):
            with self.subTest(fault=fault):
                starts_before = self.line_count(self.starts)
                failures_before = self.server.failures
                self.server.fault = fault
                data = self.prepare_data_dir(fault)
                runtime = self.start_runtime(data)
                state = data / "recovery/deep-btc-reorg"
                self.wait_for(lambda: self.server.failures >= failures_before + 3,
                              "startup did not retry the unavailable indexer")
                self.assert_running(runtime, "waiting runtime")
                self.assertEqual(self.line_count(self.starts), starts_before)
                self.assertFalse((state / "baseline.json").exists())
                self.assertFalse((state / "halted.json").exists())

                self.server.fault = ""
                self.wait_for(lambda: self.line_count(self.starts) == starts_before + 1,
                              "geth did not start after RPC recovered")
                self.assert_running(runtime, "recovered runtime")
                self.assertFalse((state / "halted.json").exists())
                self.assert_group_stopped(runtime)

    def test_miner_recovers_repeated_outages_with_same_baseline_and_config(self) -> None:
        self.server.epoch = 7
        data = self.prepare_data_dir("miner")
        marker = data / "bootstrap/usdb-init.done.json"
        marker_before = marker.read_bytes()
        runtime = self.start_runtime(data, USDB_NODE_ROLE="miner",
                                     USDB_MINER_ADDRESS="0x" + "12" * 20,
                                     FAKE_GETH_STOP_DELAY="0.3")
        self.wait_for(lambda: self.line_count(self.starts) == 1, "miner did not start")
        state = data / "recovery/deep-btc-reorg"
        baseline_before = (state / "baseline.json").read_bytes()

        for cycle, fault in enumerate(("http", "json", "timeout"), start=1):
            self.server.fault = fault
            self.wait_for(lambda: self.line_count(self.stops) == cycle,
                          "RPC outage did not gracefully stop the miner")
            failures = self.server.failures
            self.wait_for(lambda: self.server.failures > failures,
                          "stopped miner did not continue checking upstream")
            self.assertEqual(self.line_count(self.starts), cycle)
            self.assert_running(runtime, "waiting miner runtime")
            self.assertFalse((state / "halted.json").exists())
            self.assertEqual((state / "baseline.json").read_bytes(), baseline_before)

            self.server.fault = ""
            self.wait_for(lambda: self.line_count(self.starts) == cycle + 1,
                          "miner did not automatically resume")
            self.assertEqual(marker.read_bytes(), marker_before)
            self.assertEqual((state / "baseline.json").read_bytes(), baseline_before)

        events = [json.loads(line) for line in self.events.read_text().splitlines()]
        self.assertEqual([event["action"] for event in events],
                         ["start", "stop", "start", "stop", "start", "stop", "start"])
        argv = events[0]["argv"]
        self.assertIn("--mine", argv)
        self.assertEqual(argv[argv.index("--miner.threads") + 1], "1")
        self.assertEqual([event["argv"] for event in events if event["action"] == "start"],
                         [argv] * 4)
        self.assert_group_stopped(runtime)

    def test_epoch_change_during_outage_latches_instead_of_resuming(self) -> None:
        for epoch in (6, 4):
            with self.subTest(epoch=epoch):
                self.server.epoch = 5
                data = self.prepare_data_dir(f"epoch-{epoch}")
                starts = self.line_count(self.starts)
                stops = self.line_count(self.stops)
                runtime = self.start_runtime(data)
                self.wait_for(lambda: self.line_count(self.starts) == starts + 1,
                              "chain did not start")
                state = data / "recovery/deep-btc-reorg"
                baseline = (state / "baseline.json").read_bytes()
                self.server.fault = "http"
                self.wait_for(lambda: self.line_count(self.stops) == stops + 1,
                              "chain was not stopped during outage")
                self.server.epoch = epoch
                self.server.fault = ""
                incident = state / "halted.json"
                self.wait_for(incident.exists, "recovery bypassed the epoch change")
                incident_bytes = incident.read_bytes()
                self.assertEqual(json.loads(incident_bytes)["observed_epoch"], epoch)
                self.assertEqual((state / "baseline.json").read_bytes(), baseline)
                self.assertEqual(self.line_count(self.starts), starts + 1)
                self.assert_group_stopped(runtime)

                restarted = self.start_runtime(data)
                self.wait_for(lambda: "remains halted" in restarted.output_path.read_text(),
                              "container restart bypassed persistent incident")
                self.assertEqual(self.line_count(self.starts), starts + 1)
                self.assertEqual(incident.read_bytes(), incident_bytes)
                self.assert_group_stopped(restarted)

    def test_restart_retries_with_existing_baseline(self) -> None:
        self.server.epoch = 8
        data = self.prepare_data_dir("restart")
        initial = self.start_runtime(data)
        self.wait_for(lambda: self.line_count(self.starts) == 1, "chain did not start")
        baseline = data / "recovery/deep-btc-reorg/baseline.json"
        before = baseline.read_bytes()
        self.assert_group_stopped(initial)

        self.server.fault = "http"
        restarted = self.start_runtime(data)
        self.wait_for(lambda: self.server.failures >= 3, "restart did not wait for upstream")
        self.assertEqual(self.line_count(self.starts), 1)
        self.server.fault = ""
        self.wait_for(lambda: self.line_count(self.starts) == 2, "restart did not recover")
        self.assertEqual(baseline.read_bytes(), before)
        self.assert_group_stopped(restarted)

    def test_sigterm_interrupts_retry_delay(self) -> None:
        self.server.fault = "http"
        runtime = self.start_runtime(self.prepare_data_dir("retry-stop"),
                                     USDB_DEEP_REORG_GUARD_POLL_INTERVAL_SECS="30")
        self.wait_for(lambda: "waiting for upstream" in runtime.output_path.read_text(),
                      "runtime did not enter retry delay")
        self.assert_group_stopped(runtime)
        self.assertEqual(self.line_count(self.starts), 0)

    def test_sigterm_interrupts_inflight_guard_rpc(self) -> None:
        self.server.fault = "timeout"
        runtime = self.start_runtime(self.prepare_data_dir("rpc-stop"),
                                     USDB_DEEP_REORG_GUARD_REQUEST_TIMEOUT_SECS="30")
        self.wait_for(lambda: self.server.failures > 0, "guard request did not start")
        self.assert_group_stopped(runtime)
        self.assertEqual(self.line_count(self.starts), 0)

    def test_unexpected_guard_exit_stops_container(self) -> None:
        for phase, code in (("check", 7), ("watch", 0), ("watch", 7)):
            with self.subTest(phase=phase, code=code):
                starts = self.line_count(self.starts)
                stops = self.line_count(self.stops)
                guard = self.write_executable("faulty-guard.py", f'''#!/usr/bin/env python3
import os
from pathlib import Path
import runpy
import sys
import time
if sys.argv[1] == {phase!r}:
    if sys.argv[1] == "watch":
        path = Path(os.environ["FAKE_GETH_STARTS"])
        while not path.exists() or len(path.read_text().splitlines()) <= {starts}:
            time.sleep(0.01)
    raise SystemExit({code})
runpy.run_path({str(GUARD)!r}, run_name="__main__")
''')
                runtime = self.start_runtime(self.prepare_data_dir(f"exit-{phase}-{code}"),
                                             USDB_DEEP_REORG_GUARD_SCRIPT=str(guard))
                self.assertEqual(runtime.wait(timeout=5), code or 1)
                self.assertEqual(self.line_count(self.starts), starts + (phase == "watch"))
                self.assertEqual(self.line_count(self.stops), stops + (phase == "watch"))
                with self.assertRaises(ProcessLookupError):
                    os.killpg(runtime.pid, 0)

    def test_geth_crash_exits_container_for_docker_restart(self) -> None:
        runtime = self.start_runtime(self.prepare_data_dir("crash"))
        self.wait_for(lambda: self.line_count(self.starts) == 1, "geth did not start")
        geth_pid = int(self.starts.read_text().strip())
        os.kill(geth_pid, signal.SIGKILL)
        self.assertNotEqual(runtime.wait(timeout=5), 0)
        with self.assertRaises(ProcessLookupError):
            os.killpg(runtime.pid, 0)


if __name__ == "__main__":
    unittest.main()
