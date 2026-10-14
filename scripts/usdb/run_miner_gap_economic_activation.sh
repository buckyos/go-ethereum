#!/usr/bin/env bash
# Explicitly tagged real-service acceptance; never used to build release images.
set -euo pipefail
ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
USDB_REPO_DIR=${USDB_REPO_DIR:-"$ROOT_DIR/../usdb"}
MATRIX_WORK_ROOT=${MATRIX_WORK_ROOT:-/tmp/usdb-gap-economic-activation}
phase=${1:-all}
case "$phase" in all | --prepare-only | --run-only) ;; *) exit 2 ;; esac
export PYTHONDONTWRITEBYTECODE=1
# shellcheck source=lib/go_toolchain.sh
source "$ROOT_DIR/scripts/usdb/lib/go_toolchain.sh"
if [[ "$phase" != --run-only ]]; then
  mkdir -p "$MATRIX_WORK_ROOT/bin"
  usdb_build_geth "$ROOT_DIR" "$MATRIX_WORK_ROOT/bin/geth" \
    'usdb_miner_pass_conformance,usdb_activation_conformance,usdb_economic_conformance_v3'
  (cd "$ROOT_DIR" && usdb_go build -o "$MATRIX_WORK_ROOT/bin/invalidblock" ./tests/common/invalidblock)
  cargo build --locked --manifest-path "$USDB_REPO_DIR/src/btc/Cargo.toml" -p balance-history
  cp "${CARGO_TARGET_DIR:-$USDB_REPO_DIR/src/btc/target}/debug/balance-history" "$MATRIX_WORK_ROOT/bin/balance-history"
  cargo build --locked --manifest-path "$USDB_REPO_DIR/src/btc/Cargo.toml" -p usdb-indexer --features miner-pass-conformance
  cp "${CARGO_TARGET_DIR:-$USDB_REPO_DIR/src/btc/target}/debug/usdb-indexer" "$MATRIX_WORK_ROOT/bin/usdb-indexer"
  python3 "$ROOT_DIR/tests/common/gap_economic_tools.py" record "$MATRIX_WORK_ROOT" "$ROOT_DIR" "$USDB_REPO_DIR"
fi
[[ "$phase" != --prepare-only ]] || exit 0
python3 "$ROOT_DIR/tests/common/gap_economic_tools.py" check "$MATRIX_WORK_ROOT"
export GETH_BIN="$MATRIX_WORK_ROOT/bin/geth" MATRIX_WORK_ROOT
export MATRIX_OUTPUT_DIR=${MATRIX_OUTPUT_DIR:-"$MATRIX_WORK_ROOT/activation-output"}
mkdir -p "$MATRIX_OUTPUT_DIR"
cp "$MATRIX_WORK_ROOT/build-manifest.json" "$MATRIX_OUTPUT_DIR/build-manifest.json"
exec env MATRIX_SCENARIO=anchor-economics MATRIX_ECONOMIC_CASE=activation MATRIX_SKIP_BUILD=1 \
  USDB_REPO_DIR="$USDB_REPO_DIR" bash "$ROOT_DIR/scripts/usdb/run_usdb_upstream_fault_matrix.sh"
