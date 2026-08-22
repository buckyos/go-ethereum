#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
# shellcheck source=lib/go_toolchain.sh
source "$ROOT_DIR/scripts/usdb/lib/go_toolchain.sh"
USDB_REPO_DIR=${USDB_REPO_DIR:-"$ROOT_DIR/../usdb"}

E2E_WORK_DIR=${WORK_DIR:-/tmp/usdb-profile-e2e}
USDB_CHAIN_WORK_DIR=${USDB_CHAIN_WORK_DIR:-"$E2E_WORK_DIR/geth"}
DATADIR=${DATADIR:-"$USDB_CHAIN_WORK_DIR/datadir"}
GENESIS_JSON=${GENESIS_JSON:-"$USDB_CHAIN_WORK_DIR/usdb-genesis.json"}
GETH_LOG_FILE=${GETH_LOG_FILE:-"$USDB_CHAIN_WORK_DIR/geth.log"}

HTTP_ADDR=${HTTP_ADDR:-127.0.0.1}
HTTP_PORT=${HTTP_PORT:-19545}
P2P_PORT=${P2P_PORT:-31313}
AUTHRPC_PORT=${AUTHRPC_PORT:-19551}
NETWORK_ID=${NETWORK_ID:-20260323}
TARGET_BLOCKS=${TARGET_BLOCKS:-2}
BTC_STABLE_LAG_BLOCKS=${BTC_STABLE_LAG_BLOCKS:-5}
RPC_WAIT_SECONDS=${RPC_WAIT_SECONDS:-90}
BLOCK_WAIT_SECONDS=${BLOCK_WAIT_SECONDS:-180}
ENERGY_TOPUP_AMOUNT_BTC=${ENERGY_TOPUP_AMOUNT_BTC:-1.0}
ENERGY_GROWTH_BLOCKS=${ENERGY_GROWTH_BLOCKS:-2}
ACTIVATION_CONFORMANCE_BLOCK=${ACTIVATION_CONFORMANCE_BLOCK:-}
ECONOMIC_CONFORMANCE_V2_BLOCK=${ECONOMIC_CONFORMANCE_V2_BLOCK:-}
ECONOMIC_CONFORMANCE_V3_BLOCK=${ECONOMIC_CONFORMANCE_V3_BLOCK:-}
INDEXER_OUTAGE_CHECK=${INDEXER_OUTAGE_CHECK:-0}
SELECTOR_TAMPER_CHECK=${SELECTOR_TAMPER_CHECK:-0}
ACTIVATION_FRESH_VALIDATOR_CHECK=${ACTIVATION_FRESH_VALIDATOR_CHECK:-0}
ANCHOR_BOUNDARY_CHECK=${ANCHOR_BOUNDARY_CHECK:-0}
OUTAGE_OBSERVE_SECONDS=${OUTAGE_OBSERVE_SECONDS:-4}
ANCHOR_BOUNDARY_OBSERVE_SECONDS=${ANCHOR_BOUNDARY_OBSERVE_SECONDS:-4}
BTC_ANCHOR_MAX_AGE_BLOCKS=${BTC_ANCHOR_MAX_AGE_BLOCKS:-6650}
USDB_QUERY_TIMEOUT=${USDB_QUERY_TIMEOUT:-1s}
USDB_CHAIN_GCMODE=${USDB_CHAIN_GCMODE:-archive}
USDB_CHAIN_MINER_THREADS=${USDB_CHAIN_MINER_THREADS:-1}

POW_CALIBRATION_PROFILE=${POW_CALIBRATION_PROFILE:-}
POW_CALIBRATION_TARGET_BLOCK_SECONDS=${POW_CALIBRATION_TARGET_BLOCK_SECONDS:-13}
POW_CALIBRATION_SAMPLE_BLOCKS=${POW_CALIBRATION_SAMPLE_BLOCKS:-256}
POW_CALIBRATION_CONFIRMATIONS=${POW_CALIBRATION_CONFIRMATIONS:-6}
POW_CALIBRATION_DAG_WARMUP_BLOCKS=${POW_CALIBRATION_DAG_WARMUP_BLOCKS:-64}
POW_CALIBRATION_OUTPUT=${POW_CALIBRATION_OUTPUT:-"$USDB_CHAIN_WORK_DIR/pow-calibration.json"}
POW_CALIBRATION_SOURCE_COMMIT=${POW_CALIBRATION_SOURCE_COMMIT:-}
POW_CALIBRATION_SOURCE_DIRTY=${POW_CALIBRATION_SOURCE_DIRTY:-}
POW_CALIBRATION_BUILD_COMMAND=${POW_CALIBRATION_BUILD_COMMAND:-}
POW_CALIBRATION_MINER_HARDWARE=${POW_CALIBRATION_MINER_HARDWARE:-}
POW_CALIBRATION_GENESIS_DIFFICULTY=${POW_CALIBRATION_GENESIS_DIFFICULTY:-}
POW_CALIBRATION_MINIMUM_DIFFICULTY=${POW_CALIBRATION_MINIMUM_DIFFICULTY:-}
POW_CALIBRATION_ISOLATED_HARDWARE=${POW_CALIBRATION_ISOLATED_HARDWARE:-}
POW_CALIBRATION_ENVIRONMENT_NOTES=${POW_CALIBRATION_ENVIRONMENT_NOTES:-}

USDB_CHAIN_MINER_ADDRESS=${USDB_CHAIN_MINER_ADDRESS:-0x1111111111111111111111111111111111111111}
MINER_PASS_USDB_MAIN=${MINER_PASS_USDB_MAIN:-$USDB_CHAIN_MINER_ADDRESS}

VALIDATOR_DATADIR=${VALIDATOR_DATADIR:-"$USDB_CHAIN_WORK_DIR/validator"}
VALIDATOR_LOG_FILE=${VALIDATOR_LOG_FILE:-"$USDB_CHAIN_WORK_DIR/validator.log"}
VALIDATOR_HTTP_ADDR=${VALIDATOR_HTTP_ADDR:-127.0.0.1}
VALIDATOR_HTTP_PORT=${VALIDATOR_HTTP_PORT:-19546}
VALIDATOR_P2P_PORT=${VALIDATOR_P2P_PORT:-31314}
VALIDATOR_AUTHRPC_PORT=${VALIDATOR_AUTHRPC_PORT:-19552}

export REPO_ROOT="${USDB_REPO_DIR}"
export WORK_DIR="${E2E_WORK_DIR}/usdb"
export BITCOIN_DIR="${BITCOIN_DIR:-$WORK_DIR/bitcoin}"
export ORD_DATA_DIR="${ORD_DATA_DIR:-$WORK_DIR/ord}"
export BALANCE_HISTORY_ROOT="${BALANCE_HISTORY_ROOT:-$WORK_DIR/balance-history}"
export USDB_INDEXER_ROOT="${USDB_INDEXER_ROOT:-$WORK_DIR/usdb-indexer}"
export BTC_RPC_PORT="${BTC_RPC_PORT:-39932}"
export BTC_P2P_PORT="${BTC_P2P_PORT:-39933}"
export BH_RPC_PORT="${BH_RPC_PORT:-39910}"
export USDB_INDEXER_RPC_PORT="${USDB_INDEXER_RPC_PORT:-39920}"
export ORD_RPC_PORT="${ORD_RPC_PORT:-39930}"
export WALLET_NAME="${WALLET_NAME:-usdbprofile}"
export ORD_WALLET_NAME="${ORD_WALLET_NAME:-ord-usdb-profile-a}"
export ORD_WALLET_NAME_B="${ORD_WALLET_NAME_B:-ord-usdb-profile-b}"
export PREMINE_BLOCKS="${PREMINE_BLOCKS:-130}"
export FUND_CONFIRM_BLOCKS="${FUND_CONFIRM_BLOCKS:-2}"
export INSCRIBE_CONFIRM_BLOCKS="${INSCRIBE_CONFIRM_BLOCKS:-2}"
export SYNC_TIMEOUT_SEC="${SYNC_TIMEOUT_SEC:-300}"
export BALANCE_HISTORY_LOG_FILE="${BALANCE_HISTORY_LOG_FILE:-$WORK_DIR/balance-history.log}"
export USDB_INDEXER_LOG_FILE="${USDB_INDEXER_LOG_FILE:-$WORK_DIR/usdb-indexer.log}"
export ORD_SERVER_LOG_FILE="${ORD_SERVER_LOG_FILE:-$WORK_DIR/ord-server.log}"
export REGTEST_LOG_PREFIX="[usdb-profile-e2e/usdb]"

