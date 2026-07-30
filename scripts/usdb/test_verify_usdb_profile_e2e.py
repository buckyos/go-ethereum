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
    decode_selector,
    validate_selector_transition,
)


class VerifyUSDBProfileE2ETest(unittest.TestCase):
    @staticmethod
    def selector_block(
        number: int,
        btc_height: int,
        age: int,
        snapshot_byte: int = 0x11,
        system_byte: int = 0x22,
    ) -> dict:
        payload = (
            bytes([1])
            + (1).to_bytes(2, "big")
            + btc_height.to_bytes(4, "big")
            + age.to_bytes(4, "big")
            + bytes([snapshot_byte]) * 32
            + bytes([system_byte]) * 32
            + bytes([0x33]) * 32
            + (7).to_bytes(4, "big")
        )
        return {"number": hex(number), "extraData": "0x" + payload.hex()}

    def test_selector_v1_decode_and_parent_transition(self) -> None:
        parent = self.selector_block(1, 123, 0)
        child = self.selector_block(2, 123, 1)
        selector = decode_selector(child)
        self.assertEqual(selector["btc_height"], 123)
        self.assertEqual(selector["btc_anchor_age_blocks"], 1)
        self.assertEqual(selector["pass_id"], "33" * 32 + "i7")
        validate_selector_transition(parent, selector, 2, 2)
        validate_selector_transition(
            {"number": "0x0"},
            decode_selector(self.selector_block(1, 123, 0)),
            1,
            2,
        )

        advanced = decode_selector(self.selector_block(2, 124, 0))
        validate_selector_transition(parent, advanced, 2, 2)

    def test_selector_transition_rejects_invalid_age_and_identity(self) -> None:
        parent = self.selector_block(1, 123, 1)
        with self.assertRaisesRegex(SystemExit, "age mismatch"):
            validate_selector_transition(
                parent,
                decode_selector(self.selector_block(2, 123, 1)),
                2,
                3,
            )
        with self.assertRaisesRegex(SystemExit, "identity changed"):
            validate_selector_transition(
                parent,
                decode_selector(self.selector_block(2, 123, 2, snapshot_byte=0x44)),
                2,
                3,
            )
        with self.assertRaisesRegex(SystemExit, "age exceeded"):
            validate_selector_transition(
                parent,
                decode_selector(self.selector_block(2, 123, 2)),
                2,
                1,
            )

    def test_selector_transition_accepts_exact_max_and_rejects_max_plus_one(
        self,
    ) -> None:
        exact_max_parent = self.selector_block(2, 123, 2)
        validate_selector_transition(
            exact_max_parent,
            decode_selector(self.selector_block(3, 123, 3)),
            3,
            3,
        )

        max_parent = self.selector_block(3, 123, 3)
        with self.assertRaisesRegex(SystemExit, "age exceeded"):
            validate_selector_transition(
                max_parent,
                decode_selector(self.selector_block(4, 123, 4)),
                4,
                3,
            )

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
