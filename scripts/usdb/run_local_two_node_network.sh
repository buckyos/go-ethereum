#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
# shellcheck source=lib/go_toolchain.sh
source "$ROOT_DIR/scripts/usdb/lib/go_toolchain.sh"
# shellcheck source=lib/node_toolchain.sh
source "$ROOT_DIR/scripts/usdb/lib/node_toolchain.sh"
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
RUN_PUBLIC_RELEASE_E2E=${RUN_PUBLIC_RELEASE_E2E:-0}
BOOTSTRAP_ACCEPTANCE_CONFIRMATIONS=${BOOTSTRAP_ACCEPTANCE_CONFIRMATIONS:-0}
BOOTSTRAP_STATE_FILE=${BOOTSTRAP_STATE_FILE:-"$WORK_DIR/sourcedao-bootstrap-state.json"}
BOOTSTRAP_REPLAY_STATE_FILE=${BOOTSTRAP_REPLAY_STATE_FILE:-"$WORK_DIR/sourcedao-bootstrap-replay-state.json"}
NODE1_VALIDATION_FILE=${NODE1_VALIDATION_FILE:-"$WORK_DIR/node1-bootstrap-validation.json"}
NODE2_VALIDATION_FILE=${NODE2_VALIDATION_FILE:-"$WORK_DIR/node2-bootstrap-validation.json"}
FEE_PROBE_FILE=${FEE_PROBE_FILE:-"$WORK_DIR/usdb-fee-split-probe.json"}
BOOTSTRAP_ACCEPTANCE_FILE=${BOOTSTRAP_ACCEPTANCE_FILE:-"$WORK_DIR/usdb-bootstrap-acceptance.json"}
BOOTSTRAP_ACCEPTANCE_TAMPERED_FILE=${BOOTSTRAP_ACCEPTANCE_TAMPERED_FILE:-"$WORK_DIR/usdb-bootstrap-acceptance-tampered.json"}
BOOTSTRAP_VALIDATION_TAMPERED_FILE=${BOOTSTRAP_VALIDATION_TAMPERED_FILE:-"$WORK_DIR/node1-bootstrap-validation-tampered.json"}
PUBLIC_RELEASE_ID=${PUBLIC_RELEASE_ID:-usdb-public-release-e2e-v1}
PUBLIC_RELEASE_MANIFEST_FILE=${PUBLIC_RELEASE_MANIFEST_FILE:-"$WORK_DIR/usdb-public-release-manifest.json"}
PUBLIC_RELEASE_SIGNATURE_FILE=${PUBLIC_RELEASE_SIGNATURE_FILE:-"$WORK_DIR/usdb-public-release-manifest.sig.json"}
PUBLIC_RELEASE_TRUSTED_KEYS_FILE=${PUBLIC_RELEASE_TRUSTED_KEYS_FILE:-"$WORK_DIR/usdb-public-release-trusted-keys.json"}
PUBLIC_RELEASE_SIGNING_KEY_FILE=${PUBLIC_RELEASE_SIGNING_KEY_FILE:-"$WORK_DIR/usdb-public-release-signing-key.pem"}
PUBLIC_RELEASE_SIGNING_KEY_ID=${PUBLIC_RELEASE_SIGNING_KEY_ID:-usdb-public-release-e2e-key-1}
PUBLIC_RELEASE_BOOTNODES_FILE=${PUBLIC_RELEASE_BOOTNODES_FILE:-"$WORK_DIR/usdb-public-release-bootnodes.txt"}
PUBLIC_RELEASE_TAMPERED_MANIFEST_FILE=${PUBLIC_RELEASE_TAMPERED_MANIFEST_FILE:-"$WORK_DIR/usdb-public-release-manifest-tampered.json"}
PUBLIC_RELEASE_TAMPERED_SIGNATURE_FILE=${PUBLIC_RELEASE_TAMPERED_SIGNATURE_FILE:-"$WORK_DIR/usdb-public-release-signature-tampered.json"}
PUBLIC_RELEASE_TAMPERED_GENESIS_FILE=${PUBLIC_RELEASE_TAMPERED_GENESIS_FILE:-"$WORK_DIR/usdb-public-release-genesis-tampered.json"}
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
USDB_PROFILE_REWARD_RECIPIENT=${USDB_PROFILE_REWARD_RECIPIENT:-0x1111111111111111111111111111111111111111}
NODE1_USDB_CHAIN_MINER_ADDRESS=${NODE1_USDB_CHAIN_MINER_ADDRESS:-$USDB_PROFILE_REWARD_RECIPIENT}
USDB_PROFILE_TOTAL_MINER_BTC_SATS=${USDB_PROFILE_TOTAL_MINER_BTC_SATS:-100000000}
USDB_PROFILE_COLLAB_CONTRIBUTION=${USDB_PROFILE_COLLAB_CONTRIBUTION:-100}
NODE1_LOG=${NODE1_LOG:-"$WORK_DIR/node1.log"}

