# USDB PoW Difficulty Calibration

## 1. Purpose

This workflow produces evidence for selecting USDB `GenesisDifficulty` and
`MinimumDifficulty` before a public network launch.

Calibration is an offline release process. It must never make consensus
parameters depend on a node's local CPU/GPU, startup timing, or runtime
benchmark. Every node on a released network must use the same frozen genesis
and chain config.

The current SourceDAO bootstrap development profile uses these provisional
values:

```text
GenesisDifficulty = 0x180000
MinimumDifficulty = 0x100000
```

No committed calibration report currently supports these values. They are
known to permit development on the current test machines, but must be treated
as placeholders rather than measured testnet or mainnet commitments.

## 2. Inputs To Freeze

Before collecting data, record:

- target block interval;
- exact geth and miner commits and build flags;
- DAG warm-up procedure;
- miner hardware classes and expected launch distribution;
- nominal miner count;
- minimum viable hashrate after miner loss;
- profile/pass distribution used by the USDB difficulty policy;
- sample duration, confirmation depth, and acceptance thresholds.

Use at least these scenarios:

| Profile | Purpose |
| --- | --- |
| `minimum-viable` | Select and validate the minimum difficulty floor. |
| `nominal` | Select the genesis difficulty for expected launch capacity. |
| `high-load` | Verify retarget convergence and block/reorg behavior above expected capacity. |
| `miner-loss` | Verify recovery when a meaningful portion of hashrate disappears. |

## 3. Collect A Report

Run a stable chain long enough to pass DAG warm-up and several retarget
periods. Then collect a contiguous confirmed interval:

```bash
python3 scripts/usdb/calibrate_pow_difficulty.py \
  --rpc-url http://127.0.0.1:8545 \
  --profile nominal \
  --target-block-seconds 13 \
  --sample-blocks 512 \
  --confirmations 12 \
  --output /tmp/usdb-pow-nominal.json
```

The report embeds every input header and derives:

- elapsed time and total expected PoW work;
- effective hashrate as `sum(child difficulty) / elapsed seconds`;
- block interval p50/p95/p99 and maximum;
- a candidate difficulty for the explicit target interval.

Do not collect calibration data from `run_local_bootstrap_smoke.sh` while its
default `USDB_BOOTSTRAP_FAKE_POW=1` or `USDB_BOOTSTRAP_USE_MOCK_INDEXER=1` is
active. Calibration requires the complete indexer-backed consensus path and
real Ethash sealing.

The tool re-reads the sample tip before accepting the report. A same-height
replacement or parent discontinuity fails the run.

Replay a report without RPC access:

```bash
python3 scripts/usdb/calibrate_pow_difficulty.py \
  --input-report /tmp/usdb-pow-nominal.json
```

Replay recomputes the full report from embedded headers and rejects changed
metrics or candidate values.

The embedded subset proves deterministic metric recomputation, not independent
header authenticity or PoW validity. Release evidence must retain the original
RPC source/chain commit and place accepted reports in a signed release
manifest; independent nodes should reproduce the sample before parameters are
frozen.

## 4. Select Candidate Parameters

Use the `nominal` reports to propose `GenesisDifficulty`. Use
`minimum-viable` and `miner-loss` reports to propose `MinimumDifficulty`.
Do not derive the minimum floor from a fixed percentage of the nominal result
unless that percentage is independently justified by the launch hashrate
policy.

A candidate is acceptable only after repeated runs across the planned hardware
classes show:

- target mean and tail block intervals;
- bounded retarget convergence without sustained oscillation;
- acceptable stale/reorg rate;
- restart recovery within the operational objective;
- continued progress at minimum viable hashrate;
- consistent miner and validator difficulty calculations under representative
  USDB pass levels.

## 5. Freeze And Release

After review, update these as one release change:

1. UIP-0009 final parameters and evidence references.
2. Go chain parameters and tests.
3. The versioned public genesis bootstrap spec.
4. Canonical genesis JSON and genesis hash.
5. Release manifest, signatures, and operator documentation.

Regenerate the genesis from a clean artifact checkout and verify byte-for-byte
identity on at least two machines. A mismatch in artifact SHA-256, runtime code
hash, genesis JSON, chain config, or genesis hash blocks release.
