"""Identity-bound, monotonic-clock evidence for local anchor-gap acceptance.

These are test observations, not network-wide latency or PoW performance metrics.
Unknown readiness/receipt times remain null, including after observer restart.
"""


def selector_identity(selector):
    return (selector['btc_height'], selector['snapshot_id'], selector['system_state_id'])


def validate_work(block, selector, work, gap):
    """Correlate actual submitted work with the block, never a later attempt."""
    build = work['build']
    selection = build['selection']
    checks = (
        work['parent_hash'] == block['parentHash'],
        build['block_number'] == int(block['number'], 16),
        build['miner_address'].lower() == block['miner'].lower(),
        build['pass_id'] == selector['pass_id'],
        build['snapshot_id'].removeprefix('0x') == selector['snapshot_id'].removeprefix('0x'),
        build['system_state_id'].removeprefix('0x') == selector['system_state_id'].removeprefix('0x'),
        selection['configured_gap'] == gap,
        selection['latest_height'] - selection['selected_height'] == selection['actual_gap'],
        selection['selected_height'] == selector['btc_height'],
        build['anchor_age'] == selector['btc_anchor_age_blocks'],
        build['stage'] == 'validated' and not build.get('failure'),
    )
    if not all(checks):
        raise ValueError(f'work/block identity mismatch: block={block["number"]}, work={work}, selector={selector}')
    return selection


class ImportObservations:
    """Retain completed and right-censored samples, keyed by block and state identity."""

    def __init__(self):
        self.samples = {}

    def add(self, node, block_hash, identity, produced_at, *, ready_at=None, parent_wait=False):
        key = (node, block_hash, tuple(identity))
        if key in self.samples:
            raise ValueError('duplicate block/state observation')
        self.samples[key] = dict(node=node, block_hash=block_hash, identity=list(identity),
                                 produced_at=produced_at, ready_at=ready_at, received_at=None,
                                 first_failed_at=None, imported_at=None, parent_wait=parent_wait)
        return key

    def mark(self, key, event, when):
        if event not in ('ready_at', 'received_at', 'first_failed_at', 'imported_at'):
            raise ValueError('unknown observation event')
        sample = self.samples[key]
        if sample[event] is None:
            sample[event] = when

    def rows(self, now):
        rows = []
        for value in self.samples.values():
            sample = dict(value)
            start, end, ready = sample['received_at'], sample['imported_at'], sample['ready_at']
            failed = sample['first_failed_at']
            sample.update(unresolved=end is None,
                          observation_age_seconds=max(0, now - sample['produced_at']),
                          receipt_to_import_seconds=None if start is None or end is None else max(0, end - start),
                          validation_wait_seconds=None if failed is None or end is None else max(0, end - failed),
                          state_lead_seconds=None if start is None or ready is None else start - ready)
            rows.append(sample)
        return rows
