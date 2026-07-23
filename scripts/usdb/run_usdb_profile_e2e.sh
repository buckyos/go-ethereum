#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
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
RPC_WAIT_SECONDS=${RPC_WAIT_SECONDS:-90}
BLOCK_WAIT_SECONDS=${BLOCK_WAIT_SECONDS:-180}
ENERGY_TOPUP_AMOUNT_BTC=${ENERGY_TOPUP_AMOUNT_BTC:-1.0}
ENERGY_GROWTH_BLOCKS=${ENERGY_GROWTH_BLOCKS:-2}
ACTIVATION_CONFORMANCE_BLOCK=${ACTIVATION_CONFORMANCE_BLOCK:-}

USDB_CHAIN_MINER_ADDRESS=${USDB_CHAIN_MINER_ADDRESS:-0x1111111111111111111111111111111111111111}
MINER_PASS_USDB_MAIN=${MINER_PASS_USDB_MAIN:-$USDB_CHAIN_MINER_ADDRESS}

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
GETH_GO=${GETH_GO:-/usr/local/go/bin/go}
PRE_ACTIVATION_GETH_BIN=${PRE_ACTIVATION_GETH_BIN:-}
POST_ACTIVATION_GETH_BIN=${POST_ACTIVATION_GETH_BIN:-}

if [[ -n "$GETH_BIN" ]]; then
  GETH_CMD=("$GETH_BIN")
else
  GETH_CMD=("$GETH_GO" run -ldflags=-checklinkname=0 ./cmd/geth)
fi
if [[ -n "$PRE_ACTIVATION_GETH_BIN" ]]; then
  PRE_ACTIVATION_GETH_CMD=("$PRE_ACTIVATION_GETH_BIN")
else
  PRE_ACTIVATION_GETH_CMD=("${GETH_CMD[@]}")
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

usdb_chain_stop_mining() {
  usdb_chain_rpc_call "miner_stop" "[]" >/dev/null || true
}

usdb_chain_start_node() {
  local append_log="$1"
  shift
  local -a command=("$@")
  if [[ "$append_log" != "true" ]]; then
    : >"$GETH_LOG_FILE"
  fi
  (
    cd "$ROOT_DIR"
    exec "${command[@]}" \
      --datadir "$DATADIR" \
      --networkid "$NETWORK_ID" \
      --http \
      --http.addr "$HTTP_ADDR" \
      --http.port "$HTTP_PORT" \
      --http.api eth,net,web3,admin,miner,txpool \
      --authrpc.addr "$HTTP_ADDR" \
      --authrpc.port "$AUTHRPC_PORT" \
      --port "$P2P_PORT" \
      --nodiscover \
      --maxpeers 0 \
      --mine \
      --miner.threads 1 \
      --miner.etherbase "$USDB_CHAIN_MINER_ADDRESS" \
      --miner.usdb-indexer.rpcurl "http://127.0.0.1:${USDB_INDEXER_RPC_PORT}" \
      --miner.usdb.passid "$pass_id" \
      --ethash.usdb-indexer.rpcurl "http://127.0.0.1:${USDB_INDEXER_RPC_PORT}"
  ) >>"$GETH_LOG_FILE" 2>&1 &
  GETH_PID=$!
}

