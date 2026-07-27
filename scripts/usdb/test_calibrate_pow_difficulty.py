#!/usr/bin/env python3

import json
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from calibrate_pow_difficulty import (
    BlockHeader,
    CalibrationError,
    MeasurementContext,
    build_report,
    load_and_verify_report,
)


def hash_for(number: int) -> str:
    return f"0x{number:064x}"


def sample_headers() -> list[BlockHeader]:
    return [
        BlockHeader(10, hash_for(10), hash_for(9), 100, 900),
        BlockHeader(11, hash_for(11), hash_for(10), 110, 1000),
        BlockHeader(12, hash_for(12), hash_for(11), 120, 1000),
    ]


def sample_measurement() -> MeasurementContext:
    return MeasurementContext(
        source_commit="0123456789abcdef",
        source_dirty=False,
        build_command="go build ./cmd/geth",
        miner_hardware="test cpu",
        miner_threads=1,
        dag_warmup_blocks=64,
        genesis_difficulty=0x40000,
        minimum_difficulty=0x20000,
        isolated_hardware=True,
        environment_notes="isolated unit-test fixture",
    )


class PowDifficultyCalibrationTest(unittest.TestCase):
    def test_builds_replayable_candidate_from_total_work(self) -> None:
        report = build_report(
            20260323, "nominal", 15, sample_headers(), sample_measurement()
        )

        self.assertEqual(report["schemaVersion"], 4)
        self.assertEqual(report["measurement"]["minerThreads"], 1)
        self.assertEqual(
            report["measurement"]["genesisDifficulty"]["hex"], "0x40000"
        )
        self.assertEqual(report["sample"]["totalWork"], "2000")
        self.assertEqual(report["sample"]["elapsedSeconds"], "20")
        self.assertEqual(report["observed"]["blockIntervalSeconds"]["p95"], 10)
        self.assertEqual(report["candidateDifficulty"]["decimal"], "1500")
        self.assertEqual(report["candidateDifficulty"]["hex"], "0x5dc")
        self.assertFalse(report["quality"]["timestampResolutionLimited"])
        self.assertFalse(report["quality"]["releaseEligible"])
        self.assertEqual(
            report["quality"]["releaseBlockers"],
            ["sample_interval_count_below_release_minimum"],
        )

        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "report.json"
            path.write_text(json.dumps(report), encoding="utf-8")
            self.assertEqual(load_and_verify_report(path, 20260323), report)

    def test_rejects_parent_discontinuity(self) -> None:
        headers = sample_headers()
        headers[2] = BlockHeader(12, hash_for(12), hash_for(99), 120, 1000)

        with self.assertRaisesRegex(CalibrationError, "parent hash mismatch"):
            build_report(
                20260323, "nominal", 15, headers, sample_measurement()
            )

    def test_rejects_non_positive_timestamp_interval(self) -> None:
        headers = sample_headers()
        headers[2] = BlockHeader(12, hash_for(12), hash_for(11), 110, 1000)

        with self.assertRaisesRegex(CalibrationError, "non-positive timestamp"):
            build_report(
                20260323, "nominal", 15, headers, sample_measurement()
            )

    def test_replay_rejects_tampered_candidate(self) -> None:
        report = build_report(
            20260323, "nominal", 15, sample_headers(), sample_measurement()
        )
        report["candidateDifficulty"]["decimal"] = "1499"

        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "report.json"
            path.write_text(json.dumps(report), encoding="utf-8")
            with self.assertRaisesRegex(CalibrationError, "does not match"):
                load_and_verify_report(path, 20260323)

    def test_rejects_invalid_measurement_context(self) -> None:
        invalid = MeasurementContext(
            source_commit="commit",
            source_dirty=False,
            build_command="go build ./cmd/geth",
            miner_hardware="test cpu",
            miner_threads=0,
            dag_warmup_blocks=64,
            genesis_difficulty=0x40000,
            minimum_difficulty=0x20000,
            isolated_hardware=True,
            environment_notes="isolated unit-test fixture",
        )
        with self.assertRaisesRegex(CalibrationError, "threads must be positive"):
            build_report(20260323, "nominal", 15, sample_headers(), invalid)

    def test_marks_timestamp_floor_and_dirty_source_as_release_blockers(self) -> None:
        measurement = MeasurementContext(
            source_commit="commit",
            source_dirty=True,
            build_command="go build ./cmd/geth",
            miner_hardware="test cpu",
            miner_threads=1,
            dag_warmup_blocks=64,
            genesis_difficulty=0x2000,
            minimum_difficulty=0x2000,
            isolated_hardware=False,
            environment_notes="concurrent unit-test load",
        )
        headers = [
            BlockHeader(1, hash_for(1), hash_for(0), 100, 8192),
            BlockHeader(2, hash_for(2), hash_for(1), 101, 8192),
            BlockHeader(3, hash_for(3), hash_for(2), 102, 8192),
        ]

        report = build_report(20260323, "pilot", 13, headers, measurement)

        self.assertTrue(report["quality"]["timestampResolutionLimited"])
        self.assertFalse(report["quality"]["releaseEligible"])
        self.assertEqual(
            report["quality"]["releaseBlockers"],
            [
                "source_worktree_dirty",
                "hardware_not_isolated",
                "timestamp_resolution_limited",
                "sample_interval_count_below_release_minimum",
            ],
        )

    def test_rejects_sample_at_timestamp_resolution_limit(self) -> None:
        timestamps = [100, 101, 111, 121, 131]
        headers = [
            BlockHeader(
                number,
                hash_for(number),
                hash_for(number - 1),
                timestamp,
                1000,
            )
            for number, timestamp in enumerate(timestamps, start=1)
        ]

        report = build_report(
            20260323, "pilot", 13, headers, sample_measurement()
        )

        self.assertEqual(report["quality"]["oneSecondIntervalRatioBps"], 2500)
        self.assertTrue(report["quality"]["timestampResolutionLimited"])

    def test_release_quality_requires_long_isolated_sample(self) -> None:
        headers = [
            BlockHeader(
                number,
                hash_for(number),
                hash_for(number - 1),
                100 + number * 13,
                1000,
            )
            for number in range(1, 258)
        ]

        report = build_report(
            20260323, "release", 13, headers, sample_measurement()
        )

        self.assertTrue(report["quality"]["releaseEligible"])
        self.assertEqual(report["quality"]["releaseBlockers"], [])


if __name__ == "__main__":
    unittest.main()
