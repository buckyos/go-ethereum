#!/usr/bin/env python3

import contextlib
import importlib.util
import io
import json
import pathlib
import subprocess
import tempfile
import unittest


MODULE_PATH = pathlib.Path(__file__).with_name("ci_revisions.py")
SPEC = importlib.util.spec_from_file_location("ci_revisions", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
ci_revisions = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(ci_revisions)


def valid_lock() -> dict:
    return {
        "schema_version": "usdb-ci-revisions:v1",
        "coordinator": "go_ethereum",
        "repositories": {
            "go_ethereum": {
                "repository": "buckyos/go-ethereum",
                "directory": "go-ethereum",
                "revision": "1" * 40,
            },
            "usdb": {
                "repository": "buckyos/usdb",
                "directory": "usdb",
                "revision": "2" * 40,
            },
            "source_dao": {
                "repository": "buckyos/SourceDAO",
                "directory": "SourceDAO",
                "revision": "3" * 40,
            },
        },
        "toolchains": {
            "runner": "ubuntu-24.04",
            "go_release": "1.18.5",
            "go_compatibility": "1.26.0",
            "python": "3.13.7",
            "rust": "1.91.0",
            "node": "24.12.0",
            "npm": "11.6.2",
        },
    }


class CIRevisionsTests(unittest.TestCase):
    def write_lock(self, value: dict | str) -> pathlib.Path:
        temp_dir = tempfile.TemporaryDirectory()
        self.addCleanup(temp_dir.cleanup)
        path = pathlib.Path(temp_dir.name) / "ci-revisions.json"
        if isinstance(value, str):
            path.write_text(value, encoding="utf-8")
        else:
            path.write_text(json.dumps(value), encoding="utf-8")
        return path

    def test_loads_valid_lock_and_emits_stable_outputs(self) -> None:
        lock = ci_revisions.load_lock(self.write_lock(valid_lock()))
        outputs = ci_revisions.github_outputs(lock)
        self.assertIn("usdb_revision=" + "2" * 40, outputs)
        self.assertIn("node_version=24.12.0", outputs)

    def test_rejects_duplicate_and_unknown_keys(self) -> None:
        duplicate = '{"schema_version":"a","schema_version":"b"}'
        with self.assertRaisesRegex(ValueError, "duplicate JSON key"):
            ci_revisions.load_lock(self.write_lock(duplicate))

        unknown = valid_lock()
        unknown["unknown"] = True
        with self.assertRaisesRegex(ValueError, "top-level keys mismatch"):
            ci_revisions.load_lock(self.write_lock(unknown))

    def test_rejects_floating_or_wrong_repository_values(self) -> None:
        floating = valid_lock()
        floating["repositories"]["usdb"]["revision"] = "main"
        with self.assertRaisesRegex(ValueError, "full lowercase SHA"):
            ci_revisions.load_lock(self.write_lock(floating))

        wrong_repo = valid_lock()
        wrong_repo["repositories"]["source_dao"]["repository"] = "fork/SourceDAO"
        with self.assertRaisesRegex(ValueError, "must be 'buckyos/SourceDAO'"):
            ci_revisions.load_lock(self.write_lock(wrong_repo))

    def test_rejects_unpinned_toolchain_version(self) -> None:
        lock = valid_lock()
        lock["toolchains"]["rust"] = "stable"
        with self.assertRaisesRegex(ValueError, "fixed numeric version"):
            ci_revisions.load_lock(self.write_lock(lock))

    def test_verify_allows_only_current_checkout_to_drift(self) -> None:
        lock = valid_lock()
        with tempfile.TemporaryDirectory() as temp_dir:
            root = pathlib.Path(temp_dir)
            heads = {
                "go_ethereum": "a" * 40,
                "usdb": "b" * 40,
                "source_dao": "c" * 40,
            }
            for name, entry in lock["repositories"].items():
                repo = root / entry["directory"]
                repo.mkdir()
                (repo / ".git").mkdir()
                lock["repositories"][name]["revision"] = heads[name]

            original_run = subprocess.run

            def fake_run(command, **kwargs):
                directory = pathlib.Path(command[2]).name
                name = next(
                    key
                    for key, entry in lock["repositories"].items()
                    if entry["directory"] == directory
                )
                head = "d" * 40 if name == "go_ethereum" else heads[name]
                return subprocess.CompletedProcess(command, 0, stdout=head + "\n", stderr="")

            subprocess.run = fake_run
            self.addCleanup(setattr, subprocess, "run", original_run)
            with contextlib.redirect_stdout(io.StringIO()):
                ci_revisions.verify_worktrees(lock, root, "go_ethereum")
                with self.assertRaisesRegex(ValueError, "go_ethereum revision mismatch"):
                    ci_revisions.verify_worktrees(lock, root, None)


if __name__ == "__main__":
    unittest.main()
