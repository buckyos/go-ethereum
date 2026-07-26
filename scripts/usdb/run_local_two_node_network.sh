#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
SOURCE_DAO_DIR=${SOURCE_DAO_DIR:-"$ROOT_DIR/../SourceDAO"}
USDB_CONFIG=${USDB_CONFIG:-"$ROOT_DIR/tools/config/usdb-local-chain.json"}
USDB_ARTIFACTS=${USDB_ARTIFACTS:-"$SOURCE_DAO_DIR/artifacts-usdb"}
SOURCE_DAO_CONFIG=${SOURCE_DAO_CONFIG:-"$SOURCE_DAO_DIR/tools/config/sourcedao-local.json"}
SOURCE_DAO_FULL_CONFIG=${SOURCE_DAO_FULL_CONFIG:-"$SOURCE_DAO_DIR/tools/config/sourcedao-bootstrap-full.example.json"}
SOURCE_DAO_BOOTSTRAP_PRIVATE_KEY=${SOURCE_DAO_BOOTSTRAP_PRIVATE_KEY:-4f3edf983ac636a65a842ce7c78d9aa706d3b113bce036f4f5bcaeaf3f4e6f54}
WORK_DIR=${WORK_DIR:-/tmp/usdb-local-two-node}
GENESIS_JSON=${GENESIS_JSON:-"$WORK_DIR/usdb-bootstrap-genesis.json"}
NETWORK_ID=${NETWORK_ID:-20260323}
RPC_WAIT_SECONDS=${RPC_WAIT_SECONDS:-45}
KEEP_RUNNING=${KEEP_RUNNING:-1}
RUN_SMOKE=${RUN_SMOKE:-0}
RUN_FULL_BOOTSTRAP=${RUN_FULL_BOOTSTRAP:-0}
START_JOINER_AFTER_BOOTSTRAP=${START_JOINER_AFTER_BOOTSTRAP:-$RUN_FULL_BOOTSTRAP}
RESTART_NODE1_AFTER_BOOTSTRAP=${RESTART_NODE1_AFTER_BOOTSTRAP:-$RUN_FULL_BOOTSTRAP}
BOOTSTRAP_STATE_FILE=${BOOTSTRAP_STATE_FILE:-"$WORK_DIR/sourcedao-bootstrap-state.json"}
BOOTSTRAP_REPLAY_STATE_FILE=${BOOTSTRAP_REPLAY_STATE_FILE:-"$WORK_DIR/sourcedao-bootstrap-replay-state.json"}
NODE1_VALIDATION_FILE=${NODE1_VALIDATION_FILE:-"$WORK_DIR/node1-bootstrap-validation.json"}
NODE2_VALIDATION_FILE=${NODE2_VALIDATION_FILE:-"$WORK_DIR/node2-bootstrap-validation.json"}
FEE_PROBE_FILE=${FEE_PROBE_FILE:-"$WORK_DIR/usdb-fee-split-probe.json"}
FEE_PROBE_TIMEOUT_MS=${FEE_PROBE_TIMEOUT_MS:-600000}
USDB_BOOTSTRAP_FAKE_POW=${USDB_BOOTSTRAP_FAKE_POW:-1}
USDB_BOOTSTRAP_FAKE_POW_DELAY=${USDB_BOOTSTRAP_FAKE_POW_DELAY:-1s}
USDB_BOOTSTRAP_POST_BOOTSTRAP_FAKE_POW_DELAY=${USDB_BOOTSTRAP_POST_BOOTSTRAP_FAKE_POW_DELAY:-$USDB_BOOTSTRAP_FAKE_POW_DELAY}
USDB_BOOTSTRAP_USE_MOCK_INDEXER=${USDB_BOOTSTRAP_USE_MOCK_INDEXER:-$USDB_BOOTSTRAP_FAKE_POW}
USDB_BOOTSTRAP_PASS_ID=${USDB_BOOTSTRAP_PASS_ID:-3333333333333333333333333333333333333333333333333333333333333333i0}
USDB_QUERY_TIMEOUT=${USDB_QUERY_TIMEOUT:-5s}
USDB_INDEXER_RPC_URL=${USDB_INDEXER_RPC_URL:-}
MOCK_INDEXER_LOG=${MOCK_INDEXER_LOG:-"$WORK_DIR/mock-usdb-indexer.log"}

