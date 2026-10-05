#!/usr/bin/env python3
"""Preload both scoped registry revisions without changing the indexer's current pin."""

import argparse
import json
from pathlib import Path

from verify_usdb_profile_e2e import BTC_REGTEST_ACTIVATION_REGISTRY_ID


def prepare_catalog(config_path, catalog_path):
    """Only extend the generated MinerPass fixture; preserve all immutable revision records."""
    config = json.loads(config_path.read_text())
    settings = config['usdb']
    current = settings['activation_registry_id']
    if current != BTC_REGTEST_ACTIVATION_REGISTRY_ID:
        raise ValueError(f'Expected base MinerPass fixture registry: config={config_path}, registry={current}')
    destination = config_path.parent / settings['activation_registry_catalog_file']
    base = json.loads(destination.read_text())
    staged = json.loads(catalog_path.read_text())
    if staged['current_registry_id'] != current:
        raise ValueError(f'Staged catalog must preserve the current pin: catalog={catalog_path}, '
                         f'expected={current}, actual={staged["current_registry_id"]}')
    revisions = staged['registries']
    if len(revisions) != 2 or revisions[0] != base['registries'][0]:
        raise ValueError(f'Staged catalog must preserve the complete base revision: catalog={catalog_path}')
    scope = revisions[0]['scope']
    if (any(revision['scope'] != scope for revision in revisions)
            or scope['network_id'] != 'btc-regtest' or scope['rules_scope'] != settings['rules_scope']):
        raise ValueError(f'Staged catalog scope differs from indexer configuration: catalog={catalog_path}')
    # The staged fixture preloads the future revision while retaining the old
    # default. Do not move that default or rewrite the existing config.
    destination.write_text(json.dumps(staged, indent=2) + '\n')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--indexer-config', type=Path, required=True)
    parser.add_argument('--catalog', type=Path, required=True)
    args = parser.parse_args()
    prepare_catalog(args.indexer_config, args.catalog)


if __name__ == '__main__':
    main()
