#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
USDB_REPO_DIR=${USDB_REPO_DIR:-"$ROOT_DIR/../usdb"}

E2E_WORK_DIR=${WORK_DIR:-/tmp/usdb-ethw-reward-e2e}
ETHW_WORK_DIR=${ETHW_WORK_DIR:-"$E2E_WORK_DIR/geth"}
DATADIR=${DATADIR:-"$ETHW_WORK_DIR/datadir"}
GENESIS_JSON=${GENESIS_JSON:-"$ETHW_WORK_DIR/usdb-genesis.json"}
GETH_LOG_FILE=${GETH_LOG_FILE:-"$ETHW_WORK_DIR/geth.log"}

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

MINER_ETHERBASE=${MINER_ETHERBASE:-0x1111111111111111111111111111111111111111}
MINER_PASS_ETH_MAIN=${MINER_PASS_ETH_MAIN:-$MINER_ETHERBASE}

export REPO_ROOT="${USDB_REPO_DIR}"
export WORK_DIR="${E2E_WORK_DIR}/usdb"
export BITCOIN_DIR="${BITCOIN_DIR:-$WORK_DIR/bitcoin}"
export ORD_DATA_DIR="${ORD_DATA_DIR:-$WORK_DIR/ord}"
export BALANCE_HISTORY_ROOT="${BALANCE_HISTORY_ROOT:-$WORK_DIR/balance-history}"
export USDB_INDEXER_ROOT="${USDB_INDEXER_ROOT:-$WORK_DIR/usdb-indexer}"
export BTC_RPC_PORT="${BTC_RPC_PORT:-39932}"
export BTC_P2P_PORT="${BTC_P2P_PORT:-39933}"
export BH_RPC_PORT="${BH_RPC_PORT:-39910}"
export USDB_RPC_PORT="${USDB_RPC_PORT:-39920}"
export ORD_RPC_PORT="${ORD_RPC_PORT:-39930}"
export WALLET_NAME="${WALLET_NAME:-usdbethwreward}"
export ORD_WALLET_NAME="${ORD_WALLET_NAME:-ord-usdb-ethw-reward-a}"
export ORD_WALLET_NAME_B="${ORD_WALLET_NAME_B:-ord-usdb-ethw-reward-b}"
export PREMINE_BLOCKS="${PREMINE_BLOCKS:-130}"
export FUND_CONFIRM_BLOCKS="${FUND_CONFIRM_BLOCKS:-2}"
export INSCRIBE_CONFIRM_BLOCKS="${INSCRIBE_CONFIRM_BLOCKS:-2}"
export SYNC_TIMEOUT_SEC="${SYNC_TIMEOUT_SEC:-300}"
export BALANCE_HISTORY_LOG_FILE="${BALANCE_HISTORY_LOG_FILE:-$WORK_DIR/balance-history.log}"
export USDB_INDEXER_LOG_FILE="${USDB_INDEXER_LOG_FILE:-$WORK_DIR/usdb-indexer.log}"
export ORD_SERVER_LOG_FILE="${ORD_SERVER_LOG_FILE:-$WORK_DIR/ord-server.log}"
export REGTEST_LOG_PREFIX="[usdb-ethw-reward-e2e/usdb]"

GETH_BIN=${GETH_BIN:-}
GETH_GO=${GETH_GO:-/usr/local/go/bin/go}

if [[ -n "$GETH_BIN" ]]; then
  GETH_CMD=("$GETH_BIN")
else
  GETH_CMD=("$GETH_GO" run -ldflags=-checklinkname=0 ./cmd/geth)
fi

# shellcheck source=/dev/null
source "$USDB_REPO_DIR/src/btc/usdb-indexer/scripts/regtest_reorg_lib.sh"

run_geth() {
  (
    cd "$ROOT_DIR"
    "${GETH_CMD[@]}" "$@"
  )
}

ethw_log() {
  echo "[usdb-ethw-reward-e2e/geth] $*"
}

eth_rpc_call() {
  local method="$1"
  local params="${2:-[]}"
  curl -s --connect-timeout 2 --max-time 8 \
    -X POST "http://${HTTP_ADDR}:${HTTP_PORT}" \
    -H 'content-type: application/json' \
    --data "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"${method}\",\"params\":${params}}"
}

