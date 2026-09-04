#!/usr/bin/env python3

from __future__ import annotations

import os
import pathlib
import shlex
import subprocess
import tempfile
import unittest


RUNNER = pathlib.Path(__file__).with_name("run_long_ci.sh")
TOOLS = pathlib.Path(__file__).with_name("prepare_regtest_tools.sh")
REPOSITORY = pathlib.Path(__file__).resolve().parents[2]
WORKFLOWS = REPOSITORY / ".github" / "workflows"


class LongCiRunnerTests(unittest.TestCase):
    def run_script(
        self,
        script: pathlib.Path,
        *args: str,
        env: dict[str, str] | None = None,
    ) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            ["bash", str(script), *args],
            check=False,
            capture_output=True,
            text=True,
            env=env,
        )

    def missing_checkout_environment(self, root: str) -> dict[str, str]:
        env = os.environ.copy()
        env["USDB_REPO_DIR"] = str(pathlib.Path(root) / "missing-usdb")
        env["SOURCE_DAO_REPO"] = str(pathlib.Path(root) / "missing-sourcedao")
        return env

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
        with tempfile.TemporaryDirectory() as root:
            result = self.run_script(
                RUNNER,
                "nightly",
                "unknown",
                env=self.missing_checkout_environment(root),
            )
            self.assertEqual(result.returncode, 2)
            self.assertIn("Unknown nightly shard", result.stderr)
            result = self.run_script(
                RUNNER,
                "weekly",
                "unknown",
                env=self.missing_checkout_environment(root),
            )
            self.assertEqual(result.returncode, 2)
            self.assertIn("Unknown weekly shard", result.stderr)

    def test_valid_shard_checks_external_repositories(self) -> None:
        with tempfile.TemporaryDirectory() as root:
            result = self.run_script(
                RUNNER,
                "nightly",
                "go-profile",
                env=self.missing_checkout_environment(root),
            )
            self.assertEqual(result.returncode, 1)
            self.assertIn("Missing usdb checkout", result.stderr)

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
        self.assertIn("USDB_LONG_CI_ARTIFACT_NAME", integration)

    def test_runner_prebuilds_services_before_readiness_bound_cases(self) -> None:
        runner = RUNNER.read_text(encoding="utf-8")
        self.assertIn("prepare_usdb_service_binaries \"$tier\" \"$shard\"", runner)
        self.assertIn("cargo build --locked", runner)
        self.assertIn("run_case rust-usdb-indexer-build", runner)
        self.assertIn("-p usdb-indexer \\\n          --bin usdb-indexer", runner)
        self.assertIn("run_case rust-balance-history-build", runner)
        self.assertIn("-p balance-history \\\n          --bin balance-history", runner)

    def test_runner_builds_sourcedao_artifacts_for_activation_shard(self) -> None:
        runner = RUNNER.read_text(encoding="utf-8")
        self.assertIn("prepare_source_dao_artifacts \"$tier\" \"$shard\"", runner)
        self.assertIn("nightly:go-activation)", runner)
        self.assertIn("run_case source-dao-usdb-build", runner)
        self.assertIn('npm --prefix "$SOURCE_DAO_REPO" run build:usdb', runner)

    def test_historical_profile_e2e_uses_stable_btc_context(self) -> None:
        script = (REPOSITORY / "scripts/usdb/run_usdb_profile_historical_stability_e2e.sh").read_text(
            encoding="utf-8"
        )
        self.assertIn("BTC_STABLE_LAG_BLOCKS=${BTC_STABLE_LAG_BLOCKS:-10}", script)
        self.assertIn(
            "current_context_height=$((current_tip_height - BTC_STABLE_LAG_BLOCKS))",
            script,
        )
        self.assertIn(
            'regtest_mine_blocks "$((BTC_STABLE_LAG_BLOCKS + 1))"', script
        )
        self.assertNotIn('pass_energy_now "$pass_id"', script)

    def test_run_case_reports_the_command_failure_before_diagnostics(self) -> None:
        with tempfile.TemporaryDirectory() as root:
            summary = pathlib.Path(root) / "summary.md"
            command = "\n".join(
                [
                    f"source {shlex.quote(str(RUNNER))}",
                    f"OUTPUT_ROOT={shlex.quote(root)}",
                    f"GITHUB_STEP_SUMMARY={shlex.quote(str(summary))}",
                    "GITHUB_ACTIONS=true",
                    "USDB_LONG_CI_ARTIFACT_NAME=test-artifact",
                    "run_case failing-case bash -c 'echo root-cause-line; exit 7'",
                ]
            )
            result = subprocess.run(
                ["bash", "-c", command],
                check=False,
                capture_output=True,
                text=True,
            )

            self.assertEqual(result.returncode, 7, result.stderr)
            combined = result.stdout + result.stderr
            self.assertIn("root-cause-line", combined)
            self.assertIn("FAIL failing-case: command_exit=7", combined)
            self.assertIn("::error title=USDB long CI case failed::", combined)
            self.assertIn("`failing-case`", summary.read_text(encoding="utf-8"))
            self.assertIn("`test-artifact`", summary.read_text(encoding="utf-8"))


if __name__ == "__main__":
    unittest.main()