NODE2_DATADIR=${NODE2_DATADIR:-"$WORK_DIR/node2"}
NODE2_HTTP_ADDR=${NODE2_HTTP_ADDR:-127.0.0.1}
NODE2_HTTP_PORT=${NODE2_HTTP_PORT:-18546}
NODE2_P2P_PORT=${NODE2_P2P_PORT:-31304}
NODE2_AUTHRPC_PORT=${NODE2_AUTHRPC_PORT:-18552}
NODE2_LOG=${NODE2_LOG:-"$WORK_DIR/node2.log"}
NODE2_GCMODE=${NODE2_GCMODE:-full}
USDB_BOOTSTRAP_INDEXER_PORT=${USDB_BOOTSTRAP_INDEXER_PORT:-$((NODE2_HTTP_PORT + 1))}

GETH_BIN=${GETH_BIN:-}
USDB_GO_TOOLCHAIN_MODE=${USDB_GO_TOOLCHAIN_MODE:-auto}
usdb_prepare_geth_binary GETH_BIN "$ROOT_DIR" "$WORK_DIR/bin/geth"
GETH_CMD=("$GETH_BIN")

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
RUN_PUBLIC_RELEASE_E2E=$(normalize_boolean RUN_PUBLIC_RELEASE_E2E "$RUN_PUBLIC_RELEASE_E2E")

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
if [[ "$RUN_PUBLIC_RELEASE_E2E" == "1" ]] &&
  { [[ "$RUN_FULL_BOOTSTRAP" != "1" ]] ||
    [[ "$START_JOINER_AFTER_BOOTSTRAP" != "1" ]] ||
    [[ "$RESTART_NODE1_AFTER_BOOTSTRAP" != "1" ]]; }; then
  echo "RUN_PUBLIC_RELEASE_E2E requires full bootstrap, delayed joiner, and node restart" >&2
  exit 1
fi
if [[ ! "$BOOTSTRAP_ACCEPTANCE_CONFIRMATIONS" =~ ^[0-9]+$ ]]; then
  echo "BOOTSTRAP_ACCEPTANCE_CONFIRMATIONS must be a non-negative integer" >&2
  exit 1
fi
if [[ "$RUN_PUBLIC_RELEASE_E2E" == "1" && "$BOOTSTRAP_ACCEPTANCE_CONFIRMATIONS" == "0" ]]; then
  echo "Public release E2E requires non-zero bootstrap acceptance confirmations" >&2
  exit 1
fi
if [[ "$NODE2_GCMODE" != "full" && "$NODE2_GCMODE" != "archive" ]]; then
  echo "NODE2_GCMODE must be full or archive" >&2
  exit 1
fi
if [[ "$RUN_PUBLIC_RELEASE_E2E" == "1" && "$NODE2_GCMODE" != "archive" ]]; then
  echo "Public release E2E requires NODE2_GCMODE=archive" >&2
  exit 1