eth_wait_rpc_ready() {
  local expected_chain_id
  expected_chain_id="$(printf '0x%x' "$NETWORK_ID")"
  local deadline=$((SECONDS + RPC_WAIT_SECONDS))
  while (( SECONDS < deadline )); do
    local response
    response="$(eth_rpc_call "eth_chainId" "[]" || true)"
    if [[ "$response" == *"\"result\":\"${expected_chain_id}\""* ]]; then
      return 0
    fi
    sleep 1
  done
  echo "Timed out waiting for ETHW RPC at http://${HTTP_ADDR}:${HTTP_PORT}" >&2
  return 1
}

eth_wait_block_height() {
  local target_height="$1"
  local deadline=$((SECONDS + BLOCK_WAIT_SECONDS))
  while (( SECONDS < deadline )); do
    local response block_hex current_height
    response="$(eth_rpc_call "eth_blockNumber" "[]" || true)"
    block_hex="$(printf '%s' "$response" | python3 -c 'import json,sys; payload=json.load(sys.stdin); print(payload.get("result") or "0x0")' 2>/dev/null || echo 0x0)"
    current_height=$((block_hex))
    if (( current_height >= target_height )); then
      printf '%d\n' "$current_height"
      return 0
    fi
    sleep 0.2
  done
  echo "Timed out waiting for ETHW block height >= ${target_height}" >&2
  return 1
}

eth_stop_mining() {
  eth_rpc_call "miner_stop" "[]" >/dev/null || true
}

eth_stop_residual_nodes() {
  while IFS= read -r pid; do
    if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
      ethw_log "Stopping residual geth process pid=${pid} for datadir=${DATADIR}, http_port=${HTTP_PORT}"
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
    ethw_log "---- geth log (tail -n 120) ----"
    tail -n 120 "$GETH_LOG_FILE" || true
    ethw_log "---- end geth log ----"
  fi
}

cleanup() {
  local exit_code=$?
  set +e
  if [[ -n "${GETH_PID:-}" ]] && kill -0 "$GETH_PID" 2>/dev/null; then
    regtest_stop_process "$GETH_PID"
  fi
  eth_stop_residual_nodes
  if [[ "$exit_code" -ne 0 ]]; then
    eth_print_failure_diagnostics
  fi
  regtest_cleanup
}

