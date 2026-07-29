# USDB Chain Profile and Difficulty E2E Plan

This note defines the current end-to-end path between the BTC-side USDB regtest
stack and the local USDB chain. The active contract is UIP-0006 through
UIP-0009; the earlier 105-byte reward payload and level multiplier are no longer
part of this test plan.

## Current Boundary

The built-in development chain activates these policies from USDB block `0`:

- `payload_version = 1`
- `difficulty_policy_version = 1`
- USDB activation checkpoint binding
  `btcActivationRegistryId = 596728fd...330aa9` (`btc-regtest` revision 1)

The same checkpoint activates UIP-0011 CoinBase emission, UIP-0012
collaboration efficiency, and UIP-0013 fixed price at version `1`.
`fee_split_policy_version = 0` retains the bootstrap window behavior, while
`quote_policy_version = 0` and `aux_pool_policy_version = 0` explicitly disable
UIP-0014 and UIP-0015. Unknown non-zero versions fail closed.

Runtime CLI flags only provide companion-service access and the selected pass:

- `--miner.usdb-indexer.rpcurl`
- `--miner.usdb.passid`
- `--miner.usdb-indexer.timeout`
- `--ethash.usdb-indexer.rpcurl`
- `--ethash.usdb-indexer.timeout`

USDB-chain activation checkpoints and expected versions come only from
`params.ChainConfig.USDB`. The bound BTC registry revision supplies the
payload-height BTC-side version set; it does not activate USDB-chain policies.

## Shared Flow

Each live runner performs the following operations:

1. Start bitcoind regtest, ord, balance-history, and usdb-indexer.
2. Mint one UIP-0001 `v=1` standard pass using `usdb_main`.
3. Wait until the BTC-side services expose a consensus-ready UIP-0006 profile.
4. Generate the canonical USDB genesis and initialize one or two geth nodes.
5. Mine USDB blocks with the 111-byte UIP-0007 selector in `header.Extra`.
6. Stop mining and replay every payload against `get_pass_economic_profile`.
7. Require every historical profile to match the chain-bound BTC registry ID,
   v1 active-version-set ID, and complete active-version-set golden.
8. Independently recompute effective energy, level, difficulty factor, base
   difficulty, and real difficulty.
9. Recompute issued supply, K, fixed-price state, CoinBase emission, reward
   credits, and final balances.

The common validator is:

- `scripts/usdb/verify_usdb_profile_e2e.py`

It is shared by all live runners so offsets, profile validation, formula
thresholds, and rounding cannot drift between scenarios.

Historical storage assertions run nodes with `--gcmode archive`; otherwise a
fast local PoW run can prune early tries before the validator queries each
block's system slots.

## Required Assertions

For every mined block the common validator checks:

- `header.Extra` is exactly 111 bytes.
- `payload_version = 1`; the normal runners require `difficulty_policy_version = 1`, while the activation runner selects the expected policy from the USDB block.
- `btc_anchor_age_blocks` is zero on the first/new-height selector, increments
  exactly once for same-height reuse, and stays within the activation-bound max.
- `btc_height`, `snapshot_id`, `system_state_id`, and `pass_id` reproduce one
  exact historical UIP-0006 profile.
- `activation_registry_id` equals the chain-config-bound `btc-regtest` registry.
- `active_version_set_id` and the full `active_version_set` equal the generated
  Rust/Go v1 golden selected at the payload BTC height.
- The selected pass is `Active / standard`, including zero-energy candidates.
- `effective_energy = saturating_u128(raw_energy + collab_contribution)`.
- Returned `level` and `difficulty_factor_bps` match the frozen UIP-0005 table.
- `real_difficulty = ceil(base_difficulty * factor / 10000)`.
- The header difficulty exactly equals the independently recomputed value.
- No deterministic smoke block contains uncles.
- CoinBase emission uses the parent fixed price, issued supply, miner BTC
  aggregate, and K derived from the prior 50,400-sample window.
- `quote_policy_version = 0` writes no quote activity state and uses the nominal
  profile factor / collaboration contribution.
- `aux_pool_policy_version = 0` credits 100% of CoinBase emission to the profile
  reward recipient.

## Live Runners

### Basic Profile/Difficulty Smoke

`scripts/usdb/run_usdb_profile_e2e.sh`

- Mints one pass.
- Mines a short USDB chain.
- Cross-checks every selector, profile, real difficulty, and reward transition.