GETH_BIN=${GETH_BIN:-}
USDB_GO_TOOLCHAIN_MODE=${USDB_GO_TOOLCHAIN_MODE:-auto}
PRE_ACTIVATION_GETH_BIN=${PRE_ACTIVATION_GETH_BIN:-}
MID_ACTIVATION_GETH_BIN=${MID_ACTIVATION_GETH_BIN:-}
POST_ACTIVATION_GETH_BIN=${POST_ACTIVATION_GETH_BIN:-}

usdb_prepare_geth_binary GETH_BIN "$ROOT_DIR" "$E2E_WORK_DIR/bin/geth"
GETH_CMD=("$GETH_BIN")
if [[ -n "$PRE_ACTIVATION_GETH_BIN" ]]; then
  PRE_ACTIVATION_GETH_CMD=("$PRE_ACTIVATION_GETH_BIN")
else
  PRE_ACTIVATION_GETH_CMD=("${GETH_CMD[@]}")
fi
if [[ -n "$MID_ACTIVATION_GETH_BIN" ]]; then
  MID_ACTIVATION_GETH_CMD=("$MID_ACTIVATION_GETH_BIN")
else
  MID_ACTIVATION_GETH_CMD=("${GETH_CMD[@]}")
fi
if [[ -n "$POST_ACTIVATION_GETH_BIN" ]]; then
  POST_ACTIVATION_GETH_CMD=("$POST_ACTIVATION_GETH_BIN")
else
  POST_ACTIVATION_GETH_CMD=("${GETH_CMD[@]}")
fi

# shellcheck source=/dev/null
source "$USDB_REPO_DIR/src/btc/usdb-indexer/scripts/regtest_reorg_lib.sh"

run_geth() {
  (
    cd "$ROOT_DIR"
    "${GETH_CMD[@]}" "$@"
  )
}

usdb_chain_log() {
  echo "[usdb-profile-e2e/geth] $*"
}

usdb_chain_rpc_call() {
  local method="$1"
  local params="${2:-[]}"
  curl -s --connect-timeout 2 --max-time 8 \
    -X POST "http://${HTTP_ADDR}:${HTTP_PORT}" \
    -H 'content-type: application/json' \
    --data "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"${method}\",\"params\":${params}}"
}

usdb_chain_rpc_call_url() {
  local url="$1"
  local method="$2"
  local params="${3:-[]}"
  curl -s --connect-timeout 2 --max-time 8 \
    -X POST "$url" \
    -H 'content-type: application/json' \
    --data "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"${method}\",\"params\":${params}}"
}

usdb_chain_validator_required() {
  [[ "$INDEXER_OUTAGE_CHECK" == "1" ||
    "$ACTIVATION_FRESH_VALIDATOR_CHECK" == "1" ||
    "$ANCHOR_BOUNDARY_CHECK" == "1" ]]
}

usdb_chain_economic_conformance_enabled() {
  [[ -n "$ECONOMIC_CONFORMANCE_V2_BLOCK" && -n "$ECONOMIC_CONFORMANCE_V3_BLOCK" ]]
}

usdb_chain_wait_rpc_ready() {
  local expected_chain_id
  expected_chain_id="$(printf '0x%x' "$NETWORK_ID")"
  local deadline=$((SECONDS + RPC_WAIT_SECONDS))
  while (( SECONDS < deadline )); do
    local response
    response="$(usdb_chain_rpc_call "eth_chainId" "[]" || true)"
    if [[ "$response" == *"\"result\":\"${expected_chain_id}\""* ]]; then
      return 0
    fi
    sleep 1
  done
  echo "Timed out waiting for USDB-chain RPC at http://${HTTP_ADDR}:${HTTP_PORT}" >&2
  return 1
}

usdb_chain_wait_rpc_url_ready() {
  local url="$1"
  local expected_chain_id
  expected_chain_id="$(printf '0x%x' "$NETWORK_ID")"
  local deadline=$((SECONDS + RPC_WAIT_SECONDS))
  while (( SECONDS < deadline )); do
    local response
    response="$(usdb_chain_rpc_call_url "$url" "eth_chainId" "[]" || true)"
    if [[ "$response" == *"\"result\":\"${expected_chain_id}\""* ]]; then
      return 0
    fi
    sleep 1
  done
  echo "Timed out waiting for USDB-chain RPC at ${url}" >&2
  return 1
}

usdb_chain_wait_block_height() {
  local target_height="$1"
  local deadline=$((SECONDS + BLOCK_WAIT_SECONDS))
  while (( SECONDS < deadline )); do
    local response block_hex current_height
    response="$(usdb_chain_rpc_call "eth_blockNumber" "[]" || true)"
    block_hex="$(printf '%s' "$response" | python3 -c 'import json,sys; payload=json.load(sys.stdin); print(payload.get("result") or "0x0")' 2>/dev/null || echo 0x0)"
    current_height=$((block_hex))
    if (( current_height >= target_height )); then
      printf '%d\n' "$current_height"
      return 0
    fi
    sleep 0.2
  done
  echo "Timed out waiting for USDB block height >= ${target_height}" >&2
  return 1
}

usdb_chain_wait_block_height_url() {
  local url="$1"
  local target_height="$2"
  local deadline=$((SECONDS + BLOCK_WAIT_SECONDS))
  while (( SECONDS < deadline )); do
    local response block_hex current_height
    response="$(usdb_chain_rpc_call_url "$url" "eth_blockNumber" "[]" || true)"
    block_hex="$(printf '%s' "$response" | python3 -c 'import json,sys; payload=json.load(sys.stdin); print(payload.get("result") or "0x0")' 2>/dev/null || echo 0x0)"
    current_height=$((block_hex))
    if (( current_height >= target_height )); then
      printf '%d\n' "$current_height"
      return 0
    fi
    sleep 0.2
  done
  echo "Timed out waiting for USDB block height >= ${target_height} at ${url}" >&2
  return 1
}

usdb_chain_stop_mining() {
  usdb_chain_rpc_call "miner_stop" "[]" >/dev/null || true
}

usdb_chain_start_mining() {
  usdb_chain_rpc_call "miner_start" "[${USDB_CHAIN_MINER_THREADS}]" >/dev/null || true
}

usdb_chain_start_node() {
  local append_log="$1"
  shift
  local -a command=("$@")
  local max_peers=0
  local http_apis="eth,net,web3,admin,miner,txpool"
  if usdb_chain_validator_required; then
    max_peers=10
  fi
  if [[ "$ANCHOR_BOUNDARY_CHECK" == "1" ]]; then
    http_apis="${http_apis},debug"
  fi
  if [[ "$append_log" != "true" ]]; then
    : >"$GETH_LOG_FILE"
  fi
  (
    cd "$ROOT_DIR"
    exec "${command[@]}" \
      --datadir "$DATADIR" \
      --networkid "$NETWORK_ID" \
      --gcmode "$USDB_CHAIN_GCMODE" \
      --http \
      --http.addr "$HTTP_ADDR" \
      --http.port "$HTTP_PORT" \
      --http.api "$http_apis" \
      --authrpc.addr "$HTTP_ADDR" \
      --authrpc.port "$AUTHRPC_PORT" \
      --port "$P2P_PORT" \
      --nodiscover \
      --maxpeers "$max_peers" \
      --mine \
      --miner.threads "$USDB_CHAIN_MINER_THREADS" \
      --miner.etherbase "$USDB_CHAIN_MINER_ADDRESS" \
      --miner.usdb-indexer.rpcurl "http://127.0.0.1:${USDB_INDEXER_RPC_PORT}" \
      --miner.usdb.passid "$pass_id" \
      --miner.usdb-indexer.timeout "$USDB_QUERY_TIMEOUT" \
      --ethash.usdb-indexer.rpcurl "http://127.0.0.1:${USDB_INDEXER_RPC_PORT}" \
      --ethash.usdb-indexer.timeout "$USDB_QUERY_TIMEOUT"
  ) >>"$GETH_LOG_FILE" 2>&1 &
  GETH_PID=$!
}

