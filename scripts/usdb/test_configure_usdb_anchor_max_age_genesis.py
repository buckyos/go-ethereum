#!/usr/bin/env python3

import copy
import sys
import unittest
from pathlib import Path

SCRIPT_DIR = Path(__file__).resolve().parent
sys.path.insert(0, str(SCRIPT_DIR))
from configure_usdb_anchor_max_age_genesis import configure_anchor_max_age


class ConfigureUSDBAnchorMaxAgeGenesisTest(unittest.TestCase):
    def setUp(self) -> None:
        self.genesis = {
            "config": {
                "chainId": 20260323,
                "usdb": {
                    "activations": [
                        {
                            "block": 0,
                            "btcActivationRegistryId": "11" * 32,
                            "btcAnchorMaxAgeBlocks": 6650,
                            "versions": {"btcAnchorPolicyVersion": 1},
                        }
                    ]
                },
            },
            "alloc": {},
        }

    def test_updates_only_anchor_max_age(self) -> None:
        original = copy.deepcopy(self.genesis)
        configured = configure_anchor_max_age(self.genesis, 3)
        self.assertEqual(
            configured["config"]["usdb"]["activations"][0][
                "btcAnchorMaxAgeBlocks"
            ],
            3,
        )
        original["config"]["usdb"]["activations"][0][
            "btcAnchorMaxAgeBlocks"
        ] = 3
        self.assertEqual(configured, original)

    def test_rejects_non_positive_or_overflow_value(self) -> None:
        for value in (0, -1, 0x100000000):
            with self.subTest(value=value):
                with self.assertRaisesRegex(ValueError, "positive uint32"):
                    configure_anchor_max_age(copy.deepcopy(self.genesis), value)

    def test_rejects_ambiguous_activation_schedule(self) -> None:
        self.genesis["config"]["usdb"]["activations"].append(
            {
                "block": 10,
                "btcAnchorMaxAgeBlocks": 6650,
                "versions": {"btcAnchorPolicyVersion": 1},
            }
        )
        with self.assertRaisesRegex(ValueError, "one block-0"):
            configure_anchor_max_age(self.genesis, 3)


if __name__ == "__main__":
    unittest.main()
