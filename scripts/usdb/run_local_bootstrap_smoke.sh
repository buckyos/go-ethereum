#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
SOURCE_DAO_DIR=${SOURCE_DAO_DIR:-"$ROOT_DIR/../SourceDAO"}
USDB_CONFIG=${USDB_CONFIG:-"$ROOT_DIR/tools/config/usdb-local-chain.json"}
USDB_ARTIFACTS=${USDB_ARTIFACTS:-"$SOURCE_DAO_DIR/artifacts-usdb"}
SOURCE_DAO_BOOTSTRAP_PRIVATE_KEY=${SOURCE_DAO_BOOTSTRAP_PRIVATE_KEY:-4f3edf983ac636a65a842ce7c78d9aa706d3b113bce036f4f5bcaeaf3f4e6f54}
WORK_DIR=${WORK_DIR:-/tmp/usdb-local-bootstrap}
DATADIR=${DATADIR:-"$WORK_DIR/datadir"}
GENESIS_JSON=${GENESIS_JSON:-"$WORK_DIR/usdb-bootstrap-genesis.json"}
LOG_FILE=${LOG_FILE:-"$WORK_DIR/geth.log"}
HTTP_ADDR=${HTTP_ADDR:-127.0.0.1}
HTTP_PORT=${HTTP_PORT:-18545}
P2P_PORT=${P2P_PORT:-31303}
AUTHRPC_PORT=${AUTHRPC_PORT:-18551}
NETWORK_ID=${NETWORK_ID:-20260323}
USDB_CHAIN_MINER_ADDRESS=${USDB_CHAIN_MINER_ADDRESS:-0x0000000000000000000000000000000000001003}
RUN_SMOKE=${RUN_SMOKE:-1}
KEEP_RUNNING=${KEEP_RUNNING:-0}
RPC_WAIT_SECONDS=${RPC_WAIT_SECONDS:-45}
USDB_BOOTSTRAP_FAKE_POW=${USDB_BOOTSTRAP_FAKE_POW:-1}
USDB_BOOTSTRAP_USE_MOCK_INDEXER=${USDB_BOOTSTRAP_USE_MOCK_INDEXER:-$USDB_BOOTSTRAP_FAKE_POW}
USDB_BOOTSTRAP_PASS_ID=${USDB_BOOTSTRAP_PASS_ID:-3333333333333333333333333333333333333333333333333333333333333333i0}
USDB_BOOTSTRAP_INDEXER_PORT=${USDB_BOOTSTRAP_INDEXER_PORT:-$((HTTP_PORT + 1))}
USDB_INDEXER_RPC_URL=${USDB_INDEXER_RPC_URL:-}
MOCK_INDEXER_LOG=${MOCK_INDEXER_LOG:-"$WORK_DIR/mock-usdb-indexer.log"}

GETH_BIN=${GETH_BIN:-}
GETH_GO=${GETH_GO:-/usr/local/go/bin/go}

if [[ -n "$GETH_BIN" ]]; then
  GETH_CMD=("$GETH_BIN")
else
  GETH_CMD=("$GETH_GO" run -ldflags=-checklinkname=0 ./cmd/geth)
fi

POW_ARGS=()
case "$USDB_BOOTSTRAP_FAKE_POW" in
  1|true|TRUE|yes|YES)
    # The bootstrap smoke validates consensus metadata but not Ethash work.
    POW_ARGS=(--fakepow)
    ;;
  0|false|FALSE|no|NO)
    ;;
  *)
    echo "USDB_BOOTSTRAP_FAKE_POW must be a boolean, have: $USDB_BOOTSTRAP_FAKE_POW" >&2
    exit 1
    ;;
esac

case "$USDB_BOOTSTRAP_USE_MOCK_INDEXER" in
  1|true|TRUE|yes|YES)
    USE_MOCK_INDEXER=1
    ;;
  0|false|FALSE|no|NO)
    USE_MOCK_INDEXER=0
    ;;
  *)
    echo "USDB_BOOTSTRAP_USE_MOCK_INDEXER must be a boolean, have: $USDB_BOOTSTRAP_USE_MOCK_INDEXER" >&2
    exit 1
    ;;
esac

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
  if [[ -n "${MOCK_INDEXER_PID:-}" ]] && kill -0 "$MOCK_INDEXER_PID" 2>/dev/null; then
    kill "$MOCK_INDEXER_PID" 2>/dev/null || true
    wait "$MOCK_INDEXER_PID" 2>/dev/null || true
  fi
}