### Energy Growth

`scripts/usdb/run_usdb_profile_energy_growth_e2e.sh`

- Mines a first USDB-chain stage.
- Advances BTC state and increases the same pass raw energy.
- Mines a second USDB-chain stage with the same pass id.
- Confirms both stages independently satisfy the profile/difficulty formula.
- Confirms reward and issued-supply transitions use each selector-bound profile.

### Historical Replay and Fresh Validator

`scripts/usdb/run_usdb_profile_historical_stability_e2e.sh`

- Mines blocks on node 1.
- Advances the BTC head and changes current pass energy.
- Starts a fresh validator node 2 and syncs the old USDB blocks.
- Confirms node 2 reaches node 1's height and hash.
- Replays old selectors against their committed historical state rather than the
  new BTC head.

### Same-Height BTC State Replacement

`scripts/usdb/run_usdb_profile_same_height_replacement_e2e.sh`

- Mines a USDB chain whose selectors reference one BTC snapshot.
- Invalidates the referenced BTC tip and mines a different block at the same
  height.
- Confirms the BTC snapshot identity changes even though the height does not.
- Starts a fresh validator and requires it to reject the stale selector with
  `SNAPSHOT_ID_MISMATCH` without importing any post-genesis block.

### Activation Upgrade and Binary Restart

`scripts/usdb/run_usdb_activation_upgrade_e2e.sh`

- Builds one default geth and one `usdb_activation_conformance` geth.
- Adds a test-only block-4 activation that selects BTC regtest registry revision
  2 and reserved difficulty policy `65535`.
- Requires the default binary to mine blocks 1-3 and fail closed at block 4.
- Restarts the tagged binary on the same datadir and mines through the activation.
- Replays every profile with the per-block registry revision and independently
  checks the test policy's deterministic `v1 difficulty + 1` result.
- Starts an independent tagged validator from genesis and requires it to accept
  the complete activation-spanning chain at the same height and head hash.

### Quote/Aux Three-Stage Activation

`scripts/usdb/run_usdb_economic_activation_upgrade_e2e.sh`

- Builds default, `usdb_economic_conformance_v2`, and
  `usdb_economic_conformance_v3` geth binaries.
- Adds test-only quote/aux checkpoints using reserved IDs `65534` and `65535`.
- Requires the default binary to stop at fake v2 and the v2 binary to stop at
  fake v3 while reusing the same datadir.
- Uses fake quote v2 to model no accepted quote (`raw` difficulty, `CE=0`).
- Uses fake quote v3 to model a current-block implicit FixedPriceHeartbeat:
  active FixedPrice v1 plus a selector-bound matching reward recipient produces
  `effective` difficulty and nominal CE before the block is sealed. It writes no
  per-Leader activity state and is not a production quote policy.
- Uses distinct 10%/20% test-only aux splits and sentinel recipients so reward
  dispatch is observable without defining a production UIP-0015 policy.
- Independently recomputes quote policy storage, price ranges, K, issued supply,
  miner reward, both aux balances, and starts a fresh v3 validator from genesis.

### Indexer Outage and Selector Tampering

`scripts/usdb/run_usdb_profile_failure_matrix_e2e.sh`

- Stops usdb-indexer while both the miner and a fresh validator are running.
- Requires mining to stop and the validator to remain at genesis while the
  consensus dependency is unavailable.
- Restarts usdb-indexer and requires both nodes to recover to the same head.
- Exports one canonical block and imports it into a clean datadir as a control.
- Mutates each selector field independently and requires offline import to
  reject `payload_version`, `difficulty_policy_version`, `btc_height`,
  `btc_anchor_age_blocks`, `snapshot_id`, `system_state_id`, and `pass_id`
  with the expected reason.

## Fail-Closed Matrix

Rust and Go unit/integration tests cover exact codec boundaries, activation
before/at/after lookup, unknown networks and registry bindings, public manual
override rejection, conflicting records, unsupported formula versions,
active-set/local-commit identity changes, synthetic cross-activation
rollback/replay/reload, structured RPC error mapping, historical replay,
same-height state replacement, service timeout, selector-field tampering, and
miner/validator difficulty agreement. The Rust generator `--check` command
cross-checks the committed Go artifact against both Rust registry files.

