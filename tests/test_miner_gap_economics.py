#!/usr/bin/env python3
"""Negative controls for the live historical-economic evidence assertions."""
import copy
import json
from pathlib import Path
import tempfile
import unittest

from miner_gap_economics import assert_economics, expected_pass, AUX2, AUX3
from common.profile_blocks import selector_block
from common.gap_economic_tools import BINARIES, TAGS, check, sha256
from multi_miner_acceptance import validate_chain, MINER


class EconomicEvidenceTests(unittest.TestCase):
    def fixture(self, number=1, height=165):
        block = selector_block(number, height, 0)
        block.update(difficulty=hex(8192), timestamp=hex(109), transactions=[], uncles=[])
        parent = dict(difficulty=hex(8192), timestamp=hex(100), uncles=[])
        profile = dict(pass_=None, miner_aggregate=dict(total_miner_btc_sats='100000000'))
        profile['pass'] = dict(raw_energy='1000', collab_contribution='1000', effective_energy='2000',
                               level=0, difficulty_factor_bps=10000)
        return block, parent, profile

    def test_latest_pass_cannot_replace_historical_pass(self):
        timeline = [(100, 'old'), (120, 'new'), (130, None)]
        for height, wanted in ((99, None), (100, 'old'), (119, 'old'), (120, 'new'), (130, None)):
            self.assertEqual(expected_pass(timeline, height), wanted)
        blocks = [dict(number='0x0', hash='hash-0'), selector_block(1, 119, 0), selector_block(2, 120, 0, pass_byte=2)]
        schedule = [(100, '01' * 32 + 'i0'), (120, '02' * 32 + 'i0')]
        validate_chain(blocks, {MINER: schedule[-1][1]}, pass_at=lambda _, h: expected_pass(schedule, h))
        blocks[1] = selector_block(1, 119, 0, pass_byte=2)
        with self.assertRaisesRegex(ValueError, 'wrong miner/pass'):
            validate_chain(blocks, {MINER: schedule[-1][1]}, pass_at=lambda _, h: expected_pass(schedule, h))

    def test_btc_formula_uses_selected_height(self):
        block, parent, profile = self.fixture()
        assert_economics(block, parent, profile, activation=True, issued=0)
        # Applying the already-active-at-L=167 level to selected H=165 must fail.
        profile['pass'].update(level=2, difficulty_factor_bps=9800)
        with self.assertRaisesRegex(ValueError, 'wrong BTC level'):
            assert_economics(block, parent, profile, activation=True, issued=0)

    def test_child_policy_switch_preserves_same_btc_height(self):
        for number, recipient, numerator in ((2, None, 0), (3, AUX2, 1), (5, AUX3, 2)):
            block, parent, profile = self.fixture(number=number)
            if number >= 3:
                payload = bytearray.fromhex(block['extraData'][2:])
                payload[1:3] = (65535).to_bytes(2, 'big')
                block['extraData'] = '0x' + payload.hex()
                block['difficulty'] = hex(8193)
            rewards = assert_economics(block, parent, profile, activation=True, issued=0)
            self.assertEqual(rewards['recipient'], recipient)
            self.assertEqual(rewards['aux'], rewards['emission'] * numerator // 10)
            self.assertEqual(rewards['miner'] + rewards['aux'], rewards['emission'])
            if number >= 3:
                bad = copy.deepcopy(block)
                bad['difficulty'] = hex(8192)
                with self.assertRaisesRegex(ValueError, 'wrong historical difficulty'):
                    assert_economics(bad, parent, profile, activation=True, issued=0)

    def test_prepared_binary_tampering_is_rejected(self):
        with tempfile.TemporaryDirectory() as temp:
            directory = Path(temp)
            (directory / 'bin').mkdir()
            for name in BINARIES:
                (directory / 'bin' / name).write_bytes(name.encode())
            manifest = dict(schema='gap-economic-tools:v1', go_tags=TAGS, rust_feature='miner-pass-conformance',
                            binaries={name: sha256(directory / 'bin' / name) for name in BINARIES})
            (directory / 'build-manifest.json').write_text(json.dumps(manifest))
            check(directory)
            (directory / 'bin/geth').write_bytes(b'different binary')
            with self.assertRaisesRegex(ValueError, 'binary changed: geth'):
                check(directory)


if __name__ == '__main__':
    unittest.main()
