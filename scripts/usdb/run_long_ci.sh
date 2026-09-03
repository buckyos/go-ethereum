#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
WORKSPACE_ROOT=${USDB_WORKSPACE_ROOT:-$(dirname "$ROOT_DIR")}
USDB_REPO_DIR=${USDB_REPO_DIR:-"$WORKSPACE_ROOT/usdb"}
SOURCE_DAO_REPO=${SOURCE_DAO_REPO:-"$WORKSPACE_ROOT/SourceDAO"}
OUTPUT_ROOT=${USDB_LONG_CI_OUTPUT_DIR:-/tmp/usdb-long-ci-output}
WORK_ROOT=${USDB_LONG_CI_WORK_DIR:-/tmp/usdb-long-ci-work}

declare -a NIGHTLY_SHARDS=(
  go-profile
  go-activation
  balance-history
  indexer-protocol
  indexer-reorg
  indexer-validator
)
declare -a WEEKLY_SHARDS=(
  world-soak
  economic-capacity
  balance-history-extended
  release-e2e
)

usage() {
  cat <<'EOF'
Usage: scripts/usdb/run_long_ci.sh <nightly|weekly> <shard>
       scripts/usdb/run_long_ci.sh <nightly|weekly> --list

Run one deterministic long-CI shard. External Bitcoin Core and ord binaries are
provided through BITCOIN_BIN_DIR and ORD_BIN for shards that require regtest.
EOF
}

list_shards() {
  local tier="$1"
  case "$tier" in
    nightly) printf '%s\n' "${NIGHTLY_SHARDS[@]}" ;;
    weekly) printf '%s\n' "${WEEKLY_SHARDS[@]}" ;;
  esac
}

has_shard() {
  local expected="$1"
  shift
  local candidate
  for candidate in "$@"; do
    [[ "$candidate" == "$expected" ]] && return 0
  done
  return 1
}

validate_shard() {
  local tier="$1"
  local shard="$2"
  case "$tier" in
    nightly)
      has_shard "$shard" "${NIGHTLY_SHARDS[@]}" || {
        echo "Unknown nightly shard: $shard" >&2
        return 2
      }
      ;;
    weekly)
      has_shard "$shard" "${WEEKLY_SHARDS[@]}" || {
        echo "Unknown weekly shard: $shard" >&2
        return 2
      }
      ;;
  esac
}

require_directory() {
  local path="$1"
  local label="$2"
  [[ -d "$path" ]] || {
    echo "Missing ${label} checkout: $path" >&2
    exit 1
  }
}

require_regtest_tools() {
  [[ -x "${BITCOIN_BIN_DIR:-}/bitcoind" && -x "${BITCOIN_BIN_DIR:-}/bitcoin-cli" ]] || {
    echo "BITCOIN_BIN_DIR must contain executable bitcoind and bitcoin-cli" >&2
    exit 1
  }
  [[ -n "${ORD_BIN:-}" && -x "$ORD_BIN" ]] || {
    echo "ORD_BIN must identify an executable ord binary" >&2
    exit 1
  }
}

run_case() {
  local name="$1"
  shift
  local log_file="$OUTPUT_ROOT/${name}.log"
  echo "[usdb-long-ci] START ${name}"
  "$@" 2>&1 | tee "$log_file"
  echo "[usdb-long-ci] PASS ${name}"
}

