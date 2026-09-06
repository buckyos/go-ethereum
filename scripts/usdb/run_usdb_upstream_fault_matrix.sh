#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
USDB_REPO_DIR=${USDB_REPO_DIR:-"$ROOT_DIR/../usdb"}
# Compilation belongs to the preparation budget. The inner invocation owns all
# services and its EXIT trap also runs when the simulation budget expires.
if [[ "${1:-}" != --execute ]]; then
  source "$ROOT_DIR/scripts/usdb/lib/go_toolchain.sh"
  MATRIX_WORK_ROOT=${MATRIX_WORK_ROOT:-/tmp/usdb-upstream-matrix}
  mkdir -p "$MATRIX_WORK_ROOT/bin"
  if [[ "${MATRIX_SKIP_BUILD:-0}" == 1 ]]; then
    [[ -x "${GETH_BIN:-}" ]] || { echo "Missing prepared GETH_BIN" >&2; exit 1; }
  fi
  usdb_prepare_geth_binary GETH_BIN "$ROOT_DIR" "$MATRIX_WORK_ROOT/bin/geth"
  # Build once, then all nodes run the same immutable executable copies.
  for package in balance-history usdb-indexer; do
    binary="$MATRIX_WORK_ROOT/bin/$package"
    if [[ "${MATRIX_SKIP_BUILD:-0}" == 1 ]]; then
      [[ -x "$binary" ]] || { echo "Missing prepared service binary: $binary" >&2; exit 1; }
    else
      cargo build --locked --manifest-path "$USDB_REPO_DIR/src/btc/Cargo.toml" -p "$package" --bin "$package"
      cp "${CARGO_TARGET_DIR:-$USDB_REPO_DIR/src/btc/target}/debug/$package" "$binary"
    fi
  done
  export GETH_BIN USDB_REPO_DIR MATRIX_WORK_ROOT
  exec timeout --signal=TERM --kill-after=30s "${MATRIX_TIMEOUT_SEC:-1200}" bash "$0" --execute
fi

MATRIX_DIR=$(mktemp -d "$MATRIX_WORK_ROOT/run-XXXXXX")
MATRIX_OUTPUT_DIR=${MATRIX_OUTPUT_DIR:-"$MATRIX_DIR/output"}
MATRIX_PORT_BASE=${MATRIX_PORT_BASE:-22400}
export PYTHONDONTWRITEBYTECODE=1
export REPO_ROOT="$USDB_REPO_DIR" WORK_DIR="$MATRIX_DIR/a"
export BITCOIN_DIR="$WORK_DIR/bitcoin" ORD_DATA_DIR="$WORK_DIR/ord"
export BALANCE_HISTORY_ROOT="$WORK_DIR/balance-history" USDB_INDEXER_ROOT="$WORK_DIR/usdb-indexer"
export BTC_RPC_PORT=$MATRIX_PORT_BASE BTC_P2P_PORT=$((MATRIX_PORT_BASE + 10))
export ORD_RPC_PORT=$((MATRIX_PORT_BASE + 2)) BH_RPC_PORT=$((MATRIX_PORT_BASE + 3))
export USDB_INDEXER_RPC_PORT=$((MATRIX_PORT_BASE + 4))
export BTC_STABLE_LAG_BLOCKS=10 SYNC_TIMEOUT_SEC=180
export USDB_UPSTREAM_POLL_INTERVAL_MS=200
export ORD_SAVEPOINT_INTERVAL=1 ORD_MAX_SAVEPOINTS=64
export WALLET_NAME=upstream-matrix ORD_WALLET_NAME=ord-upstream-a ORD_WALLET_NAME_B=ord-upstream-unused
# shellcheck source=/dev/null
source "$USDB_REPO_DIR/src/btc/usdb-indexer/scripts/regtest_reorg_lib.sh"
regtest_start_balance_history() {
  "$MATRIX_WORK_ROOT/bin/balance-history" --root-dir "$BALANCE_HISTORY_ROOT" --skip-process-lock >"$BALANCE_HISTORY_LOG_FILE" 2>&1 &
  # Used by regtest_cleanup in the sourced library.
  # shellcheck disable=SC2034
  BALANCE_HISTORY_PID=$!
}
regtest_start_usdb_indexer() {
  "$MATRIX_WORK_ROOT/bin/usdb-indexer" --root-dir "$USDB_INDEXER_ROOT" --skip-process-lock >"$USDB_INDEXER_LOG_FILE" 2>&1 &
  # Used by regtest_cleanup in the sourced library.
  # shellcheck disable=SC2034
  USDB_INDEXER_PID=$!
}
trap regtest_cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
mkdir -p "$MATRIX_OUTPUT_DIR"
python3 "$ROOT_DIR/scripts/usdb/upstream_fault_matrix.py" --probe-ports "$MATRIX_PORT_BASE"
regtest_resolve_bitcoin_binaries
regtest_ensure_workspace_dirs
regtest_start_bitcoind
regtest_ensure_wallet
miner_address=$(regtest_get_new_address)
regtest_mine_blocks 130 "$miner_address"
regtest_start_ord_server
regtest_wait_until_ord_server_synced_to_bitcoind
regtest_prepare_ord_wallets
owner_address=$(regtest_get_ord_wallet_receive_address "$ORD_WALLET_NAME")
regtest_fund_address "$owner_address" 5.0
regtest_mine_blocks 2 "$miner_address"
regtest_wait_until_ord_server_synced_to_bitcoind
printf '%s\n' '{"p":"usdb","op":"mint","v":1,"usdb_main":"0x1111111111111111111111111111111111111111","prev":[]}' >"$WORK_DIR/mint.json"
pass_id=$(regtest_ord_inscribe_file "$ORD_WALLET_NAME" "$WORK_DIR/mint.json" "$owner_address")
regtest_mine_blocks 2 "$miner_address"
# Keep the mint below every injected stable-frontier reorg and give its owner
# a nonzero energy history before creating any USDB blocks.
regtest_fund_address "$owner_address" 1.0
regtest_mine_blocks 20 "$miner_address"
regtest_wait_until_ord_server_synced_to_bitcoind
regtest_create_balance_history_config
regtest_create_usdb_indexer_config
regtest_start_balance_history
regtest_wait_balance_history_rpc_ready
regtest_wait_balance_history_consensus_ready
regtest_start_usdb_indexer
regtest_wait_usdb_rpc_ready
regtest_wait_usdb_consensus_ready
python3 "$ROOT_DIR/scripts/usdb/upstream_fault_matrix.py" \
  --work-dir "$MATRIX_DIR" --output-dir "$MATRIX_OUTPUT_DIR" \
  --usdb-repo "$USDB_REPO_DIR" --geth "$GETH_BIN" \
  --bitcoin "$BITCOIND_BIN" --ord "$ORD_BIN" --port-base "$MATRIX_PORT_BASE" \
  --balance-history "$MATRIX_WORK_ROOT/bin/balance-history" --indexer "$MATRIX_WORK_ROOT/bin/usdb-indexer" \
  --miner-address "$miner_address" --owner-address "$owner_address" --pass-id "$pass_id"
