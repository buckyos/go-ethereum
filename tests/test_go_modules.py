#!/usr/bin/env python3
"""Fault-inject module downloads without running builds, tests or network requests."""
import json
import re
import os
from pathlib import Path
import signal
import subprocess
import sys
import time
import unittest

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "scripts/usdb"))
import prepare_go_modules as MODULES
from common.go_modules import DownloadFixture, failure

HTTP2 = "stream error: stream ID 99; INTERNAL_ERROR; received from peer"


def process_stopped(pid):
    """Allow signal delivery/reaping to finish without accepting a live child."""
    deadline = time.monotonic() + 2
    while time.monotonic() < deadline:
        try:
            if Path(f"/proc/{pid}/stat").read_text().split()[2] == "Z":
                return True
        except FileNotFoundError:
            return True
        time.sleep(0.01)
    return False


class GoModulePreparationTests(unittest.TestCase):
    def test_success_preserves_locks_network_policy_and_only_downloads(self):
        with DownloadFixture(MODULES, [{}]) as f:
            before = {name: (f.repo / name).read_bytes() for name in ("go.mod", "go.sum")}
            self.assertEqual(f.run(), 0)
            self.assertEqual(f.report()["status"], "success")
            self.assertEqual(f.calls()[0]["args"], ["mod", "download", "-json"])
            self.assertEqual(f.calls()[0]["proxy"], "https://proxy.example,direct")
            self.assertEqual(f.calls()[0]["sumdb"], "sum.golang.org")
            self.assertEqual({name: (f.repo / name).read_bytes() for name in before}, before)
            f.sleeper.assert_not_called()

    def test_http2_recovery_only_changes_download_child_environment(self):
        with DownloadFixture(MODULES, [failure(HTTP2), {}]) as f:
            self.assertEqual(f.run(), 0)
            self.assertEqual([c["debug"] for c in f.calls()], ["gctrace=1,http2client=1", "gctrace=1,http2client=0"])
            self.assertEqual(os.environ["GODEBUG"], "gctrace=1,http2client=1")
            self.assertEqual(f.report()["status"], "recovered")
            self.assertTrue(f.report()["http1_fallback"])
            self.assertIn("recovered", f.summary.read_text())
            self.assertIn(HTTP2, (f.output / "attempt-1.json").read_text())
            f.sleeper.assert_called_once_with(10)

    def test_transient_errors_recover_without_http1_fallback(self):
        for error in ("read: connection reset by peer", "dial tcp: connection refused", "i/o timeout",
                      "TLS handshake timeout", "context deadline exceeded", "unexpected EOF",
                      "Get https://proxy.example: EOF", "429 Too Many Requests", "500 Internal Server Error",
                      "502 Bad Gateway", "503 Service Unavailable", "504 Gateway Timeout"):
            with self.subTest(error=error), DownloadFixture(MODULES, [failure(error), {}]) as f:
                self.assertEqual(f.run(), 0)
                self.assertEqual(len(f.calls()), 2)
                self.assertFalse(f.report()["http1_fallback"])

    def test_persistent_transient_failure_stops_after_three_attempts(self):
        with DownloadFixture(MODULES, [failure(HTTP2)]) as f:
            self.assertEqual(f.run(), 1)
            self.assertEqual(len(f.calls()), 3)
            self.assertEqual([c.args for c in f.sleeper.call_args_list], [(10,), (30,)])
            self.assertEqual(f.report()["status"], "failed")
            self.assertEqual(len(list(f.output.glob("attempt-*.stderr.log"))), 3)

    def test_permanent_and_unknown_failures_are_not_retried(self):
        for error in ("checksum mismatch", "SECURITY ERROR", "unknown revision v99.0.0",
                      "Authentication failed", "terminal prompts disabled", "403 Forbidden", "404 Not Found",
                      "x509: certificate signed by unknown authority", "unexpected module parser error",
                      "checksum mismatch; " + HTTP2):
            with self.subTest(error=error), DownloadFixture(MODULES, [failure(error)]) as f:
                self.assertEqual(f.run(), 1)
                self.assertEqual(len(f.calls()), 1)
                f.sleeper.assert_not_called()

    def test_mixed_module_errors_cannot_hide_a_permanent_or_unknown_failure(self):
        for other in ("checksum mismatch", "unexpected module parser error"):
            step = failure(HTTP2)
            step["modules"].extend(failure(other)["modules"])
            with self.subTest(error=other), DownloadFixture(MODULES, [step]) as f:
                self.assertEqual(f.run(), 1)
                self.assertEqual(len(f.calls()), 1)

    def test_successful_module_names_do_not_affect_failure_classification(self):
        step = failure(HTTP2)
        step["modules"].append({"Path": "example.com/certificate", "Version": "v1.0.0"})
        with DownloadFixture(MODULES, [step, {}]) as f:
            self.assertEqual(f.run(), 0)
            self.assertEqual(len(f.calls()), 2)

    def test_stderr_only_dependency_graph_failure_is_classified(self):
        with DownloadFixture(MODULES, [{"exit": 1, "modules": [], "stderr": "go: fetching go.mod: 502 Bad Gateway"}, {}]) as f:
            self.assertEqual(f.run(), 0)
            self.assertEqual(len(f.calls()), 2)
            self.assertIn("502 Bad Gateway", (f.output / "attempt-1.stderr.log").read_text())

    def test_mixed_stderr_graph_errors_do_not_retry_unknown_failures(self):
        step = {"exit": 1, "modules": [], "stderr": "go: first: 502 Bad Gateway\ngo: second: bad module metadata"}
        with DownloadFixture(MODULES, [step]) as f:
            self.assertEqual(f.run(), 1)
            self.assertEqual(len(f.calls()), 1)
            f.sleeper.assert_not_called()

    def test_timeout_does_not_hide_completed_module_failures(self):
        for error in ("checksum mismatch", "bad module metadata"):
            raw = json.dumps(failure(error)["modules"][0]) + '\n{"Path":'
            with self.subTest(error=error), DownloadFixture(MODULES, [{"raw": raw, "sleep": 60}]) as f:
                self.assertEqual(f.run(timeout=1), 124)
                self.assertEqual(len(f.calls()), 1)
                f.sleeper.assert_not_called()

    def test_repeated_timeouts_remain_bounded(self):
        with DownloadFixture(MODULES, [{"sleep": 60}]) as f:
            self.assertEqual(f.run(timeout=1), 124)
            self.assertEqual(len(f.calls()), 3)
            self.assertEqual(f.report()["reason"], "timeout")
            self.assertTrue(all(process_stopped(call["pid"]) for call in f.calls()))

    def test_invalid_reports_or_nonzero_exit_without_errors_never_pass(self):
        for step in ({"exit": 1, "raw": ""}, {"raw": "not json"}, {"raw": "[]"},
                     {"raw": '{"Path":"test","Error":true}'}, {**failure(HTTP2), "exit": 0}):
            with self.subTest(step=step), DownloadFixture(MODULES, [step]) as f:
                self.assertNotEqual(f.run(), 0)
                self.assertEqual(f.report()["status"], "failed")

    def test_go_mod_or_sum_mutation_is_a_failure_without_retry(self):
        for name in ("go.mod", "go.sum"):
            with self.subTest(name=name), DownloadFixture(MODULES, [{"mutate": name}]) as f:
                self.assertEqual(f.run(), 1)
                self.assertEqual(f.report()["reason"], "lockfile_changed")
                self.assertEqual(len(f.calls()), 1)

    def test_timeout_cleans_up_process_and_children_before_retry(self):
        with DownloadFixture(MODULES, [{"sleep": 60, "child": True, "raw": '{"Path":'}, {}]) as f:
            self.assertEqual(f.run(timeout=1), 0)
            self.assertEqual(f.report()["attempts"][0]["classification"], "timeout")
            self.assertTrue(process_stopped(f.calls()[0]["pid"]))
            child = int((f.root / "child.pid").read_text())
            self.assertTrue(process_stopped(child))

    def test_signalled_downloader_does_not_retry(self):
        with DownloadFixture(MODULES, [{"signal": signal.SIGTERM}]) as f:
            self.assertEqual(f.run(), 143)
            self.assertEqual(f.report()["status"], "cancelled")
            self.assertEqual(len(f.calls()), 1)
            f.sleeper.assert_not_called()

    def test_cli_cancellation_stops_children_and_does_not_retry(self):
        with DownloadFixture(MODULES, [{"sleep": 60, "child": True}]) as f:
            proc = subprocess.Popen([sys.executable, str(ROOT / "scripts/usdb/prepare_go_modules.py"),
                "--repo", str(f.repo), "--go-binary", str(f.go), "--output-dir", str(f.output)],
                stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
            try:
                deadline = time.monotonic() + 5
                while not (f.root / "child.pid").exists() and time.monotonic() < deadline:
                    time.sleep(0.02)
                self.assertTrue((f.root / "child.pid").exists())
                proc.terminate()
                stdout, stderr = proc.communicate(timeout=8)
                self.assertEqual(proc.returncode, 143, stdout + stderr)
                self.assertEqual(f.report()["status"], "cancelled")
                self.assertEqual(len(f.calls()), 1)
                self.assertTrue(process_stopped(f.calls()[0]["pid"]))
                self.assertTrue(process_stopped(int((f.root / "child.pid").read_text())))
            finally:
                if proc.poll() is None:
                    proc.kill()
                    proc.communicate()

    def test_missing_go_and_invalid_timeout_fail_without_retries(self):
        with DownloadFixture(MODULES, [{}]) as f:
            f.go.unlink()
            self.assertEqual(f.run(), 1)
            self.assertEqual(f.report()["reason"], "local_error")
            f.sleeper.assert_not_called()
            for value in (0, 181):
                with self.assertRaises(ValueError):
                    f.run(value)

    def test_workflows_keep_prep_separate_and_preserve_cache_policy(self):
        for name, job_name, condition in (("fast", "go_fast", None),
                ("integration", "nightly", "matrix.shard == 'go-profile' || matrix.shard == 'go-activation'"),
                ("integration", "weekly", "matrix.shard == 'upstream-fault-matrix' || matrix.shard == 'release-e2e'")):
            source = (ROOT / f".github/workflows/usdb-{name}.yml").read_text()
            job = re.search(rf"(?ms)^  {job_name}:\n(.*?)(?=^  [a-z_]+:|\Z)", source).group(1)
            steps = {block.split("\n", 1)[0]: block for block in re.split(r"(?m)^      - name: ", job)[1:]}
            prep = steps["Prepare Go modules"]
            if condition:
                self.assertIn("if: " + condition, prep)
            self.assertIn("timeout-minutes: 12", prep)
            self.assertIn("prepare_go_modules.py", prep)
            self.assertNotIn("continue-on-error", prep)
            build = "Run Go fast gate" if name == "fast" else "Set up Rust"
            self.assertLess(job.index("name: Prepare Go modules"), job.index("name: " + build))
            upload = next(block for block in steps.values() if "upload-artifact@" in block)
            self.assertIn("if: always()", upload)
            for block in steps.values():
                if "setup-go@" in block:
                    self.assertIn("cache: false", block)



if __name__ == "__main__":
    unittest.main()