start_mock_indexer() {
  USDB_INDEXER_RPC_URL="http://127.0.0.1:${USDB_BOOTSTRAP_INDEXER_PORT}"
  python3 "$ROOT_DIR/scripts/usdb/mock_bootstrap_indexer.py" \
    --port "$USDB_BOOTSTRAP_INDEXER_PORT" \
    --pass-id "$USDB_BOOTSTRAP_PASS_ID" \
    >"$MOCK_INDEXER_LOG" 2>&1 &
  MOCK_INDEXER_PID=$!

  local deadline=$((SECONDS + RPC_WAIT_SECONDS))
  while (( SECONDS < deadline )); do
    if curl -sf "${USDB_INDEXER_RPC_URL}/health" >/dev/null; then
      return
    fi
    if ! kill -0 "$MOCK_INDEXER_PID" 2>/dev/null; then
      echo "Mock USDB indexer stopped before becoming ready; see $MOCK_INDEXER_LOG" >&2
      return 1
    fi
    sleep 1
  done
  echo "Timed out waiting for mock USDB indexer at $USDB_INDEXER_RPC_URL" >&2
  return 1
}

wait_for_rpc() {
  local deadline=$((SECONDS + RPC_WAIT_SECONDS))
  local expected_chain_id
  expected_chain_id=$(printf '0x%x' "$NETWORK_ID")
  while (( SECONDS < deadline )); do
    local response
    response=$(curl -sf \
      -H 'Content-Type: application/json' \
      --data '{"jsonrpc":"2.0","method":"eth_chainId","params":[],"id":1}' \
      "http://${HTTP_ADDR}:${HTTP_PORT}" || true)
    if [[ "$response" == *"\"result\":\"${expected_chain_id}\""* ]]; then
      return 0
    fi
    sleep 1
  done
  echo "Timed out waiting for USDB-chain RPC at http://${HTTP_ADDR}:${HTTP_PORT} with chainId ${expected_chain_id}" >&2
  return 1
}

mkdir -p "$WORK_DIR"
rm -rf "$DATADIR"
mkdir -p "$DATADIR"

echo "Generating USDB bootstrap genesis from $USDB_CONFIG"
run_geth dumpgenesis \
  --usdb \
  --usdb.bootstrap.config "$USDB_CONFIG" \
  --usdb.bootstrap.artifacts "$USDB_ARTIFACTS" \
  > "$GENESIS_JSON"

echo "Initializing datadir $DATADIR"
run_geth init --datadir "$DATADIR" "$GENESIS_JSON" >/dev/null

trap cleanup EXIT
if [[ "$USE_MOCK_INDEXER" == "1" ]]; then
  start_mock_indexer
elif [[ -z "$USDB_INDEXER_RPC_URL" ]]; then
  echo "USDB_INDEXER_RPC_URL is required when the test-only mock indexer is disabled" >&2
  exit 1
fi

echo "Starting local USDB geth node"
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
    "${POW_ARGS[@]}" \
    --miner.usdb.passid "$USDB_BOOTSTRAP_PASS_ID" \
    --miner.usdb-indexer.rpcurl "$USDB_INDEXER_RPC_URL" \
    --ethash.usdb-indexer.rpcurl "$USDB_INDEXER_RPC_URL" \
    --mine \
    --miner.threads 1 \
    --miner.etherbase "$USDB_CHAIN_MINER_ADDRESS"
) >"$LOG_FILE" 2>&1 &
GETH_PID=$!

wait_for_rpc

echo "USDB-chain RPC is ready at http://${HTTP_ADDR}:${HTTP_PORT}"
echo "geth log: $LOG_FILE"
echo "genesis: $GENESIS_JSON"
if [[ "$USE_MOCK_INDEXER" == "1" ]]; then
  echo "test-only indexer: $USDB_INDEXER_RPC_URL"
fi

if [[ "$RUN_SMOKE" == "1" ]]; then
  echo "Running SourceDAO bootstrap smoke"
  (
    cd "$SOURCE_DAO_DIR"
    # shellcheck source=/dev/null
    source "$HOME/.nvm/nvm.sh"
    nvm use 24 >/dev/null
    # SourceDAO owns this external env key; its value is the USDB-chain RPC endpoint.
    SOURCE_DAO_USDB_CONFIG="${SOURCE_DAO_USDB_CONFIG:-$SOURCE_DAO_DIR/tools/config/sourcedao-local.json}" \
    SOURCE_DAO_USDB_RPC_URL="http://${HTTP_ADDR}:${HTTP_PORT}" \
    SOURCE_DAO_BOOTSTRAP_PRIVATE_KEY="$SOURCE_DAO_BOOTSTRAP_PRIVATE_KEY" \
    npm run test:usdb:smoke
  )
fi

if [[ "$KEEP_RUNNING" == "1" ]]; then
  echo "USDB node is still running (pid=$GETH_PID). Press Ctrl-C to stop."
  wait "$GETH_PID"
fi
