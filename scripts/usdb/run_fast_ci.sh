#!/usr/bin/env bash
# Canonical and compatibility lanes intentionally use isolated subshells.
# shellcheck disable=SC2030,SC2031
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
USDB_REPO_DIR=${USDB_REPO_DIR:-"$ROOT_DIR/../usdb"}
SOURCE_DAO_REPO_DIR=${SOURCE_DAO_REPO_DIR:-"$ROOT_DIR/../SourceDAO"}
FAST_SCOPE=${USDB_FAST_SCOPE:-all}
FAST_OUTPUT_DIR=${USDB_FAST_OUTPUT_DIR:-"${TMPDIR:-/tmp}/usdb-fast-ci"}
CANONICAL_GO_BIN=${USDB_CANONICAL_GO_BIN:-${USDB_GO_BIN:-}}
COMPAT_GO_BIN=${USDB_COMPAT_GO_BIN:-}
REQUIRE_COMPAT_GO=${USDB_FAST_REQUIRE_COMPAT_GO:-0}
SOURCE_DAO_INSTALL=${USDB_FAST_SOURCE_DAO_INSTALL:-none}
NODE_BIN_DIR=${USDB_NODE_BIN_DIR:-}

# shellcheck source=lib/go_toolchain.sh
source "$ROOT_DIR/scripts/usdb/lib/go_toolchain.sh"

log() {
  echo "[usdb-fast-ci] $*"
}

scope_enabled() {
  local component="$1"
  [[ ",$FAST_SCOPE," == *,all,* || ",$FAST_SCOPE," == *",$component,"* ]]
}

