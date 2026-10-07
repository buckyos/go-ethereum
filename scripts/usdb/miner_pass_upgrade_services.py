#!/usr/bin/env python3
"""Evidence and independent arithmetic for isolated MinerPass multi-epoch services."""
import argparse
import base64
from decimal import Decimal
import json
import hashlib
import subprocess
import random
from pathlib import Path
import urllib.request

from verify_usdb_profile_e2e import VIEW_VERSION, decode_selector, level_for_energy

SCOPE = "miner-pass-upgrade-conformance"
MAX = 2**128 - 1
HEIGHTS = [159, 160, 161, 162, 163, 164, 165, 166, 167, 169, 170, 171, 172, 173, 179, 180, 181, 182]


def rpc(url, method, params=None, cookie=None, expected_error=None):
    headers = {"content-type": "application/json"}
    if cookie:
        headers["Authorization"] = "Basic " + base64.b64encode(Path(cookie).read_bytes().strip()).decode()
    req = urllib.request.Request(url, json.dumps(dict(jsonrpc="2.0", id=1, method=method, params=params or [])).encode(), headers)
    with urllib.request.urlopen(req, timeout=20) as response:
        data = json.load(response)
    if expected_error is not None:
        if data.get('error', {}).get('code') != expected_error:
            raise RuntimeError(f"{method}: expected error {expected_error}, got {data}")
        return {'error': data['error']}
    if "error" in data:
        raise RuntimeError(f"{method}: {data['error']}")
    return data["result"]


def configure(config_path, catalog_path):
    config = json.loads(config_path.read_text())
    catalog = json.loads(catalog_path.read_text())
    if catalog['registries'][0]['scope']['rules_scope'] != SCOPE:
        raise ValueError('Wrong service conformance scope')
    if 'bitcoin' in config:
        if config['bitcoin']['network'] != 'regtest' or config['usdb']['genesis_block_height'] != 1:
            raise ValueError('Only generated regtest origin-1 configurations are accepted')
        config['usdb'].update(rules_scope=SCOPE, activation_registry_id=catalog['current_registry_id'],
                             activation_registry_catalog_file=str(catalog_path.resolve()),
                             inscription_source="bitcoind", inscription_source_shadow_compare=True, inscription_source_shadow_fail_fast=True)
    else:
        settings = config['config']['usdb']
        if settings['btcNetworkId'] != 'btc-regtest' or settings['btcIndexOriginHeight'] != 1:
            raise ValueError('Only generated regtest genesis is accepted')
        if len(settings['activations']) != 1:
            raise ValueError('Expected fresh single-checkpoint genesis')
        settings['activations'][0]['btcActivationRegistryId'] = catalog['current_registry_id']
    config_path.write_text(json.dumps(config, indent=2)+'\n')