fi
if [[ "$RUN_PUBLIC_RELEASE_E2E" == "1" ]]; then
  for required_file in "$PUBLIC_RELEASE_SIGNING_KEY_FILE" "$PUBLIC_RELEASE_TRUSTED_KEYS_FILE"; do
    if [[ ! -f "$required_file" ]]; then
      echo "Public release E2E input does not exist: $required_file" >&2
      exit 1
    fi
  done
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
  local pid
  for pid in "${NODE1_PID:-}" "${NODE2_PID:-}" "${MOCK_INDEXER_PID:-}"; do
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
  local -a discovery_args
  if [[ "$RUN_PUBLIC_RELEASE_E2E" == "1" ]]; then
    discovery_args=(--nat extip:127.0.0.1)
  else
    discovery_args=(--nodiscover)
  fi
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
      "${discovery_args[@]}" \
      "${POW_ARGS[@]}" \
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
  local -a discovery_args
  if [[ "$RUN_PUBLIC_RELEASE_E2E" == "1" ]]; then
    local bootnodes
    bootnodes="$(tr -d '\r\n ' <"$PUBLIC_RELEASE_BOOTNODES_FILE")"
    if [[ -z "$bootnodes" ]]; then
      echo "Public release bootnodes file is empty: $PUBLIC_RELEASE_BOOTNODES_FILE" >&2
      return 1
    fi
    discovery_args=(--bootnodes "$bootnodes" --nat extip:127.0.0.1)
  else
    discovery_args=(--nodiscover)
  fi
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
      "${discovery_args[@]}" \
      --syncmode full \
      --gcmode "$NODE2_GCMODE" \
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
  if [[ "$RUN_PUBLIC_RELEASE_E2E" == "1" ]]; then
    echo "Waiting for node 2 to discover node 1 through the signed bootnode artifact"
    wait_for_peers "$NODE1_RPC" 1
    wait_for_peers "$NODE2_RPC" 1
    return
  fi
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
    usdb_load_node_toolchain
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
    usdb_load_node_toolchain
    npm run validate:bootstrap -- \
      --config "$SOURCE_DAO_FULL_CONFIG" \
      --rpc-url "$rpc_url" \
      --state-file "$state_file" \
      --output "$output_file" \
      --strict
  )
}

run_bootstrap_acceptance_create() {
  local rpc_url=$1
  local validation_file=$2
  local checkpoint_block=$3
  run_geth usdb-bootstrap-acceptance create \
    --rpc-url "$rpc_url" \
    --genesis "$GENESIS_JSON" \
    --bootstrap-config "$SOURCE_DAO_FULL_CONFIG" \
    --bootstrap-state "$BOOTSTRAP_STATE_FILE" \
    --validation "$validation_file" \
    --checkpoint-block "$checkpoint_block" \
    --min-confirmations "$BOOTSTRAP_ACCEPTANCE_CONFIRMATIONS" \
    --artifact "$BOOTSTRAP_ACCEPTANCE_FILE"
}

run_bootstrap_acceptance_verify() {
  local rpc_url=$1
  local validation_file=$2
  local artifact_file=${3:-$BOOTSTRAP_ACCEPTANCE_FILE}
  run_geth usdb-bootstrap-acceptance verify \
    --rpc-url "$rpc_url" \
    --genesis "$GENESIS_JSON" \
    --bootstrap-config "$SOURCE_DAO_FULL_CONFIG" \
    --bootstrap-state "$BOOTSTRAP_STATE_FILE" \
    --validation "$validation_file" \
    --artifact "$artifact_file"
}