verify_reward_sum() {
  local blocks_file="$1"
  local coinbase="$2"
  local balance_hex="$3"

  python3 - "$blocks_file" "$coinbase" "$balance_hex" "$USDB_RPC_PORT" "$MINER_PASS_ETH_MAIN" <<'PY'
import json
import sys
import urllib.request
from fractions import Fraction

blocks_path, coinbase, balance_hex, usdb_rpc_port, expected_eth_main = sys.argv[1:6]
with open(blocks_path, "r", encoding="utf-8") as f:
    blocks = json.load(f)

BASE_REWARD = 5 * 10**18
PAYLOAD_SIZE = 105
MIN_LEVEL = 1
MAX_LEVEL = 50
MIN_BPS = 5000
MAX_BPS = 20000
LEVEL_BASE = Fraction(1_000_000, 1)
LEVEL_RATIO = Fraction(118, 100)

def rpc_call(method, params):
    payload = json.dumps({"jsonrpc": "2.0", "id": 1, "method": method, "params": params}).encode()
    req = urllib.request.Request(
        f"http://127.0.0.1:{usdb_rpc_port}",
        data=payload,
        headers={"content-type": "application/json"},
        method="POST",
    )
    with urllib.request.urlopen(req, timeout=8) as resp:
        body = json.loads(resp.read().decode())
    if body.get("error") is not None:
        raise SystemExit(f"USDB RPC {method} failed: {body['error']}")
    return body["result"]

def level_for_energy(energy: int) -> int:
    if energy == 0:
        return 0
    remaining = Fraction(energy, 1)
    increment = LEVEL_BASE
    level = 0
    while remaining >= increment:
        remaining -= increment
        level += 1
        increment *= LEVEL_RATIO
    return level

def multiplier_bps(level: int) -> int:
    if level <= MIN_LEVEL:
        return MIN_BPS
    if level >= MAX_LEVEL:
        return MAX_BPS
    span = MAX_BPS - MIN_BPS
    offset = level - MIN_LEVEL
    steps = MAX_LEVEL - MIN_LEVEL
    return MIN_BPS + (span * offset) // steps

actual_balance = int(balance_hex, 16)
expected_total = 0

for block in blocks:
    number = int(block["number"], 16)
    block_coinbase = (block.get("miner") or block.get("author") or "").lower()
    if block_coinbase != coinbase.lower():
        raise SystemExit(f"unexpected block coinbase at height {number}: {block_coinbase} != {coinbase}")

    extra_hex = (block.get("extraData") or "0x")[2:]
    if len(extra_hex) != PAYLOAD_SIZE * 2:
        raise SystemExit(f"unexpected extraData size at block {number}: have {len(extra_hex)//2} want {PAYLOAD_SIZE}")

    payload = bytes.fromhex(extra_hex)
    if payload[0] != 1:
        raise SystemExit(f"unexpected payload version at block {number}: {payload[0]}")

    btc_height = int.from_bytes(payload[1:5], "big")
    snapshot_id = payload[5:37].hex()
    system_state_id = payload[37:69].hex()
    pass_txid = payload[69:101].hex()
    pass_index = int.from_bytes(payload[101:105], "big")
    pass_id = f"{pass_txid}i{pass_index}"

    context = {
        "requested_height": btc_height,
        "expected_state": {
            "snapshot_id": snapshot_id,
            "system_state_id": system_state_id,
        },
    }
    snapshot = rpc_call("get_pass_snapshot", [{
        "inscription_id": pass_id,
        "at_height": btc_height,
        "context": context,
    }])
    if snapshot.get("eth_main", "").lower() != expected_eth_main.lower():
        raise SystemExit(
            f"unexpected eth_main at block {number}: {snapshot.get('eth_main')} != {expected_eth_main}"
        )

    energy_info = rpc_call("get_pass_energy", [{
        "inscription_id": pass_id,
        "block_height": btc_height,
        "mode": "at_or_before",
        "context": context,
    }])
    energy = int(energy_info["energy"])
    level = level_for_energy(energy)
    reward = (BASE_REWARD * multiplier_bps(level)) // 10_000
    expected_total += reward
    print(
        json.dumps(
            {
                "eth_block": number,
                "btc_height": btc_height,
                "pass_id": pass_id,
                "energy": energy,
                "level": level,
                "reward": reward,
            }
        )
    )

if expected_total != actual_balance:
    raise SystemExit(
        f"unexpected coinbase balance: have {actual_balance} want {expected_total}"
    )
print(json.dumps({"status": "ok", "expected_total": expected_total, "actual_balance": actual_balance}))
PY
}