The live runners now cover service outage/recovery, all selector-field tampering,
same-height BTC state replacement, historical replay after BTC head advancement,
and fresh-validator sync across the test-only activation boundary. Offline
`import` and `export` expose the same validator RPC URL and timeout controls as a
running node; a failed `import --nocompaction` returns a nonzero status so the
matrix cannot mistake a logged consensus error for success.

Production difficulty still has only policy v1. The reserved difficulty policy
`65535` is compiled only with `usdb_activation_conformance`.
Production quote/aux remain policy `0`; formal quote policy `1` is reserved but
unimplemented. Reserved `65534`/`65535` are compiled only with the economic
conformance tags. Normal binaries reject formal v1 and every reserved policy.
These tests prove the shared quote context/decision and activation machinery
without defining or accidentally shipping mock production policies.

## Historical Execution

On 2026-07-23, before UIP-0011 through UIP-0013 activation, the then-current
geth binary and BTC-side services completed:

- `run_usdb_profile_e2e.sh`: 10 mined USDB blocks, with every profile matching
  the regtest registry/set golden and every difficulty/reward recomputation.
- `run_usdb_profile_energy_growth_e2e.sh`: 12 blocks across zero-energy and
  2000-energy stages, with selector/profile/difficulty/reward agreement.
- `run_usdb_profile_historical_stability_e2e.sh`: 12 blocks mined by node 1;
  after BTC advanced from height 134 to 137 and raw energy changed from 0 to
  2000, a fresh node 2 synchronized the same head and replayed all height-134
  profiles successfully.
- `run_usdb_profile_same_height_replacement_e2e.sh`: replaced the referenced BTC
  block without changing height; the snapshot ID changed and a fresh validator
  rejected the stale 11-block chain with `SNAPSHOT_ID_MISMATCH`.
- `run_usdb_profile_failure_matrix_e2e.sh`: mining stalled at block 16 while
  usdb-indexer was down, recovered to block 18 after restart, and the fresh
  validator reached the same head. The canonical import control succeeded and
  all six independently tampered selector fields were rejected.
- `run_usdb_activation_upgrade_e2e.sh`: the default binary stopped at block 3;
  the tagged binary reused the datadir and mined through block 19. Blocks 1-3
  replayed registry revision 1/policy 1, while blocks 4-19 replayed revision
  2/policy 65535 with exact profile, difficulty, and reward agreement. An
  independent tagged validator replayed the complete chain to the identical
  height and head hash.
- The normal and `usdb_activation_conformance` Go regression suites passed for
  `internal/usdb`, `consensus/ethash`, `miner`, `params`, `cmd/utils`,
  `cmd/geth`, and `scripts/usdb`.

## Latest Economic Activation Execution

On 2026-07-26, after the shared quote decision and current-block implicit
heartbeat refactor, clean regtest workspaces completed the three-stage economic
activation runner. The first 15-block run established the transition baseline;
a follow-up 30-block soak repeated the same default -> fake-v2 -> fake-v3
restart path against BTC profile height `137`:

- The default binary stopped before fake v2 block `3`.
- The fake v2 binary replayed the same datadir and stopped before fake v3 block
  `6`.
- The fake v3 binary replayed both prior stages and mined through block `30`.
- Per-block verification matched difficulty, quote policy slot, price range, K,
  issued supply, miner credit, and aux credits.
- Final issued supply was `19026028117406476574`; miner balance
  `15664799550741754380`, fake-v2 aux balance `190274157602036399`, and fake-v3
  aux balance `3170954409062685795` summed to the same value.
- A fresh fake-v3 validator replayed from genesis and reached the identical
  block-30 head.
- The live profile had zero collaboration contribution, so non-zero
  raw/effective differentiation is cross-checked by the tagged resolver and
  miner/validator engine tests using `raw=1,000,000`, `collab=20,000,000`.

## Deferred Policy Work

- UIP-0014 formal quote v1 still needs a quote source with real per-Leader
  economic meaning, canonical header-visible evidence, authorization, state
  bounds, bootstrap/recovery rules, and public activation. Public activation
  does not need to pass through the test-only FixedPriceHeartbeat.
- UIP-0015 still needs proof encoding, historical BTC validation, pass binding,
  recipient/verifier identity, and final distribution rules.
- A production-version cross-activation scenario remains deferred until a real
  second policy is finalized. Test-only tagged scenarios remain permanent
  activation/restart/replay conformance gates.
