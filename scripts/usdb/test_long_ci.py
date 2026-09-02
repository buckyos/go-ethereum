#!/usr/bin/env python3

from __future__ import annotations

import pathlib
import subprocess
import unittest


RUNNER = pathlib.Path(__file__).with_name("run_long_ci.sh")
TOOLS = pathlib.Path(__file__).with_name("prepare_regtest_tools.sh")
REPOSITORY = pathlib.Path(__file__).resolve().parents[2]
WORKFLOWS = REPOSITORY / ".github" / "workflows"


class LongCiRunnerTests(unittest.TestCase):
    def run_script(self, script: pathlib.Path, *args: str) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            ["bash", str(script), *args],
            check=False,
            capture_output=True,
            text=True,
        )

    def test_lists_frozen_nightly_shards(self) -> None:
        result = self.run_script(RUNNER, "nightly", "--list")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(
            result.stdout.splitlines(),
            [
                "go-profile",
                "go-activation",
                "balance-history",
                "indexer-protocol",
                "indexer-reorg",
                "indexer-validator",
            ],
        )

    def test_lists_frozen_weekly_shards(self) -> None:
        result = self.run_script(RUNNER, "weekly", "--list")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(
            result.stdout.splitlines(),
            [
                "world-soak",
                "economic-capacity",
                "balance-history-extended",
                "release-e2e",
            ],
        )

    def test_rejects_unknown_tier_and_shard(self) -> None:
        result = self.run_script(RUNNER, "monthly", "--list")
        self.assertEqual(result.returncode, 2)
        result = self.run_script(RUNNER, "nightly", "unknown")
        self.assertEqual(result.returncode, 2)
        self.assertIn("Unknown nightly shard", result.stderr)

    def test_tool_preparer_requires_output_without_network_access(self) -> None:
        result = self.run_script(TOOLS)
        self.assertEqual(result.returncode, 2)
        self.assertIn("--output is required", result.stderr)

    def test_scheduled_workflows_call_the_frozen_integration_tiers(self) -> None:
        nightly = (WORKFLOWS / "usdb-nightly.yml").read_text(encoding="utf-8")
        weekly = (WORKFLOWS / "usdb-weekly.yml").read_text(encoding="utf-8")
        self.assertIn("cron: '37 8 * * *'", nightly)
        self.assertIn("uses: ./.github/workflows/usdb-integration.yml", nightly)
        self.assertIn("tier: nightly", nightly)
        self.assertIn("cron: '23 9 * * 0'", weekly)
        self.assertIn("uses: ./.github/workflows/usdb-integration.yml", weekly)
        self.assertIn("tier: weekly", weekly)

    def test_integration_workflow_uses_revision_lock_and_pinned_tools(self) -> None:
        integration = (WORKFLOWS / "usdb-integration.yml").read_text(
            encoding="utf-8"
        )
        self.assertIn("scripts/usdb/ci_revisions.py validate", integration)
        self.assertIn("needs.revision_lock.outputs.usdb_revision", integration)
        self.assertIn("needs.revision_lock.outputs.source_dao_revision", integration)
        self.assertIn("scripts/usdb/prepare_regtest_tools.sh", integration)
        self.assertIn("go-ethereum/scripts/usdb/run_long_ci.sh", integration)


if __name__ == "__main__":
    unittest.main()
