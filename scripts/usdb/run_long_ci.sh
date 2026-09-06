#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
# shellcheck source=lib/node_toolchain.sh
source "$ROOT_DIR/scripts/usdb/lib/node_toolchain.sh"
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
  upstream-fault-matrix
  economic-capacity
  balance-history-extended
  release-e2e
)

usage() {
  cat <<'EOF'
Usage: scripts/usdb/run_long_ci.sh <nightly|weekly> <shard> [--prepare-only|--run-only]
       scripts/usdb/run_long_ci.sh <nightly|weekly> --list

Run one deterministic long-CI shard. External Bitcoin Core and ord binaries are
provided through BITCOIN_BIN_DIR and ORD_BIN for shards that require regtest.
Use --prepare-only followed by --run-only in the same workspace/toolchain to
give compilation and simulation independent CI step budgets. The default runs both.
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
  local command_status tee_status exit_code
  local -a pipeline_status

  echo "[usdb-long-ci] START ${name}"
  if [[ "${GITHUB_ACTIONS:-}" == "true" ]]; then
    echo "::group::USDB long CI: ${name}"
  fi
  if "$@" 2>&1 | tee "$log_file"; then
    if [[ "${GITHUB_ACTIONS:-}" == "true" ]]; then
      echo "::endgroup::"
    fi
    echo "[usdb-long-ci] PASS ${name}"
    return 0
  else
    pipeline_status=("${PIPESTATUS[@]}")
    command_status="${pipeline_status[0]}"
    tee_status="${pipeline_status[1]}"
  fi

  if [[ "${GITHUB_ACTIONS:-}" == "true" ]]; then
    echo "::endgroup::"
  fi
  exit_code="$command_status"
  if (( tee_status != 0 )); then
    exit_code="$tee_status"
  fi
  echo "[usdb-long-ci] FAIL ${name}: command_exit=${command_status}, tee_exit=${tee_status}, log=${log_file}" >&2
  if [[ "${GITHUB_ACTIONS:-}" == "true" ]]; then
    echo "::error title=USDB long CI case failed::case=${name}, command_exit=${command_status}, log=${log_file}, diagnostics=${OUTPUT_ROOT}/diagnostics"
  fi
  if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
    {
      echo "### USDB long CI failure"
      echo
      echo "- Case: \`${name}\`"
      echo "- Command exit: \`${command_status}\`"
      echo "- Primary log in uploaded artifact: \`${name}.log\`"
      echo "- Service diagnostics in uploaded artifact: \`diagnostics/\`"
      echo "- Artifact: \`${USDB_LONG_CI_ARTIFACT_NAME:-not configured}\`"
    } >>"$GITHUB_STEP_SUMMARY"
  fi
  return "$exit_code"
}

prepare_usdb_service_binaries() {
  local tier="$1"
  local shard="$2"

  case "${tier}:${shard}" in
    nightly:go-profile | nightly:go-activation | nightly:indexer-protocol | nightly:indexer-reorg | nightly:indexer-validator | weekly:world-soak | weekly:upstream-fault-matrix | weekly:release-e2e)
      # Build each package with the same selection used by its later cargo run.
      # A combined build unifies dependency features and does not warm the
      # single-package fingerprints used inside the readiness window.
      run_case rust-usdb-indexer-build \
        cargo build --locked \
          --manifest-path "$USDB_REPO_DIR/src/btc/Cargo.toml" \
          -p usdb-indexer \
          --bin usdb-indexer
      run_case rust-balance-history-build \
        cargo build --locked \
          --manifest-path "$USDB_REPO_DIR/src/btc/Cargo.toml" \
          -p balance-history \
          --bin balance-history
      ;;
  esac
  if [[ "${tier}:${shard}" == weekly:upstream-fault-matrix ]]; then
    # Keep Go compilation outside the twenty-minute fault simulation budget.
    source "$ROOT_DIR/scripts/usdb/lib/go_toolchain.sh"
    run_case go-upstream-matrix-build \
      usdb_build_geth "$ROOT_DIR" "$WORK_ROOT/upstream-matrix/bin/geth"
    local package
    for package in balance-history usdb-indexer; do
      cp "${CARGO_TARGET_DIR:-$USDB_REPO_DIR/src/btc/target}/debug/$package" "$WORK_ROOT/upstream-matrix/bin/$package"
    done
  fi
}

