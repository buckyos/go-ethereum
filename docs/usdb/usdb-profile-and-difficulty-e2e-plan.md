# USDB Chain Profile and Difficulty E2E Plan

This note defines the current end-to-end path between the BTC-side USDB regtest
stack and the local USDB chain. The active contract is UIP-0006 through
UIP-0009; the earlier 105-byte reward payload and level multiplier are no longer
part of this test plan.

## Current Boundary

The built-in development chain activates these policies from USDB block `0`:

- `payload_version = 1`
- `difficulty_policy_version = 1`
- activation record `btcActivationRegistryId = 22d820e6...aaf83d` (`btc-regtest` revision 1)

The remaining UIP-0009 version families are present in the chain-config
activation record but remain `0` until their defining UIP is implemented. In
particular, UIP-0011 reward and CoinBase emission are not active. Blocks still
receive the existing Constantinople Ethash reward while both difficulty and the
reward transition require a valid selector-bound historical profile.

Runtime CLI flags only provide companion-service access and the selected pass:

- `--miner.usdb-indexer.rpcurl`
- `--miner.usdb.passid`
- `--miner.usdb-indexer.timeout`
- `--ethash.usdb-indexer.rpcurl`
- `--ethash.usdb-indexer.timeout`

Activation and expected versions come only from `params.ChainConfig.USDB`.

## Shared Flow

Each live runner performs the following operations:

1. Start bitcoind regtest, ord, balance-history, and usdb-indexer.
2. Mint one UIP-0001 `v=1` standard pass using `usdb_main`.
3. Wait until the BTC-side services expose a consensus-ready UIP-0006 profile.
4. Generate the canonical USDB genesis and initialize one or two geth nodes.
5. Mine USDB blocks with the 107-byte UIP-0007 selector in `header.Extra`.
6. Stop mining and replay every payload against `get_pass_economic_profile`.
7. Require every historical profile to match the chain-bound BTC registry ID,
   v1 active-version-set ID, and complete active-version-set golden.
8. Independently recompute effective energy, level, difficulty factor, base
   difficulty, and real difficulty.
9. Verify the static pre-UIP-0011 block reward and final coinbase balance.

The common validator is:

- `scripts/usdb/verify_usdb_profile_e2e.py`

It is shared by all live runners so offsets, profile validation, formula
thresholds, and rounding cannot drift between scenarios.

## Required Assertions

For every mined block the common validator checks:

- `header.Extra` is exactly 107 bytes.
- `payload_version = 1`; the normal runners require `difficulty_policy_version = 1`, while the activation runner selects the expected policy from the USDB block.
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
- Until reward versions activate, each block contributes the existing `2e18`
  base reward and energy does not change the reward.

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
- Confirms reward remains unchanged while UIP-0011 is inactive.

### Historical Replay and Fresh Validator

`scripts/usdb/run_usdb_profile_historical_stability_e2e.sh`

- Mines blocks on node 1.
- Advances the BTC head and changes current pass energy.
- Starts a fresh validator node 2 and syncs the old USDB blocks.
- Confirms node 2 reaches node 1's height and hash.
- Replays old selectors against their committed historical state rather than the
  new BTC head.

### Activation Upgrade and Binary Restart

`scripts/usdb/run_usdb_activation_upgrade_e2e.sh`

- Builds one default geth and one `usdb_activation_conformance` geth.
- Adds a test-only block-4 activation that selects BTC regtest registry revision
  2 and reserved difficulty policy `65535`.
- Requires the default binary to mine blocks 1-3 and fail closed at block 4.
- Restarts the tagged binary on the same datadir and mines through the activation.
- Replays every profile with the per-block registry revision and independently
  checks the test policy's deterministic `v1 difficulty + 1` result.

## Fail-Closed Matrix

Rust and Go unit/integration tests cover exact codec boundaries, activation
before/at/after lookup, unknown networks and registry bindings, public manual
override rejection, conflicting records, unsupported formula versions,
active-set/local-commit identity changes, synthetic cross-activation
rollback/replay/reload, structured RPC error mapping, historical replay,
same-height state replacement, service timeout, selector-field tampering, and
miner/validator difficulty agreement. The Rust generator `--check` command
cross-checks the committed Go artifact against both Rust registry files.

The remaining live-only additions should reuse the same common validator and cover:

- stopping usdb-indexer while mining and syncing;
- tampering each selector field in an imported block fixture;
- replacing the referenced BTC state at the same height and proving the old
  selector is rejected;
- fresh-validator peer sync across the test-only activation boundary.

Production still has only policy v1. The reserved policy `65535` is compiled only
with `usdb_activation_conformance`; normal binaries reject it. This proves the
activation machinery without defining or accidentally shipping a mock production v2.

## Latest Execution

On 2026-07-22 the current-source geth binary and BTC-side services completed:

- `run_usdb_profile_e2e.sh`: 13 mined USDB blocks, with every profile matching
  the regtest registry/set golden and every difficulty/reward recomputation.
- `run_usdb_profile_historical_stability_e2e.sh`: 12 blocks mined by node 1;
  after BTC advanced from height 134 to 137 and raw energy changed from 0 to
  2000, a fresh node 2 synchronized the same head and replayed all height-134
  profiles successfully.
- `run_usdb_activation_upgrade_e2e.sh`: the default binary stopped at block 3;
  the tagged binary reused the datadir and mined through block 13. Blocks 1-3
  replayed registry revision 1/policy 1, while blocks 4-13 replayed revision
  2/policy 65535 with exact profile, difficulty, and reward agreement.

## Deferred Policy Work

- UIP-0011 defines reward recipient validation, CoinBase emission, fee split,
  and issued-supply state transitions.
- UIP-0014 defines quote activity and the candidate difficulty factor. Until it
  activates, difficulty v1 uses the nominal UIP-0006 factor.
- A production-version cross-activation scenario remains deferred until a real
  second formula or policy is specified. The test-only tagged scenario remains a
  permanent activation/restart conformance gate.
