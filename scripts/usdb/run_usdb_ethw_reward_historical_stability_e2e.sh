#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
USDB_REPO_DIR=${USDB_REPO_DIR:-"$ROOT_DIR/../usdb"}

E2E_WORK_DIR=${WORK_DIR:-/tmp/usdb-ethw-reward-historical-stability-e2e}
ETHW_WORK_DIR=${ETHW_WORK_DIR:-"$E2E_WORK_DIR/geth"}
GENESIS_JSON=${GENESIS_JSON:-"$ETHW_WORK_DIR/usdb-genesis.json"}

NODE1_DATADIR=${NODE1_DATADIR:-"$ETHW_WORK_DIR/node1"}
NODE1_LOG_FILE=${NODE1_LOG_FILE:-"$ETHW_WORK_DIR/node1.log"}
NODE1_HTTP_ADDR=${NODE1_HTTP_ADDR:-127.0.0.1}
NODE1_HTTP_PORT=${NODE1_HTTP_PORT:-19745}
NODE1_P2P_PORT=${NODE1_P2P_PORT:-31333}
NODE1_AUTHRPC_PORT=${NODE1_AUTHRPC_PORT:-19751}

NODE2_DATADIR=${NODE2_DATADIR:-"$ETHW_WORK_DIR/node2"}
NODE2_LOG_FILE=${NODE2_LOG_FILE:-"$ETHW_WORK_DIR/node2.log"}
NODE2_HTTP_ADDR=${NODE2_HTTP_ADDR:-127.0.0.1}
NODE2_HTTP_PORT=${NODE2_HTTP_PORT:-19746}
NODE2_P2P_PORT=${NODE2_P2P_PORT:-31334}
NODE2_AUTHRPC_PORT=${NODE2_AUTHRPC_PORT:-19752}

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
export BTC_RPC_PORT="${BTC_RPC_PORT:-39972}"
export BTC_P2P_PORT="${BTC_P2P_PORT:-39973}"
export BH_RPC_PORT="${BH_RPC_PORT:-39970}"
export USDB_RPC_PORT="${USDB_RPC_PORT:-39980}"
export ORD_RPC_PORT="${ORD_RPC_PORT:-39990}"
export WALLET_NAME="${WALLET_NAME:-usdbethwrewardhistorical}"
export ORD_WALLET_NAME="${ORD_WALLET_NAME:-ord-usdb-ethw-reward-historical-a}"
export ORD_WALLET_NAME_B="${ORD_WALLET_NAME_B:-ord-usdb-ethw-reward-historical-b}"
export PREMINE_BLOCKS="${PREMINE_BLOCKS:-130}"
export FUND_CONFIRM_BLOCKS="${FUND_CONFIRM_BLOCKS:-2}"
export INSCRIBE_CONFIRM_BLOCKS="${INSCRIBE_CONFIRM_BLOCKS:-2}"
export SYNC_TIMEOUT_SEC="${SYNC_TIMEOUT_SEC:-300}"
export BALANCE_HISTORY_LOG_FILE="${BALANCE_HISTORY_LOG_FILE:-$WORK_DIR/balance-history.log}"
export USDB_INDEXER_LOG_FILE="${USDB_INDEXER_LOG_FILE:-$WORK_DIR/usdb-indexer.log}"
export ORD_SERVER_LOG_FILE="${ORD_SERVER_LOG_FILE:-$WORK_DIR/ord-server.log}"
export REGTEST_LOG_PREFIX="[usdb-ethw-reward-historical/usdb]"

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
  echo "[usdb-ethw-reward-historical/geth] $*"
}

rpc_call() {
  local url="$1"
  local method="$2"
  local params="${3:-[]}"
  curl -s --connect-timeout 2 --max-time 8 \
    -X POST "$url" \
    -H 'content-type: application/json' \
    --data "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"${method}\",\"params\":${params}}"
}

wait_chain_id() {
  local url="$1"
  local expected_chain_id
  expected_chain_id="$(printf '0x%x' "$NETWORK_ID")"
  local deadline=$((SECONDS + RPC_WAIT_SECONDS))
  while (( SECONDS < deadline )); do
    local response
    response="$(rpc_call "$url" "eth_chainId" "[]" || true)"
    if [[ "$response" == *"\"result\":\"${expected_chain_id}\""* ]]; then
      return 0
    fi
    sleep 1
  done
  echo "Timed out waiting for ETHW RPC at ${url}" >&2
  return 1
}