usdb_chain_current_height() {
  usdb_chain_rpc_call "eth_blockNumber" "[]" |
    python3 -c 'import json,sys; print(int((json.load(sys.stdin).get("result") or "0x0"), 16))'
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

eth_print_failure_diagnostics() {
  if [[ -f "$GETH_LOG_FILE" ]]; then
    usdb_chain_log "---- geth log (tail -n 120) ----"
    tail -n 120 "$GETH_LOG_FILE" || true
    usdb_chain_log "---- end geth log ----"
  fi
}

cleanup() {
  local exit_code=$?
  set +e
  if [[ -n "${GETH_PID:-}" ]] && kill -0 "$GETH_PID" 2>/dev/null; then
    regtest_stop_process "$GETH_PID"
  fi
  usdb_chain_stop_residual_nodes
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

  python3 "$ROOT_DIR/scripts/usdb/verify_usdb_profile_e2e.py" \
    --blocks "$blocks_file" \
    --coinbase "$coinbase" \
    --balance-hex "$balance_hex" \
    --usdb-chain-rpc-url "http://${HTTP_ADDR}:${HTTP_PORT}" \
    --usdb-indexer-rpc-url "http://127.0.0.1:${USDB_INDEXER_RPC_PORT}" \
    --expected-pass-id "$expected_pass_id" \
    "${activation_args[@]}"
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
  regtest_assert_ord_server_port_available
  if [[ ! -x "$ORD_BIN" ]]; then
    echo "Missing required ORD_BIN executable: $ORD_BIN" >&2
    exit 1
  fi

  regtest_ensure_workspace_dirs
  mkdir -p "$USDB_CHAIN_WORK_DIR"
  usdb_chain_stop_residual_nodes
  rm -rf "$DATADIR"
  mkdir -p "$DATADIR"

  regtest_start_bitcoind
  regtest_ensure_wallet

  local miner_btc_address ord_receive_address mint_content_file pass_id current_tip_height
  local system_state_resp pass_profile_resp
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
  regtest_wait_until_ord_server_synced_to_bitcoind
  current_tip_height="$("$BITCOIN_CLI_BIN" -regtest -datadir="$BITCOIN_DIR" -rpcport="$BTC_RPC_PORT" getblockcount)"

  regtest_create_balance_history_config
  regtest_create_usdb_indexer_config
  regtest_start_balance_history
  regtest_wait_balance_history_rpc_ready
  regtest_wait_until_balance_history_synced_eq "$current_tip_height"
  regtest_wait_balance_history_consensus_ready

  regtest_start_usdb_indexer
  regtest_wait_usdb_rpc_ready
  regtest_wait_until_usdb_synced_eq "$current_tip_height"
  regtest_wait_usdb_consensus_ready

  system_state_resp="$(regtest_rpc_call_usdb_indexer "get_system_state_info" "[]")"
  regtest_assert_json_expr "$system_state_resp" "data.get('error') is None" "True"
  pass_profile_resp="$(regtest_get_pass_economic_profile_response "$pass_id" "$current_tip_height")"
  regtest_assert_json_expr "$pass_profile_resp" "data.get('error') is None" "True"
  regtest_assert_json_expr "$pass_profile_resp" "(data.get('result') or {}).get('pass', {}).get('pass_id')" "$pass_id"
  regtest_assert_json_expr "$pass_profile_resp" "(data.get('result') or {}).get('pass', {}).get('state')" "active"
  regtest_assert_json_expr "$pass_profile_resp" "(data.get('result') or {}).get('pass', {}).get('pass_kind')" "standard"
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
    regtest_wait_until_ord_server_synced_to_bitcoind
    current_tip_height="$("$BITCOIN_CLI_BIN" -regtest -datadir="$BITCOIN_DIR" -rpcport="$BTC_RPC_PORT" getblockcount)"
    regtest_wait_until_balance_history_synced_eq "$current_tip_height"
    regtest_wait_until_usdb_synced_eq "$current_tip_height"
    regtest_wait_balance_history_consensus_ready
    regtest_wait_usdb_consensus_ready

    system_state_resp="$(regtest_rpc_call_usdb_indexer "get_system_state_info" "[]")"
    regtest_assert_json_expr "$system_state_resp" "data.get('error') is None" "True"
    pass_profile_resp="$(regtest_get_pass_economic_profile_response "$pass_id" "$current_tip_height")"
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
  if [[ -n "$ACTIVATION_CONFORMANCE_BLOCK" ]]; then
    usdb_chain_log "Adding test-only activation at USDB block ${ACTIVATION_CONFORMANCE_BLOCK}"
    python3 "$ROOT_DIR/scripts/usdb/configure_usdb_activation_conformance_genesis.py" \
      --genesis "$GENESIS_JSON" \
      --activation-block "$ACTIVATION_CONFORMANCE_BLOCK"
  fi
  usdb_chain_log "Initializing USDB-chain datadir ${DATADIR}"
  run_geth init --datadir "$DATADIR" "$GENESIS_JSON" >/dev/null

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
  else
    usdb_chain_log "Starting USDB-chain node with USDB profile/difficulty integration"
    usdb_chain_start_node false "${GETH_CMD[@]}"
    usdb_chain_wait_rpc_ready
  fi
  final_block_height="$(usdb_chain_wait_block_height "$TARGET_BLOCKS")"
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

  usdb_chain_log "USDB-chain profile/difficulty E2E succeeded."
  usdb_chain_log "pass_id=${pass_id}, coinbase=${USDB_CHAIN_MINER_ADDRESS}, blocks=${final_block_height}, balance=${latest_balance_hex}"
}

main "$@"