validate_fast_scope() {
  local -a components
  local component
  IFS=',' read -r -a components <<<"$FAST_SCOPE"
  if (( ${#components[@]} == 0 )); then
    echo "USDB_FAST_SCOPE must not be empty" >&2
    exit 1
  fi
  for component in "${components[@]}"; do
    case "$component" in
      all|go|rust|golden|sourcedao) ;;
      *)
        echo "unsupported USDB_FAST_SCOPE component: $component" >&2
        exit 1
        ;;
    esac
  done
  if [[ "$REQUIRE_COMPAT_GO" != "0" && "$REQUIRE_COMPAT_GO" != "1" ]]; then
    echo "USDB_FAST_REQUIRE_COMPAT_GO must be 0 or 1" >&2
    exit 1
  fi
}

require_command() {
  local command="$1"
  if ! command -v "$command" >/dev/null; then
    echo "required command is unavailable: $command" >&2
    exit 1
  fi
}

run_consensus_checks() {
  local test_filter="$1"
  local build_tags="${2:-}"
  local report="$FAST_OUTPUT_DIR/consensus-${build_tags:-default}.jsonl"
  local -a args=(test -json)
  if [[ -n "$build_tags" ]]; then
    args+=(-tags "$build_tags")
  fi
  args+=(./consensus/ethash -run "$test_filter")
  usdb_go_with_geth_linker_compat "${args[@]}" | tee "$report"
  python3 "$ROOT_DIR/scripts/usdb/check_fast_go_coverage.py" --report "$report"
}

run_go_checks() {
  require_command shellcheck
  require_command python3
  if [[ -z "$CANONICAL_GO_BIN" ]]; then
    echo "USDB_CANONICAL_GO_BIN or USDB_GO_BIN is required for Go fast checks" >&2
    exit 1
  fi

  log "running Go toolchain policy tests"
  "$ROOT_DIR/scripts/usdb/test_go_toolchain.sh"
  log "running Node toolchain policy tests"
  "$ROOT_DIR/scripts/usdb/test_node_toolchain.sh"
  env PYTHONDONTWRITEBYTECODE=1 python3 "$ROOT_DIR/scripts/usdb/test_fast_go_coverage.py"

  (
    export USDB_GO_BIN="$CANONICAL_GO_BIN"
    export USDB_GO_TOOLCHAIN_MODE=release
    export USDB_GOCACHE="${USDB_CANONICAL_GOCACHE:-$FAST_OUTPUT_DIR/go118-cache}"
    unset USDB_GO_TOOLCHAIN_INITIALIZED
    usdb_init_go_toolchain

    log "checking Go formatting"
    mapfile -d '' go_files < <(
      git -C "$ROOT_DIR" ls-files -z '*.go' \
        ':(exclude)crypto/secp256k1/libsecp256k1/**'
    )
    gofmt_bin="$($USDB_GO_BIN env GOROOT)/bin/gofmt"
    unformatted="$(cd "$ROOT_DIR" && "$gofmt_bin" -l "${go_files[@]}")"
    if [[ -n "$unformatted" ]]; then
      echo "Go files require formatting:" >&2
      printf '%s\n' "$unformatted" >&2
      exit 1
    fi

    local -a vet_packages=(
      ./internal/usdb
      ./internal/usdbacceptance
      ./internal/usdbrelease
      ./core/usdbstate
      ./core
      ./params
      ./consensus/ethash
      ./miner
      ./eth/ethconfig
      ./cmd/utils
      ./cmd/geth
      ./cmd/usdb-genesis-hash
    )
    local consensus_tests='USDB|Usdb|Profile|BTCAnchor|ActivationConformance|EconomicActivation|QuotePolicy|MinimumDifficulty|DefaultBuildRejectsEconomic|PrepareKTransition|PrepareFixedPriceTransition|ExpectedVersionAtActivationBoundary'
    local miner_tests='USDB|Usdb|Profile'
    local geth_tests='USDB|Usdb|Acceptance|CanonicalPositiveBigInt|ChainCommand.*USDB'
    log "running canonical Go vet and tests"
    (
      cd "$ROOT_DIR"
      usdb_go vet "${vet_packages[@]}"
      usdb_go_with_geth_linker_compat test \
        ./internal/usdb \
        ./internal/usdbacceptance \
        ./internal/usdbrelease \
        ./core/usdbstate \
        ./params
      usdb_go_with_geth_linker_compat test ./core/forkid
      usdb_go_with_geth_linker_compat test ./core -run 'USDB|Usdb'
      run_consensus_checks "$consensus_tests"
      usdb_go_with_geth_linker_compat test ./miner -run "$miner_tests"
      usdb_go_with_geth_linker_compat test ./eth/ethconfig ./cmd/utils
      usdb_go_with_geth_linker_compat test ./cmd/geth -run "$geth_tests"
      usdb_go_with_geth_linker_compat test ./cmd/usdb-genesis-hash
      usdb_go_with_geth_linker_compat test \
        -tags usdb_activation_conformance ./internal/usdb
      run_consensus_checks "$consensus_tests" usdb_activation_conformance
      usdb_go_with_geth_linker_compat test \
        -tags usdb_economic_conformance_v2 ./internal/usdb
      run_consensus_checks "$consensus_tests" usdb_economic_conformance_v2
      usdb_go_with_geth_linker_compat test \
        -tags usdb_economic_conformance_v3 ./internal/usdb
      run_consensus_checks "$consensus_tests" usdb_economic_conformance_v3
    )
    usdb_build_geth "$ROOT_DIR" "$FAST_OUTPUT_DIR/geth-go118"
    "$FAST_OUTPUT_DIR/geth-go118" version >/dev/null
  )

  if [[ -n "$COMPAT_GO_BIN" ]]; then
    (
      export USDB_GO_BIN="$COMPAT_GO_BIN"
      export USDB_GO_TOOLCHAIN_MODE=compatibility
      export USDB_GOCACHE="${USDB_COMPAT_GOCACHE:-$FAST_OUTPUT_DIR/go-compat-cache}"
      unset USDB_GO_TOOLCHAIN_INITIALIZED
      usdb_init_go_toolchain
      log "running modern Go compatibility build and focused tests"
      (
        cd "$ROOT_DIR"
        usdb_go_with_geth_linker_compat test \
          ./internal/usdb \
          ./internal/usdbacceptance \
          ./internal/usdbrelease
        usdb_go_with_geth_linker_compat test ./eth/ethconfig ./cmd/utils
        usdb_go_with_geth_linker_compat test ./cmd/geth \
          -run 'USDB|Usdb|Acceptance|CanonicalPositiveBigInt|ChainCommand.*USDB'
        usdb_go_with_geth_linker_compat test ./cmd/usdb-genesis-hash
      )
      usdb_build_geth "$ROOT_DIR" "$FAST_OUTPUT_DIR/geth-go-compat"
      "$FAST_OUTPUT_DIR/geth-go-compat" version >/dev/null
    )
  elif [[ "$REQUIRE_COMPAT_GO" == "1" ]]; then
    echo "USDB_COMPAT_GO_BIN is required when USDB_FAST_REQUIRE_COMPAT_GO=1" >&2
    exit 1
  else
    log "skipping modern Go compatibility lane: USDB_COMPAT_GO_BIN is unset"
  fi

  log "running Go-side shell and Python checks"
  shellcheck -x -P "$ROOT_DIR/scripts/usdb" \
    "$ROOT_DIR"/scripts/usdb/*.sh \
    "$ROOT_DIR"/scripts/usdb/lib/*.sh
  env PYTHONDONTWRITEBYTECODE=1 python3 "$ROOT_DIR/scripts/usdb/test_verify_usdb_profile_e2e.py"
  env PYTHONDONTWRITEBYTECODE=1 python3 "$ROOT_DIR/scripts/usdb/test_ci_revisions.py"
  env PYTHONDONTWRITEBYTECODE=1 python3 "$ROOT_DIR/scripts/usdb/test_ci_change_scope.py"
  env PYTHONDONTWRITEBYTECODE=1 python3 "$ROOT_DIR/scripts/usdb/test_prepare_release.py"
  env PYTHONDONTWRITEBYTECODE=1 python3 "$ROOT_DIR/scripts/usdb/test_calibrate_pow_difficulty.py"
  env PYTHONDONTWRITEBYTECODE=1 python3 "$ROOT_DIR/scripts/usdb/test_configure_usdb_pow_calibration_genesis.py"
  env PYTHONDONTWRITEBYTECODE=1 python3 "$ROOT_DIR/scripts/usdb/test_configure_usdb_anchor_max_age_genesis.py"
  env PYTHONDONTWRITEBYTECODE=1 python3 "$ROOT_DIR/scripts/usdb/test_p2p_defaults.py"
  env PYTHONDONTWRITEBYTECODE=1 python3 "$ROOT_DIR/scripts/usdb/test_release_three_node_e2e.py"
  env PYTHONDONTWRITEBYTECODE=1 python3 "$ROOT_DIR/scripts/usdb/test_mock_bootstrap_indexer.py"
  env PYTHONDONTWRITEBYTECODE=1 python3 "$ROOT_DIR/scripts/usdb/test_usdb_deep_reorg_guard.py"
  env PYTHONDONTWRITEBYTECODE=1 python3 "$ROOT_DIR/scripts/usdb/test_usdb_runtime_deep_reorg.py"
  env PYTHONDONTWRITEBYTECODE=1 python3 "$ROOT_DIR/scripts/usdb/test_long_ci.py"
  env PYTHONDONTWRITEBYTECODE=1 python3 "$ROOT_DIR/tests/test_upstream_fault_matrix.py"
}

run_rust_checks() {
  local runner="$USDB_REPO_DIR/src/btc/scripts/run_fast_ci.sh"

  if [[ ! -x "$runner" ]]; then
    echo "USDB Rust fast runner is unavailable or not executable: $runner" >&2
    exit 1
  fi
  "$runner"
}

run_golden_checks() {
  local manifest="$USDB_REPO_DIR/src/btc/Cargo.toml"
  local activation_golden="$ROOT_DIR/internal/usdb/btc_activation_golden.json"
  local release_golden="$ROOT_DIR/internal/usdb/cross_chain_release_manifest.json"

  log "checking Rust-to-Go activation and release artifacts"
  cargo run --quiet --manifest-path "$manifest" -p usdb-util \
    --bin generate_go_btc_activation_golden -- --check "$activation_golden"
  cargo run --quiet --manifest-path "$manifest" -p usdb-util \
    --bin generate_go_release_manifest_golden -- --check "$release_golden"
}

run_sourcedao_checks() {
  if [[ -n "$NODE_BIN_DIR" ]]; then
    if [[ ! -x "$NODE_BIN_DIR/node" || ! -x "$NODE_BIN_DIR/npm" ]]; then
      echo "USDB_NODE_BIN_DIR must contain executable node and npm: $NODE_BIN_DIR" >&2
      exit 1
    fi
    export PATH="$NODE_BIN_DIR:$PATH"
  fi
  require_command node
  require_command npm

  if [[ ! -f "$SOURCE_DAO_REPO_DIR/package-lock.json" ]]; then
    echo "SourceDAO checkout is unavailable: $SOURCE_DAO_REPO_DIR" >&2
    exit 1
  fi
  case "$SOURCE_DAO_INSTALL" in
    none)
      if [[ ! -d "$SOURCE_DAO_REPO_DIR/node_modules" ]]; then
        echo "SourceDAO node_modules is missing; use USDB_FAST_SOURCE_DAO_INSTALL=ci" >&2
        exit 1
      fi
      ;;
    ci)
      (cd "$SOURCE_DAO_REPO_DIR" && npm ci)
      ;;
    *)
      echo "USDB_FAST_SOURCE_DAO_INSTALL must be none or ci" >&2
      exit 1
      ;;
  esac

  log "running SourceDAO tests, USDB build, and bytecode audit"
  (
    cd "$SOURCE_DAO_REPO_DIR"
    npm run test:usdb:fast
  )
}

main() {
  validate_fast_scope
  mkdir -p "$FAST_OUTPUT_DIR"

  if scope_enabled go; then
    require_command git
    run_go_checks
  fi
  if scope_enabled rust; then
    require_command cargo
    run_rust_checks
  fi
  if scope_enabled golden; then
    require_command cargo
    run_golden_checks
  fi
  if scope_enabled sourcedao; then
    run_sourcedao_checks
  fi

  log "fast checks passed for scope=$FAST_SCOPE"
}

main "$@"
