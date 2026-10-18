"""Exercise the real init/start scripts without starting a blockchain process."""
import os
import json
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]


class RuntimeIdentityTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix='usdb-runtime-identity-')
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.data = self.root / 'chain'
        self.data.mkdir()
        self.key = self.data / 'geth/nodekey'
        self.genesis = self.root / 'genesis.json'
        self.genesis.write_text('{}\n')
        validator = self.root / 'validator'
        validator.write_text('#!/bin/sh\nexit 0\n')
        validator.chmod(0o700)
        binary = self.root / 'geth'
        binary.write_text('#!/bin/sh\nprintf "%s\\n" "$@" > "$PROBE_ARGS"\n')
        binary.chmod(0o700)
        self.env = {**os.environ, 'USDB_CHAIN_DATA_DIR':str(self.data), 'USDB_GENESIS_FILE':str(self.genesis),
                    'USDB_GENESIS_MANIFEST_FILE':str(self.genesis), 'USDB_GENESIS_VALIDATOR':str(validator),
                    'USDB_GETH_BIN':str(binary), 'PROBE_ARGS':str(self.root / 'args'),
                    'USDB_CHAIN_ID':'202610030', 'USDB_NETWORK_ID':'202610030',
                    'USDB_INDEXER_RPC_URL':'http://127.0.0.1:1'}

    def run_script(self, name):
        return subprocess.run(['bash', str(ROOT / 'scripts/usdb/docker' / name)], env=self.env,
                              capture_output=True, text=True, timeout=10)

    def identity(self):
        self.key.parent.mkdir(exist_ok=True)
        self.key.write_text('0' * 63 + '1')
        return self.key.read_bytes()

    def test_missing_identity_never_starts_init_or_node(self):
        for script in ('usdb_runtime_init.sh', 'usdb_runtime_node.sh'):
            result = self.run_script(script)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn('P2P_IDENTITY_REQUIRED', result.stderr)
            self.assertFalse(self.key.exists())
            self.assertFalse((self.root / 'args').exists())

    def test_preserved_identity_allows_init_without_replacement(self):
        before = self.identity()
        result = self.run_script('usdb_runtime_init.sh')
        self.assertEqual(result.returncode, 0, result.stderr)
        arguments = (self.root / 'args').read_text().splitlines()
        self.assertIn('--nodekey', arguments)
        self.assertEqual(arguments[arguments.index('--nodekey') + 1], str(self.key))
        self.assertEqual(self.key.read_bytes(), before)

    def test_database_unknown_file_and_symlink_are_not_identity_only(self):
        self.identity()
        for relative in ('chaindata', 'unknown-state'):
            path = self.key.parent / relative
            path.mkdir()
            result = self.run_script('usdb_runtime_init.sh')
            self.assertNotEqual(result.returncode, 0)
            self.assertFalse((self.root / 'args').exists())
            path.rmdir()
        self.key.unlink()
        source = self.root / 'source-key'
        source.write_text('0' * 63 + '1')
        self.key.symlink_to(source)
        self.assertNotEqual(self.run_script('usdb_runtime_init.sh').returncode, 0)

    def test_runtime_always_loads_explicit_identity(self):
        self.identity()
        self.assertEqual(self.run_script('usdb_runtime_init.sh').returncode, 0)
        chain = self.data / 'geth/chaindata'
        chain.mkdir()
        (chain / 'CURRENT').write_text('fixture')
        result = self.run_script('usdb_runtime_node.sh')
        self.assertEqual(result.returncode, 0, result.stderr)
        arguments = (self.root / 'args').read_text().splitlines()
        self.assertEqual(arguments[arguments.index('--nodekey') + 1], str(self.key))

    @unittest.skipUnless(os.environ.get('USDB_IDENTITY_GETH_TEST_BIN'), 'requires an explicitly built local geth binary')
    def test_real_geth_init_preserves_identity_and_rejects_invalid_key(self):
        self.env['USDB_GETH_BIN'] = os.environ['USDB_IDENTITY_GETH_TEST_BIN']
        self.genesis.write_text(json.dumps({'config': {'chainId': 202610030},
            'gasLimit': '0x1c9c380', 'difficulty': '0x1', 'alloc': {}}))
        before = self.identity()
        result = self.run_script('usdb_runtime_init.sh')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.key.read_bytes(), before)
        self.assertTrue((self.data / 'geth/chaindata/CURRENT').is_file())
        self.assertEqual(self.run_script('usdb_runtime_init.sh').returncode, 0)
        self.assertEqual(self.key.read_bytes(), before)
        # A different fresh chain directory has an invalid existing identity.
        self.data = self.root / 'invalid-chain'
        self.key = self.data / 'geth/nodekey'
        self.key.parent.mkdir(parents=True)
        self.key.write_bytes(b'corrupt-key')
        self.env['USDB_CHAIN_DATA_DIR'] = str(self.data)
        result = self.run_script('usdb_runtime_init.sh')
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.key.read_bytes(), b'corrupt-key')
        self.assertFalse((self.data / 'bootstrap/usdb-init.done.json').exists())


if __name__ == '__main__':
    unittest.main()