run_pow_calibration() {
  local final_height="$1"
  if [[ -z "$POW_CALIBRATION_PROFILE" ]]; then
    return
  fi
  local required_height=$((POW_CALIBRATION_DAG_WARMUP_BLOCKS +
    POW_CALIBRATION_SAMPLE_BLOCKS +
    POW_CALIBRATION_CONFIRMATIONS))
  if ((final_height < required_height)); then
    echo "PoW calibration requires at least ${required_height} blocks, have ${final_height}" >&2
    return 1
  fi
  for value in \
    "$POW_CALIBRATION_SOURCE_COMMIT" \
    "$POW_CALIBRATION_SOURCE_DIRTY" \
    "$POW_CALIBRATION_BUILD_COMMAND" \
    "$POW_CALIBRATION_MINER_HARDWARE" \
    "$POW_CALIBRATION_GENESIS_DIFFICULTY" \
    "$POW_CALIBRATION_MINIMUM_DIFFICULTY" \
    "$POW_CALIBRATION_ISOLATED_HARDWARE" \
    "$POW_CALIBRATION_ENVIRONMENT_NOTES"; do
    if [[ -z "$value" ]]; then
      echo "PoW calibration measurement metadata is incomplete" >&2
      return 1
    fi
  done
  mkdir -p "$(dirname "$POW_CALIBRATION_OUTPUT")"
  usdb_chain_log "Collecting real-Ethash PoW calibration profile ${POW_CALIBRATION_PROFILE}"
  python3 "$ROOT_DIR/scripts/usdb/calibrate_pow_difficulty.py" \
    --rpc-url "http://${HTTP_ADDR}:${HTTP_PORT}" \
    --profile "$POW_CALIBRATION_PROFILE" \
    --target-block-seconds "$POW_CALIBRATION_TARGET_BLOCK_SECONDS" \
    --sample-blocks "$POW_CALIBRATION_SAMPLE_BLOCKS" \
    --confirmations "$POW_CALIBRATION_CONFIRMATIONS" \
    --expected-chain-id "$NETWORK_ID" \
    --source-commit "$POW_CALIBRATION_SOURCE_COMMIT" \
    --source-dirty "$POW_CALIBRATION_SOURCE_DIRTY" \
    --build-command "$POW_CALIBRATION_BUILD_COMMAND" \
    --miner-hardware "$POW_CALIBRATION_MINER_HARDWARE" \
    --miner-threads "$USDB_CHAIN_MINER_THREADS" \
    --dag-warmup-blocks "$POW_CALIBRATION_DAG_WARMUP_BLOCKS" \
    --genesis-difficulty "$POW_CALIBRATION_GENESIS_DIFFICULTY" \
    --minimum-difficulty "$POW_CALIBRATION_MINIMUM_DIFFICULTY" \
    --isolated-hardware "$POW_CALIBRATION_ISOLATED_HARDWARE" \
    --environment-notes "$POW_CALIBRATION_ENVIRONMENT_NOTES" \
    --output "$POW_CALIBRATION_OUTPUT"
  python3 "$ROOT_DIR/scripts/usdb/calibrate_pow_difficulty.py" \
    --input-report "$POW_CALIBRATION_OUTPUT" \
    --expected-chain-id "$NETWORK_ID" >/dev/null
  usdb_chain_log "PoW calibration report replayed successfully: ${POW_CALIBRATION_OUTPUT}"
}

usdb_chain_current_height() {
  usdb_chain_rpc_call "eth_blockNumber" "[]" |
    python3 -c 'import json,sys; print(int((json.load(sys.stdin).get("result") or "0x0"), 16))'
}

usdb_chain_height_at() {
  local url="$1"
  usdb_chain_rpc_call_url "$url" "eth_blockNumber" "[]" |
    python3 -c 'import json,sys; print(int((json.load(sys.stdin).get("result") or "0x0"), 16))'
}

usdb_chain_head_hash_at() {
  local url="$1"
  usdb_chain_rpc_call_url "$url" "eth_getBlockByNumber" "[\"latest\", false]" |
    python3 -c 'import json,sys; print(((json.load(sys.stdin).get("result") or {}).get("hash") or ""))'
}