prepare_source_dao_artifacts() {
  local tier="$1"
  local shard="$2"

  case "${tier}:${shard}" in
    nightly:go-activation)
      USDB_REQUIRED_NODE_MAJOR=24
      usdb_load_node_toolchain
      command -v npm >/dev/null 2>&1 || {
        echo "npm is required to build SourceDAO USDB artifacts" >&2
        exit 1
      }
      run_case source-dao-usdb-build \
        npm --prefix "$SOURCE_DAO_REPO" run build:usdb
      ;;
  esac
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
    upstream-fault-matrix)
      require_regtest_tools
      run_case upstream-fault-matrix \
        env USDB_REPO_DIR="$USDB_REPO_DIR" BITCOIN_BIN_DIR="$BITCOIN_BIN_DIR" ORD_BIN="$ORD_BIN" \
          GETH_BIN="$WORK_ROOT/upstream-matrix/bin/geth" \
          MATRIX_SKIP_BUILD=1 \
          MATRIX_WORK_ROOT="$WORK_ROOT/upstream-matrix" \
          MATRIX_OUTPUT_DIR="$OUTPUT_ROOT/upstream-fault-matrix" \
          bash "$ROOT_DIR/scripts/usdb/run_usdb_upstream_fault_matrix.sh"
      ;;
    world-soak)
      require_regtest_tools
      run_case world-readiness \
        env PYTHONDONTWRITEBYTECODE=1 \
          python3 "$USDB_REPO_DIR/tests/test_regtest_world_readiness.py"
      run_case world-reorg-wallet-recovery \
        env PYTHONDONTWRITEBYTECODE=1 BITCOIN_BIN_DIR="$BITCOIN_BIN_DIR" ORD_BIN="$ORD_BIN" \
          python3 "$USDB_REPO_DIR/tests/test_regtest_world_reorg.py"
      run_case world-soak \
        env BITCOIN_BIN_DIR="$BITCOIN_BIN_DIR" ORD_BIN="$ORD_BIN" \
          ORD_POLLING_INTERVAL="${ORD_POLLING_INTERVAL:-200ms}" \
          USDB_UPSTREAM_POLL_INTERVAL_MS="${USDB_UPSTREAM_POLL_INTERVAL_MS:-200}" \
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
  local phase="${3:-all}"
  [[ "$tier" == nightly || "$tier" == weekly ]] || {
    usage >&2
    exit 2
  }
  if [[ "$shard" == --list ]]; then
    list_shards "$tier"
    exit 0
  fi
  [[ -n "$shard" && ( $# == 2 || $# == 3 ) ]] || {
    usage >&2
    exit 2
  }
  validate_shard "$tier" "$shard"
  case "$phase" in
    all | --prepare-only | --run-only) ;;
    *) usage >&2; exit 2 ;;
  esac

  require_directory "$USDB_REPO_DIR" usdb
  require_directory "$SOURCE_DAO_REPO" SourceDAO
  mkdir -p "$OUTPUT_ROOT" "$WORK_ROOT"
  trap collect_diagnostics EXIT
  if [[ "$phase" != --run-only ]]; then
    prepare_usdb_service_binaries "$tier" "$shard"
    prepare_source_dao_artifacts "$tier" "$shard"
  fi
  if [[ "$phase" == --prepare-only ]]; then
    return 0
  fi

  case "$tier" in
    nightly) run_nightly "$shard" ;;
    weekly) run_weekly "$shard" ;;
  esac
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  main "$@"
fi
