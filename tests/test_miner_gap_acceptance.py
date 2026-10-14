#!/usr/bin/env python3
"""Regression gates for actual work evidence and isolated long-CI controls."""

import copy
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

from common.anchor_gap_evidence import ImportObservations, selector_identity, validate_work
from common.profile_blocks import selector_block
from multi_miner_acceptance import decode_selector, validate_chain, MINER

ROOT = Path(__file__).resolve().parents[1]


class GapEvidenceTests(unittest.TestCase):
    def test_work_requires_actual_selected_identity_not_latest_attempt(self):
        block = selector_block(1, 100, 0)
        selector = decode_selector(block)
        work = dict(parent_hash=block['parentHash'], build=dict(
            block_number=1, miner_address=MINER, pass_id=selector['pass_id'], stage='validated',
            snapshot_id=selector['snapshot_id'], system_state_id=selector['system_state_id'], anchor_age=0,
            selection=dict(latest_height=102, selected_height=100, configured_gap=2, actual_gap=2)))
        self.assertEqual(validate_work(block, selector, work, 2)['actual_gap'], 2)
        for key, value in [('block_number', 2), ('pass_id', 'another-pass'), ('snapshot_id', 'wrong'),
                           ('system_state_id', 'wrong'), ('anchor_age', 1), ('failure', 'state_not_ready')]:
            bad = copy.deepcopy(work)
            bad['build'][key] = value
            with self.subTest(key=key), self.assertRaises(ValueError):
                validate_work(block, selector, bad, 2)
        for key, value in [('selected_height', 101), ('latest_height', 103), ('actual_gap', 0), ('configured_gap', 0)]:
            bad = copy.deepcopy(work)
            bad['build']['selection'][key] = value
            with self.subTest(key=key), self.assertRaises(ValueError):
                validate_work(block, selector, bad, 2)
        with self.assertRaises(ValueError):
            validate_work(block, selector, {**work, 'parent_hash': 'fork'}, 2)

    def test_observations_preserve_unknown_receipt_and_unfinished_parent_wait(self):
        ledger = ImportObservations()
        first = ledger.add('b', 'block1', (100, 'snapshot', 'state'), 10)
        child = ledger.add('b', 'block2', (100, 'snapshot', 'state'), 12, parent_wait=True)
        ledger.mark(first, 'first_failed_at', 11)
        ledger.mark(first, 'ready_at', 20)
        ledger.mark(first, 'imported_at', 22)
        rows = ledger.rows(25)
        self.assertEqual(rows[0]['validation_wait_seconds'], 11)
        self.assertIsNone(rows[0]['receipt_to_import_seconds'])
        self.assertIsNone(rows[0]['state_lead_seconds'])
        self.assertFalse(rows[0]['unresolved'])
        self.assertTrue(rows[1]['unresolved'])
        self.assertTrue(rows[1]['parent_wait'])
        self.assertIsNone(rows[1]['validation_wait_seconds'])
        # A later sample cannot rewrite the first observed event.
        ledger.mark(first, 'imported_at', 24)
        self.assertEqual(ledger.rows(25)[0]['imported_at'], 22)
        self.assertEqual(len(ledger.samples), 2)
        with self.assertRaises(ValueError):
            ledger.add('b', 'block1', (100, 'snapshot', 'state'), 26)
        ledger.mark(child, 'received_at', 15)
        ledger.mark(child, 'ready_at', 20)
        self.assertEqual(ledger.rows(25)[1]['state_lead_seconds'], -5)

    def test_same_height_different_fork_and_observer_restart_never_share_times(self):
        ledger = ImportObservations()
        first = ledger.add('b', 'block1', (100, 'branch1', 'state1'), 10, ready_at=8)
        ledger.mark(first, 'received_at', 11)
        ledger.add('b', 'block2', (100, 'branch2', 'state2'), 12)
        self.assertEqual(ledger.rows(13)[0]['state_lead_seconds'], 3)
        self.assertIsNone(ledger.rows(13)[1]['state_lead_seconds'])
        restarted = ImportObservations()
        restarted.add('b', 'block1', (100, 'branch1', 'state1'), 20)
        self.assertIsNone(restarted.rows(21)[0]['ready_at'])
        self.assertNotEqual(selector_identity(dict(btc_height=100, snapshot_id='a', system_state_id='a')),
                            selector_identity(dict(btc_height=100, snapshot_id='b', system_state_id='b')))

    def test_small_m_chain_validator_detects_overreuse(self):
        blocks = [dict(number='0x0', hash='hash-0')] + [selector_block(i + 1, 100, i) for i in range(4)]
        passes = {MINER: '01' * 32 + 'i0'}
        validate_chain(blocks, passes, max_age=3)
        blocks.append(selector_block(5, 100, 4))
        with self.assertRaises(ValueError):
            validate_chain(blocks, passes, max_age=3)

    def test_ci_controls_use_distinct_invocations_and_expected_parameters(self):
        # Fake only the child shell at the suite boundary; verify all real
        # entrypoint invocations and isolation destinations without services.
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            fake = root / 'bash'
            fake.write_text('#!/usr/bin/env python3\nimport json, os\n'
                            'print(json.dumps({k:v for k,v in os.environ.items() if k.startswith("MATRIX_")}))\n')
            fake.chmod(0o755)
            env = dict(os.environ, PATH=str(root) + os.pathsep + os.environ['PATH'],
                       MATRIX_WORK_ROOT='/tmp/isolated-work', MATRIX_OUTPUT_DIR='/tmp/isolated-output')
            results = {}
            for tier in ('nightly', 'weekly'):
                run = subprocess.run(['/bin/bash', str(ROOT / 'scripts/usdb/run_miner_gap_matrix.sh'), tier],
                                     env=env, text=True, capture_output=True, check=True)
                results[tier] = [json.loads(row) for row in run.stdout.splitlines()]
            nightly = results['nightly']
            self.assertEqual(len(nightly), 8)
            self.assertEqual({(r['MATRIX_GAP'], r['MATRIX_GAP_LAG']) for r in nightly[:-2]},
                             {(str(g), str(l)) for g in range(3) for l in (1, 2)})
            self.assertEqual((nightly[-2]['MATRIX_GAP'], nightly[-2]['MATRIX_GAP_LAG']), ('2', '3'))
            self.assertEqual(nightly[-1]['MATRIX_GAP_CASE'], 'exhaustion')
            weekly = results['weekly']
            self.assertEqual(weekly[0]['MATRIX_GAP_CASE'], 'mixed')
            self.assertEqual([r['MATRIX_GAP_DELAY_SECONDS'] for r in weekly[1:-1]], ['10', '30', '60', '120'])
            self.assertEqual(weekly[-1]['MATRIX_MINER_GAPS'], '0,2,1')
            for rows in results.values():
                self.assertEqual(len({r['MATRIX_OUTPUT_DIR'] for r in rows}), len(rows))
                self.assertTrue(all(r['MATRIX_SKIP_BUILD'] == '1' for r in rows))


if __name__ == '__main__':
    unittest.main()