assert_tampered_bootstrap_acceptance_rejected() {
  local checkpoint_replacement validation_admin_replacement
  checkpoint_replacement=0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
  validation_admin_replacement=0x0000000000000000000000000000000000009999

  jq --arg replacement "$checkpoint_replacement" \
    '.checkpoint.hash = $replacement' \
    "$BOOTSTRAP_ACCEPTANCE_FILE" >"$BOOTSTRAP_ACCEPTANCE_TAMPERED_FILE"
  if run_bootstrap_acceptance_verify "$NODE1_RPC" "$NODE1_VALIDATION_FILE" "$BOOTSTRAP_ACCEPTANCE_TAMPERED_FILE"; then
    echo "Tampered bootstrap acceptance checkpoint was accepted" >&2
    return 1
  fi

  jq --arg replacement "$validation_admin_replacement" \
    '.bootstrapAdmin = $replacement' \
    "$NODE1_VALIDATION_FILE" >"$BOOTSTRAP_VALIDATION_TAMPERED_FILE"
  if run_bootstrap_acceptance_verify "$NODE1_RPC" "$BOOTSTRAP_VALIDATION_TAMPERED_FILE"; then
    echo "Bootstrap acceptance accepted a polluted bootstrap admin" >&2
    return 1
  fi
  echo "Tampered checkpoint and bootstrap-admin pollution were rejected."
}

write_public_release_bootnodes() {
  local node1_enode
  node1_enode="$(fetch_enode "$NODE1_RPC")"
  if [[ -z "$node1_enode" ]]; then
    echo "Failed to resolve node 1 enode for the public release bundle" >&2
    return 1
  fi
  printf '%s\n' "$node1_enode" >"$PUBLIC_RELEASE_BOOTNODES_FILE"
  echo "Published candidate bootnode: $node1_enode"
}

run_public_release_create() {
  run_geth usdb-release-manifest create \
    --release-id "$PUBLIC_RELEASE_ID" \
    --network-id "$NETWORK_ID" \
    --genesis "$GENESIS_JSON" \
    --acceptance "$BOOTSTRAP_ACCEPTANCE_FILE" \
    --bootnodes "$PUBLIC_RELEASE_BOOTNODES_FILE" \
    --manifest "$PUBLIC_RELEASE_MANIFEST_FILE" \
    --signature "$PUBLIC_RELEASE_SIGNATURE_FILE" \
    --private-key "$PUBLIC_RELEASE_SIGNING_KEY_FILE" \
    --key-id "$PUBLIC_RELEASE_SIGNING_KEY_ID"
}

run_public_release_verify() {
  local genesis_file=${1:-$GENESIS_JSON}
  local manifest_file=${2:-$PUBLIC_RELEASE_MANIFEST_FILE}
  local signature_file=${3:-$PUBLIC_RELEASE_SIGNATURE_FILE}
  run_geth usdb-release-manifest verify \
    --release-id "$PUBLIC_RELEASE_ID" \
    --network-id "$NETWORK_ID" \
    --genesis "$genesis_file" \
    --acceptance "$BOOTSTRAP_ACCEPTANCE_FILE" \
    --bootnodes "$PUBLIC_RELEASE_BOOTNODES_FILE" \
    --manifest "$manifest_file" \
    --signature "$signature_file" \
    --trusted-keys "$PUBLIC_RELEASE_TRUSTED_KEYS_FILE"
}