wait_block_height() {
  local url="$1"
  local target_height="$2"
  local deadline=$((SECONDS + BLOCK_WAIT_SECONDS))
  while (( SECONDS < deadline )); do
    local response block_hex current_height
    response="$(rpc_call "$url" "eth_blockNumber" "[]" || true)"
    block_hex="$(printf '%s' "$response" | python3 -c 'import json,sys; payload=json.load(sys.stdin); print(payload.get("result") or "0x0")' 2>/dev/null || echo 0x0)"
    current_height=$((block_hex))
    if (( current_height >= target_height )); then
      printf '%d\n' "$current_height"
      return 0
    fi
    sleep 0.2
  done
  echo "Timed out waiting for block height >= ${target_height} on ${url}" >&2
  return 1
}

fetch_block_number() {
  local url="$1"
  printf '%s' "$(rpc_call "$url" "eth_blockNumber" "[]")" | \
    python3 -c 'import json,sys; print(int((json.load(sys.stdin).get("result") or "0x0"), 16))'
}

fetch_head_hash() {
  local url="$1"
  printf '%s' "$(rpc_call "$url" "eth_getBlockByNumber" "[\"latest\", false]")" | \
    python3 -c 'import json,sys; payload=json.load(sys.stdin); block=payload.get("result") or {}; print(block.get("hash") or "")'
}

