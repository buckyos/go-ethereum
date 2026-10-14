"""Bind isolated activation binaries to their source identities and build tags."""
import argparse
import json
from pathlib import Path
import sys

sys.path.insert(0, str(Path(__file__).resolve().parents[2] / 'scripts/usdb'))
from miner_pass_upgrade_services import sha256, source_identity

BINARIES = ('geth', 'invalidblock', 'balance-history', 'usdb-indexer')
TAGS = ['usdb_miner_pass_conformance', 'usdb_activation_conformance', 'usdb_economic_conformance_v3']


def check(directory):
    manifest = json.loads((directory / 'build-manifest.json').read_text())
    if (manifest.get('schema') != 'gap-economic-tools:v1' or manifest.get('go_tags') != TAGS
            or manifest.get('rust_feature') != 'miner-pass-conformance'
            or set(manifest.get('binaries', {})) != set(BINARIES)):
        raise ValueError('Invalid economic activation tools manifest')
    for name in BINARIES:
        if sha256(directory / 'bin' / name) != manifest['binaries'][name]:
            raise ValueError(f'Prepared economic fixture binary changed: {name}')
    return manifest


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=('record', 'check'))
    parser.add_argument('directory', type=Path)
    parser.add_argument('go_repo', type=Path, nargs='?')
    parser.add_argument('usdb_repo', type=Path, nargs='?')
    args = parser.parse_args()
    if args.action == 'record':
        manifest = dict(schema='gap-economic-tools:v1', go_tags=TAGS, rust_feature='miner-pass-conformance',
                        sources=dict(go_ethereum=source_identity(args.go_repo), usdb=source_identity(args.usdb_repo)),
                        binaries={name: sha256(args.directory / 'bin' / name) for name in BINARIES})
        (args.directory / 'build-manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')
    else:
        check(args.directory)