usdb_chain_fetch_enode() {
  local url="$1"
  local deadline=$((SECONDS + RPC_WAIT_SECONDS))
  while (( SECONDS < deadline )); do
    local response enode
    response="$(usdb_chain_rpc_call_url "$url" "admin_nodeInfo" "[]" || true)"
    enode="$(printf '%s' "$response" | sed -n 's/.*"enode":"\([^"]*\)".*/\1/p')"
    if [[ -n "$enode" ]]; then
      printf '%s\n' "$enode"
      return 0
    fi
    sleep 1
  done
  echo "Timed out waiting for admin_nodeInfo at ${url}" >&2
  return 1
}

usdb_chain_start_validator() {
  local -a command=("${POST_ACTIVATION_GETH_CMD[@]}")
  : >"$VALIDATOR_LOG_FILE"
  (
    cd "$ROOT_DIR"
    exec "${command[@]}" \
      --datadir "$VALIDATOR_DATADIR" \
      --networkid "$NETWORK_ID" \
      --gcmode "$USDB_CHAIN_GCMODE" \
      --http \
      --http.addr "$VALIDATOR_HTTP_ADDR" \
      --http.port "$VALIDATOR_HTTP_PORT" \
      --http.api eth,net,web3,admin \
      --authrpc.addr "$VALIDATOR_HTTP_ADDR" \
      --authrpc.port "$VALIDATOR_AUTHRPC_PORT" \
      --port "$VALIDATOR_P2P_PORT" \
      --nodiscover \
      --maxpeers 10 \
      --ethash.usdb-indexer.rpcurl "http://127.0.0.1:${USDB_INDEXER_RPC_PORT}" \
      --ethash.usdb-indexer.timeout "$USDB_QUERY_TIMEOUT"
  ) >"$VALIDATOR_LOG_FILE" 2>&1 &
  VALIDATOR_PID=$!
}

usdb_chain_stop_validator() {
  if [[ -n "${VALIDATOR_PID:-}" ]] && kill -0 "$VALIDATOR_PID" 2>/dev/null; then
    regtest_stop_process "$VALIDATOR_PID"
  fi
  VALIDATOR_PID=""
}

usdb_chain_connect_validator() {
  local node_rpc="http://${HTTP_ADDR}:${HTTP_PORT}"
  local validator_rpc="http://${VALIDATOR_HTTP_ADDR}:${VALIDATOR_HTTP_PORT}"
  local node_enode
  node_enode="$(usdb_chain_fetch_enode "$node_rpc")"
  usdb_chain_rpc_call_url "$validator_rpc" "admin_addPeer" "[\"${node_enode}\"]" >/dev/null
}

usdb_chain_assert_validator_synced() {
  local expected_height="$1"
  local node_rpc="http://${HTTP_ADDR}:${HTTP_PORT}"
  local validator_rpc="http://${VALIDATOR_HTTP_ADDR}:${VALIDATOR_HTTP_PORT}"
  local validator_height node_hash validator_hash
  validator_height="$(usdb_chain_wait_block_height_url "$validator_rpc" "$expected_height")"
  node_hash="$(usdb_chain_head_hash_at "$node_rpc")"
  validator_hash="$(usdb_chain_head_hash_at "$validator_rpc")"
  if (( validator_height != expected_height )); then
    echo "Validator reached unexpected height: have ${validator_height}, want ${expected_height}" >&2
    return 1
  fi
  if [[ "$validator_hash" != "$node_hash" ]]; then
    echo "Validator reached unexpected head: have ${validator_hash}, want ${node_hash}" >&2
    return 1
  fi
}

usdb_chain_wait_log_pattern() {
  local pattern="$1"
  local deadline=$((SECONDS + RPC_WAIT_SECONDS))
  while (( SECONDS < deadline )); do
    if [[ -f "$GETH_LOG_FILE" ]] && grep -Fq "$pattern" "$GETH_LOG_FILE"; then
      return 0
    fi
    sleep 0.2
  done
  echo "Timed out waiting for geth log pattern: ${pattern}" >&2
  return 1
}

usdb_chain_block_summary() {
  local block_height="$1"
  local response
  response="$(usdb_chain_rpc_call "eth_getBlockByNumber" "[$(printf '\"0x%x\"' "$block_height"), false]")"
  printf '%s' "$response" | python3 -c '
import json
import sys

envelope = json.load(sys.stdin)
if envelope.get("error"):
    raise SystemExit("eth_getBlockByNumber failed: " + json.dumps(envelope["error"], sort_keys=True))
block = envelope.get("result")
if block is None:
    raise SystemExit("missing requested USDB block")
extra = bytes.fromhex((block.get("extraData") or "0x")[2:])
if len(extra) != 111:
    raise SystemExit(f"unexpected USDB selector size: {len(extra)}")
btc_height = int.from_bytes(extra[3:7], "big")
anchor_age = int.from_bytes(extra[7:11], "big")
timestamp = int(block["timestamp"], 16)
print("|".join([
    block["hash"],
    block["parentHash"],
    str(timestamp),
    str(btc_height),
    str(anchor_age),
]))'
}

usdb_chain_assert_anchor_block() {
  local block_height="$1"
  local expected_btc_height="$2"
  local expected_anchor_age="$3"
  local summary block_hash parent_hash timestamp btc_height anchor_age

  summary="$(usdb_chain_block_summary "$block_height")"
  IFS='|' read -r block_hash parent_hash timestamp btc_height anchor_age <<<"$summary"
  if [[ "$btc_height" != "$expected_btc_height" || "$anchor_age" != "$expected_anchor_age" ]]; then
    echo "Unexpected anchor selector at USDB block ${block_height}: btc_height=${btc_height}, age=${anchor_age}, want btc_height=${expected_btc_height}, age=${expected_anchor_age}" >&2
    return 1
  fi
  ANCHOR_ASSERTED_BLOCK_HASH="$block_hash"
  ANCHOR_ASSERTED_BLOCK_TIMESTAMP="$timestamp"
  usdb_chain_log "Anchor block ${block_height}: btc_height=${btc_height}, age=${anchor_age}, hash=${block_hash}, parent=${parent_hash}"
}

usdb_chain_wait_exact_height() {
  local expected_height="$1"
  local actual_height
  actual_height="$(usdb_chain_wait_block_height "$expected_height")"
  if (( actual_height != expected_height )); then
    echo "USDB chain crossed anchor boundary: have ${actual_height}, want ${expected_height}" >&2
    return 1
  fi
}

usdb_chain_assert_height_stalled() {
  local expected_height="$1"
  local before_height after_height

  before_height="$(usdb_chain_current_height)"
  if (( before_height != expected_height )); then
    echo "USDB chain was not at the expected anchor boundary: have ${before_height}, want ${expected_height}" >&2
    return 1
  fi
  sleep "$ANCHOR_BOUNDARY_OBSERVE_SECONDS"
  after_height="$(usdb_chain_current_height)"
  if (( after_height != expected_height )); then
    echo "USDB chain mined max+1 with a stale BTC anchor: ${before_height} -> ${after_height}" >&2
    return 1
  fi
  usdb_chain_log "USDB mining remained stopped at height=${expected_height} for ${ANCHOR_BOUNDARY_OBSERVE_SECONDS}s"
}

usdb_chain_advance_btc_stable_height() {
  local mining_address="$1"
  local expected_context_height="$2"
  local system_state_resp

  regtest_log "Mining one BTC block to advance the stable context to height=${expected_context_height}"
  regtest_mine_blocks 1 "$mining_address"
  regtest_wait_until_ord_server_synced_to_bitcoind
  regtest_wait_until_balance_history_synced_eq "$expected_context_height"
  regtest_wait_balance_history_consensus_ready
  regtest_wait_until_usdb_synced_eq "$expected_context_height"
  regtest_wait_usdb_consensus_ready

  system_state_resp="$(regtest_rpc_call_usdb_indexer "get_system_state_info" "[]")"
  regtest_assert_json_expr "$system_state_resp" "data.get('error') is None" "True"
  regtest_assert_json_expr \
    "$system_state_resp" \
    "(data.get('result') or {}).get('local_synced_block_height')" \
    "$expected_context_height"
}

usdb_chain_set_head() {
  local block_height="$1"
  local response
  response="$(usdb_chain_rpc_call "debug_setHead" "[$(printf '\"0x%x\"' "$block_height")]")"
  printf '%s' "$response" | python3 -c '
import json
import sys

envelope = json.load(sys.stdin)
if envelope.get("error"):
    raise SystemExit("debug_setHead failed: " + json.dumps(envelope["error"], sort_keys=True))
if "result" not in envelope:
    raise SystemExit("debug_setHead response is missing result")'
}

usdb_chain_wait_after_timestamp() {
  local minimum_timestamp="$1"
  local deadline=$((SECONDS + RPC_WAIT_SECONDS))
  local now

  while (( SECONDS < deadline )); do
    now="$(date +%s)"
    if (( now > minimum_timestamp )); then
      return 0
    fi
    sleep 1
  done
  echo "Timed out waiting for wall clock to pass USDB branch timestamp ${minimum_timestamp}" >&2
  return 1
}

run_anchor_boundary_check() {
  local mining_address="$1"
  local initial_btc_height="$2"
  local max_age="$BTC_ANCHOR_MAX_AGE_BLOCKS"
  local first_segment_end=$((max_age + 1))
  local second_segment_start=$((first_segment_end + 1))
  local second_segment_end=$((second_segment_start + max_age))
  local replacement_first_height=$((second_segment_start + 1))
  local advanced_btc_height=$((initial_btc_height + 1))
  local old_branch_hash new_branch_hash rewind_parent_timestamp rewound_height
  local validator_rpc="http://${VALIDATOR_HTTP_ADDR}:${VALIDATOR_HTTP_PORT}"

  usdb_chain_log "Waiting for exact max-age boundary: max=${max_age}, expected_head=${first_segment_end}"
  usdb_chain_wait_exact_height "$first_segment_end"
  usdb_chain_assert_anchor_block 1 "$initial_btc_height" 0
  usdb_chain_assert_anchor_block "$first_segment_end" "$initial_btc_height" "$max_age"
  usdb_chain_assert_height_stalled "$first_segment_end"

  usdb_chain_advance_btc_stable_height "$mining_address" "$advanced_btc_height"
  usdb_chain_wait_exact_height "$second_segment_end"
  usdb_chain_assert_anchor_block "$second_segment_start" "$advanced_btc_height" 0
  usdb_chain_assert_anchor_block "$replacement_first_height" "$advanced_btc_height" 1
  old_branch_hash="$ANCHOR_ASSERTED_BLOCK_HASH"
  usdb_chain_assert_anchor_block "$second_segment_end" "$advanced_btc_height" "$max_age"
  usdb_chain_assert_height_stalled "$second_segment_end"

  usdb_chain_log "Rewinding USDB canonical head to block ${second_segment_start} for branch replacement"
  usdb_chain_stop_mining
  sleep 1
  usdb_chain_set_head "$second_segment_start"
  rewound_height="$(usdb_chain_current_height)"
  if (( rewound_height != second_segment_start )); then
    echo "debug_setHead did not rewind to ${second_segment_start}: have ${rewound_height}" >&2
    return 1
  fi
  usdb_chain_assert_anchor_block "$second_segment_start" "$advanced_btc_height" 0
  rewind_parent_timestamp="$ANCHOR_ASSERTED_BLOCK_TIMESTAMP"
  usdb_chain_wait_after_timestamp "$((rewind_parent_timestamp + 1))"

  usdb_chain_start_mining
  usdb_chain_wait_exact_height "$second_segment_end"
  usdb_chain_assert_anchor_block "$replacement_first_height" "$advanced_btc_height" 1
  new_branch_hash="$ANCHOR_ASSERTED_BLOCK_HASH"
  if [[ "$new_branch_hash" == "$old_branch_hash" ]]; then
    echo "USDB branch replacement reproduced the original block hash ${old_branch_hash}" >&2
    return 1
  fi
  usdb_chain_assert_anchor_block "$second_segment_end" "$advanced_btc_height" "$max_age"
  usdb_chain_assert_height_stalled "$second_segment_end"

  usdb_chain_log "Starting fresh validator against the replacement USDB branch"
  usdb_chain_start_validator
  usdb_chain_wait_rpc_url_ready "$validator_rpc"
  usdb_chain_connect_validator
  usdb_chain_assert_validator_synced "$second_segment_end"
  usdb_chain_stop_validator

  ANCHOR_BOUNDARY_FINAL_HEIGHT="$second_segment_end"
  usdb_chain_log "Anchor boundary check succeeded: old_branch=${old_branch_hash}, replacement_branch=${new_branch_hash}"
}

usdb_chain_stop_residual_nodes() {
  while IFS= read -r pid; do
    if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
      usdb_chain_log "Stopping residual geth process pid=${pid} for datadir=${DATADIR}, http_port=${HTTP_PORT}"
      regtest_stop_process "$pid"
    fi
  done < <(
    ps -eo pid=,args= | awk -v datadir="$DATADIR" -v http_port="$HTTP_PORT" -v p2p_port="$P2P_PORT" '
      index($0, " --datadir " datadir) && index($0, " --http.port " http_port) && index($0, " --port " p2p_port) {
        print $1
      }
    '
  )
}

usdb_chain_stop_residual_validator() {
  while IFS= read -r pid; do
    if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
      usdb_chain_log "Stopping residual validator pid=${pid} for datadir=${VALIDATOR_DATADIR}"
      regtest_stop_process "$pid"
    fi
  done < <(
    ps -eo pid=,args= | awk -v datadir="$VALIDATOR_DATADIR" -v http_port="$VALIDATOR_HTTP_PORT" -v p2p_port="$VALIDATOR_P2P_PORT" '
      index($0, " --datadir " datadir) && index($0, " --http.port " http_port) && index($0, " --port " p2p_port) {
        print $1
      }
    '
  )
}

eth_print_failure_diagnostics() {
  if [[ -f "$GETH_LOG_FILE" ]]; then
    usdb_chain_log "---- geth log (tail -n 120) ----"
    tail -n 120 "$GETH_LOG_FILE" || true
    usdb_chain_log "---- end geth log ----"
  fi
  if [[ -f "$VALIDATOR_LOG_FILE" ]]; then
    usdb_chain_log "---- validator log (tail -n 120) ----"
    tail -n 120 "$VALIDATOR_LOG_FILE" || true
    usdb_chain_log "---- end validator log ----"
  fi
}

cleanup() {
  local exit_code=$?
  set +e
  if [[ -n "${GETH_PID:-}" ]] && kill -0 "$GETH_PID" 2>/dev/null; then
    regtest_stop_process "$GETH_PID"
  fi
  usdb_chain_stop_validator
  usdb_chain_stop_residual_nodes
  usdb_chain_stop_residual_validator
  if [[ "$exit_code" -ne 0 ]]; then
    eth_print_failure_diagnostics
  fi
  regtest_cleanup
}

verify_profile_blocks() {
  local blocks_file="$1"
  local coinbase="$2"
  local balance_hex="$3"
  local expected_pass_id="$4"

  local -a activation_args=()
  if [[ -n "$ACTIVATION_CONFORMANCE_BLOCK" ]]; then
    activation_args+=(--activation-conformance-block "$ACTIVATION_CONFORMANCE_BLOCK")
  fi
  if usdb_chain_economic_conformance_enabled; then
    activation_args+=(
      --economic-conformance-v2-block "$ECONOMIC_CONFORMANCE_V2_BLOCK"
      --economic-conformance-v3-block "$ECONOMIC_CONFORMANCE_V3_BLOCK"
    )
  fi

  python3 "$ROOT_DIR/scripts/usdb/verify_usdb_profile_e2e.py" \
    --blocks "$blocks_file" \
    --coinbase "$coinbase" \
    --balance-hex "$balance_hex" \
    --usdb-chain-rpc-url "http://${HTTP_ADDR}:${HTTP_PORT}" \
    --usdb-indexer-rpc-url "http://127.0.0.1:${USDB_INDEXER_RPC_PORT}" \
    --expected-pass-id "$expected_pass_id" \
    --btc-anchor-max-age-blocks "$BTC_ANCHOR_MAX_AGE_BLOCKS" \
    "${activation_args[@]}"
}

run_indexer_outage_recovery_check() {
  local validator_rpc="http://${VALIDATOR_HTTP_ADDR}:${VALIDATOR_HTTP_PORT}"
  local stalled_height observed_height validator_height resumed_height

  usdb_chain_log "Stopping mining before the usdb-indexer outage checkpoint"
  usdb_chain_stop_mining
  sleep 1

  regtest_log "Stopping usdb-indexer to verify miner and fresh-validator fail-closed behavior"
  regtest_stop_usdb_indexer
  sleep 1

  usdb_chain_log "Starting a fresh validator while usdb-indexer is unavailable"
  usdb_chain_start_validator
  usdb_chain_wait_rpc_url_ready "$validator_rpc"
  usdb_chain_connect_validator

  usdb_chain_log "Restarting mining while usdb-indexer is unavailable"
  usdb_chain_start_mining
  sleep 2
  stalled_height="$(usdb_chain_current_height)"
  sleep "$OUTAGE_OBSERVE_SECONDS"
  observed_height="$(usdb_chain_current_height)"
  if (( observed_height != stalled_height )); then
    echo "USDB miner advanced while usdb-indexer was unavailable: ${stalled_height} -> ${observed_height}" >&2
    return 1
  fi
  validator_height="$(usdb_chain_height_at "$validator_rpc")"
  if (( validator_height != 0 )); then
    echo "Fresh validator imported blocks without usdb-indexer: height=${validator_height}" >&2
    return 1
  fi

  regtest_log "Restarting usdb-indexer and verifying mining and fresh-validator recovery"
  regtest_start_usdb_indexer
  regtest_wait_usdb_rpc_ready
  regtest_wait_usdb_consensus_ready
  usdb_chain_connect_validator
  usdb_chain_stop_mining
  usdb_chain_start_mining
  resumed_height="$(usdb_chain_wait_block_height "$((stalled_height + 2))")"
  usdb_chain_stop_mining
  sleep 1
  resumed_height="$(usdb_chain_current_height)"
  usdb_chain_connect_validator
  usdb_chain_assert_validator_synced "$resumed_height"
  usdb_chain_stop_validator

  OUTAGE_RECOVERY_HEIGHT="$resumed_height"
  usdb_chain_log "usdb-indexer outage check succeeded at stalled_height=${stalled_height}, resumed_height=${resumed_height}"
}

run_activation_fresh_validator_check() {
  local expected_height="$1"
  local validator_rpc="http://${VALIDATOR_HTTP_ADDR}:${VALIDATOR_HTTP_PORT}"

  usdb_chain_log "Starting an independent tagged validator across the activation boundary"
  usdb_chain_start_validator
  usdb_chain_wait_rpc_url_ready "$validator_rpc"
  usdb_chain_connect_validator
  usdb_chain_assert_validator_synced "$expected_height"
  usdb_chain_stop_validator
  usdb_chain_log "Independent validator accepted the activation-spanning head at height=${expected_height}"
}

run_selector_tamper_import_case() {
  local canonical_fixture="$1"
  local helper_bin="$2"
  local field="$3"
  local expected_pattern="$4"
  local case_dir="$USDB_CHAIN_WORK_DIR/import-${field}"
  local fixture="$USDB_CHAIN_WORK_DIR/block-1-${field}.rlp"
  local log_file="$USDB_CHAIN_WORK_DIR/import-${field}.log"

  "$helper_bin" --input "$canonical_fixture" --output "$fixture" --field "$field"
  rm -rf "$case_dir"
  mkdir -p "$case_dir"
  run_geth init --datadir "$case_dir" "$GENESIS_JSON" >/dev/null
  if run_geth \
    --datadir "$case_dir" \
    --nocompaction \
    import \
    --ethash.usdb-indexer.rpcurl "http://127.0.0.1:${USDB_INDEXER_RPC_PORT}" \
    --ethash.usdb-indexer.timeout "$USDB_QUERY_TIMEOUT" \
    "$fixture" >"$log_file" 2>&1; then
    echo "Tampered selector field ${field} unexpectedly imported" >&2
    return 1
  fi
  if ! grep -Fq "$expected_pattern" "$log_file"; then
    echo "Tampered selector field ${field} failed for an unexpected reason; wanted pattern ${expected_pattern}" >&2
    tail -n 80 "$log_file" >&2 || true
    return 1
  fi
  usdb_chain_log "Rejected tampered selector field=${field}, reason=${expected_pattern}"
}

run_selector_tamper_import_matrix() {
  local canonical_fixture="$USDB_CHAIN_WORK_DIR/block-1-canonical.rlp"
  local helper_bin="$USDB_CHAIN_WORK_DIR/tamper-usdb-block-fixture"
  local control_dir="$USDB_CHAIN_WORK_DIR/import-control"
  local control_log="$USDB_CHAIN_WORK_DIR/import-control.log"

  usdb_chain_log "Stopping the source node and exporting canonical USDB block 1"
  if [[ -n "${GETH_PID:-}" ]] && kill -0 "$GETH_PID" 2>/dev/null; then
    regtest_stop_process "$GETH_PID"
  fi
  GETH_PID=""
  usdb_chain_stop_validator
  usdb_chain_stop_residual_nodes
  usdb_chain_stop_residual_validator
  rm -f "$canonical_fixture"
  run_geth \
    --datadir "$DATADIR" \
    export \
    --ethash.usdb-indexer.rpcurl "http://127.0.0.1:${USDB_INDEXER_RPC_PORT}" \
    --ethash.usdb-indexer.timeout "$USDB_QUERY_TIMEOUT" \
    "$canonical_fixture" 1 1 >/dev/null

  (
    cd "$ROOT_DIR"
    usdb_go build -o "$helper_bin" ./scripts/usdb
  )

  usdb_chain_log "Importing the unmodified fixture as a control"
  rm -rf "$control_dir"
  mkdir -p "$control_dir"
  run_geth init --datadir "$control_dir" "$GENESIS_JSON" >/dev/null
  if ! run_geth \
    --datadir "$control_dir" \
    --nocompaction \
    import \
    --ethash.usdb-indexer.rpcurl "http://127.0.0.1:${USDB_INDEXER_RPC_PORT}" \
    --ethash.usdb-indexer.timeout "$USDB_QUERY_TIMEOUT" \
    "$canonical_fixture" >"$control_log" 2>&1; then
    echo "Canonical selector fixture failed to import" >&2
    tail -n 80 "$control_log" >&2 || true
    return 1
  fi

  run_selector_tamper_import_case "$canonical_fixture" "$helper_bin" \
    "payload_version" "usdb profile selector version mismatch"
  run_selector_tamper_import_case "$canonical_fixture" "$helper_bin" \
    "difficulty_policy_version" "usdb difficulty policy version mismatch"
  run_selector_tamper_import_case "$canonical_fixture" "$helper_bin" \
    "btc_height" "SNAPSHOT_ID_MISMATCH"
  run_selector_tamper_import_case "$canonical_fixture" "$helper_bin" \
    "btc_anchor_age_blocks" "usdb BTC anchor age mismatch"
  run_selector_tamper_import_case "$canonical_fixture" "$helper_bin" \
    "snapshot_id" "SNAPSHOT_ID_MISMATCH"
  run_selector_tamper_import_case "$canonical_fixture" "$helper_bin" \
    "system_state_id" "SYSTEM_STATE_ID_MISMATCH"
  run_selector_tamper_import_case "$canonical_fixture" "$helper_bin" \
    "pass_id" "PASS_NOT_FOUND"
  usdb_chain_log "Selector tamper import matrix succeeded"
}

main() {
  trap cleanup EXIT

  regtest_resolve_bitcoin_binaries
  regtest_require_cmd cargo
  regtest_require_cmd curl
  regtest_require_cmd python3
  if [[ -n "$ACTIVATION_CONFORMANCE_BLOCK" ]]; then
    if (( ACTIVATION_CONFORMANCE_BLOCK < 2 )); then
      echo "ACTIVATION_CONFORMANCE_BLOCK must be at least 2" >&2
      exit 1
    fi
    if (( TARGET_BLOCKS <= ACTIVATION_CONFORMANCE_BLOCK )); then
      echo "TARGET_BLOCKS must be after ACTIVATION_CONFORMANCE_BLOCK" >&2
      exit 1
    fi
  fi
  if [[ -n "$ECONOMIC_CONFORMANCE_V2_BLOCK" || -n "$ECONOMIC_CONFORMANCE_V3_BLOCK" ]]; then
    if ! usdb_chain_economic_conformance_enabled; then
      echo "ECONOMIC_CONFORMANCE_V2_BLOCK and ECONOMIC_CONFORMANCE_V3_BLOCK must be set together" >&2
      exit 1
    fi
    if (( ECONOMIC_CONFORMANCE_V2_BLOCK < 2 )); then
      echo "ECONOMIC_CONFORMANCE_V2_BLOCK must be at least 2" >&2
      exit 1
    fi
    if (( ECONOMIC_CONFORMANCE_V3_BLOCK <= ECONOMIC_CONFORMANCE_V2_BLOCK )); then
      echo "ECONOMIC_CONFORMANCE_V3_BLOCK must follow ECONOMIC_CONFORMANCE_V2_BLOCK" >&2
      exit 1
    fi
    if (( TARGET_BLOCKS <= ECONOMIC_CONFORMANCE_V3_BLOCK )); then
      echo "TARGET_BLOCKS must be after ECONOMIC_CONFORMANCE_V3_BLOCK" >&2
      exit 1
    fi
    if [[ -n "$ACTIVATION_CONFORMANCE_BLOCK" ]]; then
      echo "difficulty and economic activation conformance modes cannot be combined" >&2
      exit 1
    fi
    if [[ -z "$MID_ACTIVATION_GETH_BIN" || -z "$POST_ACTIVATION_GETH_BIN" ]]; then
      echo "economic activation conformance requires MID_ACTIVATION_GETH_BIN and POST_ACTIVATION_GETH_BIN" >&2
      exit 1
    fi
  fi
  for check_name in \
    INDEXER_OUTAGE_CHECK \
    SELECTOR_TAMPER_CHECK \
    ACTIVATION_FRESH_VALIDATOR_CHECK \
    ANCHOR_BOUNDARY_CHECK; do
    if [[ "${!check_name}" != "0" && "${!check_name}" != "1" ]]; then
      echo "${check_name} must be 0 or 1" >&2
      exit 1
    fi
  done
  if [[ ! "$BTC_ANCHOR_MAX_AGE_BLOCKS" =~ ^[0-9]+$ ]] ||
    (( BTC_ANCHOR_MAX_AGE_BLOCKS == 0 || BTC_ANCHOR_MAX_AGE_BLOCKS > 4294967295 )); then
    echo "BTC_ANCHOR_MAX_AGE_BLOCKS must fit one positive uint32" >&2
    exit 1
  fi
  if [[ ! "$ANCHOR_BOUNDARY_OBSERVE_SECONDS" =~ ^[0-9]+$ ]] ||
    (( ANCHOR_BOUNDARY_OBSERVE_SECONDS <= 0 )); then
    echo "ANCHOR_BOUNDARY_OBSERVE_SECONDS must be positive" >&2
    exit 1
  fi
  if [[ "$ANCHOR_BOUNDARY_CHECK" == "1" ]]; then
    if (( BTC_ANCHOR_MAX_AGE_BLOCKS < 2 )); then
      echo "ANCHOR_BOUNDARY_CHECK requires BTC_ANCHOR_MAX_AGE_BLOCKS >= 2" >&2
      exit 1
    fi
    if [[ "$INDEXER_OUTAGE_CHECK" == "1" ||
      "$SELECTOR_TAMPER_CHECK" == "1" ||
      "$ACTIVATION_FRESH_VALIDATOR_CHECK" == "1" ||
      -n "$ACTIVATION_CONFORMANCE_BLOCK" ||
      -n "$ECONOMIC_CONFORMANCE_V2_BLOCK" ||
      -n "$ECONOMIC_CONFORMANCE_V3_BLOCK" ||
      -n "$POW_CALIBRATION_PROFILE" ]]; then
      echo "ANCHOR_BOUNDARY_CHECK must run without other conformance/failure/calibration modes" >&2
      exit 1
    fi
  fi
  if [[ "$INDEXER_OUTAGE_CHECK" == "1" ]] &&
    { [[ -n "$ACTIVATION_CONFORMANCE_BLOCK" ]] || usdb_chain_economic_conformance_enabled; }; then
    echo "INDEXER_OUTAGE_CHECK cannot be combined with activation conformance" >&2
    exit 1
  fi
  if [[ "$ACTIVATION_FRESH_VALIDATOR_CHECK" == "1" ]] &&
    [[ -z "$ACTIVATION_CONFORMANCE_BLOCK" ]] &&
    ! usdb_chain_economic_conformance_enabled; then
    echo "ACTIVATION_FRESH_VALIDATOR_CHECK requires an activation conformance mode" >&2
    exit 1
  fi
  regtest_assert_ord_server_port_available
  if [[ ! -x "$ORD_BIN" ]]; then
    echo "Missing required ORD_BIN executable: $ORD_BIN" >&2
    exit 1
  fi

  regtest_ensure_workspace_dirs
  mkdir -p "$USDB_CHAIN_WORK_DIR"
  usdb_chain_stop_residual_nodes
  usdb_chain_stop_residual_validator
  rm -rf "$DATADIR"
  mkdir -p "$DATADIR"
  if usdb_chain_validator_required; then
    rm -rf "$VALIDATOR_DATADIR"
    mkdir -p "$VALIDATOR_DATADIR"
  fi

  regtest_start_bitcoind
  regtest_ensure_wallet

  local miner_btc_address ord_receive_address mint_content_file pass_id
  local current_btc_tip_height current_context_height
  local snapshot_info_resp system_state_resp pass_profile_resp
  local final_block_height balance_resp blocks_file latest_balance_hex current_energy

  miner_btc_address="$(regtest_get_new_address)"
  regtest_log "Premining ${PREMINE_BLOCKS} BTC blocks to address=${miner_btc_address}"
  regtest_mine_blocks "$PREMINE_BLOCKS" "$miner_btc_address"

  regtest_start_ord_server
  regtest_wait_until_ord_server_synced_to_bitcoind
  regtest_prepare_ord_wallets

  ord_receive_address="$(regtest_get_ord_wallet_receive_address "$ORD_WALLET_NAME")"
  regtest_fund_address "$ord_receive_address" "$FUND_ORD_AMOUNT_BTC"
  regtest_mine_blocks "$FUND_CONFIRM_BLOCKS" "$miner_btc_address"
  regtest_wait_until_ord_server_synced_to_bitcoind

  mint_content_file="$WORK_DIR/usdb_profile_mint.json"
  cat >"$mint_content_file" <<EOF
{"p":"usdb","op":"mint","v":1,"usdb_main":"${MINER_PASS_USDB_MAIN}","prev":[]}
EOF

  pass_id="$(regtest_ord_inscribe_file "$ORD_WALLET_NAME" "$mint_content_file" "$ord_receive_address")"
  regtest_mine_blocks "$INSCRIBE_CONFIRM_BLOCKS" "$miner_btc_address"
  if (( BTC_STABLE_LAG_BLOCKS > 0 )); then
    regtest_log "Mining ${BTC_STABLE_LAG_BLOCKS} blocks so the mint reaches the stable frontier"
    regtest_mine_blocks "$BTC_STABLE_LAG_BLOCKS" "$miner_btc_address"
  fi
  regtest_wait_until_ord_server_synced_to_bitcoind
  current_btc_tip_height="$("$BITCOIN_CLI_BIN" -regtest -datadir="$BITCOIN_DIR" -rpcport="$BTC_RPC_PORT" getblockcount)"
  current_context_height=$((current_btc_tip_height - BTC_STABLE_LAG_BLOCKS))

  regtest_create_balance_history_config
  regtest_create_usdb_indexer_config
  regtest_start_balance_history
  regtest_wait_balance_history_rpc_ready
  regtest_wait_until_balance_history_synced_eq "$current_context_height"
  regtest_wait_balance_history_consensus_ready
  snapshot_info_resp="$(regtest_rpc_call_balance_history "get_snapshot_info" "[]")"
  regtest_assert_json_expr "$snapshot_info_resp" "(data.get('result') or {}).get('stable_lag')" "$BTC_STABLE_LAG_BLOCKS"

  regtest_start_usdb_indexer
  regtest_wait_usdb_rpc_ready
  regtest_wait_until_usdb_synced_eq "$current_context_height"
  regtest_wait_usdb_consensus_ready

  system_state_resp="$(regtest_rpc_call_usdb_indexer "get_system_state_info" "[]")"
  regtest_assert_json_expr "$system_state_resp" "data.get('error') is None" "True"
  pass_profile_resp="$(regtest_get_pass_economic_profile_response "$pass_id" "$current_context_height")"
  regtest_assert_json_expr "$pass_profile_resp" "data.get('error') is None" "True"
  regtest_assert_json_expr "$pass_profile_resp" "(data.get('result') or {}).get('pass', {}).get('pass_id')" "$pass_id"
  regtest_assert_json_expr "$pass_profile_resp" "(data.get('result') or {}).get('pass', {}).get('state')" "active"
  regtest_assert_json_expr "$pass_profile_resp" "(data.get('result') or {}).get('pass', {}).get('pass_kind')" "standard"
  regtest_assert_json_expr "$pass_profile_resp" "(data.get('result') or {}).get('external_state', {}).get('stable_lag')" "$BTC_STABLE_LAG_BLOCKS"
  current_energy="$(regtest_json_expr "$pass_profile_resp" "(data.get('result') or {}).get('pass', {}).get('raw_energy')")"

  # Fresh ord mint flows may leave the owner at a zero-energy floor at the mint
  # height. Fund the same address once more and mine a few growth blocks so the
  # first USDB-chain smoke gets a best-effort retry toward a positive level so the
  # real-difficulty path exercises a non-default factor when possible.
  if [[ "$current_energy" == "0" ]]; then
    regtest_log "Current pass energy is zero; funding owner address ${ord_receive_address} with ${ENERGY_TOPUP_AMOUNT_BTC} BTC"
    regtest_fund_address "$ord_receive_address" "$ENERGY_TOPUP_AMOUNT_BTC"
    regtest_mine_blocks 1 "$miner_btc_address"
    if (( ENERGY_GROWTH_BLOCKS > 0 )); then
      regtest_mine_blocks "$ENERGY_GROWTH_BLOCKS" "$miner_btc_address"
    fi
    if (( BTC_STABLE_LAG_BLOCKS > 0 )); then
      regtest_log "Mining ${BTC_STABLE_LAG_BLOCKS} blocks so the top-up reaches the stable frontier"
      regtest_mine_blocks "$BTC_STABLE_LAG_BLOCKS" "$miner_btc_address"
    fi
    regtest_wait_until_ord_server_synced_to_bitcoind
    current_btc_tip_height="$("$BITCOIN_CLI_BIN" -regtest -datadir="$BITCOIN_DIR" -rpcport="$BTC_RPC_PORT" getblockcount)"
    current_context_height=$((current_btc_tip_height - BTC_STABLE_LAG_BLOCKS))
    regtest_wait_until_balance_history_synced_eq "$current_context_height"
    regtest_wait_until_usdb_synced_eq "$current_context_height"
    regtest_wait_balance_history_consensus_ready
    regtest_wait_usdb_consensus_ready

    system_state_resp="$(regtest_rpc_call_usdb_indexer "get_system_state_info" "[]")"
    regtest_assert_json_expr "$system_state_resp" "data.get('error') is None" "True"
    pass_profile_resp="$(regtest_get_pass_economic_profile_response "$pass_id" "$current_context_height")"
    regtest_assert_json_expr "$pass_profile_resp" "data.get('error') is None" "True"
    current_energy="$(regtest_json_expr "$pass_profile_resp" "(data.get('result') or {}).get('pass', {}).get('raw_energy')")"
  fi
  if [[ "$current_energy" == "0" ]]; then
    usdb_chain_log "Pass energy is still zero after retry; proceeding with difficulty factor 10000"
  fi

  usdb_chain_log "Using pass_id=${pass_id}"
  usdb_chain_log "Current USDB system state: ${system_state_resp}"
  usdb_chain_log "Current pass economic profile: ${pass_profile_resp}"

  usdb_chain_log "Generating canonical USDB genesis"
  run_geth dumpgenesis --usdb >"$GENESIS_JSON"
  usdb_chain_log "Configuring development BTC anchor max age=${BTC_ANCHOR_MAX_AGE_BLOCKS}"
  python3 "$ROOT_DIR/scripts/usdb/configure_usdb_anchor_max_age_genesis.py" \
    --genesis "$GENESIS_JSON" \
    --max-age-blocks "$BTC_ANCHOR_MAX_AGE_BLOCKS"
  if [[ -n "$POW_CALIBRATION_PROFILE" ]]; then
    usdb_chain_log "Applying PoW calibration difficulty: genesis=${POW_CALIBRATION_GENESIS_DIFFICULTY}, minimum=${POW_CALIBRATION_MINIMUM_DIFFICULTY}"
    python3 "$ROOT_DIR/scripts/usdb/configure_usdb_pow_calibration_genesis.py" \
      --genesis "$GENESIS_JSON" \
      --genesis-difficulty "$POW_CALIBRATION_GENESIS_DIFFICULTY" \
      --minimum-difficulty "$POW_CALIBRATION_MINIMUM_DIFFICULTY"
  fi
  if [[ -n "$ACTIVATION_CONFORMANCE_BLOCK" ]]; then
    usdb_chain_log "Adding test-only activation at USDB block ${ACTIVATION_CONFORMANCE_BLOCK}"
    python3 "$ROOT_DIR/scripts/usdb/configure_usdb_activation_conformance_genesis.py" \
      --genesis "$GENESIS_JSON" \
      --activation-block "$ACTIVATION_CONFORMANCE_BLOCK"
  elif usdb_chain_economic_conformance_enabled; then
    usdb_chain_log "Adding test-only economic activations at USDB blocks ${ECONOMIC_CONFORMANCE_V2_BLOCK} and ${ECONOMIC_CONFORMANCE_V3_BLOCK}"
    python3 "$ROOT_DIR/scripts/usdb/configure_usdb_economic_activation_conformance_genesis.py" \
      --genesis "$GENESIS_JSON" \
      --v2-activation-block "$ECONOMIC_CONFORMANCE_V2_BLOCK" \
      --v3-activation-block "$ECONOMIC_CONFORMANCE_V3_BLOCK"
  fi
  usdb_chain_log "Initializing USDB-chain datadir ${DATADIR}"
  run_geth init --datadir "$DATADIR" "$GENESIS_JSON" >/dev/null
  if usdb_chain_validator_required; then
    usdb_chain_log "Initializing fresh-validator datadir ${VALIDATOR_DATADIR}"
    run_geth init --datadir "$VALIDATOR_DATADIR" "$GENESIS_JSON" >/dev/null
  fi

  if [[ -n "$ACTIVATION_CONFORMANCE_BLOCK" ]]; then
    local pre_activation_height=$((ACTIVATION_CONFORMANCE_BLOCK - 1)) stalled_height
    usdb_chain_log "Starting default binary before the test activation"
    usdb_chain_start_node false "${PRE_ACTIVATION_GETH_CMD[@]}"
    usdb_chain_wait_rpc_ready
    usdb_chain_wait_block_height "$pre_activation_height" >/dev/null
    usdb_chain_wait_log_pattern "unsupported usdb difficulty policy version 65535"
    stalled_height="$(usdb_chain_current_height)"
    if (( stalled_height != pre_activation_height )); then
      echo "Default binary crossed activation boundary: have ${stalled_height}, want ${pre_activation_height}" >&2
      exit 1
    fi
    usdb_chain_log "Default binary failed closed at block ${ACTIVATION_CONFORMANCE_BLOCK}; restarting tagged binary"
    regtest_stop_process "$GETH_PID"
    GETH_PID=""
    sleep 1
    usdb_chain_start_node true "${POST_ACTIVATION_GETH_CMD[@]}"
    usdb_chain_wait_rpc_ready
  elif usdb_chain_economic_conformance_enabled; then
    local v2_pre_activation_height=$((ECONOMIC_CONFORMANCE_V2_BLOCK - 1))
    local v3_pre_activation_height=$((ECONOMIC_CONFORMANCE_V3_BLOCK - 1))
    local stalled_height

    usdb_chain_log "Starting default binary before fake v2 activation"
    usdb_chain_start_node false "${PRE_ACTIVATION_GETH_CMD[@]}"
    usdb_chain_wait_rpc_ready
    usdb_chain_wait_block_height "$v2_pre_activation_height" >/dev/null
    usdb_chain_wait_log_pattern "unsupported usdb quote policy version 65534"
    stalled_height="$(usdb_chain_current_height)"
    if (( stalled_height != v2_pre_activation_height )); then
      echo "Default binary crossed fake v2 boundary: have ${stalled_height}, want ${v2_pre_activation_height}" >&2
      exit 1
    fi
    usdb_chain_log "Default binary failed closed; restarting fake v2 binary"
    regtest_stop_process "$GETH_PID"
    GETH_PID=""
    sleep 1

    usdb_chain_start_node true "${MID_ACTIVATION_GETH_CMD[@]}"
    usdb_chain_wait_rpc_ready
    usdb_chain_wait_block_height "$v3_pre_activation_height" >/dev/null
    usdb_chain_wait_log_pattern "unsupported usdb quote policy version 65535"
    stalled_height="$(usdb_chain_current_height)"
    if (( stalled_height != v3_pre_activation_height )); then
      echo "Fake v2 binary crossed fake v3 boundary: have ${stalled_height}, want ${v3_pre_activation_height}" >&2
      exit 1
    fi
    usdb_chain_log "Fake v2 binary failed closed; restarting fake v3 binary"
    regtest_stop_process "$GETH_PID"
    GETH_PID=""
    sleep 1

    usdb_chain_start_node true "${POST_ACTIVATION_GETH_CMD[@]}"
    usdb_chain_wait_rpc_ready
  else
    usdb_chain_log "Starting USDB-chain node with USDB profile/difficulty integration"
    usdb_chain_start_node false "${GETH_CMD[@]}"
    usdb_chain_wait_rpc_ready
  fi
  if [[ "$ANCHOR_BOUNDARY_CHECK" == "1" ]]; then
    run_anchor_boundary_check "$miner_btc_address" "$current_context_height"
    final_block_height="$ANCHOR_BOUNDARY_FINAL_HEIGHT"
  elif [[ "$INDEXER_OUTAGE_CHECK" == "1" ]]; then
    usdb_chain_wait_block_height 1 >/dev/null
    run_indexer_outage_recovery_check
    final_block_height="$OUTAGE_RECOVERY_HEIGHT"
    if (( final_block_height < TARGET_BLOCKS )); then
      usdb_chain_start_mining
      final_block_height="$(usdb_chain_wait_block_height "$TARGET_BLOCKS")"
    fi
  else
    final_block_height="$(usdb_chain_wait_block_height "$TARGET_BLOCKS")"
  fi
  usdb_chain_log "USDB chain reached block height ${final_block_height}; stopping mining for deterministic verification"
  usdb_chain_stop_mining
  sleep 2

  final_block_height="$(printf '%s' "$(usdb_chain_rpc_call "eth_blockNumber" "[]")" | python3 -c 'import json,sys; print(int((json.load(sys.stdin).get("result") or "0x0"), 16))')"
  balance_resp="$(usdb_chain_rpc_call "eth_getBalance" "[\"${USDB_CHAIN_MINER_ADDRESS}\",\"latest\"]")"
  latest_balance_hex="$(printf '%s' "$balance_resp" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("result") or "0x0")')"

  blocks_file="$USDB_CHAIN_WORK_DIR/mined_blocks.json"
  python3 - "$final_block_height" "$blocks_file" "$HTTP_ADDR" "$HTTP_PORT" <<'PY'
import json
import sys
import urllib.request

final_height = int(sys.argv[1])
output_path = sys.argv[2]
http_addr = sys.argv[3]
http_port = sys.argv[4]
blocks = []
for number in range(1, final_height + 1):
    hex_num = hex(number)
    payload = json.dumps({
        "jsonrpc": "2.0",
        "id": 1,
        "method": "eth_getBlockByNumber",
        "params": [hex_num, False],
    }).encode()
    req = urllib.request.Request(
        f"http://{http_addr}:{http_port}",
        data=payload,
        headers={"content-type": "application/json"},
        method="POST",
    )
    with urllib.request.urlopen(req, timeout=8) as resp:
        body = json.loads(resp.read().decode())
    block = body.get("result")
    if block is None:
        raise SystemExit(f"missing block {number}")
    blocks.append(block)
with open(output_path, "w", encoding="utf-8") as f:
    json.dump(blocks, f, indent=2)
PY

  usdb_chain_log "Verifying payloads and reward totals across ${final_block_height} mined blocks"
  verify_profile_blocks "$blocks_file" "$USDB_CHAIN_MINER_ADDRESS" "$latest_balance_hex" "$pass_id"

  if [[ "$ACTIVATION_FRESH_VALIDATOR_CHECK" == "1" ]]; then
    run_activation_fresh_validator_check "$final_block_height"
  fi
  if [[ "$SELECTOR_TAMPER_CHECK" == "1" ]]; then
    run_selector_tamper_import_matrix
  fi
  run_pow_calibration "$final_block_height"

  usdb_chain_log "USDB-chain profile/difficulty E2E succeeded."
  usdb_chain_log "pass_id=${pass_id}, coinbase=${USDB_CHAIN_MINER_ADDRESS}, blocks=${final_block_height}, balance=${latest_balance_hex}"
}

main "$@"
