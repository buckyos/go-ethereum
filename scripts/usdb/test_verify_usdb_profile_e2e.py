#!/usr/bin/env python3

import sys
import unittest
from pathlib import Path

SCRIPT_DIR = Path(__file__).resolve().parent
sys.path.insert(0, str(SCRIPT_DIR))

from verify_usdb_profile_e2e import (  # noqa: E402
    FIXED_PRICE_ATOMS_PER_BTC,
    calculate_emission,
    calculate_k_bps,
)


class VerifyUSDBProfileE2ETest(unittest.TestCase):
    def test_emission_matches_rust_and_go_golden_vectors(self) -> None:
        vectors = (
            (0, 0, 10_000, 0),
            (100_000_000, 0, 10_000, 634_195_839_675_291_730),
            (
                1_234_567_890,
                234_567_890_000_000_000_000_000,
                8_001,
                5_074_200_913_242_009_132,
            ),
            (1, 0, 10_000, 6_341_958_396),
        )
        for total_sats, issued, k_bps, expected in vectors:
            with self.subTest(total_sats=total_sats, issued=issued, k_bps=k_bps):
                self.assertEqual(
                    calculate_emission(
                        total_sats,
                        FIXED_PRICE_ATOMS_PER_BTC,
                        issued,
                        k_bps,
                    ),
                    expected,
                )

    def test_k_matches_rust_and_go_golden_vectors(self) -> None:
        vectors = (
            (0, 0, 10_000),
            (100, 0, 10_000),
            (0, 100, 8_001),
            (50, 100, 9_090),
            (99, 100, 9_983),
            (100, 100, 10_000),
            (150, 100, 15_000),
            (200, 100, 20_000),
            (201, 100, 20_000),
        )
        for current, average, expected in vectors:
            with self.subTest(current=current, average=average):
                self.assertEqual(calculate_k_bps(current, average), expected)


if __name__ == "__main__":
    unittest.main()
