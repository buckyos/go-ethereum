#!/usr/bin/env python3

import unittest
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from configure_usdb_pow_calibration_genesis import configure_genesis


def sample_genesis() -> dict:
    return {
        "config": {
            "chainId": 20260323,
            "usdb": {"activations": [{"block": 0}]},
        },
        "difficulty": "0x2000",
    }


class ConfigurePowCalibrationGenesisTest(unittest.TestCase):
    def test_configures_explicit_difficulty_pair(self) -> None:
        configured = configure_genesis(sample_genesis(), 0x40000, 0x20000)

        self.assertEqual(configured["difficulty"], "0x40000")
        self.assertEqual(
            configured["config"]["ethPoWMinimumDifficulty"], 0x20000
        )

    def test_rejects_genesis_below_minimum(self) -> None:
        with self.assertRaisesRegex(ValueError, "must not be below"):
            configure_genesis(sample_genesis(), 0x10000, 0x20000)

    def test_rejects_non_usdb_genesis(self) -> None:
        with self.assertRaisesRegex(ValueError, "activation schedule"):
            configure_genesis({"config": {}, "difficulty": "0x2000"}, 1, 1)


if __name__ == "__main__":
    unittest.main()
