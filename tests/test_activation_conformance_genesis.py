"""Generated conformance checkpoints must remain in the selected BTC rule scope."""
import copy
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / 'scripts/usdb'))
from prepare_usdb_activation_conformance_catalog import prepare_catalog

GOLDEN = json.loads((ROOT / 'internal/usdb/testdata/miner_pass_v2_activation_golden.json').read_text())


class ActivationConformanceGenesisTests(unittest.TestCase):
    def configure(self, script, arguments, explicit_scope=True):
        base = dict(block=0, btcActivationRegistryId=GOLDEN['registries'][0]['activation_registry_id'],
                    btcAnchorMaxAgeBlocks=6650,
                    versions=dict(difficultyPolicyVersion=1, quotePolicyVersion=0, auxPoolPolicyVersion=0,
                                  payloadVersion=1, btcAnchorPolicyVersion=1))
        if explicit_scope:
            base['btcRulesScope'] = GOLDEN['registries'][0]['rules_scope']
        original = dict(config=dict(usdb=dict(btcNetworkId='btc-regtest', btcIndexOriginHeight=1,
                                             activations=[copy.deepcopy(base)])), alloc={})
        with tempfile.TemporaryDirectory(prefix='usdb-activation-config-') as directory:
            path = Path(directory) / 'genesis.json'
            path.write_text(json.dumps(original))
            result = subprocess.run([sys.executable, str(ROOT / 'scripts/usdb' / script),
                                     '--genesis', str(path), *arguments], capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            return base, json.loads(path.read_text())['config']['usdb']['activations']

    def test_difficulty_upgrade_uses_scoped_staged_registry(self):
        for explicit in (False, True):
            with self.subTest(explicit_scope=explicit):
                base, checkpoints = self.configure('configure_usdb_activation_conformance_genesis.py',
                                                  ['--activation-block', '4'], explicit)
                self.assertEqual(checkpoints[0], base)
                expected = copy.deepcopy(base)
                expected.update(block=4, btcActivationRegistryId=GOLDEN['registries'][1]['activation_registry_id'])
                expected['versions']['difficultyPolicyVersion'] = 65535
                self.assertEqual(checkpoints, [base, expected])
                self.assertEqual(GOLDEN['registries'][0]['rules_scope'], GOLDEN['registries'][1]['rules_scope'])

    def test_economic_upgrade_preserves_complete_btc_identity(self):
        base, checkpoints = self.configure('configure_usdb_economic_activation_conformance_genesis.py',
                                           ['--v2-activation-block', '3', '--v3-activation-block', '6'])
        self.assertEqual(checkpoints[0], base)
        for checkpoint, block, version in zip(checkpoints[1:], (3, 6), (65534, 65535)):
            expected = copy.deepcopy(base)
            expected['block'] = block
            expected['versions'].update(quotePolicyVersion=version, auxPoolPolicyVersion=version)
            self.assertEqual(checkpoint, expected)


class ActivationConformanceCatalogTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix='usdb-activation-catalog-')
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.current = GOLDEN['registries'][0]['activation_registry_id']
        self.scope = dict(network_id='btc-regtest', rules_scope=GOLDEN['registries'][0]['rules_scope'])
        revision = dict(scope=self.scope, records=[dict(status='Active', value='unchanged')])
        self.base = dict(current_registry_id=self.current, registries=[revision])
        self.staged = dict(current_registry_id=self.current, registries=[copy.deepcopy(revision), copy.deepcopy(revision)])
        self.staged['registries'][1]['records'].append(dict(status='Planned', value='future'))
        self.config = dict(usdb=dict(activation_registry_id=self.current, rules_scope=self.scope['rules_scope'],
                                    activation_registry_catalog_file='catalog.json'))
        (self.root / 'config.json').write_text(json.dumps(self.config))
        (self.root / 'catalog.json').write_text(json.dumps(self.base))
        self.source = self.root / 'staged.json'

    def prepare(self):
        self.source.write_text(json.dumps(self.staged))
        prepare_catalog(self.root / 'config.json', self.source)

    def test_preloads_new_revision_preserving_current_pin_and_immutable_records(self):
        self.prepare()
        catalog = json.loads((self.root / 'catalog.json').read_text())
        self.assertEqual(catalog, dict(self.staged, current_registry_id=self.current))
        self.assertEqual(json.loads((self.root / 'config.json').read_text()), self.config)
        self.assertEqual(json.loads(self.source.read_text()), self.staged)
        self.prepare()  # Repeated preparation does not change the selected default.

    def test_rejects_foreign_scope_or_rewritten_base_without_writing(self):
        for mutation in ('scope', 'base', 'current_pin'):
            with self.subTest(mutation=mutation):
                original = copy.deepcopy(self.staged)
                if mutation == 'scope':
                    self.staged['registries'][1]['scope']['rules_scope'] = 'foreign'
                elif mutation == 'base':
                    self.staged['registries'][0]['records'] = []
                else:
                    self.staged['current_registry_id'] = GOLDEN['registries'][1]['activation_registry_id']
                with self.assertRaises(ValueError):
                    self.prepare()
                self.assertEqual(json.loads((self.root / 'catalog.json').read_text()), self.base)
                self.staged = original


if __name__ == '__main__':
    unittest.main()
