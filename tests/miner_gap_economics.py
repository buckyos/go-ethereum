#!/usr/bin/env python3
"""Real three-node historical pass transitions and independent activation axes.

The activation arm requires explicitly tagged regtest binaries. Production
registries, deployed nodes and successful upstream RPC results are never changed.
"""

import argparse
from decimal import Decimal
import json
from pathlib import Path
import shutil
import subprocess
import sys
import time

from miner_gap_acceptance import GapMatrix, main
from multi_miner_acceptance import MINER, SECOND_MINER, require, decode_selector
from upstream_fault_matrix import RPC
from verify_usdb_profile_e2e import (
    VIEW_VERSION, SYSTEM_STATE_SLOTS, USDB_SYSTEM_STATE_ADDRESS, level_for_energy,
    expected_policy_difficulty, calculate_emission, FIXED_PRICE_ATOMS_PER_BTC,
)
from miner_pass_upgrade_services import core_history, reference
from common.anchor_gap_evidence import selector_identity

THIRD_MINER = '0x3333333333333333333333333333333333333333'
AUX2 = '0x000000000000000000000000000000000000fa02'
AUX3 = '0x000000000000000000000000000000000000fa03'


def configure_indexer(path, repo):
    """Copy the existing immutable test catalog into a fresh regtest fixture."""
    data = json.loads(path.read_text())
    require(data['bitcoin']['network'] == 'regtest', 'conformance requires regtest')
    source = repo / 'tests/fixtures/miner-pass-upgrade/live-catalog.json'
    catalog = json.loads(source.read_text())
    name = 'gap-economic-catalog.json'
    shutil.copyfile(source, path.parent / name)
    data['usdb'].update(rules_scope='miner-pass-upgrade-conformance',
                       activation_registry_id=catalog['current_registry_id'],
                       activation_registry_catalog_file=name)
    path.write_text(json.dumps(data, indent=2) + '\n')


def expected_pass(timeline, height):
    """An event schedule is independent of the node's latest candidate response."""
    eligible = [(h, pass_id) for h, pass_id in timeline if h <= height]
    return max(eligible, key=lambda item: item[0])[1] if eligible else None