collect_diagnostics() {
  local source relative destination
  mkdir -p "$OUTPUT_ROOT/diagnostics"
  [[ -d "$WORK_ROOT" ]] || return 0
  while IFS= read -r -d '' source; do
    relative=${source#"$WORK_ROOT"/}
    destination="$OUTPUT_ROOT/diagnostics/$relative"
    mkdir -p "$(dirname "$destination")"
    cp "$source" "$destination"
  done < <(
    find "$WORK_ROOT" -type f \
      \( -name '*.log' -o -name '*.json' -o -name '*.jsonl' -o -name '*.txt' \) \
      -size -64M -print0 2>/dev/null
  )
}

run_nightly() {
  local shard="$1"
  case "$shard" in
    go-profile)
      require_regtest_tools
      run_case profile \
        env WORK_DIR="$WORK_ROOT/profile" USDB_REPO_DIR="$USDB_REPO_DIR" \
          BITCOIN_BIN_DIR="$BITCOIN_BIN_DIR" ORD_BIN="$ORD_BIN" \
          "$ROOT_DIR/scripts/usdb/run_usdb_profile_e2e.sh"
      run_case profile-same-height-replacement \
        env REPLACEMENT_WORK_DIR="$WORK_ROOT/profile-same-height" USDB_REPO_DIR="$USDB_REPO_DIR" \
          BITCOIN_BIN_DIR="$BITCOIN_BIN_DIR" ORD_BIN="$ORD_BIN" \
          "$ROOT_DIR/scripts/usdb/run_usdb_profile_same_height_replacement_e2e.sh"
      run_case profile-failure-matrix \
        env FAILURE_WORK_DIR="$WORK_ROOT/profile-failure" USDB_REPO_DIR="$USDB_REPO_DIR" \
          BITCOIN_BIN_DIR="$BITCOIN_BIN_DIR" ORD_BIN="$ORD_BIN" \
          "$ROOT_DIR/scripts/usdb/run_usdb_profile_failure_matrix_e2e.sh"
      run_case profile-anchor-boundary \
        env ANCHOR_WORK_DIR="$WORK_ROOT/profile-anchor" USDB_REPO_DIR="$USDB_REPO_DIR" \
          BITCOIN_BIN_DIR="$BITCOIN_BIN_DIR" ORD_BIN="$ORD_BIN" \
          "$ROOT_DIR/scripts/usdb/run_usdb_profile_anchor_boundary_e2e.sh"
      ;;
    go-activation)
      require_regtest_tools
      run_case activation-upgrade \
        env UPGRADE_WORK_DIR="$WORK_ROOT/activation" USDB_REPO_DIR="$USDB_REPO_DIR" \
          BITCOIN_BIN_DIR="$BITCOIN_BIN_DIR" ORD_BIN="$ORD_BIN" \
          "$ROOT_DIR/scripts/usdb/run_usdb_activation_upgrade_e2e.sh"
      run_case economic-activation-upgrade \
        env UPGRADE_WORK_DIR="$WORK_ROOT/economic-activation" USDB_REPO_DIR="$USDB_REPO_DIR" \
          BITCOIN_BIN_DIR="$BITCOIN_BIN_DIR" ORD_BIN="$ORD_BIN" \
          "$ROOT_DIR/scripts/usdb/run_usdb_economic_activation_upgrade_e2e.sh"
      run_case bootstrap-restart-joiner \
        env WORK_DIR="$WORK_ROOT/bootstrap" USDB_REPO_DIR="$USDB_REPO_DIR" \
          SOURCE_DAO_REPO="$SOURCE_DAO_REPO" BITCOIN_BIN_DIR="$BITCOIN_BIN_DIR" \
          ORD_BIN="$ORD_BIN" \
          "$ROOT_DIR/scripts/usdb/run_local_full_bootstrap_restart_joiner.sh"
      ;;
    balance-history)
      require_regtest_tools
      run_case balance-history-correctness \
        env BITCOIN_BIN_DIR="$BITCOIN_BIN_DIR" \
          bash "$USDB_REPO_DIR/src/btc/balance-history/scripts/run_regtest_suite.sh" correctness
      run_case balance-history-stable-lag \
        env BITCOIN_BIN_DIR="$BITCOIN_BIN_DIR" \
          bash "$USDB_REPO_DIR/src/btc/balance-history/scripts/run_regtest_suite.sh" stable-lag-reorg
      ;;
    indexer-protocol)
      require_regtest_tools
      run_case indexer-protocol \
        env BITCOIN_BIN_DIR="$BITCOIN_BIN_DIR" ORD_BIN="$ORD_BIN" \
          RUN_REGTEST_SMOKE=1 RUN_LIVE_ORD_REALWORLD_SUITE=1 \
          RUN_UIP0001_0004_LIVE_MATRIX=1 RUN_REORG_REGRESSION=0 \
          bash "$USDB_REPO_DIR/src/btc/usdb-indexer/scripts/run_regression.sh"
      ;;
    indexer-reorg)
      require_regtest_tools
      run_case indexer-reorg \
        env BITCOIN_BIN_DIR="$BITCOIN_BIN_DIR" ORD_BIN="$ORD_BIN" \
          RUN_SMOKE_REORG_SUITE=1 RUN_LIVE_ORD_REORG_SUITE=1 \
          RUN_PENDING_RECOVERY_SUITE=1 RUN_HISTORICAL_VALIDATION_SUITE=1 \
          RUN_VALIDATOR_BLOCK_BODY_SUITE=0 \
          bash "$USDB_REPO_DIR/src/btc/usdb-indexer/scripts/run_reorg_regression.sh"
      ;;
    indexer-validator)
      require_regtest_tools
      run_case indexer-validator \
        env BITCOIN_BIN_DIR="$BITCOIN_BIN_DIR" ORD_BIN="$ORD_BIN" \
          RUN_SMOKE_REORG_SUITE=0 RUN_LIVE_ORD_REORG_SUITE=0 \
          RUN_PENDING_RECOVERY_SUITE=0 RUN_HISTORICAL_VALIDATION_SUITE=0 \
          RUN_VALIDATOR_BLOCK_BODY_SUITE=1 \
          bash "$USDB_REPO_DIR/src/btc/usdb-indexer/scripts/run_reorg_regression.sh"
      ;;
    *)
      echo "Unknown nightly shard: $shard" >&2
      exit 2
      ;;
  esac
}

