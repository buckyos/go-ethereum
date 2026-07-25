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


class PowDifficultyCalibrationTest(unittest.TestCase):
    def test_builds_replayable_candidate_from_total_work(self) -> None:
        report = build_report(20260323, "nominal", 15, sample_headers())

        self.assertEqual(report["sample"]["totalWork"], "2000")
        self.assertEqual(report["sample"]["elapsedSeconds"], "20")
        self.assertEqual(report["observed"]["blockIntervalSeconds"]["p95"], 10)
        self.assertEqual(report["candidateDifficulty"]["decimal"], "1500")
        self.assertEqual(report["candidateDifficulty"]["hex"], "0x5dc")

        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "report.json"
            path.write_text(json.dumps(report), encoding="utf-8")
            self.assertEqual(load_and_verify_report(path, 20260323), report)

    def test_rejects_parent_discontinuity(self) -> None:
        headers = sample_headers()
        headers[2] = BlockHeader(12, hash_for(12), hash_for(99), 120, 1000)

        with self.assertRaisesRegex(CalibrationError, "parent hash mismatch"):
            build_report(20260323, "nominal", 15, headers)

    def test_rejects_non_positive_timestamp_interval(self) -> None:
        headers = sample_headers()
        headers[2] = BlockHeader(12, hash_for(12), hash_for(11), 110, 1000)

        with self.assertRaisesRegex(CalibrationError, "non-positive timestamp"):
            build_report(20260323, "nominal", 15, headers)

    def test_replay_rejects_tampered_candidate(self) -> None:
        report = build_report(20260323, "nominal", 15, sample_headers())
        report["candidateDifficulty"]["decimal"] = "1499"

        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "report.json"
            path.write_text(json.dumps(report), encoding="utf-8")
            with self.assertRaisesRegex(CalibrationError, "does not match"):
                load_and_verify_report(path, 20260323)


if __name__ == "__main__":
    unittest.main()
