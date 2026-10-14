#!/usr/bin/env python3
"""UIP-0018 isolated gap controls and mixed-miner recovery on real upstreams.

Each invocation starts fresh Core/BH/indexer/geth stores. Only seal computation
uses delayed fake PoW. No chain restart or peer reconnect is allowed in a case.
"""

import argparse
import json
from pathlib import Path
import signal
import subprocess
import time

from multi_miner_acceptance import (MultiMinerMatrix, ANCHOR_MAX_AGE,
                                    configure_anchor_max_age, decode_selector, require, RPCError)
from common.anchor_gap_evidence import ImportObservations, selector_identity, validate_work


class GapMatrix(MultiMinerMatrix):
    def __init__(self, args):
        super().__init__(args)
        self.max_age = 3 if args.gap_case == 'exhaustion' else ANCHOR_MAX_AGE
        gaps = (0, 2, 1) if args.gap_case == 'mixed' else (args.gap,) * 3
        for node, gap in zip(self.nodes, gaps):
            node.anchor_gap = gap
        self.observations = ImportObservations()
        self.report.update(schema='usdb-miner-anchor-gap:v1', case=args.gap_case,
                           gaps=dict(zip('abc', gaps)), lag=args.lag, delay_seconds=args.delay_seconds,
                           max_anchor_age=self.max_age, clock='single-host-monotonic',
                           observation_scope='test queries and imported blocks; wire receipt time unknown',
                           work=[], observations=[])
        for entry, node in zip(self.report['topology'], self.nodes):
            entry['miner'] = node.beneficiary if args.gap_case == 'mixed' or node is self.a else None

    def baseline(self):
        self.phase('baseline')
        self.wait_upstream(self.a)
        genesis = json.loads(subprocess.check_output([str(self.args.geth), 'dumpgenesis', '--usdb'], timeout=30))
        self.write_json(self.genesis, configure_anchor_max_age(genesis, self.max_age))
        self.command(['python3', str(self.args.usdb_repo / 'tests/common/miner_pass_regtest.py'),
                      'configure-genesis', str(self.genesis)], 'configure-v2-genesis.log')
        for node in (self.b, self.c):
            node.fresh_upstream()
        states = [self.state(node) for node in self.nodes]
        require(all(self.difference(states[0], state) is None for state in states[1:]), 'baseline stores disagree')
        for pass_id in self.expected_passes.values():
            profile = states[0]['passes'][pass_id]['profile']['pass']
            require(profile['state'] == 'active' and int(profile['effective_energy']) > 0, 'unfunded baseline pass')
        for node in self.nodes:
            node.init_geth()
            node.start_geth()
            require(node.height() == 0, 'gap control inherited a previous parent')
            status = node.rpc('geth')('miner_getUSDBWorkStatus')
            require(status['configured_gap'] == node.anchor_gap, 'running gap differs from fixture')
        self.a.pinned_geth = self.a.processes['geth']
        self.identities['a'] = self.a.validator_identity()
        for node in (self.b, self.c):
            self.connect_once(node)
        self.passed('baseline', initial_height=self.frontier(self.a)[0], independent_databases=True,
                    initial_usdb_head=0, upstream_sha256=self.digest(states[0]), identities=self.identities)

    def sample_save(self):
        self.report['observations'] = self.observations.rows(time.monotonic())
        self.write_json(self.args.output_dir / 'summary.json', self.report)

    def probe_ready(self, node, block, key):
        selector = decode_selector(block)
        try:
            profile = node.rpc('usdb-indexer')('get_pass_economic_profile', [{
                'view_version': 'uip-0006-usdb-economic-state-view:v1',
                'pass_id': selector['pass_id'], 'block_height': selector['btc_height']}])
        except RPCError as error:
            if error.code not in (-32040, -32041):
                raise
            return False
        require(selector_identity(profile['external_state']) == selector_identity(selector),
                'state availability probe returned a different identity')
        self.observations.mark(key, 'ready_at', time.monotonic())
        return True

    def mine_one(self, node, expected_gap=None, reason=None):
        """Capture the submitted work while sealing; stop before a second task."""
        before = node.block()
        work = {}
        node.rpc('geth')('miner_start', [1])
        try:
            def submitted():
                status = node.rpc('geth')('miner_getUSDBWorkStatus')
                if status['work_current'] and status['work']['parent_hash'] == before['hash']:
                    work.update(status['work'])
                    return True
                return False
            self.wait(f'{node.name} actual sealing diagnostics', submitted, seconds=30, interval=0.02)
        finally:
            node.rpc('geth')('miner_stop')
        number = int(before['number'], 16) + 1
        self.wait(f'{node.name} submitted block imported', lambda: node.height() >= number, seconds=15, interval=0.05)
        block = node.block(number)
        selection = validate_work(block, decode_selector(block), work, node.anchor_gap)
        if expected_gap is not None:
            require(selection['actual_gap'] == expected_gap, f'unexpected actual gap: {selection}')
        if reason is not None:
            require(selection['reason'] == reason, f'unexpected selection reason: {selection}')
        require(node.height() == number, 'single-work fixture produced an extra block')
        self.report['work'].append(dict(node=node.name, block_hash=block['hash'], selection=selection, work=work))
        self.sample_save()
        return block

    def hold(self, node, kind, lag):
        old = node.rpc('usdb-indexer')('get_synced_block_height')
        hashes = [node.rpc('bitcoin')('getblockhash', [h]) for h in range(old + 1, old + lag + 1)]
        node.gates[kind].hold(hashes)
        return old

    def control(self):
        name = 'gap-control'
        self.phase(name)
        lag, gap = self.args.lag, self.args.gap
        old = self.hold(self.b, 'balance-history', lag)
        require(self.hold(self.c, 'usdb-indexer', lag) == old, 'unequal control starting heights')
        self.advance_btc(lag)
        self.wait_upstream(self.a)
        for node, kind in ((self.b, 'balance-history'), (self.c, 'usdb-indexer')):
            self.wait(f'{node.name} real {kind} read held', lambda n=node, k=kind: bool(n.gates[k].events), seconds=35)
            self.wait(f'{node.name} reports catch-up', lambda n=node:
                      not n.rpc('usdb-indexer')('get_readiness')['consensus_ready'], seconds=20)
            require(node.rpc('usdb-indexer')('get_synced_block_height') == old, 'delay did not pin committed height')
        self.wait('C BH progresses independently', lambda:
                  self.c.rpc('balance-history')('get_snapshot_info')['stable_height'] == old + lag)
        require(self.b.rpc('balance-history')('get_snapshot_info')['stable_height'] == old, 'B BH escaped gate')
        started = time.monotonic()
        expected_wait = gap < lag
        blocks, keys = [], []
        # Two blocks exercise both the unavailable selector and its descendant.
        # Parent-wait classification is conservative; it is not wire receipt timing.
        for index in range(2):
            block = self.mine_one(self.a, gap)
            blocks.append(block)
            identity = selector_identity(decode_selector(block))
            for node in (self.b, self.c):
                key = self.observations.add(node.name, block['hash'], identity, time.monotonic(),
                                            parent_wait=expected_wait and index > 0)
                keys.append((node, block, key))
                require(self.probe_ready(node, block, key) == (not expected_wait),
                        'real historical availability differs from gap/lag control')
        target = blocks[-1]
        for node in (self.b, self.c):
            if expected_wait:
                self.wait(f'{node.name} real unavailable profile validation', lambda n=node:
                          bool(n.proxy.profile_calls(name, errors=True)), seconds=30)
                require(node.height() == 0, 'validator accepted unavailable state')
                failures = node.proxy.profile_calls(name, errors=True)
                require(any(e['params'][0]['block_height'] == old + lag - gap for e in failures),
                        'failure did not query selected height')
                for n, block, key in keys:
                    if n is node and block is blocks[0]:
                        self.observations.mark(key, 'first_failed_at', failures[0]['completed_monotonic'])
            else:
                self.wait(f'{node.name} imports held-prefix blocks', lambda n=node: n.block()['hash'] == target['hash'])
                require(not node.proxy.profile_calls(name, errors=True), 'available prefix produced query failures')
                for n, block, key in keys:
                    if n is node:
                        self.observations.mark(key, 'imported_at', time.monotonic())
        # Hold for the requested wall-clock interval with liveness supervision.
        self.wait('configured upstream delay', lambda: time.monotonic() - started >= self.args.delay_seconds,
                  seconds=self.args.delay_seconds + 10)
        self.sample_save()  # Retain unresolved samples before opening either gate.
        self.write_json(self.args.output_dir / 'before-recovery.json', self.report)
        failures = {node.name: node.proxy.profile_calls(name, errors=True) for node in (self.b, self.c)}
        restore_at = time.monotonic()
        self.b.gates['balance-history'].restore()
        self.c.gates['usdb-indexer'].restore()
        # No miner_start, admin_addPeer or geth restart after restoring upstreams.
        def imported():
            complete = True
            for node, block, key in keys:
                self.probe_ready(node, block, key)
                if (node.block(int(block['number'], 16)) or {}).get('hash') == block['hash']:
                    self.observations.mark(key, 'imported_at', time.monotonic())
                else:
                    complete = False
            self.sample_save()
            return complete
        self.wait('automatic import of frozen target', imported, seconds=90)
        require(self.a.block()['hash'] == target['hash'], 'producer advanced during recovery')
        for node in (self.b, self.c):
            self.wait_upstream(node)
        self.passed(name, gap=gap, lag=lag, expected_wait=expected_wait,
                    selected_height=decode_selector(target)['btc_height'], validator_committed_before=old,
                    failure_counts={node: len(events) for node, events in failures.items()},
                    delay_observed_seconds=restore_at - started,
                    recovery_seconds=time.monotonic() - restore_at, frozen_target=target['hash'],
                    **self.audit_chain([self.b, self.c]))

    def mixed(self):
        self.phase('mixed-gap')
        self.mine_one(self.b, 2)
        self.audit_chain([self.b, self.c])
        self.mine_one(self.c, 1)
        self.audit_chain([self.b, self.c])
        self.mine_one(self.a, 0)
        self.audit_chain([self.b, self.c])
        self.mine_one(self.b, 0, 'parent_anchor')
        self.audit_chain([self.b, self.c])
        for actual_gap in (1, 2):
            self.advance_btc(1)
            for node in self.nodes:
                self.wait_upstream(node)
            self.mine_one(self.b, actual_gap)
            self.audit_chain([self.b, self.c])
        self.mine_one(self.c, 1)
        self.audit_chain([self.b, self.c])
        self.mine_one(self.a, 0)
        self.audit_chain([self.b, self.c])
        self.mine_one(self.b, 0, 'parent_anchor')
        self.passed('mixed-gap', gaps=self.report['gaps'], **self.audit_chain([self.b, self.c]))

    def exhaustion(self):
        self.phase('progressive-exhaustion')
        latest = self.frontier(self.a)[0]
        self.a.rpc('geth')('miner_start', [1])
        try:
            def exhausted():
                if self.a.height() == 0:
                    return False
                selector = decode_selector(self.a.block())
                status = self.a.rpc('geth')('miner_getUSDBWorkStatus')
                return (selector['btc_height'] == latest and selector['btc_anchor_age_blocks'] == self.max_age
                        and (status.get('attempt') or {}).get('failure') == 'anchor_age_exhausted')
            self.wait('all retained gap heights exhausted', exhausted, seconds=60)
            target = self.a.block()
            status = self.a.rpc('geth')('miner_getUSDBWorkStatus')
            require(status['attempt']['required_height'] == latest + 1, 'wrong exhaustion dependency')
            blocks = [self.a.block(h) for h in range(1, self.a.height() + 1)]
            anchors = [decode_selector(block) for block in blocks]
            expected = [(h, age) for h in range(latest - 2, latest + 1) for age in range(self.max_age + 1)]
            require([(s['btc_height'], s['btc_anchor_age_blocks']) for s in anchors] == expected,
                    'miner skipped a progressive P+1 yield or exceeded age')
            stopped_at = time.monotonic()
            self.wait('exhaustion remains stopped', lambda: time.monotonic() - stopped_at >= 5, seconds=10)
            require(self.a.block()['hash'] == target['hash'], 'miner exceeded exhausted height')
            self.advance_btc(1)
            self.wait('miner resumes without restart', lambda: self.a.height() > int(target['number'], 16), seconds=40)
        finally:
            self.a.rpc('geth')('miner_stop')
        drained_at = time.monotonic()
        self.wait('last sealing task drains', lambda: time.monotonic() - drained_at >= 3, seconds=6)
        resumed = decode_selector(self.a.block(int(target['number'], 16) + 1))
        require((resumed['btc_height'], resumed['btc_anchor_age_blocks']) == (latest + 1, 0), 'invalid recovery anchor')
        self.passed('progressive-exhaustion', exhausted_work=status, sequence=expected,
                    miner_restarted=False, **self.audit_chain([self.b, self.c]))

    def run(self):
        self.baseline()
        if self.args.gap_case == 'control':
            self.control()
        elif self.args.gap_case == 'mixed':
            self.mixed()
        else:
            self.exhaustion()
        self.phase('final-state')
        for node in self.nodes:
            self.wait_upstream(node)
        states = [self.state(node) for node in self.nodes]
        require(all(self.difference(states[0], state) is None for state in states[1:]), 'final independent stores disagree')
        self.passed('final-state', upstream_sha256=self.digest(states[0]), **self.audit_chain([self.b, self.c]))

    def close(self):
        self.sample_save()
        super().close()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ('work-dir', 'output-dir', 'usdb-repo', 'geth', 'bitcoin', 'ord', 'balance-history', 'indexer', 'invalidblock'):
        parser.add_argument('--' + name, type=Path, required=True)
    for name in ('miner-address', 'owner-address', 'pass-id', 'second-owner', 'second-pass'):
        parser.add_argument('--' + name, required=True)
    parser.add_argument('--port-base', type=int, default=22400)
    parser.add_argument('--timeout-sec', type=int, default=900)
    parser.add_argument('--cycles', type=int, default=1)
    parser.add_argument('--retry-expiry', default='none')
    parser.add_argument('--gap-case', choices=('control', 'mixed', 'exhaustion'), default='control')
    parser.add_argument('--gap', type=int, choices=(0, 1, 2), default=2)
    parser.add_argument('--lag', type=int, choices=(1, 2, 3), default=2)
    parser.add_argument('--delay-seconds', type=int, choices=(10, 30, 60, 120), default=10)
    args = parser.parse_args()
    matrix = GapMatrix(args)

    def interrupted(signum, _frame):
        raise RuntimeError(f'anchor-gap acceptance interrupted by signal {signum}')

    for signum in (signal.SIGINT, signal.SIGTERM):
        signal.signal(signum, interrupted)
    try:
        matrix.run()
        matrix.report['status'] = 'ok'
    except BaseException as error:
        matrix.report.update(status='failed', error=str(error))
        raise
    finally:
        try:
            matrix.close()
        except BaseException as error:
            matrix.report.update(status='failed', cleanup_error=str(error))
            raise
        finally:
            matrix.report['elapsed_seconds'] = round(time.monotonic() - matrix.started, 2)
            matrix.write_json(args.output_dir / 'summary.json', matrix.report)


if __name__ == '__main__':
    main()
