#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
SOURCE_DAO_DIR=${SOURCE_DAO_DIR:-"$ROOT_DIR/../SourceDAO"}
USDB_CONFIG=${USDB_CONFIG:-"$ROOT_DIR/tools/config/usdb-local-chain.json"}
USDB_ARTIFACTS=${USDB_ARTIFACTS:-"$SOURCE_DAO_DIR/artifacts-usdb"}
WORK_DIR=${WORK_DIR:-/tmp/usdb-devnet-node}
DATADIR=${DATADIR:-"$WORK_DIR/datadir"}
GENESIS_JSON=${GENESIS_JSON:-"$WORK_DIR/usdb-bootstrap-genesis.json"}
LOG_FILE=${LOG_FILE:-"$WORK_DIR/geth.log"}

NODE_ROLE=${NODE_ROLE:-full} # bootnode | miner | full
NETWORK_ID=${NETWORK_ID:-20260323}
HTTP_ADDR=${HTTP_ADDR:-127.0.0.1}
HTTP_PORT=${HTTP_PORT:-28545}
P2P_PORT=${P2P_PORT:-32303}
AUTHRPC_PORT=${AUTHRPC_PORT:-28551}
USDB_CHAIN_MINER_ADDRESS=${USDB_CHAIN_MINER_ADDRESS:-0x0000000000000000000000000000000000001003}
USDB_INDEXER_RPC_URL=${USDB_INDEXER_RPC_URL:-}
USDB_PASS_ID=${USDB_PASS_ID:-}
USDB_QUERY_TIMEOUT=${USDB_QUERY_TIMEOUT:-5s}
RPC_WAIT_SECONDS=${RPC_WAIT_SECONDS:-45}
KEEP_RUNNING=${KEEP_RUNNING:-1}
REINIT=${REINIT:-0}

BOOTNODES=${BOOTNODES:-}
BOOTNODES_FILE=${BOOTNODES_FILE:-}
STATIC_NODES_FILE=${STATIC_NODES_FILE:-}

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
  if [[ -n "${GETH_PID:-}" ]] && kill -0 "$GETH_PID" 2>/dev/null; then
    kill "$GETH_PID" 2>/dev/null || true
    wait "$GETH_PID" 2>/dev/null || true
  fi
}

wait_for_chain() {
  local expected
  expected=$(printf '0x%x' "$NETWORK_ID")
  local deadline=$((SECONDS + RPC_WAIT_SECONDS))
  while (( SECONDS < deadline )); do
    local response
    response=$(curl -sf -H 'Content-Type: application/json' \
      --data '{"jsonrpc":"2.0","method":"eth_chainId","params":[],"id":1}' \
      "http://${HTTP_ADDR}:${HTTP_PORT}" || true)
    if [[ "$response" == *"\"result\":\"${expected}\""* ]]; then
      return 0
    fi
    sleep 1
  done
  echo "Timed out waiting for chainId ${expected} on http://${HTTP_ADDR}:${HTTP_PORT}" >&2
  return 1
}

fetch_enode() {
  curl -sf -H 'Content-Type: application/json' \
    --data '{"jsonrpc":"2.0","method":"admin_nodeInfo","params":[],"id":1}' \
    "http://${HTTP_ADDR}:${HTTP_PORT}" \
    | sed -n 's/.*"enode":"\([^"]*\)".*/\1/p'
}

resolve_bootnodes() {
  if [[ -n "$BOOTNODES" ]]; then
    printf '%s\n' "$BOOTNODES"
    return 0
  fi
  if [[ -n "$BOOTNODES_FILE" ]]; then
    tr -d '\n' <"$BOOTNODES_FILE"
    printf '\n'
    return 0
  fi
  printf '\n'
}

case "$NODE_ROLE" in
  bootnode|miner|full) ;;
  *)
    echo "Unsupported NODE_ROLE=$NODE_ROLE (expected bootnode|miner|full)" >&2
    exit 1
    ;;
esac

if [[ -z "$USDB_INDEXER_RPC_URL" ]]; then
  echo "USDB_INDEXER_RPC_URL is required for USDB consensus verification" >&2
  exit 1
fi
if [[ "$NODE_ROLE" == "miner" && -z "$USDB_PASS_ID" ]]; then
  echo "USDB_PASS_ID is required when NODE_ROLE=miner" >&2
  exit 1
fi

mkdir -p "$WORK_DIR"
mkdir -p "$DATADIR"

if [[ ! -f "$GENESIS_JSON" || "$REINIT" == "1" ]]; then
  echo "Generating bootstrap genesis at $GENESIS_JSON"
  run_geth dumpgenesis \
    --usdb \
    --usdb.bootstrap.config "$USDB_CONFIG" \
    --usdb.bootstrap.artifacts "$USDB_ARTIFACTS" \
    > "$GENESIS_JSON"
fi

if [[ "$REINIT" == "1" ]]; then
  rm -rf "$DATADIR"
  mkdir -p "$DATADIR"
fi

if [[ ! -f "$DATADIR/geth/chaindata/CURRENT" ]]; then
  echo "Initializing datadir $DATADIR"
  run_geth init --datadir "$DATADIR" "$GENESIS_JSON" >/dev/null
fi

if [[ -n "$STATIC_NODES_FILE" ]]; then
  mkdir -p "$DATADIR/geth"
  cp "$STATIC_NODES_FILE" "$DATADIR/geth/static-nodes.json"
fi

BOOTNODES_ARG=$(resolve_bootnodes)

declare -a role_args
case "$NODE_ROLE" in
  bootnode)
    role_args=(--maxpeers 25)
    ;;
  miner)
    role_args=(
      --mine
      --miner.threads 1
      --miner.etherbase "$USDB_CHAIN_MINER_ADDRESS"
      --miner.usdb.passid "$USDB_PASS_ID"
      --miner.usdb-indexer.rpcurl "$USDB_INDEXER_RPC_URL"
      --miner.usdb-indexer.timeout "$USDB_QUERY_TIMEOUT"
      --maxpeers 25
    )
    ;;
  full)
    role_args=(--maxpeers 25)
    ;;
esac

declare -a bootnode_args
if [[ -n "$BOOTNODES_ARG" ]]; then
  bootnode_args=(--bootnodes "$BOOTNODES_ARG")
fi

trap cleanup EXIT

echo "Starting USDB devnet node role=$NODE_ROLE"
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
    --ipcpath "$DATADIR/geth.ipc" \
    --ethash.usdb-indexer.rpcurl "$USDB_INDEXER_RPC_URL" \
    --ethash.usdb-indexer.timeout "$USDB_QUERY_TIMEOUT" \
    "${bootnode_args[@]}" \
    "${role_args[@]}"
) >"$LOG_FILE" 2>&1 &
GETH_PID=$!

wait_for_chain

echo "USDB devnet node is ready."
echo "role:    $NODE_ROLE"
echo "rpc:     http://${HTTP_ADDR}:${HTTP_PORT}"
echo "p2p:     $P2P_PORT"
echo "log:     $LOG_FILE"
echo "genesis: $GENESIS_JSON"

if [[ "$NODE_ROLE" == "bootnode" ]]; then
  ENODE=$(fetch_enode || true)
  if [[ -n "$ENODE" ]]; then
    echo "enode:   $ENODE"
  fi
fi

if [[ "$KEEP_RUNNING" == "1" ]]; then
  echo "Node is still running. Press Ctrl-C to stop."
  wait "$GETH_PID"
fi
