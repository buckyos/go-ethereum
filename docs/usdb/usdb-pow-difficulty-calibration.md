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

## 3. Run The Pilot Ladder

The built-in development genesis starts at `0x2000`. On current hardware that
can produce blocks faster than the one-second header timestamp resolution, so
a report collected directly at that difficulty is only a lower bound.

Use the pilot ladder to find a measurable starting point:

```bash
USDB_POW_LADDER_OUTPUT_ROOT=/tmp/usdb-pow-ladder \
USDB_POW_LADDER_PROFILE_PREFIX=nominal-cpu \
USDB_POW_LADDER_MINER_THREADS=1 \
scripts/usdb/run_usdb_pow_calibration_ladder.sh
```

Each round creates a fresh genesis and datadir. If at least 25% of sampled
intervals are one second, the next round starts from the reported candidate
difficulty. The geth binary is reused by SHA-256 identity, but chain state is
not reused. The first uncensored report is copied to `accepted-pilot.json`.

The ladder is a sizing step, not release evidence. Run it without unrelated
CPU or I/O load, then use its difficulty as the explicit input to the longer
hardware-class samples.

## 4. Collect A Report

The complete indexer-backed real-Ethash workflow can build geth, create an
isolated BTC/ord/indexer stack, mine the requested interval, and replay the
result:

```bash
USDB_POW_CALIBRATION_OUTPUT_ROOT=/tmp/usdb-pow-nominal \
USDB_POW_CALIBRATION_PROFILE=nominal \
USDB_POW_CALIBRATION_GENESIS_DIFFICULTY=0x123456 \
USDB_POW_CALIBRATION_MINIMUM_DIFFICULTY=0x100000 \
USDB_POW_CALIBRATION_MINER_THREADS=4 \
USDB_POW_CALIBRATION_SAMPLE_BLOCKS=512 \
USDB_POW_CALIBRATION_ISOLATED_HARDWARE=1 \
USDB_POW_CALIBRATION_ENVIRONMENT_NOTES='exclusive launch-class host' \
USDB_POW_CALIBRATION_REQUIRE_RELEASE_ELIGIBLE=1 \
scripts/usdb/run_usdb_pow_calibration.sh
```

Run a stable chain long enough to pass DAG warm-up and several retarget
periods. Then collect a contiguous confirmed interval:

```bash
python3 scripts/usdb/calibrate_pow_difficulty.py \
  --rpc-url http://127.0.0.1:8545 \
  --profile nominal \
  --target-block-seconds 13 \
  --sample-blocks 512 \
  --confirmations 12 \
  --source-commit <commit> \
  --source-dirty false \
  --build-command '<build and artifact identity>' \
  --miner-hardware '<hardware and runtime class>' \
  --miner-threads 4 \
  --dag-warmup-blocks 64 \
  --genesis-difficulty 0x123456 \
  --minimum-difficulty 0x100000 \
  --isolated-hardware true \
  --environment-notes '<isolation and runtime notes>' \
  --output /tmp/usdb-pow-nominal.json
```

The report embeds every input header and derives:

- elapsed time and total expected PoW work;
- effective hashrate as `sum(child difficulty) / elapsed seconds`;
- block interval p50/p95/p99 and maximum;
- a candidate difficulty for the explicit target interval.
- one-second interval ratio and timestamp-resolution quality;
- exact source, artifact, hardware, thread, warm-up, genesis-difficulty, and
  minimum-difficulty inputs.

`quality.timestampResolutionLimited=true` means the sample cannot distinguish
the miner's actual throughput. `run_usdb_pow_calibration.sh` rejects that
condition by default and tells the operator which candidate to use for another
round. `quality.releaseEligible` is false for timestamp-limited samples, dirty
source worktrees, non-isolated hardware, fewer than 256 sampled intervals, or
fewer than 64 warm-up blocks.

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

## 5. Local Workflow Evidence

On 2026-07-27, the complete one-thread path was run for 333 blocks on an
i7-13700KF virtualized host. The 256-block interval at the built-in `0x2000`
difficulty had 256 one-second intervals and produced a lower-bound candidate
of `0x1c71a`.

That report validates real Ethash sealing, indexer integration, metadata
capture, and offline replay. It is explicitly not a public parameter result:
the source tree was dirty and the interval was timestamp-resolution limited.

The follow-up ladder reached an uncensored pilot at genesis difficulty
`0x13237c`. Its 16 sampled intervals covered 195 seconds (mean 12.19 seconds,
p50 8, p95/max 47) and proposed `0x136f7d`. Only 2 of 16 intervals were one
second. This confirms the override and ladder workflow and gives a local
one-thread order of magnitude.

The follow-up is still not release evidence: it used a dirty source tree, a
short sample, four warm-up blocks, and ran alongside the world-simulator soak.
A clean, isolated run across launch hardware classes remains mandatory.

## 6. Select Candidate Parameters

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

## 7. Freeze And Release

After review, update these as one release change:

1. UIP-0009 final parameters and evidence references.
2. Go chain parameters and tests.
3. The versioned public genesis bootstrap spec.
4. Canonical genesis JSON and genesis hash.
5. Release manifest, signatures, and operator documentation.

Regenerate the genesis from a clean artifact checkout and verify byte-for-byte
identity on at least two machines. A mismatch in artifact SHA-256, runtime code
hash, genesis JSON, chain config, or genesis hash blocks release.
