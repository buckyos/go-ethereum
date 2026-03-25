# USDB + ETHW Reward E2E Plan

This note defines the first end-to-end integration path between the BTC-side
USDB regtest stack and the local USDB/ETHW chain.

## Goal

The first version focuses on one narrow loop:

1. Mint one miner pass on BTC regtest with a target `eth_main` address.
2. Let `usdb-indexer` resolve the pass energy and current system state.
   - If the freshly minted pass still resolves to `energy=0`, the smoke may
     top up the same BTC owner address once, mine a few extra BTC blocks, and retry.
   - The v1 smoke does not require the final energy to become positive; a
     minimum-band reward path is still a valid first end-to-end result.
3. Start one local ETHW node with `--miner.usdb.*` and `--ethash.usdb.*`.
4. Mine a few ETHW blocks whose `header.Extra` carries `RewardPayloadV1`.
5. Verify the coinbase reward matches the historical USDB reward input.

This version intentionally excludes:

- dividend / fee-split logic
- SourceDAO bootstrap flow
- ETHW multi-node validation
- BTC reorg or historical drift scenarios

Those belong to later phases once the reward loop itself is stable.

## Existing Reusable Infrastructure

### BTC / USDB side

From the sibling repository `/home/bucky/work/usdb` we already have:

- `bitcoind regtest + ord + balance-history + usdb-indexer` startup helpers
- live-ord mint flows with `eth_main`
- JSON-RPC helpers for:
  - `get_system_state_info`
  - `get_pass_snapshot`
  - `get_pass_energy`

The entry point reused by this E2E is:

- `/home/bucky/work/usdb/src/btc/usdb-indexer/scripts/regtest_reorg_lib.sh`

### ETHW side

This repository already has:

- `RewardPayloadV1` codec under `internal/usdb`
- miner-side payload builder
- validator-side reward verifier
- CLI flags:
  - `--miner.usdb`
  - `--miner.usdb.rpcurl`
  - `--miner.usdb.passid`
  - `--ethash.usdb`
  - `--ethash.usdb.rpcurl`

## First-Version Flow

The first-version smoke should perform the following steps:

1. Start the BTC regtest stack.
2. Fund one ord wallet and inscribe one mint payload:
   - `{"p":"usdb","op":"mint","eth_main":"<coinbase address>","prev":[]}`
3. Wait until `balance-history` and `usdb-indexer` are synced and consensus ready.
4. Start one ETHW node on the built-in USDB genesis.
5. If the current pass energy is zero, fund the same BTC owner address once
   more, mine a few extra BTC growth blocks, and wait until
   `balance-history` / `usdb-indexer` catch up.
6. Enable both:
   - miner-side payload generation
   - validator-side reward replay
7. Wait until the ETHW node mines a small number of blocks.
8. Stop mining and inspect every mined header.
9. For each block:
   - decode `header.Extra` as `RewardPayloadV1`
   - query USDB historical state using the payload selectors
   - recompute `energy -> level -> multiplier -> reward`
10. Assert the coinbase balance equals the sum of the recomputed block rewards.

## Core Assertions

The v1 smoke should assert:

1. `header.Extra` is present and has the exact `RewardPayloadV1` length.
2. The payload version is `1`.
3. The payload `pass_id` matches the minted inscription id.
4. The USDB snapshot resolved from the payload is valid.
5. The pass snapshot resolved from the payload is valid and keeps:
   - `eth_main == miner etherbase`
6. The pass energy resolved from the payload is valid.
7. The mined ETHW balance equals the sum of:
   - `BaseReward(blockNumber) * Multiplier(level)`

## Why This Shape

This smoke validates the critical path with minimal moving parts:

- no DAO bootstrap
- no fee split
- no cross-node networking
- no BTC reorg choreography

If this loop fails, later scenarios are not worth debugging yet.

## Script Layout

The first executable entry should live in this repository:

- `scripts/usdb/run_usdb_ethw_reward_e2e.sh`

It should:

- reuse the USDB regtest helper library directly
- generate the built-in USDB genesis via `geth dumpgenesis --usdb`
- run one local ETHW miner with USDB reward flags
- validate reward results with plain shell + Python

## Next Phases

After this smoke is stable, the next extensions should be:

1. `energy change -> reward change`
   - first planned runner:
     - `scripts/usdb/run_usdb_ethw_reward_energy_growth_e2e.sh`
2. `historical stability after BTC head advances`
3. `fail-closed cases`
   - bad pass id
   - verifier unavailable
   - tampered payload
4. `ETHW two-node validation`

## Phase 1 Outline

The first reward-growth scenario should keep the same pass id across both
stages:

1. Mint one pass and start one ETHW miner.
2. Mine a small first batch of ETHW blocks and record the stage-1 reward.
3. Stop ETHW mining.
4. Top up the BTC owner address and mine a few BTC growth blocks.
5. Wait for `balance-history` and `usdb-indexer` to converge to the new BTC tip.
6. Resume ETHW mining and mine a second batch of ETHW blocks.
7. Assert:
   - boosted USDB energy > initial USDB energy
   - stage-2 ETHW reward > stage-1 ETHW reward
   - the same pass id appears in both payload batches