run_weekly() {
  local shard="$1"
  case "$shard" in
    world-soak)
      require_regtest_tools
      run_case world-soak \
        env BITCOIN_BIN_DIR="$BITCOIN_BIN_DIR" ORD_BIN="$ORD_BIN" \
          WORLD_SOAK_OUTPUT_ROOT="$OUTPUT_ROOT/world-soak" \
          WORLD_SOAK_WORKSPACE_ROOT="$WORK_ROOT/world-soak" \
          WORLD_SOAK_BLOCKS="${WORLD_SOAK_BLOCKS:-2500}" \
          WORLD_SOAK_SEEDS="${WORLD_SOAK_SEEDS:-41 42 43}" \
          WORLD_SOAK_PARALLELISM="${WORLD_SOAK_PARALLELISM:-1}" \
          bash "$USDB_REPO_DIR/src/btc/usdb-indexer/scripts/run_regtest_world_soak_matrix.sh"
      ;;
    economic-capacity)
      run_case economic-scale \
        env USDB_ECONOMIC_SCALE_OUTPUT_DIR="$OUTPUT_ROOT/economic-scale" \
          bash "$USDB_REPO_DIR/src/btc/usdb-indexer/scripts/run_economic_scale_eval.sh" 100 1000 10000
      run_case economic-capacity \
        env USDB_ECONOMIC_CAPACITY_OUTPUT_DIR="$OUTPUT_ROOT/economic-capacity" \
          bash "$USDB_REPO_DIR/src/btc/usdb-indexer/scripts/run_economic_capacity_supplement.sh"
      ;;
    balance-history-extended)
      require_regtest_tools
      local script
      for script in \
        regtest_exact_height_snapshot_failure_paths.sh \
        regtest_exact_height_snapshot_restart.sh \
        regtest_exact_height_snapshot_same_height_reorg.sh \
        regtest_exact_height_snapshot_signed_install.sh \
        regtest_deep_reorg_smoke.sh \
        regtest_undo_retention_reorg.sh; do
        run_case "${script%.sh}" \
          env BITCOIN_BIN_DIR="$BITCOIN_BIN_DIR" \
            bash "$USDB_REPO_DIR/src/btc/balance-history/scripts/$script"
      done
      ;;
    release-e2e)
      require_regtest_tools
      run_case public-release-candidate \
        env WORK_DIR="$WORK_ROOT/public-release" USDB_REPO_DIR="$USDB_REPO_DIR" \
          USDB_REPO="$USDB_REPO_DIR" SOURCE_DAO_REPO="$SOURCE_DAO_REPO" \
          BITCOIN_BIN_DIR="$BITCOIN_BIN_DIR" ORD_BIN="$ORD_BIN" \
          "$ROOT_DIR/scripts/usdb/run_usdb_public_release_candidate_e2e.sh"
      ;;
    *)
      echo "Unknown weekly shard: $shard" >&2
      exit 2
      ;;
  esac
}

main() {
  local tier="${1:-}"
  local shard="${2:-}"
  [[ "$tier" == nightly || "$tier" == weekly ]] || {
    usage >&2
    exit 2
  }
  if [[ "$shard" == --list ]]; then
    list_shards "$tier"
    exit 0
  fi
  [[ -n "$shard" && $# == 2 ]] || {
    usage >&2
    exit 2
  }
  validate_shard "$tier" "$shard"

  require_directory "$USDB_REPO_DIR" usdb
  require_directory "$SOURCE_DAO_REPO" SourceDAO
  mkdir -p "$OUTPUT_ROOT" "$WORK_ROOT"
  trap collect_diagnostics EXIT

  case "$tier" in
    nightly) run_nightly "$shard" ;;
    weekly) run_weekly "$shard" ;;
  esac
}

main "$@"
