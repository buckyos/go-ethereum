#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
SOURCE_DAO_DIR=${SOURCE_DAO_DIR:-"$ROOT_DIR/../SourceDAO"}
USDB_CONFIG=${USDB_CONFIG:-"$ROOT_DIR/tools/config/usdb-local-chain.json"}
WORK_DIR=${WORK_DIR:-/tmp/usdb-local-two-node}
GENESIS_JSON=${GENESIS_JSON:-"$WORK_DIR/usdb-bootstrap-genesis.json"}
NETWORK_ID=${NETWORK_ID:-20260323}
RPC_WAIT_SECONDS=${RPC_WAIT_SECONDS:-45}
KEEP_RUNNING=${KEEP_RUNNING:-1}
RUN_SMOKE=${RUN_SMOKE:-0}

NODE1_DATADIR=${NODE1_DATADIR:-"$WORK_DIR/node1"}
NODE1_HTTP_ADDR=${NODE1_HTTP_ADDR:-127.0.0.1}
NODE1_HTTP_PORT=${NODE1_HTTP_PORT:-18545}
NODE1_P2P_PORT=${NODE1_P2P_PORT:-31303}
NODE1_AUTHRPC_PORT=${NODE1_AUTHRPC_PORT:-18551}
NODE1_USDB_CHAIN_MINER_ADDRESS=${NODE1_USDB_CHAIN_MINER_ADDRESS:-0x0000000000000000000000000000000000001003}
NODE1_LOG=${NODE1_LOG:-"$WORK_DIR/node1.log"}

NODE2_DATADIR=${NODE2_DATADIR:-"$WORK_DIR/node2"}
NODE2_HTTP_ADDR=${NODE2_HTTP_ADDR:-127.0.0.1}
NODE2_HTTP_PORT=${NODE2_HTTP_PORT:-18546}
NODE2_P2P_PORT=${NODE2_P2P_PORT:-31304}
NODE2_AUTHRPC_PORT=${NODE2_AUTHRPC_PORT:-18552}
NODE2_LOG=${NODE2_LOG:-"$WORK_DIR/node2.log"}

GETH_BIN=${GETH_BIN:-}
GETH_GO=${GETH_GO:-/usr/local/go/bin/go}

if [[ -n "$GETH_BIN" ]]; then
  GETH_CMD=("$GETH_BIN")
else
  GETH_CMD=("$GETH_GO" run -ldflags=-checklinkname=0 ./cmd/geth)
fi

run_geth() {
  (
    cd "$ROOT_DIR"
    "${GETH_CMD[@]}" "$@"
  )
}

cleanup() {
  for pid_var in NODE1_PID NODE2_PID; do
    local pid=${!pid_var:-}
    if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
      kill "$pid" 2>/dev/null || true
      wait "$pid" 2>/dev/null || true
    fi
  done
}

rpc_call() {
  local url=$1
  local payload=$2
  curl -sf -H 'Content-Type: application/json' --data "$payload" "$url"
}

wait_for_chain() {
  local url=$1
  local expected
  expected=$(printf '0x%x' "$NETWORK_ID")
  local deadline=$((SECONDS + RPC_WAIT_SECONDS))
  while (( SECONDS < deadline )); do
    local response
    response=$(rpc_call "$url" '{"jsonrpc":"2.0","method":"eth_chainId","params":[],"id":1}' || true)
    if [[ "$response" == *"\"result\":\"${expected}\""* ]]; then
      return 0
    fi
    sleep 1
  done
  echo "Timed out waiting for chainId ${expected} on ${url}" >&2
  return 1
}

extract_json_result_string() {
  sed -n 's/.*"result":"\([^"]*\)".*/\1/p'
}

fetch_enode() {
  local url=$1
  local deadline=$((SECONDS + RPC_WAIT_SECONDS))
  while (( SECONDS < deadline )); do
    local response
    response=$(rpc_call "$url" '{"jsonrpc":"2.0","method":"admin_nodeInfo","params":[],"id":1}' || true)
    local enode
    enode=$(printf '%s' "$response" | sed -n 's/.*"enode":"\([^"]*\)".*/\1/p')
    if [[ -n "$enode" ]]; then
      printf '%s\n' "$enode"
      return 0
    fi
    sleep 1
  done
  echo "Timed out waiting for admin_nodeInfo on ${url}" >&2
  return 1
}