def assert_economics(block, parent, profile, *, activation, issued):
    """Check child-chain policy and historical BTC formula separately."""
    number = int(block['number'], 16)
    selector = decode_selector(block)
    height = selector['btc_height']
    p = profile['pass']
    raw, collab, effective = (int(p[k]) for k in ('raw_energy', 'collab_contribution', 'effective_energy'))
    require(effective == raw + collab, f'wrong effective energy at BTC {height}')
    level = min(effective // 1000, 50) if activation and height >= 166 else level_for_energy(effective)
    factor = 10000 - 100 * level
    require((p['level'], p['difficulty_factor_bps']) == (level, factor), f'wrong BTC level at {height}')
    policy = 65535 if activation and number >= 3 else 1
    require(selector['difficulty_policy_version'] == policy, f'wrong child policy at USDB {number}')
    require(int(block['difficulty'], 16) == expected_policy_difficulty(parent, block, factor, policy),
            f'wrong historical difficulty at USDB {number}, BTC {height}')
    require(not block['transactions'] and not block['uncles'], 'economic oracle requires empty blocks')
    # These bounded scenarios remain inside the 50,400-block K warmup window.
    emission = calculate_emission(int(profile['miner_aggregate']['total_miner_btc_sats']), FIXED_PRICE_ATOMS_PER_BTC, issued, 10000)
    bps, recipient = (2000, AUX3) if activation and number >= 5 else (
        (1000, AUX2) if activation and number >= 3 else (0, None))
    return dict(emission=emission, miner=emission - emission * bps // 10000,
                aux=emission * bps // 10000, recipient=recipient, collaboration=collab)


class EconomicGapMatrix(GapMatrix):
    def __init__(self, args):
        super().__init__(args)
        self.activation = args.gap_case == 'activation'
        if self.activation:
            self.b.anchor_gap, self.c.anchor_gap = 0, 1
            self.report['gaps'] = dict(a=2, b=0, c=1)
        self.timelines = {MINER: [(0, args.pass_id)], SECOND_MINER: [(0, args.second_pass)]}
        if not self.activation:
            self.c.beneficiary = THIRD_MINER
            self.expected_passes[THIRD_MINER] = None
            self.timelines[THIRD_MINER] = []
        self.report.update(schema='usdb-miner-anchor-economics:v1', economics=[], transitions=[])
        self.pass_sources = {args.pass_id: (args.owner_address, []), args.second_pass: (args.second_owner, [])}
        self.collaborators = {}
        for entry, node in zip(self.report['topology'], self.nodes):
            entry['miner'] = node.beneficiary
        sys.path.insert(0, str(args.usdb_repo / 'tests/common'))
        from miner_pass_regtest import select_satpoint
        self.select_satpoint = select_satpoint

    def expected_pass_at(self, author, height):
        return expected_pass(self.timelines[author], height)

    def configure_genesis(self):
        super().configure_genesis()
        if self.activation:
            genesis = json.loads(self.genesis.read_text())
            config = json.loads((self.a.root / 'usdb-indexer/config.json').read_text())['usdb']
            checkpoints = genesis['config']['usdb']['activations']
            checkpoints[0]['btcActivationRegistryId'] = config['activation_registry_id']
            versions = dict(checkpoints[0]['versions'], difficultyPolicyVersion=65535, auxPoolPolicyVersion=65534)
            second = dict(checkpoints[0], block=3, versions=versions)
            checkpoints.extend([second, dict(second, block=5, versions=dict(versions, auxPoolPolicyVersion=65535))])
            self.write_json(self.genesis, genesis)

    def ord(self, wallet, *args):
        command = [str(self.args.ord), '--regtest', '--bitcoin-rpc-url', self.a.url('bitcoin'),
                   '--cookie-file', str(self.a.root / 'bitcoin/regtest/.cookie'),
                   '--bitcoin-data-dir', str(self.a.root / 'bitcoin'), '--data-dir', str(self.a.root / 'ord'),
                   'wallet', '--no-sync', '--server-url', self.a.url('ord'), '--name', wallet, *args]
        result = subprocess.run(command, capture_output=True, text=True, timeout=90)
        require(result.returncode == 0, f'Ord operation failed: wallet={wallet}, args={args}, error={result.stderr}')
        return json.loads(result.stdout)

    def wallet_rpc(self, wallet):
        return RPC(self.a.url('bitcoin') + '/wallet/' + wallet, self.a.root / 'bitcoin/regtest/.cookie')

    def confirm(self):
        self.advance_btc(1)
        self.wait_ord(self.a)
        return self.a.rpc('bitcoin')('getblockcount')

    def advance_to(self, height):
        count = height - self.frontier(self.a)[0]
        require(count >= 0, f'fixture cannot rewind BTC frontier to {height}')
        if count:
            self.advance_btc(count)
        for node in self.nodes:
            self.wait_upstream(node)

    def fund(self, address, amount=1):
        self.send_funding(address, amount)
        return self.confirm()

    def send_funding(self, address, amount):
        # Avoid Core grouping every regtest coinbase paid to the same address
        # into a large automatic funding transaction. Never retry a mutation.
        wallet = self.wallet_rpc('upstream-matrix')
        sats, fee = int(Decimal(str(amount)) * 100000000), 1000
        coins = [c for c in wallet('listunspent', [1]) if c['spendable'] and c['safe']
                 and int(Decimal(str(c['amount'])) * 100000000) > sats + fee]
        require(bool(coins), 'no mature isolated fixture funding coin')
        coin = min(coins, key=lambda c: (c['txid'], c['vout']))
        change = int(Decimal(str(coin['amount'])) * 100000000) - sats - fee
        raw = wallet('createrawtransaction', [[dict(txid=coin['txid'], vout=coin['vout'])],
                                             [{address: sats / 100000000}, {wallet('getnewaddress'): change / 100000000}]])
        signed = wallet('signrawtransactionwithwallet', [raw])
        require(signed['complete'], 'fixture funding transaction was not fully signed')
        wallet('sendrawtransaction', [signed['hex']])

    def fund_cardinals(self, address):
        # Leave a separate balance UTXO behind when Ord spends the funding coin.
        for _ in range(4):
            self.send_funding(address, 1)
        return self.confirm()

    def mint(self, name, wallet, source, destination, binding, prev=()):
        self.wait_ord(self.a)
        protected = self.ord(wallet, 'inscriptions')
        coins = self.wallet_rpc(wallet)('listunspent', [1])
        satpoint = self.select_satpoint(coins, protected, source)
        path = self.args.output_dir / (name + '-mint.json')
        self.write_json(path, dict(p='usdb', op='mint', v=1, prev=list(prev), **binding))
        result = self.ord(wallet, 'inscribe', '--fee-rate', '1', '--file', str(path),
                          '--destination', destination, '--satpoint', satpoint)
        self.write_json(self.args.output_dir / (name + '-ord.json'), result)
        pass_id = result['inscriptions'][0]['id']
        birth = self.confirm()
        positions = {p['inscription']: p['location'] for p in self.ord(wallet, 'inscriptions')}
        require(all(positions.get(p['inscription']) == p['location'] for p in protected),
                f'{name}: existing inscription was spent during mint')
        self.pass_sources[pass_id] = (destination, list(prev))
        if 'usdb_main' not in binding:
            self.collaborators[pass_id] = (birth, binding)
        self.report['transitions'].append(dict(name=name, height=birth, pass_id=pass_id))
        return pass_id, birth

    def profile(self, node, pass_id, height):
        return node.rpc('usdb-indexer')('get_pass_economic_profile', [{
            'view_version': VIEW_VERSION, 'pass_id': pass_id, 'block_height': height}])

    def check_pass(self, pass_id, height, state):
        profiles = [self.profile(node, pass_id, height) for node in self.nodes]
        require(all(p == profiles[0] for p in profiles), f'independent profiles disagree: {pass_id}@{height}')
        require(profiles[0]['pass']['state'] == state, f'wrong {pass_id}@{height} state: {profiles[0]}')
        return profiles[0]

    def waiting(self, node, height):
        before = node.block()['hash']
        node.rpc('geth')('miner_start', [1])
        try:
            def failed():
                status = node.rpc('geth')('miner_getUSDBWorkStatus')
                attempt = status.get('attempt') or {}
                return (attempt.get('failure') == 'candidate_unavailable'
                        and (attempt.get('selection') or {}).get('selected_height') == height)
            self.wait(f'{node.name} waits for historical candidate at {height}', failed, seconds=30)
            started = time.monotonic()
            self.wait('missing candidate remains unmined', lambda: time.monotonic() - started >= 3, seconds=6)
            require(node.block()['hash'] == before, 'miner fell back to latest candidate')
        finally:
            node.rpc('geth')('miner_stop')
        self.report['transitions'].append(dict(name='candidate-wait', node=node.name, height=height))

    def economic_block(self, node, actual_gap=2):
        block = self.mine_one(node, actual_gap)
        self.audit_chain([self.b, self.c])
        selector = decode_selector(block)
        height, pass_id = selector['btc_height'], selector['pass_id']
        profile = self.check_pass(pass_id, height, 'active')
        require(selector_identity(profile['external_state']) == selector_identity(selector),
                f'profile does not match executed selector at BTC {height}')
        require(pass_id == self.expected_pass_at(node.beneficiary, height), 'latest pass leaked into historical work')
        # Rebuild balances from Core inputs/outputs. Pass ownership, inheritance
        # and collaboration bindings come from operations authored by this test.
        owners = sorted({owner for owner, _ in self.pass_sources.values()})
        births, balances = core_history(self.a.url('bitcoin'), self.a.root / 'bitcoin/regtest/.cookie', owners, height)
        def raw_energy(pid, through):
            birth = births[pid.split('i')[0]]
            owner, parents = self.pass_sources[pid]
            if self.activation:
                require(not parents, 'activation oracle expects pre-upgrade first mints')
                return reference(birth, balances[owner], through)
            energy = sum(raw_energy(parent, birth) * 95 // 100 for parent in parents)
            age = birth
            for h in range(birth + 1, through + 1):
                before, after = balances[owner][h - 1] // 100000, balances[owner][h] // 100000
                energy = max(0, energy + before - max(0, before - after) * (h - age) * 3 // 2)
                if (before == 0) != (after == 0):
                    age = h
            return energy
        owner = self.pass_sources[pass_id][0]
        contribution = 0
        active = {self.expected_pass_at(author, height) for author in self.timelines} - {None}
        for pid, (birth, binding) in self.collaborators.items():
            if birth > height:
                continue
            active.add(pid)
            if binding.get('leader_pass_id') == pass_id or binding.get('leader_btc_addr') == owner:
                contribution += raw_energy(pid, height) * (2500 if self.activation and height >= 163 else 5000) // 10000
        require(int(profile['pass']['raw_energy']) == raw_energy(pass_id, height),
                f'Core-derived raw/inheritance oracle mismatch: pass={pass_id}, height={height}')
        require(int(profile['pass']['collab_contribution']) == contribution, f'collab binding oracle mismatch at {height}')
        active_owners = {self.pass_sources[pid][0] for pid in active}
        aggregate = profile['miner_aggregate']
        require(int(aggregate['total_miner_btc_sats']) == sum(balances[o][height] for o in active_owners)
                and aggregate['active_miner_owner_count'] == len(active_owners), f'aggregate oracle mismatch at {height}')
        number = int(block['number'], 16)
        parent = node.block(number - 1)
        def storage(key, tag):
            return int(node.rpc('geth')('eth_getStorageAt', [USDB_SYSTEM_STATE_ADDRESS, SYSTEM_STATE_SLOTS[key], tag]), 16)
        issued = storage('issued', parent['number'])
        rewards = assert_economics(block, parent, profile, activation=self.activation, issued=issued)
        require(storage('issued', block['number']) - issued == rewards['emission'], 'wrong issued supply delta')
        require(storage('k_last_ce', block['number']) == rewards['collaboration'], 'wrong historical K sample')
        for address in (MINER, SECOND_MINER, THIRD_MINER, AUX2, AUX3):
            before = int(node.rpc('geth')('eth_getBalance', [address, parent['number']]), 16)
            after = int(node.rpc('geth')('eth_getBalance', [address, block['number']]), 16)
            expected = rewards['miner'] if address == node.beneficiary else (
                rewards['aux'] if address == rewards['recipient'] else 0)
            require(after - before == expected, f'wrong reward delta: block={number}, address={address}')
            for peer in self.nodes:
                require(int(peer.rpc('geth')('eth_getBalance', [address, block['number']]), 16) == after,
                        f'{peer.name}: independent reward differs at {number}/{address}')
        self.report['economics'].append(dict(block=number, btc_height=height, pass_id=pass_id,
                                            profile=profile, rewards=rewards))
        self.sample_save()
        return profile

    def lifecycle(self):
        wallet, other = 'ord-upstream-a', 'ord-upstream-unused'
        self.phase('first-mint-historical-eligibility')
        destination = self.ord(wallet, 'receive')['addresses'][0]
        first, birth = self.mint('first', wallet, self.args.owner_address, destination, dict(usdb_main=THIRD_MINER))
        self.timelines[THIRD_MINER].append((birth, first))
        self.fund(destination)
        self.advance_to(birth + 1)
        self.check_pass(first, birth + 1, 'active')
        self.waiting(self.c, birth - 1)
        self.advance_to(birth + 2)
        self.economic_block(self.c)
        self.advance_to(birth + 3)
        self.economic_block(self.c)
        self.passed('first-mint-historical-eligibility', birth=birth)

        self.phase('collaboration-attachment')
        collaborators = []
        for name, binding in (
                ('fixed-collab', dict(leader_pass_id=self.args.pass_id)),
                ('address-collab', dict(leader_btc_addr=self.args.owner_address))):
            self.fund(self.args.second_owner)
            owner = self.ord(other, 'receive')['addresses'][0]
            collab, birth = self.mint(name, other, self.args.second_owner, owner, binding)
            self.fund(owner)
            collaborators.append(collab)
        self.advance_to(birth + 4)
        attached = self.economic_block(self.a)
        require(int(attached['pass']['collab_contribution']) > 0, 'funded collaborators did not contribute')
        self.passed('collaboration-attachment', collaborators=collaborators)

        self.phase('same-owner-inheritance')
        self.fund_cardinals(self.args.owner_address)
        remint, birth = self.mint('remint', wallet, self.args.owner_address, self.args.owner_address,
                                  dict(usdb_main=MINER), [self.args.pass_id])
        self.timelines[MINER].append((birth, remint))
        self.advance_to(birth + 1)
        self.check_pass(self.args.pass_id, birth, 'consumed')
        self.economic_block(self.a)  # H=birth-1 still uses the consumed-in-latest predecessor.
        self.advance_to(birth + 2)
        profile = self.economic_block(self.a)
        require(int(profile['pass']['raw_energy']) > 0, 'same-owner inheritance lost all energy')
        address_energy = self.check_pass(collaborators[1], birth, 'active')['pass']['raw_energy']
        require(int(profile['pass']['collab_contribution']) == int(address_energy) // 2,
                'remint must keep address collaboration and detach fixed-pass collaboration')
        self.passed('same-owner-inheritance', birth=birth, old=self.args.pass_id, new=remint)

        self.phase('forced-gift-does-not-replace')
        self.fund(self.args.second_owner)
        gift, birth = self.mint('gift', other, self.args.second_owner, self.args.owner_address,
                                dict(usdb_main=SECOND_MINER), [remint])
        self.advance_to(birth + 2)
        self.check_pass(gift, birth, 'invalid')
        self.check_pass(remint, birth, 'active')
        self.economic_block(self.a)
        self.passed('forced-gift-does-not-replace', gift=gift)

        self.phase('cross-owner-inheritance')
        self.fund_cardinals(self.args.owner_address)
        destination = self.ord(wallet, 'receive')['addresses'][0]
        cross, birth = self.mint('cross', wallet, self.args.owner_address, destination, dict(usdb_main=MINER), [remint])
        self.timelines[MINER].append((birth, cross))
        self.advance_to(birth + 1)
        self.check_pass(remint, birth, 'consumed')
        self.economic_block(self.a)
        self.advance_to(birth + 2)
        profile = self.economic_block(self.a)
        require(int(profile['pass']['raw_energy']) > 0, 'cross-owner inheritance lost all energy')
        require(int(profile['pass']['collab_contribution']) == 0, 'cross-owner mint retained old address collaboration')
        self.passed('cross-owner-inheritance', birth=birth, old=remint, new=cross)

        self.phase('transfer-historical-validity')
        recipient = self.ord(other, 'receive')['addresses'][0]
        self.ord(wallet, 'send', '--fee-rate', '1', recipient, cross)
        birth = self.confirm()
        self.timelines[MINER].append((birth, None))
        self.advance_to(birth + 1)
        self.check_pass(cross, birth, 'dormant')
        self.economic_block(self.a)
        self.advance_to(birth + 2)
        self.waiting(self.a, birth)
        self.economic_block(self.b)
        self.passed('transfer-historical-validity', transfer_height=birth)

    def activation_boundaries(self):
        self.phase('independent-btc-and-usdb-activation')
        wallet = 'ord-upstream-unused'
        owner = self.ord(wallet, 'receive')['addresses'][0]
        collab, birth = self.mint('collab', wallet, self.args.second_owner, owner, dict(leader_pass_id=self.args.pass_id))
        self.fund(owner)
        # L=161 already sees double energy; H=159 must keep the old rules.
        self.advance_to(161)
        for _ in range(5):
            self.economic_block(self.a)
        self.passed('child-policy-changes-with-same-btc-anchor', btc_height=159, child_boundaries=[3, 5])
        golden = json.loads((Path(__file__).resolve().parents[1] /
                             'internal/usdb/testdata/miner_pass_live_activation_golden.json').read_text())
        registry = next(r for r in golden['registries'] if r['current'])
        for height in (160, 162, 163, 165, 166, 169, 170, 171, 172, 179, 180, 209, 210):
            self.advance_to(height + 2)
            profile = self.economic_block(self.a)
            activation = max((a for a in registry['activations'] if a['btc_height'] <= height), key=lambda a: a['btc_height'])
            require(profile['external_state']['active_version_set'] == activation['active_version_set'], 'wrong historical version set')
            require(profile['external_state']['active_version_set_id'] == activation['active_version_set_id'], 'wrong historical version ID')
            self.check_pass(collab, height, 'active')
        self.passed('btc-activation-boundaries', heights=[e['btc_height'] for e in self.report['economics']],
                    existing_schema_v1_pass_still_active=True)
        self.phase('mixed-policy-after-activation')
        self.economic_block(self.b, 0)
        self.economic_block(self.a, 0)
        for gap in (1, 2):
            self.advance_to(self.frontier(self.a)[0] + 1)
            self.economic_block(self.a, gap)
        self.economic_block(self.c, 1)
        self.passed('mixed-policy-after-activation', gaps=self.report['gaps'])

    def run(self):
        self.baseline()
        if self.activation:
            self.activation_boundaries()
        else:
            self.lifecycle()
        self.passed('final-state', **self.audit_chain([self.b, self.c]))


if __name__ == '__main__':
    if '--configure-indexer' in sys.argv:
        parser = argparse.ArgumentParser()
        parser.add_argument('--configure-indexer', type=Path, required=True)
        parser.add_argument('--usdb-repo', type=Path, required=True)
        args = parser.parse_args()
        configure_indexer(args.configure_indexer, args.usdb_repo)
    else:
        main(EconomicGapMatrix, ('lifecycle', 'activation'))