assert_tampered_public_release_rejected() {
  jq '.network_id += 1' \
    "$PUBLIC_RELEASE_MANIFEST_FILE" >"$PUBLIC_RELEASE_TAMPERED_MANIFEST_FILE"
  if run_public_release_verify "$GENESIS_JSON" "$PUBLIC_RELEASE_TAMPERED_MANIFEST_FILE"; then
    echo "Tampered public release manifest was accepted" >&2
    return 1
  fi

  jq '.signature_base64 = ("A" * 88)' \
    "$PUBLIC_RELEASE_SIGNATURE_FILE" >"$PUBLIC_RELEASE_TAMPERED_SIGNATURE_FILE"
  if run_public_release_verify "$GENESIS_JSON" "$PUBLIC_RELEASE_MANIFEST_FILE" "$PUBLIC_RELEASE_TAMPERED_SIGNATURE_FILE"; then
    echo "Tampered public release signature was accepted" >&2
    return 1
  fi

  cp "$GENESIS_JSON" "$PUBLIC_RELEASE_TAMPERED_GENESIS_FILE"
  printf '\n' >>"$PUBLIC_RELEASE_TAMPERED_GENESIS_FILE"
  if run_public_release_verify "$PUBLIC_RELEASE_TAMPERED_GENESIS_FILE"; then
    echo "Tampered canonical genesis bytes were accepted" >&2
    return 1
  fi
  echo "Tampered release manifest, signature, and genesis bytes were rejected."
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
    usdb_load_node_toolchain
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
    and (.preGate.totalFee | tonumber) > 0
    and .preGate.daoFee == "0"
    and .preGate.dividendBalanceBefore == .preGate.dividendBalanceAfter
    and .preGate.blockNumber < .feeSplitBlock
    and (.probe.daoFee | tonumber) > 0
    and .probe.blockNumber >= .feeSplitBlock
    and (.probe.emission | tonumber) > 0
    and (.ledgerSync.pendingBefore | tonumber) > 0
    and .ledgerSync.pendingAfter == .ledgerSync.daoFee
  ' "$output_file" >/dev/null; then
    echo "USDB fee split probe did not produce the expected accounting result" >&2
    return 1
  fi
}

historical_dividend_proof() {
  local rpc_url=$1
  local block_tag=$2
  local dividend_address finalized_slot response
  dividend_address="$(jq -r '.fee_policy.dividend_address' "$PUBLIC_RELEASE_MANIFEST_FILE")"
  finalized_slot=0x7d8bb76c5e489191d3f481f0b7ade016df922a8ec91d3eb9c93c07ee5a337054
  response="$(rpc_call "$rpc_url" \
    "{\"jsonrpc\":\"2.0\",\"method\":\"eth_getProof\",\"params\":[\"${dividend_address}\",[\"${finalized_slot}\"],\"${block_tag}\"],\"id\":1}")"
  if ! jq -e '.error == null and .result != null' <<<"$response" >/dev/null; then
    echo "Historical Dividend proof failed at block ${block_tag}: ${response}" >&2
    return 1
  fi
  jq -S -c \
    '.result | {address, balance, codeHash, nonce, storageHash, finalized: .storageProof[0].value}' \
    <<<"$response"
}