NODE1_DATADIR=${NODE1_DATADIR:-"$WORK_DIR/node1"}
NODE1_HTTP_ADDR=${NODE1_HTTP_ADDR:-127.0.0.1}
NODE1_HTTP_PORT=${NODE1_HTTP_PORT:-18545}
NODE1_P2P_PORT=${NODE1_P2P_PORT:-31303}
NODE1_AUTHRPC_PORT=${NODE1_AUTHRPC_PORT:-18551}
NODE1_USDB_CHAIN_MINER_ADDRESS=${NODE1_USDB_CHAIN_MINER_ADDRESS:-0x0000000000000000000000000000000000001003}
USDB_PROFILE_REWARD_RECIPIENT=${USDB_PROFILE_REWARD_RECIPIENT:-0x1111111111111111111111111111111111111111}
USDB_PROFILE_TOTAL_MINER_BTC_SATS=${USDB_PROFILE_TOTAL_MINER_BTC_SATS:-100000000}
USDB_PROFILE_COLLAB_CONTRIBUTION=${USDB_PROFILE_COLLAB_CONTRIBUTION:-100}
NODE1_LOG=${NODE1_LOG:-"$WORK_DIR/node1.log"}

NODE2_DATADIR=${NODE2_DATADIR:-"$WORK_DIR/node2"}
NODE2_HTTP_ADDR=${NODE2_HTTP_ADDR:-127.0.0.1}
NODE2_HTTP_PORT=${NODE2_HTTP_PORT:-18546}
NODE2_P2P_PORT=${NODE2_P2P_PORT:-31304}
NODE2_AUTHRPC_PORT=${NODE2_AUTHRPC_PORT:-18552}
NODE2_LOG=${NODE2_LOG:-"$WORK_DIR/node2.log"}
USDB_BOOTSTRAP_INDEXER_PORT=${USDB_BOOTSTRAP_INDEXER_PORT:-$((NODE2_HTTP_PORT + 1))}

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
    # This local network validates USDB consensus metadata, not Ethash work.
    USE_FAKE_POW=1
    POW_ARGS=(--fakepow --fakepow.delay "$USDB_BOOTSTRAP_FAKE_POW_DELAY")
    ;;
  0|false|FALSE|no|NO)
    USE_FAKE_POW=0
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

normalize_boolean() {
  local name=$1
  local value=$2
  case "$value" in
    1|true|TRUE|yes|YES)
      printf '1\n'
      ;;
    0|false|FALSE|no|NO)
      printf '0\n'
      ;;
    *)
      echo "$name must be a boolean, have: $value" >&2
      return 1
      ;;
  esac
}

RUN_FULL_BOOTSTRAP=$(normalize_boolean RUN_FULL_BOOTSTRAP "$RUN_FULL_BOOTSTRAP")
START_JOINER_AFTER_BOOTSTRAP=$(normalize_boolean START_JOINER_AFTER_BOOTSTRAP "$START_JOINER_AFTER_BOOTSTRAP")
RESTART_NODE1_AFTER_BOOTSTRAP=$(normalize_boolean RESTART_NODE1_AFTER_BOOTSTRAP "$RESTART_NODE1_AFTER_BOOTSTRAP")

if [[ "$RUN_FULL_BOOTSTRAP" == "1" && "$RUN_SMOKE" == "1" ]]; then
  echo "RUN_FULL_BOOTSTRAP and RUN_SMOKE are separate test modes and cannot both be enabled" >&2
  exit 1
