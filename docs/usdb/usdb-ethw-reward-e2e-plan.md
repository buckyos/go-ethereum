# USDB + ETHW Reward E2E Plan

This note defines the first end-to-end integration path between the BTC-side
USDB regtest stack and the local USDB/ETHW chain.

The Go-side UIP-0007 path is current. The three legacy shell runners still need
their old 105-byte payload decoding and pre-UIP-0006 RPC assertions migrated
before this E2E plan can be executed as a full acceptance suite.

## Goal

The first version focuses on one narrow loop:

1. Mint one standard miner pass on BTC regtest.
2. Let `usdb-indexer` resolve its current UIP-0006 economic profile and system state.
   - If the freshly minted pass still resolves to `energy=0`, the smoke may
     top up the same BTC owner address once, mine a few extra BTC blocks, and retry.
   - The v1 smoke does not require the final energy to become positive; a
     minimum-band reward path is still a valid first end-to-end result.
3. Start one local ETHW node with the built-in USDB chain config and operational
   `--miner.usdb.*` / `--ethash.usdb.*` companion-service parameters.
4. Mine a few ETHW blocks whose `header.Extra` carries the 107-byte UIP-0007
   `ProfileSelectorPayload`.
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
- live-ord standard-pass mint flows
- JSON-RPC helpers for:
  - `get_system_state_info`
  - `get_pass_economic_profile`

The entry point reused by this E2E is:

- `/home/bucky/work/usdb/src/btc/usdb-indexer/scripts/regtest_reorg_lib.sh`

### ETHW side

This repository already has:

- `ProfileSelectorPayload` codec under `internal/usdb`
- miner-side payload builder
- validator-side reward verifier
- operational CLI flags:
  - `--miner.usdb.rpcurl`
  - `--miner.usdb.passid`
  - `--miner.usdb.timeout`
  - `--ethash.usdb.rpcurl`
  - `--ethash.usdb.timeout`

USDB consensus activation and expected payload/difficulty-policy versions come
exclusively from `params.ChainConfig.USDB`. CLI parameters cannot enable or
disable those rules.

## First-Version Flow

The first-version smoke should perform the following steps:

1. Start the BTC regtest stack.
2. Fund one ord wallet and inscribe one UIP-0001 standard mint payload.
3. Wait until `balance-history` and `usdb-indexer` are synced and consensus ready.
4. Start one ETHW node on the built-in USDB genesis.
5. If the current pass energy is zero, fund the same BTC owner address once
   more, mine a few extra BTC growth blocks, and wait until
   `balance-history` / `usdb-indexer` catch up.
6. Configure miner and validator companion RPC access. The built-in USDB chain
   config activates payload generation and validation from genesis.
7. Wait until the ETHW node mines a small number of blocks.
8. Stop mining and inspect every mined header.
9. For each block:
   - decode `header.Extra` as `ProfileSelectorPayload`
   - verify its payload and difficulty-policy versions against chain config
   - query the UIP-0006 historical profile using the payload selectors
   - recompute `energy -> level -> multiplier -> reward`
10. Assert the coinbase balance equals the sum of the recomputed block rewards.

## Core Assertions

The v1 smoke should assert:

1. `header.Extra` is present and has the exact 107-byte `ProfileSelectorPayload` length.
2. The payload version is `1`.
3. The payload difficulty policy version equals the chain-config expected version.
4. The payload `pass_id` matches the minted inscription id.
5. The USDB external state resolved from the payload matches `btc_height`,
   `snapshot_id`, and `system_state_id` exactly.
6. The selected pass is `Active / standard`, and its energy/level fields can be
   independently recomputed from the UIP-0006 profile.
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
   - planned runner:
     - `scripts/usdb/run_usdb_ethw_reward_historical_stability_e2e.sh`
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

## Phase 2 Outline

The historical-stability scenario should verify more than local reward math. It
should prove that a fresh ETHW validator can still accept old reward blocks
after the BTC head has moved forward.

1. Mint one pass and start ETHW node 1 as the only miner.
2. Mine a small stage-1 batch of ETHW blocks and stop ETHW mining.
3. Advance the BTC regtest head and top up the same BTC owner address so the
   current USDB energy becomes larger than it was during stage 1.
4. Start a fresh ETHW node 2 with the same canonical USDB genesis and
   validator-side companion RPC configured, but without mining.
5. Connect node 2 to node 1 and wait until node 2 syncs the already mined
   stage-1 blocks.
6. Assert:
   - node 2 reaches the same head height and head hash as node 1
   - historical replay of the old stage-1 payloads still yields the original
     stage-1 reward
   - the current USDB energy after the BTC head advance is larger than the
     historical stage-1 energy
   - the reward implied by the new current USDB energy is larger than the
     historical stage-1 reward, proving the synced old blocks were not
     validated against the latest BTC head