assert_archive_replay() {
  local checkpoint_number checkpoint_tag expected_code_hash
  local genesis_proof checkpoint_proof checkpoint_block expected_checkpoint_hash expected_state_root
  checkpoint_number="$(jq -r '.acceptance.checkpoint.number' "$PUBLIC_RELEASE_MANIFEST_FILE")"
  checkpoint_tag="$(printf '0x%x' "$checkpoint_number")"
  expected_code_hash="$(jq -r '.fee_policy.dividend_code_hash' "$PUBLIC_RELEASE_MANIFEST_FILE")"
  expected_checkpoint_hash="$(jq -r '.acceptance.checkpoint.hash' "$PUBLIC_RELEASE_MANIFEST_FILE")"
  expected_state_root="$(jq -r '.acceptance.checkpoint.state_root' "$PUBLIC_RELEASE_MANIFEST_FILE")"

  genesis_proof="$(historical_dividend_proof "$NODE2_RPC" 0x0)"
  checkpoint_proof="$(historical_dividend_proof "$NODE2_RPC" "$checkpoint_tag")"
  if ! jq -e \
    --arg code_hash "$expected_code_hash" \
    '.codeHash == $code_hash and (.finalized == "0x0" or .finalized == "0x")' \
    <<<"$genesis_proof" >/dev/null; then
    echo "Archive joiner genesis proof does not match the canonical Dividend predeploy: $genesis_proof" >&2
    return 1
  fi
  if ! jq -e \
    --arg code_hash "$expected_code_hash" \
    '.codeHash == $code_hash and .finalized == "0x1"' \
    <<<"$checkpoint_proof" >/dev/null; then
    echo "Archive joiner acceptance proof does not contain finalized Dividend state: $checkpoint_proof" >&2
    return 1
  fi
  checkpoint_block="$(rpc_call "$NODE2_RPC" \
    "{\"jsonrpc\":\"2.0\",\"method\":\"eth_getBlockByNumber\",\"params\":[\"${checkpoint_tag}\",false],\"id\":1}")"
  if ! jq -e \
    --arg hash "$expected_checkpoint_hash" \
    --arg state_root "$expected_state_root" \
    '.result.hash == $hash and .result.stateRoot == $state_root' \
    <<<"$checkpoint_block" >/dev/null; then
    echo "Archive joiner checkpoint header differs from the signed release manifest" >&2
    return 1
  fi
  echo "Archive joiner replayed genesis and acceptance checkpoint state proofs."
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
  local bootstrap_height bootstrap_hash bootstrap_state_root required_acceptance_head node1_head

  echo "Running SourceDAO full bootstrap against node 1"
  run_source_dao_full_bootstrap "$NODE1_RPC" "$BOOTSTRAP_STATE_FILE"
  run_source_dao_validation "$NODE1_RPC" "$BOOTSTRAP_STATE_FILE" "$NODE1_VALIDATION_FILE"

  bootstrap_height=$(wait_for_height "$NODE1_RPC" 1)
  read -r bootstrap_hash bootstrap_state_root < <(block_identity_at "$NODE1_RPC" "$bootstrap_height")
  if [[ -z "$bootstrap_hash" || -z "$bootstrap_state_root" ]]; then
    echo "Failed to capture the post-bootstrap block identity at $bootstrap_height" >&2
    return 1
  fi
  echo "Captured post-bootstrap checkpoint: height=${bootstrap_height} hash=${bootstrap_hash} stateRoot=${bootstrap_state_root}"
  required_acceptance_head=$((bootstrap_height + BOOTSTRAP_ACCEPTANCE_CONFIRMATIONS))
  if (( BOOTSTRAP_ACCEPTANCE_CONFIRMATIONS > 0 )); then
    echo "Waiting for ${BOOTSTRAP_ACCEPTANCE_CONFIRMATIONS} acceptance confirmations through block ${required_acceptance_head}"
    wait_for_height "$NODE1_RPC" "$required_acceptance_head" >/dev/null
  fi
  run_bootstrap_acceptance_create "$NODE1_RPC" "$NODE1_VALIDATION_FILE" "$bootstrap_height"
  run_bootstrap_acceptance_verify "$NODE1_RPC" "$NODE1_VALIDATION_FILE"
  assert_tampered_bootstrap_acceptance_rejected
  if [[ "$RUN_PUBLIC_RELEASE_E2E" == "1" ]]; then
    write_public_release_bootnodes
    run_public_release_create
    run_public_release_verify
    assert_tampered_public_release_rejected
  fi

  accelerate_fake_pow_after_bootstrap
  echo "Running UIP-0011 fee split and Dividend ledger-sync probe"
  run_source_dao_fee_probe "$NODE1_RPC" "$FEE_PROBE_FILE"
  run_source_dao_validation "$NODE1_RPC" "$BOOTSTRAP_STATE_FILE" "$NODE1_VALIDATION_FILE"

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
    run_bootstrap_acceptance_verify "$NODE1_RPC" "$NODE1_VALIDATION_FILE"
    if [[ "$RUN_PUBLIC_RELEASE_E2E" == "1" ]]; then
      run_public_release_verify
    fi
  fi

  if [[ "$START_JOINER_AFTER_BOOTSTRAP" == "1" ]]; then
    echo "Starting fresh node 2 after full bootstrap"
    if [[ "$RUN_PUBLIC_RELEASE_E2E" == "1" ]]; then
      echo "Verifying signed release bundle before initializing the archive joiner"
      run_public_release_verify
    fi
    run_geth init --datadir "$NODE2_DATADIR" "$GENESIS_JSON" >/dev/null
    start_node2
    wait_for_chain "$NODE2_RPC"
    connect_nodes
    node1_head="$(wait_for_height "$NODE1_RPC" 1)"
    wait_for_height "$NODE2_RPC" "$((node1_head))" >/dev/null
  fi

  assert_block_identity \
    "Node 2 historical replay" \
    "$NODE2_RPC" \
    "$bootstrap_height" \
    "$bootstrap_hash" \
    "$bootstrap_state_root"
  run_source_dao_validation "$NODE2_RPC" "$BOOTSTRAP_STATE_FILE" "$NODE2_VALIDATION_FILE"
  run_bootstrap_acceptance_verify "$NODE2_RPC" "$NODE2_VALIDATION_FILE"
  if [[ "$RUN_PUBLIC_RELEASE_E2E" == "1" ]]; then
    run_public_release_verify
    assert_archive_replay
  fi
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
  "$FEE_PROBE_FILE" \
  "$BOOTSTRAP_ACCEPTANCE_FILE" \
  "$BOOTSTRAP_ACCEPTANCE_TAMPERED_FILE" \
  "$BOOTSTRAP_VALIDATION_TAMPERED_FILE" \
  "$PUBLIC_RELEASE_MANIFEST_FILE" \
  "$PUBLIC_RELEASE_SIGNATURE_FILE" \
  "$PUBLIC_RELEASE_BOOTNODES_FILE" \
  "$PUBLIC_RELEASE_TAMPERED_MANIFEST_FILE" \
  "$PUBLIC_RELEASE_TAMPERED_SIGNATURE_FILE" \
  "$PUBLIC_RELEASE_TAMPERED_GENESIS_FILE"

echo "Generating shared USDB bootstrap genesis from $USDB_CONFIG"
run_geth dumpgenesis \
  --usdb \
  --usdb.bootstrap.config "$USDB_CONFIG" \
  --usdb.bootstrap.artifacts "$USDB_ARTIFACTS" \
  > "$GENESIS_JSON"
if [[ "$RUN_PUBLIC_RELEASE_E2E" == "1" ]]; then
  genesis_replay_file="$WORK_DIR/usdb-bootstrap-genesis-replay.json"
  run_geth dumpgenesis \
    --usdb \
    --usdb.bootstrap.config "$USDB_CONFIG" \
    --usdb.bootstrap.artifacts "$USDB_ARTIFACTS" \
    >"$genesis_replay_file"
  if ! cmp -s "$GENESIS_JSON" "$genesis_replay_file"; then
    echo "Repeated canonical genesis generation produced different bytes" >&2
    diff -u "$GENESIS_JSON" "$genesis_replay_file" >&2 || true
    exit 1
  fi
  echo "Repeated canonical genesis generation produced identical bytes."
fi

echo "Initializing node datadirs"
run_geth init --datadir "$NODE1_DATADIR" "$GENESIS_JSON" >/dev/null
if [[ "$START_JOINER_AFTER_BOOTSTRAP" == "0" ]]; then
  run_geth init --datadir "$NODE2_DATADIR" "$GENESIS_JSON" >/dev/null
fi

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
    usdb_load_node_toolchain
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
  echo "acceptance:      $BOOTSTRAP_ACCEPTANCE_FILE"
  echo "replay state:    $BOOTSTRAP_REPLAY_STATE_FILE"
  echo "fee probe:       $FEE_PROBE_FILE"
  if [[ "$RUN_PUBLIC_RELEASE_E2E" == "1" ]]; then
    echo "release manifest: $PUBLIC_RELEASE_MANIFEST_FILE"
    echo "release signature: $PUBLIC_RELEASE_SIGNATURE_FILE"
    echo "release bootnodes: $PUBLIC_RELEASE_BOOTNODES_FILE"
  fi
fi

if [[ "$KEEP_RUNNING" == "1" ]]; then
  echo "Both nodes are still running. Press Ctrl-C to stop."
  wait "$NODE1_PID"
fi