fi
if [[ "$START_JOINER_AFTER_BOOTSTRAP" == "1" && "$RUN_FULL_BOOTSTRAP" != "1" ]]; then
  echo "START_JOINER_AFTER_BOOTSTRAP requires RUN_FULL_BOOTSTRAP=1" >&2
  exit 1
fi
if [[ "$RESTART_NODE1_AFTER_BOOTSTRAP" == "1" && "$RUN_FULL_BOOTSTRAP" != "1" ]]; then
  echo "RESTART_NODE1_AFTER_BOOTSTRAP requires RUN_FULL_BOOTSTRAP=1" >&2
  exit 1
fi
if [[ "$RUN_FULL_BOOTSTRAP" == "1" ]] && ! command -v jq >/dev/null; then
  echo "jq is required for the full-bootstrap lifecycle assertions" >&2
  exit 1
fi

run_geth() {
  (
    cd "$ROOT_DIR"
    "${GETH_CMD[@]}" "$@"
  )
}

cleanup() {
  for pid_var in NODE1_PID NODE2_PID MOCK_INDEXER_PID; do
    local pid=${!pid_var:-}
    if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
      kill "$pid" 2>/dev/null || true
      wait "$pid" 2>/dev/null || true
    fi
  done
}

stop_named_process() {
  local pid_var=$1
  local pid=${!pid_var:-}
  if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
    kill "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
  fi
  printf -v "$pid_var" ''
}

start_mock_indexer() {
  USDB_INDEXER_RPC_URL="http://127.0.0.1:${USDB_BOOTSTRAP_INDEXER_PORT}"
  python3 "$ROOT_DIR/scripts/usdb/mock_bootstrap_indexer.py" \
    --port "$USDB_BOOTSTRAP_INDEXER_PORT" \
    --pass-id "$USDB_BOOTSTRAP_PASS_ID" \
    --usdb-main "$USDB_PROFILE_REWARD_RECIPIENT" \
    --total-miner-btc-sats "$USDB_PROFILE_TOTAL_MINER_BTC_SATS" \
    --collab-contribution "$USDB_PROFILE_COLLAB_CONTRIBUTION" \
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

wait_for_height() {
  local url=$1
  local minimum=$2
  local deadline=$((SECONDS + RPC_WAIT_SECONDS))
  while (( SECONDS < deadline )); do
    local response value height
    response=$(rpc_call "$url" '{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}' || true)
    value=$(printf '%s' "$response" | extract_json_result_string)
    if [[ -n "$value" ]]; then
      height=$((value))
      if (( height >= minimum )); then
        printf '%s\n' "$value"
        return 0
      fi
    fi
    sleep 1
  done
  echo "Timed out waiting for block height ${minimum} on ${url}" >&2
  return 1
}

block_hash_at() {
  local url=$1
  local height=$2
  rpc_call "$url" \
    "{\"jsonrpc\":\"2.0\",\"method\":\"eth_getBlockByNumber\",\"params\":[\"${height}\",false],\"id\":1}" \
    | sed -n 's/.*"hash":"\([^"]*\)".*/\1/p'
}

block_identity_at() {
  local url=$1
  local height=$2
  rpc_call "$url" \
    "{\"jsonrpc\":\"2.0\",\"method\":\"eth_getBlockByNumber\",\"params\":[\"${height}\",false],\"id\":1}" \
    | jq -r '[.result.hash // "", .result.stateRoot // ""] | @tsv'
}

assert_block_identity() {
  local label=$1
  local url=$2
  local height=$3
  local expected_hash=$4
  local expected_state_root=$5
  local height_decimal actual_hash actual_state_root

  height_decimal=$((height))
  wait_for_height "$url" "$height_decimal" >/dev/null
  read -r actual_hash actual_state_root < <(block_identity_at "$url" "$height")
  if [[ -z "$actual_hash" || "$actual_hash" != "$expected_hash" || "$actual_state_root" != "$expected_state_root" ]]; then
    echo "${label} checkpoint mismatch at ${height}: hash=${actual_hash:-missing} stateRoot=${actual_state_root:-missing}" >&2
    echo "Expected hash=${expected_hash} stateRoot=${expected_state_root}" >&2
    return 1
  fi
  echo "${label} checkpoint: height=${height} hash=${actual_hash} stateRoot=${actual_state_root}"
}

assert_cross_node_checkpoint() {
  local label=$1
  local target_height target_decimal node1_hash node2_hash

  target_height=$(wait_for_height "$NODE1_RPC" 1)
  target_decimal=$((target_height))
  wait_for_height "$NODE2_RPC" "$target_decimal" >/dev/null
  node1_hash=$(block_hash_at "$NODE1_RPC" "$target_height")
  node2_hash=$(block_hash_at "$NODE2_RPC" "$target_height")

  if [[ -z "$node1_hash" || "$node1_hash" != "$node2_hash" ]]; then
    echo "Cross-node checkpoint mismatch at ${target_height}: node1=${node1_hash:-missing}, node2=${node2_hash:-missing}" >&2
    return 1
  fi
  echo "Cross-node checkpoint ${label}: height=${target_height} hash=${node1_hash}"
}

start_node1() {
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
      "${POW_ARGS[@]}" \
      --miner.usdb.passid "$USDB_BOOTSTRAP_PASS_ID" \
      --miner.usdb-indexer.rpcurl "$USDB_INDEXER_RPC_URL" \
      --miner.usdb-indexer.timeout "$USDB_QUERY_TIMEOUT" \
      --ethash.usdb-indexer.rpcurl "$USDB_INDEXER_RPC_URL" \
      --ethash.usdb-indexer.timeout "$USDB_QUERY_TIMEOUT" \
      --mine \
      --miner.threads 1 \
      --miner.etherbase "$NODE1_USDB_CHAIN_MINER_ADDRESS"
  ) >>"$NODE1_LOG" 2>&1 &
  NODE1_PID=$!
}