main() {
  trap cleanup EXIT

  regtest_resolve_bitcoin_binaries
  regtest_require_cmd cargo
  regtest_require_cmd curl
  regtest_require_cmd python3
  regtest_assert_ord_server_port_available
  if [[ ! -x "$ORD_BIN" ]]; then
    echo "Missing required ORD_BIN executable: $ORD_BIN" >&2
    exit 1
  fi

  regtest_ensure_workspace_dirs
  mkdir -p "$ETHW_WORK_DIR"
  eth_stop_residual_nodes
  rm -rf "$DATADIR"
  mkdir -p "$DATADIR"

  regtest_start_bitcoind
  regtest_ensure_wallet

  local miner_btc_address ord_receive_address mint_content_file pass_id current_tip_height
  local system_state_resp pass_snapshot_resp pass_energy_resp
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

  mint_content_file="$WORK_DIR/usdb_ethw_reward_mint.json"
  cat >"$mint_content_file" <<EOF
{"p":"usdb","op":"mint","eth_main":"${MINER_PASS_ETH_MAIN}","prev":[]}
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
  pass_snapshot_resp="$(regtest_rpc_call_usdb_indexer "get_pass_snapshot" "[{\"inscription_id\":\"${pass_id}\"}]")"
  regtest_assert_json_expr "$pass_snapshot_resp" "data.get('error') is None" "True"
  regtest_assert_json_expr "$pass_snapshot_resp" "(data.get('result') or {}).get('eth_main')" "$MINER_PASS_ETH_MAIN"
  pass_energy_resp="$(regtest_rpc_call_usdb_indexer "get_pass_energy" "[{\"inscription_id\":\"${pass_id}\",\"mode\":\"at_or_before\"}]")"
  regtest_assert_json_expr "$pass_energy_resp" "data.get('error') is None" "True"
  current_energy="$(regtest_json_expr "$pass_energy_resp" "(data.get('result') or {}).get('energy')")"

  # Fresh ord mint flows may leave the owner at a zero-energy floor at the mint
  # height. Fund the same address once more and mine a few growth blocks so the
  # first ETHW smoke gets a best-effort retry toward a positive reward level
  # before falling back to the minimum multiplier band.
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
    pass_snapshot_resp="$(regtest_rpc_call_usdb_indexer "get_pass_snapshot" "[{\"inscription_id\":\"${pass_id}\"}]")"
    regtest_assert_json_expr "$pass_snapshot_resp" "data.get('error') is None" "True"
    pass_energy_resp="$(regtest_rpc_call_usdb_indexer "get_pass_energy" "[{\"inscription_id\":\"${pass_id}\",\"mode\":\"at_or_before\"}]")"
    regtest_assert_json_expr "$pass_energy_resp" "data.get('error') is None" "True"
    current_energy="$(regtest_json_expr "$pass_energy_resp" "(data.get('result') or {}).get('energy')")"
  fi
  if [[ "$current_energy" == "0" ]]; then
    ethw_log "Pass energy is still zero after retry; proceeding with the minimum reward multiplier path"
  fi

  ethw_log "Using pass_id=${pass_id}"
  ethw_log "Current USDB system state: ${system_state_resp}"
  ethw_log "Current pass snapshot: ${pass_snapshot_resp}"
  ethw_log "Current pass energy: ${pass_energy_resp}"

  ethw_log "Generating canonical USDB genesis"
  run_geth dumpgenesis --usdb >"$GENESIS_JSON"
  ethw_log "Initializing ETHW datadir ${DATADIR}"
  run_geth init --datadir "$DATADIR" "$GENESIS_JSON" >/dev/null

  ethw_log "Starting ETHW node with USDB reward integration"
  (
    cd "$ROOT_DIR"
    exec "${GETH_CMD[@]}" \
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
      --miner.etherbase "$MINER_ETHERBASE" \
      --miner.usdb.rpcurl "http://127.0.0.1:${USDB_RPC_PORT}" \
      --miner.usdb.passid "$pass_id" \
      --ethash.usdb.rpcurl "http://127.0.0.1:${USDB_RPC_PORT}"
  ) >"$GETH_LOG_FILE" 2>&1 &
  GETH_PID=$!

  eth_wait_rpc_ready
  final_block_height="$(eth_wait_block_height "$TARGET_BLOCKS")"
  ethw_log "ETHW reached block height ${final_block_height}; stopping mining for deterministic verification"
  eth_stop_mining
  sleep 2

  final_block_height="$(printf '%s' "$(eth_rpc_call "eth_blockNumber" "[]")" | python3 -c 'import json,sys; print(int((json.load(sys.stdin).get("result") or "0x0"), 16))')"
  balance_resp="$(eth_rpc_call "eth_getBalance" "[\"${MINER_ETHERBASE}\",\"latest\"]")"
  latest_balance_hex="$(printf '%s' "$balance_resp" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("result") or "0x0")')"

  blocks_file="$ETHW_WORK_DIR/mined_blocks.json"
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

  ethw_log "Verifying payloads and reward totals across ${final_block_height} mined blocks"
  verify_reward_sum "$blocks_file" "$MINER_ETHERBASE" "$latest_balance_hex"

  ethw_log "USDB + ETHW reward E2E succeeded."
  ethw_log "pass_id=${pass_id}, coinbase=${MINER_ETHERBASE}, blocks=${final_block_height}, balance=${latest_balance_hex}"
}

main "$@"
