# USDB Chain Profile and Difficulty E2E Plan

This note defines the current end-to-end path between the BTC-side USDB regtest
stack and the local USDB chain. The active contract is UIP-0006 through
UIP-0009; the earlier 105-byte reward payload and level multiplier are no longer
part of this test plan.

## Current Boundary

The built-in development chain activates these policies from USDB block `0`:

- `payload_version = 1`
- `difficulty_policy_version = 1`

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
7. Independently recompute effective energy, level, difficulty factor, base
   difficulty, and real difficulty.
8. Verify the static pre-UIP-0011 block reward and final coinbase balance.

The common validator is:

- `scripts/usdb/verify_usdb_profile_e2e.py`

It is shared by all live runners so offsets, profile validation, formula
thresholds, and rounding cannot drift between scenarios.

## Required Assertions

For every mined block the common validator checks:

- `header.Extra` is exactly 107 bytes.
- `payload_version` and `difficulty_policy_version` are both `1`.
- `btc_height`, `snapshot_id`, `system_state_id`, and `pass_id` reproduce one
  exact historical UIP-0006 profile.
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

## Fail-Closed Matrix

Go unit/integration tests cover exact codec boundaries, activation lookup,
candidate state/kind boundaries, structured RPC error mapping, historical
replay, same-height state replacement, service timeout, selector-field
tampering, and miner/validator difficulty agreement.

The next live-only additions should reuse the same common validator and cover:

- stopping usdb-indexer while mining and syncing;
- tampering each selector field in an imported block fixture;
- replacing the referenced BTC state at the same height and proving the old
  selector is rejected;
- activation-boundary replay after a second supported policy version exists.

## Deferred Policy Work

- UIP-0011 defines reward recipient validation, CoinBase emission, fee split,
  and issued-supply state transitions.
- UIP-0014 defines quote activity and the candidate difficulty factor. Until it
  activates, difficulty v1 uses the nominal UIP-0006 factor.
- Activation registry ids and BTC-side active-version-set commitments require
  the corresponding UIP-0006/UIP-0008 cross-service fields before they can be
  enforced by go-ethereum.