start_node2() {
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
      --syncmode full \
      "${POW_ARGS[@]}" \
      --ethash.usdb-indexer.rpcurl "$USDB_INDEXER_RPC_URL" \
      --ethash.usdb-indexer.timeout "$USDB_QUERY_TIMEOUT" \
      --maxpeers 10
  ) >>"$NODE2_LOG" 2>&1 &
  NODE2_PID=$!
}

accelerate_fake_pow_after_bootstrap() {
  if [[ "$USE_FAKE_POW" != "1" ]] ||
     [[ "$USDB_BOOTSTRAP_POST_BOOTSTRAP_FAKE_POW_DELAY" == "$USDB_BOOTSTRAP_FAKE_POW_DELAY" ]]; then
    return
  fi
  echo "Restarting node 1 with post-bootstrap fake-PoW delay $USDB_BOOTSTRAP_POST_BOOTSTRAP_FAKE_POW_DELAY"
  stop_named_process NODE1_PID
  POW_ARGS=(--fakepow --fakepow.delay "$USDB_BOOTSTRAP_POST_BOOTSTRAP_FAKE_POW_DELAY")
  start_node1
  wait_for_chain "$NODE1_RPC"
}

connect_nodes() {
  local node1_enode
  node1_enode=$(fetch_enode "$NODE1_RPC")
  echo "Node 1 enode: $node1_enode"
  rpc_call "$NODE2_RPC" \
    "{\"jsonrpc\":\"2.0\",\"method\":\"admin_addPeer\",\"params\":[\"${node1_enode}\"],\"id\":1}" \
    >/dev/null
  wait_for_peers "$NODE1_RPC" 1
  wait_for_peers "$NODE2_RPC" 1
}