def reference(birth, balances, through):
    """Per-block model, independent of Rust dispatch, checkpoints and interval settlement."""
    energy, age = 0, birth
    for height in range(birth+1, through+1):
        if height == 160:
            energy = min(MAX, energy*2)
        rate = 1 if height < 160 else 2 if height < 180 else 3
        before, after = balances[height-1]//100000, balances[height]//100000
        energy = min(MAX, energy + before*rate)
        energy = max(0, energy-max(0, before-after)*(height-age)*rate*3//2)
        if (before == 0 and after > 0) or (before > 0 and after == 0):
            age = height
    return energy


def core_history(url, cookie, owners, tip):
    """Reconstruct the owners' balances directly from canonical Core transaction inputs/outputs."""
    balances = {owner: [0] for owner in owners}
    coins = {}
    births = {}
    for height in range(1,tip+1):
        block = rpc(url,'getblock',[rpc(url,'getblockhash',[height],cookie),2],cookie)
        current = {owner: rows[-1] for owner, rows in balances.items()}
        for tx in block['tx']:
            births[tx['txid']] = height
            for vin in tx['vin']:
                coin = coins.pop((vin.get('txid'),vin.get('vout')),None)
                if coin:
                    current[coin[0]] -= coin[1]
            for out in tx['vout']:
                owner = out['scriptPubKey'].get('address')
                if owner in current:
                    sats = int(Decimal(str(out['value']))*100000000)
                    coins[tx['txid'],out['n']] = owner,sats
                    current[owner] += sats
        for owner, value in current.items():
            balances[owner].append(value)
    return births, balances


def capture(url, ids, heights):
    evidence = {}
    for height in heights:
        common = dict(view_version=VIEW_VERSION, block_height=height)
        state = rpc(url,'get_state_ref_at_height',[dict(block_height=height)])
        profiles = [rpc(url,'get_pass_economic_profile',[dict(common,pass_id=pid)]) for pid in ids]
        # Invalid mints have an audit/profile but deliberately never create an energy row.
        energies = [rpc(url,'get_pass_energy',[dict(inscription_id=pid,block_height=height)],
                        expected_error=-32012 if profile['pass']['state'] == 'invalid' else None)
                    for pid, profile in zip(ids,profiles)]
        audits = [rpc(url,'get_pass_mint_audit',[dict(inscription_id=pid,at_height=height)]) for pid in ids]
        candidates = rpc(url,'get_candidate_set_view',[dict(common,limit=100)])
        collabs = rpc(url,'get_collab_breakdown',[dict(common,leader_pass_id=ids[0],limit=100)])
        aggregate = rpc(url,'get_miner_economic_aggregate',[common])
        evidence[str(height)] = dict(state=state,profiles=profiles,energies=energies,audits=audits,
                                    candidates=candidates,collabs=collabs,aggregate=aggregate)
    return evidence


def check_reference(evidence, ids, owners, core_url, cookie):
    heights = sorted(map(int,evidence))
    births, balances = core_history(core_url,cookie,owners,max(heights))
    for height in heights:
        entry = evidence[str(height)]
        raw = [reference(births[pid.split('i')[0]],balances[owner],height) for pid,owner in zip(ids,owners)]
        contribution = sum(value*(5000 if height < 163 else 2500)//10000 for value in raw[1:])
        for index, value in enumerate(raw):
            actual = entry['profiles'][index]['pass']
            assert int(actual['raw_energy']) == value, (height,ids[index],'raw',actual,value)
            assert entry['energies'][index]['raw_energy'] == str(value)
        leader = entry['profiles'][0]['pass']
        effective = raw[0]+contribution
        level = level_for_energy(effective) if height < 166 else min(effective//1000,50)
        assert int(leader['collab_contribution']) == contribution, (height,'collab',leader,contribution)
        assert int(leader['effective_energy']) == effective
        assert (leader['level'],leader['difficulty_factor_bps']) == (level,10000-100*level)
        assert int(entry['collabs']['aggregate_collab_contribution']) == contribution
        assert entry['candidates']['items'][0]['effective_energy'] == str(effective)


def scenario(seed):
    """Frozen operation schedule; seed changes deposits, withdrawal size and reorg repetitions."""
    rng = random.Random(seed)
    return dict(seed=seed, deposits=[str(Decimal(rng.randint(100000,300000))/100000) for _ in range(3)],
                topup=str(Decimal(rng.randint(10000,50000))/100000),
                withdrawal=str(Decimal(rng.randint(10000,50000))/100000), reorg_rounds=1 if seed == 0 else 2+seed%2)


def spend_cardinal(url, cookie, owner, destination, amount, protected):
    if rpc(url,'getblockchaininfo',cookie=cookie)['chain'] != 'regtest':
        raise ValueError('This helper only spends isolated regtest coins')
    occupied = {row['location'].rsplit(':',1)[0] for row in json.loads(protected.read_text())}
    outputs = rpc(url,'listunspent',[1,9999999,[owner]],cookie)
    coins = [c for c in outputs if c['spendable'] and f"{c['txid']}:{c['vout']}" not in occupied and Decimal(str(c['amount'])) > Decimal(amount)+Decimal('0.0001')]
    coin = sorted(coins,key=lambda c:(-Decimal(str(c['amount'])),c['txid'],c['vout']))[0]
    change = Decimal(str(coin['amount']))-Decimal(amount)-Decimal('0.0001')
    tx = rpc(url,'createrawtransaction',[[dict(txid=coin['txid'],vout=coin['vout'])],{owner:float(change),destination:float(amount)}],cookie)
    signed = rpc(url,'signrawtransactionwithwallet',[tx],cookie)
    assert signed['complete']
    return rpc(url,'sendrawtransaction',[signed['hex']],cookie)


def check_inheritance(url, core_url, cookie, parent, child, owner, child_owner, height):
    """Independently settle the source through reveal, then apply this test epoch's 75%."""
    births, balances = core_history(core_url, cookie, [owner, child_owner], height)
    birth = births[child.split('i')[0]]
    source = reference(births[parent.split('i')[0]], balances[owner], birth)
    # This scenario leaves only inscription postage at the successor until the check.
    assert all(value // 100000 == 0 for value in balances[child_owner][birth:])
    expected = source * 3 // 4
    actual = rpc(url, 'get_pass_economic_profile', [dict(view_version=VIEW_VERSION, pass_id=child, block_height=height)])
    assert expected > 0 and int(actual['pass']['raw_energy']) == expected, (birth, source, expected, actual)
    return dict(reveal_height=birth, source_energy=str(source), inherited_energy=str(expected), profile=actual)


TOOLS = ('indexer-default', 'indexer-conformance', 'balance-history', 'geth-default', 'geth-conformance')


def sha256(path):
    digest = hashlib.sha256()
    with path.open('rb') as stream:
        for block in iter(lambda: stream.read(1024*1024), b''):
            digest.update(block)
    return digest.hexdigest()


def source_identity(repo):
    def git(*args):
        return subprocess.check_output(['git', '-C', str(repo), *args])
    return dict(revision=git('rev-parse', 'HEAD').decode().strip(),
                tracked_diff_sha256=hashlib.sha256(git('diff', '--binary', 'HEAD')).hexdigest(),
                untracked={name: sha256(repo/name) for name in git('ls-files', '--others', '--exclude-standard').decode().splitlines()})


def record_tools(directory, go_repo, usdb_repo):
    manifest = dict(schema_version='miner-pass-test-tools:v1',
                    rust_feature='miner-pass-conformance', go_tag='usdb_miner_pass_conformance',
                    sources=dict(go_ethereum=source_identity(go_repo), usdb=source_identity(usdb_repo)),
                    binaries={name: sha256(directory/name) for name in TOOLS})
    (directory/'build-manifest.json').write_text(json.dumps(manifest, indent=2)+'\n')


def check_tools(directory):
    manifest = json.loads((directory/'build-manifest.json').read_text())
    if manifest.get('schema_version') != 'miner-pass-test-tools:v1' or set(manifest['binaries']) != set(TOOLS):
        raise ValueError('Incomplete prepared MinerPass tools manifest')
    for name in TOOLS:
        if sha256(directory/name) != manifest['binaries'][name]:
            raise ValueError(f'Prepared binary changed since build: {directory/name}')
    return manifest


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest='command',required=True)
    inherited = sub.add_parser('inheritance')
    for arg in ['url','core-url','cookie','parent','child','owner','child-owner']:
        inherited.add_argument('--'+arg, required=True)
    inherited.add_argument('--height', type=int, required=True)
    inherited.add_argument('--output', type=Path, required=True)
    tools = sub.add_parser('record-tools')
    tools.add_argument('directory', type=Path)
    tools.add_argument('go_repo', type=Path)
    tools.add_argument('usdb_repo', type=Path)
    sub.add_parser('check-tools').add_argument('directory', type=Path)
    plan = sub.add_parser('scenario')
    plan.add_argument('--seed',type=int,required=True); plan.add_argument('--output',type=Path,required=True)
    spend = sub.add_parser('spend-cardinal')
    for arg in ['url','cookie','owner','destination','amount']: spend.add_argument('--'+arg,required=True)
    spend.add_argument('--protected',type=Path,required=True)
    config = sub.add_parser('configure')
    config.add_argument('config',type=Path); config.add_argument('catalog',type=Path)
    dump = sub.add_parser('capture')
    dump.add_argument('--url',required=True); dump.add_argument('--output',type=Path,required=True)
    dump.add_argument('--ids',nargs='+',required=True); dump.add_argument('--owners',nargs='+')
    dump.add_argument('--heights',nargs='+',type=int,default=HEIGHTS)
    dump.add_argument('--core-url'); dump.add_argument('--cookie')
    compare = sub.add_parser('compare')
    compare.add_argument('left',type=Path); compare.add_argument('right',type=Path)
    blocks = sub.add_parser('blocks')
    blocks.add_argument('--url',required=True); blocks.add_argument('--output',type=Path,required=True)
    args = parser.parse_args()
    if args.command == 'record-tools':
        record_tools(args.directory, args.go_repo, args.usdb_repo)
    elif args.command == 'check-tools':
        check_tools(args.directory)
    elif args.command == 'inheritance':
        result = check_inheritance(args.url,args.core_url,args.cookie,args.parent,args.child,args.owner,args.child_owner,args.height)
        args.output.write_text(json.dumps(result,indent=2)+'\n')
    elif args.command == 'scenario':
        args.output.write_text(json.dumps(scenario(args.seed),indent=2)+'\n')
    elif args.command == 'spend-cardinal':
        print(spend_cardinal(args.url,args.cookie,args.owner,args.destination,args.amount,args.protected))
    elif args.command == 'configure':
        configure(args.config,args.catalog)
    elif args.command == 'capture':
        evidence = capture(args.url,args.ids,args.heights)
        if args.owners:
            check_reference(evidence,args.ids,args.owners,args.core_url,args.cookie)
        args.output.write_text(json.dumps(evidence,indent=2,sort_keys=True)+'\n')
    elif args.command == 'compare':
        left, right = json.loads(args.left.read_text()),json.loads(args.right.read_text())
        assert left == right, f'Canonical evidence differs: {args.left} != {args.right}'
        print(f'MATCH {args.left.name} {args.right.name}')
    else:
        tip = int(rpc(args.url,'eth_blockNumber'),16)
        rows = [rpc(args.url,'eth_getBlockByNumber',[hex(h),False]) for h in range(1,tip+1)]
        seen = [decode_selector(row)['btc_height'] for row in rows]
        assert any(h < 160 for h in seen) and any(160 <= h < 180 for h in seen) and any(h >= 180 for h in seen), seen
        args.output.write_text(json.dumps(rows,indent=2)+'\n')
        print(f'Geth blocks={tip}, BTC anchors={sorted(set(seen))}')

if __name__ == '__main__':
    main()