fetch_enode() {
  local url="$1"
  local deadline=$((SECONDS + RPC_WAIT_SECONDS))
  while (( SECONDS < deadline )); do
    local response enode
    response="$(rpc_call "$url" "admin_nodeInfo" "[]" || true)"
    enode="$(printf '%s' "$response" | sed -n 's/.*"enode":"\([^"]*\)".*/\1/p')"
    if [[ -n "$enode" ]]; then
      printf '%s\n' "$enode"
      return 0
    fi
    sleep 1
  done
  echo "Timed out waiting for admin_nodeInfo on ${url}" >&2
  return 1
}

wait_peers() {
  local url="$1"
  local min_peers="$2"
  local deadline=$((SECONDS + RPC_WAIT_SECONDS))
  while (( SECONDS < deadline )); do
    local response value count
    response="$(rpc_call "$url" "net_peerCount" "[]" || true)"
    value="$(printf '%s' "$response" | sed -n 's/.*"result":"\([^"]*\)".*/\1/p')"
    if [[ -n "$value" ]]; then
      count=$((value))
      if (( count >= min_peers )); then
        return 0
      fi
    fi
    sleep 1
  done
  echo "Timed out waiting for ${min_peers} peers on ${url}" >&2
  return 1
}

stop_mining() {
  local url="$1"
  rpc_call "$url" "miner_stop" "[]" >/dev/null || true
}

pass_energy_now() {
  local pass_id="$1"
  local resp
  resp="$(regtest_rpc_call_usdb_indexer "get_pass_energy" "[{\"inscription_id\":\"${pass_id}\",\"mode\":\"at_or_before\"}]")"
  if [[ "$(regtest_json_expr "$resp" "data.get('error') is None")" != "True" ]]; then
    regtest_log "Failed to fetch current pass energy: pass_id=${pass_id}, response=${resp}"
    exit 1
  fi
  regtest_json_expr "$resp" "(data.get('result') or {}).get('energy', 0)"
}

stop_residual_nodes_for() {
  local datadir="$1"
  local http_port="$2"
  local p2p_port="$3"
  local label="$4"
  while IFS= read -r pid; do
    if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
      ethw_log "Stopping residual ${label} geth process pid=${pid} datadir=${datadir}"
      regtest_stop_process "$pid"
    fi
  done < <(
    ps -eo pid=,args= | awk -v datadir="$datadir" -v http_port="$http_port" -v p2p_port="$p2p_port" '
      index($0, " --datadir " datadir) && index($0, " --http.port " http_port) && index($0, " --port " p2p_port) {
        print $1
      }
    '
  )
}

print_failure_diagnostics() {
  local label="$1"
  local log_file="$2"
  if [[ -f "$log_file" ]]; then
    ethw_log "---- ${label} log (tail -n 120) ----"
    tail -n 120 "$log_file" || true
    ethw_log "---- end ${label} log ----"
  fi
}

cleanup() {
  local exit_code=$?
  set +e
  for pid_var in NODE1_PID NODE2_PID; do
    local pid=${!pid_var:-}
    if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
      regtest_stop_process "$pid"
    fi
  done
  stop_residual_nodes_for "$NODE1_DATADIR" "$NODE1_HTTP_PORT" "$NODE1_P2P_PORT" "node1"
  stop_residual_nodes_for "$NODE2_DATADIR" "$NODE2_HTTP_PORT" "$NODE2_P2P_PORT" "node2"
  if [[ "$exit_code" -ne 0 ]]; then
    print_failure_diagnostics "node1" "$NODE1_LOG_FILE"
    print_failure_diagnostics "node2" "$NODE2_LOG_FILE"
  fi
  regtest_cleanup
}

collect_eth_blocks() {
  local url="$1"
  local final_block_height="$2"
  local blocks_file="$3"
  python3 - "$url" "$final_block_height" "$blocks_file" <<'PY'
import json
import sys
import urllib.request

rpc_url = sys.argv[1]
final_height = int(sys.argv[2])
output_path = sys.argv[3]
blocks = []
for number in range(1, final_height + 1):
    payload = json.dumps({
        "jsonrpc": "2.0",
        "id": 1,
        "method": "eth_getBlockByNumber",
        "params": [hex(number), False],
    }).encode()
    req = urllib.request.Request(
        rpc_url,
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
}

verify_historical_stability() {
  local blocks_file="$1"
  local coinbase="$2"
  local balance_hex="$3"
  local expected_pass_id="$4"
  local initial_energy="$5"
  local boosted_energy="$6"

  python3 - "$blocks_file" "$coinbase" "$balance_hex" "$USDB_RPC_PORT" "$MINER_PASS_ETH_MAIN" "$expected_pass_id" "$initial_energy" "$boosted_energy" <<'PY'
import json
import sys
import urllib.request
from fractions import Fraction

(
    blocks_path,
    coinbase,
    balance_hex,
    usdb_rpc_port,
    expected_eth_main,
    expected_pass_id,
    initial_energy,
    boosted_energy,
) = sys.argv[1:9]
initial_energy = int(initial_energy)
boosted_energy = int(boosted_energy)

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
historical_rewards = []
historical_energies = []

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
    if pass_id != expected_pass_id:
        raise SystemExit(f"unexpected pass id at block {number}: {pass_id} != {expected_pass_id}")

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
        raise SystemExit(f"unexpected eth_main at block {number}: {snapshot.get('eth_main')} != {expected_eth_main}")

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
    historical_energies.append(energy)
    historical_rewards.append(reward)
    print(json.dumps({"eth_block": number, "btc_height": btc_height, "energy": energy, "level": level, "reward": reward}))

if not historical_rewards:
    raise SystemExit("missing historical rewards")
if expected_total != actual_balance:
    raise SystemExit(f"unexpected coinbase balance: have {actual_balance} want {expected_total}")
if len(set(historical_rewards)) != 1:
    raise SystemExit("historical block rewards are not stable")
if boosted_energy <= initial_energy:
    raise SystemExit(f"expected boosted energy > initial energy, got {boosted_energy} <= {initial_energy}")

historical_reward = historical_rewards[0]
historical_energy = historical_energies[0]
current_reward = (BASE_REWARD * multiplier_bps(level_for_energy(boosted_energy))) // 10_000
if current_reward <= historical_reward:
    raise SystemExit(
        f"expected current reward > historical reward after BTC head advance, got {current_reward} <= {historical_reward}"
    )

print(json.dumps({
    "status": "ok",
    "historical_energy": historical_energy,
    "historical_reward": historical_reward,
    "current_energy": boosted_energy,
    "current_reward": current_reward,
    "expected_total": expected_total,
    "actual_balance": actual_balance,
}))
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
  stop_residual_nodes_for "$NODE1_DATADIR" "$NODE1_HTTP_PORT" "$NODE1_P2P_PORT" "node1"
  stop_residual_nodes_for "$NODE2_DATADIR" "$NODE2_HTTP_PORT" "$NODE2_P2P_PORT" "node2"
  rm -rf "$NODE1_DATADIR" "$NODE2_DATADIR"
  mkdir -p "$NODE1_DATADIR" "$NODE2_DATADIR"

  regtest_start_bitcoind
  regtest_ensure_wallet

  local miner_btc_address ord_receive_address mint_content_file pass_id current_tip_height
  local initial_energy boosted_energy node1_tip_height node2_tip_height
  local node1_tip_hash node2_tip_hash node1_balance_hex blocks_file node1_enode
  local node1_rpc node2_rpc

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

  mint_content_file="$WORK_DIR/usdb_ethw_reward_historical_mint.json"
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

  initial_energy="$(pass_energy_now "$pass_id")"
  ethw_log "Initial current pass energy=${initial_energy} for pass_id=${pass_id}"

  ethw_log "Generating canonical USDB genesis"
  run_geth dumpgenesis --usdb >"$GENESIS_JSON"
  ethw_log "Initializing ETHW datadirs"
  run_geth init --datadir "$NODE1_DATADIR" "$GENESIS_JSON" >/dev/null
  run_geth init --datadir "$NODE2_DATADIR" "$GENESIS_JSON" >/dev/null

  ethw_log "Starting ETHW node 1 miner with USDB reward integration"
  (
    cd "$ROOT_DIR"
    exec "${GETH_CMD[@]}" \
      --datadir "$NODE1_DATADIR" \
      --networkid "$NETWORK_ID" \
      --http \
      --http.addr "$NODE1_HTTP_ADDR" \
      --http.port "$NODE1_HTTP_PORT" \
      --http.api eth,net,web3,admin,miner,txpool \
      --authrpc.addr "$NODE1_HTTP_ADDR" \
      --authrpc.port "$NODE1_AUTHRPC_PORT" \
      --port "$NODE1_P2P_PORT" \
      --nodiscover \
      --maxpeers 10 \
      --mine \
      --miner.threads 1 \
      --miner.etherbase "$MINER_ETHERBASE" \
      --miner.usdb \
      --miner.usdb.rpcurl "http://127.0.0.1:${USDB_RPC_PORT}" \
      --miner.usdb.passid "$pass_id" \
      --ethash.usdb \
      --ethash.usdb.rpcurl "http://127.0.0.1:${USDB_RPC_PORT}"
  ) >"$NODE1_LOG_FILE" 2>&1 &
  NODE1_PID=$!

  node1_rpc="http://${NODE1_HTTP_ADDR}:${NODE1_HTTP_PORT}"
  node2_rpc="http://${NODE2_HTTP_ADDR}:${NODE2_HTTP_PORT}"
  wait_chain_id "$node1_rpc"
  node1_tip_height="$(wait_block_height "$node1_rpc" "$TARGET_BLOCKS")"
  ethw_log "Node 1 mined through ETH block ${node1_tip_height}; stopping miner before BTC head advance"
  stop_mining "$node1_rpc"
  sleep 2
  node1_tip_height="$(fetch_block_number "$node1_rpc")"
  node1_tip_hash="$(fetch_head_hash "$node1_rpc")"
  node1_balance_hex="$(printf '%s' "$(rpc_call "$node1_rpc" "eth_getBalance" "[\"${MINER_ETHERBASE}\",\"latest\"]")" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("result") or "0x0")')"

  regtest_log "Applying BTC owner top-up to advance the BTC head and increase current pass energy"
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
  boosted_energy="$(pass_energy_now "$pass_id")"
  ethw_log "Current pass energy after BTC head advance=${boosted_energy}"

  ethw_log "Starting fresh ETHW node 2 validator after BTC head advance"
  (
    cd "$ROOT_DIR"
    exec "${GETH_CMD[@]}" \
      --datadir "$NODE2_DATADIR" \
      --networkid "$NETWORK_ID" \
      --http \
      --http.addr "$NODE2_HTTP_ADDR" \
      --http.port "$NODE2_HTTP_PORT" \
      --http.api eth,net,web3,admin \
      --authrpc.addr "$NODE2_HTTP_ADDR" \
      --authrpc.port "$NODE2_AUTHRPC_PORT" \
      --port "$NODE2_P2P_PORT" \
      --nodiscover \
      --maxpeers 10 \
      --ethash.usdb \
      --ethash.usdb.rpcurl "http://127.0.0.1:${USDB_RPC_PORT}"
  ) >"$NODE2_LOG_FILE" 2>&1 &
  NODE2_PID=$!

  wait_chain_id "$node2_rpc"
  node1_enode="$(fetch_enode "$node1_rpc")"
  rpc_call "$node2_rpc" "admin_addPeer" "[\"${node1_enode}\"]" >/dev/null
  wait_peers "$node1_rpc" 1
  wait_peers "$node2_rpc" 1
  node2_tip_height="$(wait_block_height "$node2_rpc" "$node1_tip_height")"
  node2_tip_hash="$(fetch_head_hash "$node2_rpc")"

  if [[ "$node2_tip_hash" != "$node1_tip_hash" ]]; then
    echo "Node 2 synced to unexpected head hash: have ${node2_tip_hash} want ${node1_tip_hash}" >&2
    exit 1
  fi
  if (( node2_tip_height != node1_tip_height )); then
    echo "Node 2 synced to unexpected block height: have ${node2_tip_height} want ${node1_tip_height}" >&2
    exit 1
  fi

  blocks_file="$ETHW_WORK_DIR/stage1_blocks.json"
  collect_eth_blocks "$node1_rpc" "$node1_tip_height" "$blocks_file"
  ethw_log "Verifying node 1 historical rewards remain stable after BTC head advance and node 2 sync"
  verify_historical_stability "$blocks_file" "$MINER_ETHERBASE" "$node1_balance_hex" "$pass_id" "$initial_energy" "$boosted_energy"

  ethw_log "USDB + ETHW historical reward stability E2E succeeded."
  ethw_log "pass_id=${pass_id}, node1_height=${node1_tip_height}, historical_head=${node1_tip_hash}, initial_energy=${initial_energy}, current_energy=${boosted_energy}"
}

main "$@"