run_source_dao_full_bootstrap() {
  local rpc_url=$1
  local state_file=$2
  (
    cd "$SOURCE_DAO_DIR"
    source "$HOME/.nvm/nvm.sh"
    nvm use 24 >/dev/null
    SOURCE_DAO_BOOTSTRAP_PRIVATE_KEY="$SOURCE_DAO_BOOTSTRAP_PRIVATE_KEY" \
      npx tsx scripts/usdb_bootstrap_full.ts \
        --config "$SOURCE_DAO_FULL_CONFIG" \
        --rpc-url "$rpc_url" \
        --state-file "$state_file"
  )
}

run_source_dao_validation() {
  local rpc_url=$1
  local state_file=$2
  local output_file=$3
  (
    cd "$SOURCE_DAO_DIR"
    source "$HOME/.nvm/nvm.sh"
    nvm use 24 >/dev/null
    npm run validate:bootstrap -- \
      --config "$SOURCE_DAO_FULL_CONFIG" \
      --rpc-url "$rpc_url" \
      --state-file "$state_file" \
      --output "$output_file" \
      --strict
  )
}

run_source_dao_fee_probe() {
  local rpc_url=$1
  local output_file=$2
  local dividend_address fee_split_block
  dividend_address=$(jq -r '.predeploys.dividend.address' "$USDB_CONFIG")
  fee_split_block=$(jq -r '.dividendFeeSplitBlock' "$USDB_CONFIG")
  if [[ -z "$dividend_address" || "$dividend_address" == "null" ]]; then
    echo "USDB config has no Dividend address" >&2
    return 1
  fi
  if [[ ! "$fee_split_block" =~ ^[1-9][0-9]*$ ]]; then
    echo "USDB config has invalid Dividend fee-split block: $fee_split_block" >&2
    return 1
  fi
  (
    cd "$SOURCE_DAO_DIR"
    source "$HOME/.nvm/nvm.sh"
    nvm use 24 >/dev/null
    SOURCE_DAO_BOOTSTRAP_PRIVATE_KEY="$SOURCE_DAO_BOOTSTRAP_PRIVATE_KEY" \
      npx tsx scripts/usdb_fee_split_probe.ts \
        --rpc-url "$rpc_url" \
        --dividend-address "$dividend_address" \
        --reward-recipient "$USDB_PROFILE_REWARD_RECIPIENT" \
        --fee-split-block "$fee_split_block" \
        --timeout-ms "$FEE_PROBE_TIMEOUT_MS" \
        --output "$output_file"
  )
  if ! jq -e '
    .status == "ok"
    and (.probe.daoFee | tonumber) > 0
    and (.probe.emission | tonumber) > 0
    and (.ledgerSync.pendingBefore | tonumber) > 0
    and .ledgerSync.pendingAfter == .ledgerSync.daoFee
  ' "$output_file" >/dev/null; then
    echo "USDB fee split probe did not produce the expected accounting result" >&2
    return 1
  fi
}

assert_idempotent_replay_state() {
  local state_file=$1
  if ! jq -e '
    .status == "completed"
    and .scope == "full"
    and ([.operations[] | select(.status == "completed")] | length == 0)
    and ([.operations[] | select(.status == "error")] | length == 0)
  ' "$state_file" >/dev/null; then
    echo "Full-bootstrap replay was not idempotent; inspect $state_file" >&2
    return 1
  fi
  echo "Full-bootstrap replay produced no completed or failed operations."
}

