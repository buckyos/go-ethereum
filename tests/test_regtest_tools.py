#!/usr/bin/env python3
"""Exercise release alignment, artifact rejection, and interrupted tool preparation."""
import json
from pathlib import Path
import sys
import subprocess
import tempfile
import unittest
from unittest import mock

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "scripts/usdb"))
import regtest_tools as tools
from common.regtest_tools import ReleaseFixture


class RegtestToolsTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix="regtest-tool-test-")
        self.addCleanup(temporary.cleanup)
        self.fixture = ReleaseFixture(temporary.name)

    def inputs(self):
        return tools.release_inputs(self.fixture.usdb, self.fixture.lock)

    def test_roundtrip_carries_release_inputs_and_all_file_hashes(self):
        expected = self.inputs()
        tools.record(self.fixture.tools, expected)
        tools.verify(self.fixture.tools, expected)
        manifest = json.loads((self.fixture.tools / "toolchain-manifest.json").read_text())
        self.assertEqual(manifest["ord"]["version"], "0.29.0")
        self.assertEqual(manifest["bitcoin_core"]["version"], "31.1")
        self.assertEqual(manifest["ord"]["cargo_lock_sha256"], tools.sha256(self.fixture.usdb / "docker/locks/ord-0.29.0.Cargo.lock"))
        self.assertEqual(set(manifest["files"]), set(tools.FILES))

    def test_version_change_is_read_from_release_and_rejects_old_artifact(self):
        before = self.inputs()
        tools.record(self.fixture.tools, before)
        for path in (self.fixture.usdb / "docker").rglob("*"):
            if path.is_file():
                path.write_text(path.read_text().replace("0.29.0", "0.30.0"))
        old_lock = self.fixture.usdb / "docker/locks/ord-0.29.0.Cargo.lock"
        old_lock.rename(old_lock.with_name("ord-0.30.0.Cargo.lock"))
        self.fixture.commit()
        after = self.inputs()
        self.assertEqual(after["ord"]["version"], "0.30.0")
        with self.assertRaisesRegex(ValueError, "locked USDB release"):
            tools.verify(self.fixture.tools, after)

    def test_old_baseline_or_other_checkout_is_rejected_before_execution(self):
        expected = self.inputs()
        for field, value in (("schema_version", "usdb-regtest-tools:v1"), ("usdb_revision", "c" * 40),
                             ("ord", {**expected["ord"], "version": "0.23.3"}),
                             ("bitcoin_core", {**expected["bitcoin_core"], "version": "28.1"})):
            with self.subTest(field=field):
                tools.record(self.fixture.tools, expected)
                path = self.fixture.tools / "toolchain-manifest.json"
                manifest = json.loads(path.read_text())
                manifest[field] = value
                path.write_text(json.dumps(manifest))
                with mock.patch.object(tools, "check_versions") as run:
                    with self.assertRaisesRegex(ValueError, "locked USDB release"):
                        tools.verify(self.fixture.tools, expected)
                    run.assert_not_called()

    def test_corrupt_binary_is_rejected_before_execution(self):
        expected = self.inputs()
        tools.record(self.fixture.tools, expected)
        (self.fixture.tools / "ord/ord").write_text("corrupt")
        with mock.patch.object(tools, "check_versions") as run:
            with self.assertRaisesRegex(ValueError, "file hashes"):
                tools.verify(self.fixture.tools, expected)
            run.assert_not_called()

    def test_missing_file_is_rejected(self):
        expected = self.inputs()
        tools.record(self.fixture.tools, expected)
        (self.fixture.tools / "bitcoin/bin/bitcoin-cli").unlink()
        with self.assertRaises(FileNotFoundError):
            tools.verify(self.fixture.tools, expected)

    def test_wrong_binary_version_cannot_be_recorded(self):
        expected = self.inputs()
        binary = self.fixture.tools / "ord/ord"
        binary.write_text("#!/bin/sh\necho 'ord 0.23.3'\n")
        with self.assertRaisesRegex(ValueError, "version mismatch for ord/ord"):
            tools.record(self.fixture.tools, expected)
        self.assertFalse((self.fixture.tools / "toolchain-manifest.json").exists())

    def test_core_version_check_rejects_old_release_and_version_prefix(self):
        expected = self.inputs()
        binary = self.fixture.tools / "bitcoin/bin/bitcoind"
        for version in ("28.1.0", "31.1.01", "31.1.0-rc1"):
            with self.subTest(version=version):
                binary.write_text(f"#!/bin/sh\necho 'Bitcoin Core daemon version v{version} bitcoind'\n")
                with self.assertRaisesRegex(ValueError, "version mismatch for bitcoin/bin/bitcoind"):
                    tools.record(self.fixture.tools, expected)

    def test_version_probes_isolate_operator_configuration_and_settings(self):
        expected = self.inputs()
        with mock.patch.object(tools.subprocess, "check_output", wraps=subprocess.check_output) as run:
            tools.check_versions(self.fixture.tools, expected)
        calls = [call.args[0] for call in run.call_args_list]
        self.assertIn("--data-dir", calls[0])
        for args in calls[1:]:
            data_dir = next(arg.split("=", 1)[1] for arg in args if arg.startswith("-datadir="))
            self.assertIn(f"-conf={data_dir}/bitcoin.conf", args)
            self.assertFalse(Path(data_dir).exists())
        self.assertIn("-nosettings", calls[1])

    def test_unpinned_checkout_is_rejected_without_docker(self):
        lock = json.loads(self.fixture.lock.read_text())
        lock["dependencies"]["usdb"]["revision"] = "f" * 40
        self.fixture.lock.write_text(json.dumps(lock))
        result = self.fixture.build()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("USDB revision mismatch", result.stderr)
        self.assertEqual(self.fixture.docker_calls(), [])

    def test_dirty_release_inputs_are_rejected(self):
        self.fixture.write("docker/Dockerfile.bitcoin-core", "ARG BITCOIN_VERSION=28.1\n")
        with self.assertRaisesRegex(ValueError, "must be clean"):
            self.inputs()

    def test_docker_version_disagreement_is_rejected(self):
        self.fixture.write("docker/Dockerfile.bitcoin-core", "ARG BITCOIN_VERSION=28.1\n")
        self.fixture.commit()
        with self.assertRaisesRegex(ValueError, "Bitcoin version"):
            self.inputs()

    def test_rust_lock_disagreement_is_rejected(self):
        lock = json.loads(self.fixture.lock.read_text())
        lock["toolchains"]["rust"] = "1.89.0"
        self.fixture.lock.write_text(json.dumps(lock))
        with self.assertRaisesRegex(ValueError, "Ord Rust builder"):
            self.inputs()

    def test_dependency_lock_disagreement_is_rejected(self):
        self.fixture.write("docker/locks/ord-0.29.0.Cargo.lock", '[[package]]\nname="ord"\nversion="0.23.3"\n')
        self.fixture.commit()
        with self.assertRaisesRegex(ValueError, "Cargo.lock"):
            self.inputs()

    def test_preparer_builds_release_stages_exports_and_cleans_containers(self):
        result = self.fixture.build()
        self.assertEqual(result.returncode, 0, result.stderr)
        tools.verify(self.fixture.output, self.inputs())
        calls = self.fixture.docker_calls()
        builds = [call for call in calls if call[0] == "build"]
        self.assertEqual([call[call.index("--target") + 1] for call in builds], ["ord-builder", "verifier"])
        self.assertTrue(all(call[-1] == str(self.fixture.usdb) for call in builds))
        self.assertEqual([call for call in calls if call[0] == "rm"], [["rm", "ord-builder"], ["rm", "verifier"]])
        self.assertFalse(any(call[0] in ("run", "start", "push") for call in calls))
        self.assertEqual(list(self.fixture.root.glob(".regtest-tools.*")), [])

    def test_copy_failure_does_not_publish_partial_output(self):
        self.fixture.env["FAKE_DOCKER_FAIL"] = "cp"
        result = self.fixture.build()
        self.assertEqual(result.returncode, 41, result.stderr)
        self.assertFalse(self.fixture.output.exists())
        self.assertEqual([call[0] for call in self.fixture.docker_calls()].count("rm"), 2)
        self.assertEqual(list(self.fixture.root.glob(".regtest-tools.*")), [])

    def test_existing_output_is_preserved(self):
        self.fixture.output.mkdir()
        sentinel = self.fixture.output / "keep"
        sentinel.write_text("original")
        result = self.fixture.build()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Refusing to replace", result.stderr)
        self.assertEqual(sentinel.read_text(), "original")
        self.assertEqual(self.fixture.docker_calls(), [])

    def test_workflow_qualifies_once_and_verifies_both_consumers(self):
        workflow = (ROOT / ".github/workflows/usdb-integration.yml").read_text()
        producer = workflow.split("  regtest_tools:", 1)[1].split("  nightly:", 1)[0]
        self.assertIn("ref: ${{ needs.revision_lock.outputs.usdb_revision }}", producer)
        self.assertIn("python3 usdb/tests/ord_release_smoke.py", producer)
        self.assertLess(producer.index("ord_release_smoke.py"), producer.index("actions/upload-artifact"))
        self.assertNotIn("rustup", producer)
        for job in ("nightly", "weekly"):
            step = workflow.split(f"- name: Run {job} shard", 1)[1].split("- name: Upload", 1)[0]
            self.assertLess(step.index("regtest_tools.py verify"), step.index("run_long_ci.sh"))
            self.assertIn("toolchain-manifest,compatibility-report", step)


if __name__ == "__main__":
    unittest.main()