wait_for_peers() {
  local url=$1
  local min_peers=$2
  local deadline=$((SECONDS + RPC_WAIT_SECONDS))
  while (( SECONDS < deadline )); do
    local response value count
    response=$(rpc_call "$url" '{"jsonrpc":"2.0","method":"net_peerCount","params":[],"id":1}' || true)
    value=$(printf '%s' "$response" | extract_json_result_string)
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

mkdir -p "$WORK_DIR"
rm -rf "$NODE1_DATADIR" "$NODE2_DATADIR"
mkdir -p "$NODE1_DATADIR" "$NODE2_DATADIR"

echo "Generating shared USDB bootstrap genesis from $USDB_CONFIG"
run_geth dumpgenesis \
  --usdb \
  --usdb.bootstrap.config "$USDB_CONFIG" \
  > "$GENESIS_JSON"

echo "Initializing node datadirs"
run_geth init --datadir "$NODE1_DATADIR" "$GENESIS_JSON" >/dev/null
run_geth init --datadir "$NODE2_DATADIR" "$GENESIS_JSON" >/dev/null

trap cleanup EXIT

echo "Starting node 1 (mining)"
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
    --ipcpath "$NODE1_DATADIR/geth.ipc" \
    --nodiscover \
    --mine \
    --miner.threads 1 \
    --miner.etherbase "$NODE1_USDB_CHAIN_MINER_ADDRESS"
) >"$NODE1_LOG" 2>&1 &
NODE1_PID=$!

echo "Starting node 2"
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
    --ipcpath "$NODE2_DATADIR/geth.ipc" \
    --nodiscover \
    --maxpeers 10
) >"$NODE2_LOG" 2>&1 &
NODE2_PID=$!

NODE1_RPC="http://${NODE1_HTTP_ADDR}:${NODE1_HTTP_PORT}"
NODE2_RPC="http://${NODE2_HTTP_ADDR}:${NODE2_HTTP_PORT}"

wait_for_chain "$NODE1_RPC"
wait_for_chain "$NODE2_RPC"

NODE1_ENODE=$(fetch_enode "$NODE1_RPC")
echo "Node 1 enode: $NODE1_ENODE"

rpc_call "$NODE2_RPC" "{\"jsonrpc\":\"2.0\",\"method\":\"admin_addPeer\",\"params\":[\"${NODE1_ENODE}\"],\"id\":1}" >/dev/null

wait_for_peers "$NODE1_RPC" 1
wait_for_peers "$NODE2_RPC" 1

NODE1_PEERS=$(rpc_call "$NODE1_RPC" '{"jsonrpc":"2.0","method":"net_peerCount","params":[],"id":1}' | extract_json_result_string)
NODE2_PEERS=$(rpc_call "$NODE2_RPC" '{"jsonrpc":"2.0","method":"net_peerCount","params":[],"id":1}' | extract_json_result_string)

echo "USDB two-node network is ready."
echo "genesis:   $GENESIS_JSON"
echo "node1 rpc: $NODE1_RPC"
echo "node1 p2p: $NODE1_P2P_PORT"
echo "node1 log: $NODE1_LOG"
echo "node2 rpc: $NODE2_RPC"
echo "node2 p2p: $NODE2_P2P_PORT"
echo "node2 log: $NODE2_LOG"
echo "peerCount node1=${NODE1_PEERS} node2=${NODE2_PEERS}"

if [[ "$RUN_SMOKE" == "1" ]]; then
  echo "Running SourceDAO bootstrap smoke against node 1"
  (
    cd "$SOURCE_DAO_DIR"
    source "$HOME/.nvm/nvm.sh"
    nvm use 24 >/dev/null
    # SourceDAO owns this external env key; its value is the USDB-chain RPC endpoint.
    SOURCE_DAO_USDB_CONFIG="$USDB_CONFIG" \
    SOURCE_DAO_USDB_RPC_URL="$NODE1_RPC" \
    npm run test:usdb:smoke
  )
fi

if [[ "$KEEP_RUNNING" == "1" ]]; then
  echo "Both nodes are still running. Press Ctrl-C to stop."
  wait "$NODE1_PID"
fi
