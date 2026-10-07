#!/usr/bin/env python3
"""Independent checks for the live harness's oracle and isolated transaction inputs."""
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import miner_pass_upgrade_services as service


class LiveUpgradeOracleTests(unittest.TestCase):
    def test_manual_boundary_conversion_growth_and_penalty(self):
        balances = [200000] * 183
        # Born at 158: H159 earns 2, H160 converts 2->4 then earns 4.
        self.assertEqual(service.reference(158, balances, 159), 2)
        self.assertEqual(service.reference(158, balances, 160), 8)
        self.assertEqual(service.reference(158, balances, 180), 90)
        balances[161:] = [100000] * (len(balances)-161)
        # H161 earns 4, then loses 1 unit * 3 age * 2 rate * 3/2 = 9.
        self.assertEqual(service.reference(158, balances, 161), 3)
        self.assertEqual(service.reference(158, balances, 162), 5)

    def test_zero_balance_resets_age_and_caps_arithmetic(self):
        balances = [0] * 159 + [100000, 200000, 100000]
        self.assertEqual(service.reference(158, balances, 161), 0)
        self.assertEqual(service.reference(159, [service.MAX*100000]*162, 161), service.MAX)

    def test_core_balance_reference_tracks_spends_and_same_block_change(self):
        blocks = {
            'block1': {'tx': [dict(txid='a', vin=[{'coinbase': '00'}], vout=[
                dict(n=0, value=1.00000001, scriptPubKey={'address': 'owner'}),
                dict(n=1, value=5, scriptPubKey={'address': 'other'})])]},
            'block2': {'tx': [dict(txid='b', vin=[dict(txid='a', vout=0)], vout=[
                dict(n=0, value=0.25000001, scriptPubKey={'address': 'owner'})]),
                dict(txid='c', vin=[dict(txid='b', vout=0)], vout=[
                dict(n=0, value=0.12500001, scriptPubKey={'address': 'owner'})])]},
        }
        def fake(_url, method, params, _cookie):
            return f'block{params[0]}' if method == 'getblockhash' else blocks[params[0]]
        with patch.object(service, 'rpc', side_effect=fake):
            births, balances = service.core_history('local', 'cookie', ['owner'], 2)
        self.assertEqual(balances['owner'], [0, 100000001, 12500001])
        self.assertEqual(births, {'a': 1, 'b': 2, 'c': 2})

    def test_seeded_matrix_is_repeatable_and_has_distinct_workloads(self):
        self.assertEqual(service.scenario(41), service.scenario(41))
        self.assertNotEqual(service.scenario(41)['deposits'], service.scenario(42)['deposits'])
        self.assertEqual([service.scenario(s)['reorg_rounds'] for s in (0, 41, 42, 43)], [1, 3, 2, 3])

    def test_configuration_rejects_other_networks_without_writing(self):
        with tempfile.TemporaryDirectory() as root:
            config, catalog = Path(root)/'config.json', Path(root)/'catalog.json'
            catalog.write_text(json.dumps({'registries': [{'scope': {'rules_scope': service.SCOPE}}]}))
            for value in [dict(bitcoin={'network': 'mainnet'}, usdb={'genesis_block_height': 1}),
                          dict(config={'usdb': {'btcNetworkId': 'btc-mainnet', 'btcIndexOriginHeight': 1}})]:
                before = json.dumps(value)
                config.write_text(before)
                with self.assertRaises(ValueError):
                    service.configure(config, catalog)
                self.assertEqual(config.read_text(), before)

    def test_unknown_boundary_check_waits_for_rejection_evidence(self):
        # Execute the actual bounded polling block with a controlled producer.
        import os
        import subprocess
        source = Path(__file__).with_name('run_miner_pass_upgrade_services.sh').read_text()
        start = source.index('rejection_deadline=')
        end = source.index('regtest_assert_json_expr', start)
        with tempfile.TemporaryDirectory() as root:
            directory = Path(root)
            (directory/'logs').mkdir()
            log = directory/'logs/test.log'
            log.write_text('startup ready\n')
            producer = subprocess.Popen(['bash','-c',
                'sleep 0.4; echo "version not supported: energy_formula_version=conformance-energy:double" >> "$1"; sleep 2',
                'producer',str(log)])
            try:
                result = subprocess.run(['bash','-c','set -euo pipefail\n'+source[start:end]],
                    env=dict(os.environ,RUN_ROOT=root,USDB_INDEXER_ROOT=root,
                             USDB_INDEXER_PID=str(producer.pid),SYNC_TIMEOUT_SEC='3'),
                    capture_output=True, text=True, timeout=5)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertIn('version not supported', (directory/'ordinary-indexer-rejection.txt').read_text())
            finally:
                producer.terminate()
                producer.wait()

    def test_prepared_tools_reject_replaced_binary(self):
        with tempfile.TemporaryDirectory() as root:
            directory = Path(root)
            for name in service.TOOLS:
                (directory/name).write_bytes(name.encode())
            with patch.object(service, 'source_identity', return_value={'revision':'test'}):
                service.record_tools(directory, directory, directory)
            self.assertEqual(len(service.check_tools(directory)['binaries']), 5)
            (directory/'geth-default').write_bytes(b'replaced')
            with self.assertRaisesRegex(ValueError, 'geth-default'):
                service.check_tools(directory)

    def test_cardinal_spend_excludes_inscription_coin_and_keeps_owner_change(self):
        calls = []
        def fake(_url, method, params=None, cookie=None):
            calls.append((method, params))
            if method == 'getblockchaininfo':
                return {'chain': 'regtest'}
            if method == 'listunspent':
                return [dict(txid='protected', vout=0, spendable=True, amount=5),
                        dict(txid='cardinal', vout=1, spendable=True, amount=2)]
            if method == 'createrawtransaction':
                self.assertEqual(params[0], [dict(txid='cardinal', vout=1)])
                self.assertEqual(params[1], {'owner': 1.4999, 'destination': 0.5})
                return 'raw'
            if method == 'signrawtransactionwithwallet':
                return {'complete': True, 'hex': 'signed'}
            if method == 'sendrawtransaction':
                self.assertEqual(params, ['signed'])
                return 'sent'
            self.fail(method)
        with tempfile.TemporaryDirectory() as root:
            protected = Path(root)/'inscriptions.json'
            protected.write_text('[{"location":"protected:0:100"}]')
            with patch.object(service, 'rpc', side_effect=fake):
                self.assertEqual(service.spend_cardinal('local', 'cookie', 'owner', 'destination', '0.5', protected), 'sent')
            with patch.object(service, 'rpc', return_value={'chain': 'main'}) as rpc:
                with self.assertRaises(ValueError):
                    service.spend_cardinal('local', 'cookie', 'owner', 'destination', '0.5', protected)
                self.assertEqual(rpc.call_count, 1)


if __name__ == '__main__':
    unittest.main()