assert_validation_summaries_match() {
  local node1_summary node2_summary
  node1_summary=$(jq -S -c \
    '{status, chainId, mode, daoAddress, bootstrapAdmin, modules}' \
    "$NODE1_VALIDATION_FILE")
  node2_summary=$(jq -S -c \
    '{status, chainId, mode, daoAddress, bootstrapAdmin, modules}' \
    "$NODE2_VALIDATION_FILE")
  if [[ "$node1_summary" != "$node2_summary" ]]; then
    echo "Strict bootstrap validation summaries differ between node 1 and node 2" >&2
    diff -u \
      <(jq -S '{status, chainId, mode, daoAddress, bootstrapAdmin, modules}' "$NODE1_VALIDATION_FILE") \
      <(jq -S '{status, chainId, mode, daoAddress, bootstrapAdmin, modules}' "$NODE2_VALIDATION_FILE") \
      >&2 || true
    return 1
  fi
  echo "Node 1 and node 2 strict bootstrap validation summaries match."
}

run_full_bootstrap_lifecycle() {
  local bootstrap_height bootstrap_hash bootstrap_state_root

  echo "Running SourceDAO full bootstrap against node 1"
  run_source_dao_full_bootstrap "$NODE1_RPC" "$BOOTSTRAP_STATE_FILE"
  run_source_dao_validation "$NODE1_RPC" "$BOOTSTRAP_STATE_FILE" "$NODE1_VALIDATION_FILE"
  accelerate_fake_pow_after_bootstrap
  echo "Running UIP-0011 fee split and Dividend ledger-sync probe"
  run_source_dao_fee_probe "$NODE1_RPC" "$FEE_PROBE_FILE"
  run_source_dao_validation "$NODE1_RPC" "$BOOTSTRAP_STATE_FILE" "$NODE1_VALIDATION_FILE"

  bootstrap_height=$(wait_for_height "$NODE1_RPC" 1)
  read -r bootstrap_hash bootstrap_state_root < <(block_identity_at "$NODE1_RPC" "$bootstrap_height")
  if [[ -z "$bootstrap_hash" || -z "$bootstrap_state_root" ]]; then
    echo "Failed to capture the post-bootstrap block identity at $bootstrap_height" >&2
    return 1
  fi
  echo "Captured post-bootstrap checkpoint: height=${bootstrap_height} hash=${bootstrap_hash} stateRoot=${bootstrap_state_root}"

  if [[ "$RESTART_NODE1_AFTER_BOOTSTRAP" == "1" ]]; then
    echo "Restarting node 1 with its existing datadir"
    stop_named_process NODE1_PID
    start_node1
    wait_for_chain "$NODE1_RPC"
    assert_block_identity \
      "Node 1 restart" \
      "$NODE1_RPC" \
      "$bootstrap_height" \
      "$bootstrap_hash" \
      "$bootstrap_state_root"
    run_source_dao_validation "$NODE1_RPC" "$BOOTSTRAP_STATE_FILE" "$NODE1_VALIDATION_FILE"
  fi

  if [[ "$START_JOINER_AFTER_BOOTSTRAP" == "1" ]]; then
    echo "Starting fresh node 2 after full bootstrap"
    start_node2
    wait_for_chain "$NODE2_RPC"
    connect_nodes
  fi

  assert_block_identity \
    "Node 2 historical replay" \
    "$NODE2_RPC" \
    "$bootstrap_height" \
    "$bootstrap_hash" \
    "$bootstrap_state_root"
  run_source_dao_validation "$NODE2_RPC" "$BOOTSTRAP_STATE_FILE" "$NODE2_VALIDATION_FILE"
  assert_validation_summaries_match

  echo "Replaying full bootstrap against node 1"
  run_source_dao_full_bootstrap "$NODE1_RPC" "$BOOTSTRAP_REPLAY_STATE_FILE"
  assert_idempotent_replay_state "$BOOTSTRAP_REPLAY_STATE_FILE"
  run_source_dao_validation "$NODE1_RPC" "$BOOTSTRAP_STATE_FILE" "$NODE1_VALIDATION_FILE"
  assert_cross_node_checkpoint "post-full-bootstrap-replay"
}

