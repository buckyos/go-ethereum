#!/usr/bin/env python3

from __future__ import annotations

import importlib.util
import tempfile
import unittest
from pathlib import Path


MODULE_PATH = Path(__file__).with_name("ci_change_scope.py")
SPEC = importlib.util.spec_from_file_location("ci_change_scope", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
SCOPE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(SCOPE)


class CiChangeScopeTests(unittest.TestCase):
    def test_regular_go_and_documentation_changes_skip_cross_repo_gate(self) -> None:
        result = SCOPE.classify(["miner/miner.go", "docs/usdb/example.md"])
        self.assertFalse(result["required"])
        self.assertEqual(result["matched_paths"], [])

    def test_revision_lock_and_frozen_artifacts_require_cross_repo_gate(self) -> None:
        paths = [
            "scripts/usdb/ci-revisions.json",
            "internal/usdb/btc_activation_golden.json",
        ]
        result = SCOPE.classify(paths)
        self.assertTrue(result["required"])
        self.assertEqual(result["matched_paths"], sorted(paths))

    def test_runner_and_workflow_changes_require_cross_repo_gate(self) -> None:
        for path in (
            ".github/workflows/usdb-fast.yml",
            "scripts/usdb/ci_change_scope.py",
            "scripts/usdb/ci_revisions.py",
            "scripts/usdb/run_fast_ci.sh",
        ):
            with self.subTest(path=path):
                self.assertTrue(SCOPE.classify([path])["required"])

    def test_manual_or_release_force_runs_without_changed_paths(self) -> None:
        result = SCOPE.classify([], force=True)
        self.assertTrue(result["required"])
        self.assertEqual(result["reason"], "forced_by_manual_or_reusable_release_gate")

    def test_github_output_is_append_only_and_lowercase(self) -> None:
        with tempfile.TemporaryDirectory(prefix="ci-change-scope-") as directory:
            output = Path(directory) / "github-output"
            output.write_text("existing=value\n", encoding="utf-8")
            SCOPE.write_github_output(output, SCOPE.classify([], force=True))
            self.assertEqual(
                output.read_text(encoding="utf-8"),
                "existing=value\nrequired=true\nreason=forced_by_manual_or_reusable_release_gate\n",
            )

    def test_release_workflow_explicitly_forces_full_cross_repo_gate(self) -> None:
        repository_root = Path(__file__).resolve().parents[2]
        fast_workflow = (repository_root / ".github/workflows/usdb-fast.yml").read_text(
            encoding="utf-8"
        )
        release_workflow = (
            repository_root / ".github/workflows/usdb-release-build.yml"
        ).read_text(encoding="utf-8")
        self.assertIn("run_cross_repo_golden:\n", fast_workflow)
        self.assertIn("default: false", fast_workflow)
        self.assertIn("run_cross_repo_golden: true", release_workflow)


if __name__ == "__main__":
    unittest.main()