mkdir -p "$WORK_DIR"
rm -rf "$NODE1_DATADIR" "$NODE2_DATADIR"
mkdir -p "$NODE1_DATADIR" "$NODE2_DATADIR"
rm -f \
  "$NODE1_LOG" \
  "$NODE2_LOG" \
  "$BOOTSTRAP_STATE_FILE" \
  "$BOOTSTRAP_REPLAY_STATE_FILE" \
  "$NODE1_VALIDATION_FILE" \
  "$NODE2_VALIDATION_FILE" \
  "$FEE_PROBE_FILE"

echo "Generating shared USDB bootstrap genesis from $USDB_CONFIG"
run_geth dumpgenesis \
  --usdb \
  --usdb.bootstrap.config "$USDB_CONFIG" \
  --usdb.bootstrap.artifacts "$USDB_ARTIFACTS" \
  > "$GENESIS_JSON"

echo "Initializing node datadirs"
run_geth init --datadir "$NODE1_DATADIR" "$GENESIS_JSON" >/dev/null
run_geth init --datadir "$NODE2_DATADIR" "$GENESIS_JSON" >/dev/null

trap cleanup EXIT
if [[ "$USE_MOCK_INDEXER" == "1" ]]; then
  start_mock_indexer
elif [[ -z "$USDB_INDEXER_RPC_URL" ]]; then
  echo "USDB_INDEXER_RPC_URL is required when the test-only mock indexer is disabled" >&2
  exit 1
fi

NODE1_RPC="http://${NODE1_HTTP_ADDR}:${NODE1_HTTP_PORT}"
NODE2_RPC="http://${NODE2_HTTP_ADDR}:${NODE2_HTTP_PORT}"

start_node1
wait_for_chain "$NODE1_RPC"

if [[ "$START_JOINER_AFTER_BOOTSTRAP" == "0" ]]; then
  start_node2
  wait_for_chain "$NODE2_RPC"
  connect_nodes
  assert_cross_node_checkpoint "network-ready"
else
  echo "Deferring node 2 startup until node 1 completes full bootstrap."
fi

echo "USDB local network is ready."
echo "genesis:   $GENESIS_JSON"
echo "node1 rpc: $NODE1_RPC"
echo "node1 p2p: $NODE1_P2P_PORT"
echo "node1 log: $NODE1_LOG"
echo "node2 rpc: $NODE2_RPC"
echo "node2 p2p: $NODE2_P2P_PORT"
echo "node2 log: $NODE2_LOG"
if [[ "$USE_MOCK_INDEXER" == "1" ]]; then
  echo "test-only indexer: $USDB_INDEXER_RPC_URL"
fi

if [[ "$RUN_SMOKE" == "1" ]]; then
  echo "Running SourceDAO bootstrap smoke against node 1"
  (
    cd "$SOURCE_DAO_DIR"
    source "$HOME/.nvm/nvm.sh"
    nvm use 24 >/dev/null
    # SourceDAO owns this external env key; its value is the USDB-chain RPC endpoint.
    SOURCE_DAO_USDB_CONFIG="$SOURCE_DAO_CONFIG" \
    SOURCE_DAO_USDB_RPC_URL="$NODE1_RPC" \
    SOURCE_DAO_BOOTSTRAP_PRIVATE_KEY="$SOURCE_DAO_BOOTSTRAP_PRIVATE_KEY" \
    npm run test:usdb:smoke
  )
  assert_cross_node_checkpoint "post-bootstrap"
fi

if [[ "$RUN_FULL_BOOTSTRAP" == "1" ]]; then
  run_full_bootstrap_lifecycle
  echo "Full-bootstrap restart/joiner lifecycle test passed."
  echo "bootstrap state: $BOOTSTRAP_STATE_FILE"
  echo "replay state:    $BOOTSTRAP_REPLAY_STATE_FILE"
  echo "fee probe:       $FEE_PROBE_FILE"
fi

if [[ "$KEEP_RUNNING" == "1" ]]; then
  echo "Both nodes are still running. Press Ctrl-C to stop."
  wait "$NODE1_PID"
fi
