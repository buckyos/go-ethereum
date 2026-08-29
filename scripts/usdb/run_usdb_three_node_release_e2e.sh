#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
WORKSPACE_ROOT=${USDB_E2E_WORKSPACE_ROOT:-$(dirname "$ROOT_DIR")}
USDB_REPO=${USDB_REPO:-"$WORKSPACE_ROOT/usdb"}
WORK_DIR=${USDB_E2E_WORK_DIR:-/tmp/usdb-three-node-release-e2e}
MANIFEST=${USDB_E2E_MANIFEST:-}
COMPATIBILITY_LOCK=${USDB_E2E_COMPATIBILITY_LOCK:-"$ROOT_DIR/scripts/usdb/ci-revisions.json"}
BUNDLE_DIR=${USDB_E2E_BUNDLE_DIR:-"$USDB_REPO/docker/networks/testnet-v0"}
BASE_NODE_ENV=${USDB_E2E_BASE_NODE_ENV:-}
IMAGE_MIRROR=${USDB_E2E_IMAGE_MIRROR:-}
PROJECT_PREFIX=${USDB_E2E_PROJECT_PREFIX:-usdb-release-e2e}
NETWORK_NAME=${USDB_E2E_DOCKER_NETWORK:-"${PROJECT_PREFIX}-network"}
KEEP_RUNNING=${USDB_E2E_KEEP_RUNNING:-0}
KEEP_DATA=${USDB_E2E_KEEP_DATA:-0}
ENABLE_MINING=${USDB_E2E_ENABLE_MINING:-1}
START_CONTROL_PLANE=${USDB_E2E_START_CONTROL_PLANE:-1}
DATA_READY_TIMEOUT_SECS=${USDB_E2E_DATA_READY_TIMEOUT_SECS:-604800}
INDEXER_READY_TIMEOUT_SECS=${USDB_E2E_INDEXER_READY_TIMEOUT_SECS:-7200}
CHAIN_READY_TIMEOUT_SECS=${USDB_E2E_CHAIN_READY_TIMEOUT_SECS:-300}
SYNC_TIMEOUT_SECS=${USDB_E2E_SYNC_TIMEOUT_SECS:-600}
TOOL="$ROOT_DIR/scripts/usdb/release_three_node_e2e.py"
RELEASE_MANIFEST_TOOL="$USDB_REPO/docker/scripts/tools/release_manifest.py"
NETWORK_VALIDATOR="$USDB_REPO/docker/scripts/tools/validate_network_bundle.py"
READINESS_CHECKER="$USDB_REPO/docker/scripts/tools/check_json_rpc_readiness.py"
RUNTIME_COMPOSE="$USDB_REPO/docker/compose.runtime.yml"
BITCOIN_COMPOSE="$USDB_REPO/docker/compose.bitcoin.yml"
NETWORK_COMPOSE="$BUNDLE_DIR/compose.network.yml"
PLAN="$WORK_DIR/execution-plan.json"
NODE1_ENV="$WORK_DIR/node1.env"
NODE2_ENV="$WORK_DIR/node2.env"
NODE3_ENV="$WORK_DIR/node3.env"
REPORT="$WORK_DIR/report.json"
E2E_COMPLETED=0

usage() {
  cat <<EOF
Usage: scripts/usdb/run_usdb_three_node_release_e2e.sh <action>

Actions:
  preflight  Validate manifest/bundle/node inputs and pull/inspect all three images.
  run        Run preflight, the real BTC-side service chain, and three USDB nodes.
  status     Show all E2E Compose projects and the latest report.
  down       Stop only projects owned by this E2E prefix; preserve Bitcoin bind data.

Required environment:
  USDB_E2E_MANIFEST       Cross-repository release candidate manifest.
  USDB_E2E_BASE_NODE_ENV  Private node.env containing Bitcoin paths and credentials.

Local candidate environment:
  USDB_E2E_IMAGE_MIRROR          Temporary OCI registry, for example 127.0.0.1:5000.
  USDB_E2E_COMPATIBILITY_LOCK    Test-only lock emitted by prepare_local_release_images.sh.
  USDB_E2E_BUNDLE_DIR            Clean exported bundle emitted by that same builder.

The default run starts one digest-pinned Bitcoin Core container, one real
balance-history/indexer pipeline, and three independent digest-pinned USDB chain
nodes. The Bitcoin data directory must not be open by another bitcoind process.
EOF
}

normalize_boolean() {
  local name=$1
  local value=$2
  case "$value" in
    1|true|TRUE|yes|YES) printf '1\n' ;;
    0|false|FALSE|no|NO) printf '0\n' ;;
    *)
      echo "$name must be a boolean, have: $value" >&2
      exit 1
      ;;
  esac
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Required command is unavailable: $1" >&2
    exit 1
  }
}

require_inputs() {
  if [[ -z "$MANIFEST" || ! -f "$MANIFEST" ]]; then
    echo "USDB_E2E_MANIFEST must identify an existing release manifest" >&2
    exit 1
  fi
  if [[ -z "$BASE_NODE_ENV" || ! -f "$BASE_NODE_ENV" ]]; then
    echo "USDB_E2E_BASE_NODE_ENV must identify a private node.env" >&2
    exit 1
  fi
  for path in \
    "$COMPATIBILITY_LOCK" \
    "$BUNDLE_DIR/network.json" \
    "$RELEASE_MANIFEST_TOOL" \
    "$NETWORK_VALIDATOR" \
    "$READINESS_CHECKER" \
    "$RUNTIME_COMPOSE" \
    "$BITCOIN_COMPOSE" \
    "$NETWORK_COMPOSE"; do
    if [[ ! -e "$path" ]]; then
      echo "Required release E2E input is missing: $path" >&2
      exit 1
    fi
  done
}

validate_project_prefix() {
  if [[ ! "$PROJECT_PREFIX" =~ ^usdb-[a-z0-9-]*e2e[a-z0-9-]*$ ]]; then
    echo "USDB_E2E_PROJECT_PREFIX must be an explicit usdb-*-e2e namespace" >&2
    exit 1
  fi
}

plan_value() {
  jq -er "$1" "$PLAN"
}

configure_execution_images() {
  if [[ ! -f "$PLAN" ]]; then
    unset USDB_SERVICES_IMAGE USDB_CHAIN_IMAGE USDB_BITCOIN_IMAGE
    export USDB_DOCKER_NETWORK="$NETWORK_NAME"
    export USDB_NETWORK_ARTIFACTS_DIR="$BUNDLE_DIR/artifacts"
    export BH_SNAPSHOT_TRUST_HOST_DIR="$BUNDLE_DIR/trust"
    return
  fi
  export USDB_SERVICES_IMAGE
  export USDB_CHAIN_IMAGE
  export USDB_BITCOIN_IMAGE
  USDB_SERVICES_IMAGE=$(plan_value '.images.usdb_services.execution_reference')
  USDB_CHAIN_IMAGE=$(plan_value '.images.usdb_chain.execution_reference')
  USDB_BITCOIN_IMAGE=$(plan_value '.images.bitcoin_core.execution_reference')
  export USDB_DOCKER_NETWORK="$NETWORK_NAME"
  export USDB_NETWORK_ARTIFACTS_DIR="$BUNDLE_DIR/artifacts"
  export BH_SNAPSHOT_TRUST_HOST_DIR="$BUNDLE_DIR/trust"
}

assert_container_reference() {
  local container=$1
  local expected=$2
  local actual
  if [[ -z "$container" ]]; then
    echo "Expected E2E container is missing" >&2
    return 1
  fi
  actual=$(docker inspect --format '{{.Config.Image}}' "$container")
  if [[ "$actual" != "$expected" ]]; then
    echo "E2E container image reference mismatch: expected=$expected actual=$actual container=$container" >&2
    return 1
  fi
}

assert_runtime_service_image() {
  local project=$1
  local node_env=$2
  local service=$3
  local image_key=$4
  local container expected
  container=$(runtime_compose "$project" "$node_env" ps -aq "$service")
  expected=$(plan_value ".images.${image_key}.execution_reference")
  assert_container_reference "$container" "$expected"
}

runtime_compose() {
  local project=$1
  local node_env=$2
  shift 2
  docker compose \
    --project-name "$project" \
    --env-file "$BUNDLE_DIR/network.env" \
    --env-file "$node_env" \
    -f "$RUNTIME_COMPOSE" \
    -f "$NETWORK_COMPOSE" \
    "$@"
}

bitcoin_compose() {
  docker compose \
    --project-name "${PROJECT_PREFIX}-bitcoin" \
    --env-file "$BUNDLE_DIR/network.env" \
    --env-file "$NODE1_ENV" \
    -f "$BITCOIN_COMPOSE" \
    "$@"
}

rpc_call() {
  local url=$1
  local method=$2
  local params=${3:-[]}
  curl -fsS \
    -H 'content-type: application/json' \
    --data "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"${method}\",\"params\":${params}}" \
    "$url"
}

wait_for_rpc() {
  local url=$1
  local deadline=$((SECONDS + CHAIN_READY_TIMEOUT_SECS))
  while (( SECONDS < deadline )); do
    if rpc_call "$url" web3_clientVersion '[]' | jq -e '.error == null and .result != null' >/dev/null 2>&1; then
      return
    fi
    sleep 2
  done
  echo "Timed out waiting for USDB RPC: $url" >&2
  return 1
}

wait_for_peer_count() {
  local url=$1
  local minimum=$2
  local deadline=$((SECONDS + SYNC_TIMEOUT_SECS))
  while (( SECONDS < deadline )); do
    local value
    value=$(rpc_call "$url" net_peerCount '[]' | jq -r '.result // "0x0"' 2>/dev/null || true)
    if [[ "$value" =~ ^0x[0-9a-fA-F]+$ ]] && (( 16#${value#0x} >= minimum )); then
      return
    fi
    sleep 2
  done
  echo "Timed out waiting for ${minimum} peers at $url" >&2
  return 1
}

block_number() {
  rpc_call "$1" eth_blockNumber '[]' | jq -er '.result | strings | select(test("^0x[0-9a-fA-F]+$"))'
}

wait_for_height() {
  local url=$1
  local target=$2
  local deadline=$((SECONDS + SYNC_TIMEOUT_SECS))
  while (( SECONDS < deadline )); do
    local value
    value=$(block_number "$url" 2>/dev/null || true)
    if [[ "$value" =~ ^0x[0-9a-fA-F]+$ ]] && (( 16#${value#0x} >= target )); then
      printf '%s\n' "$value"
      return
    fi
    sleep 2
  done
  echo "Timed out waiting for block ${target} at $url" >&2
  return 1
}

assert_chain_identity() {
  local url=$1
  local expected_chain expected_genesis actual_chain actual_genesis
  expected_chain=$(plan_value '.network.chain_id')
  expected_genesis=$(plan_value '.network.genesis_block_hash')
  actual_chain=$(rpc_call "$url" eth_chainId '[]' | jq -er '.result')
  actual_genesis=$(rpc_call "$url" eth_getBlockByNumber '["0x0",false]' | jq -er '.result.hash')
  if (( 16#${actual_chain#0x} != expected_chain )); then
    echo "USDB chain ID mismatch at $url: expected=$expected_chain actual=$actual_chain" >&2
    return 1
  fi
  if [[ "$actual_genesis" != "$expected_genesis" ]]; then
    echo "USDB genesis mismatch at $url: expected=$expected_genesis actual=$actual_genesis" >&2
    return 1
  fi
}

container_ip() {
  local project=$1
  local container
  container=$(runtime_compose "$project" "$NODE1_ENV" ps -q usdb-chain)
  docker inspect --format "{{with index .NetworkSettings.Networks \"${NETWORK_NAME}\"}}{{.IPAddress}}{{end}}" "$container"
}

connect_to_node1() {
  local target_url=$1
  local node1_enode node1_ip reachable_enode
  node1_enode=$(rpc_call http://127.0.0.1:18545 admin_nodeInfo '[]' | jq -er '.result.enode')
  node1_ip=$(container_ip "${PROJECT_PREFIX}-node1")
  if [[ -z "$node1_ip" ]]; then
    echo "Failed to resolve node 1 container IP" >&2
    return 1
  fi
  reachable_enode=$(sed -E "s#@[^?]+:#@${node1_ip}:#" <<<"$node1_enode")
  rpc_call "$target_url" admin_addPeer "[\"${reachable_enode}\"]" | jq -e '.result == true' >/dev/null
}

render_node_envs() {
  local node1_role=bootnode
  if [[ "$ENABLE_MINING" == "1" ]]; then
    node1_role=miner
  fi
  python3 "$TOOL" render-node-env --plan "$PLAN" --base "$BASE_NODE_ENV" --output "$NODE1_ENV" --node-index 1 --role "$node1_role"
  python3 "$TOOL" render-node-env --plan "$PLAN" --base "$BASE_NODE_ENV" --output "$NODE2_ENV" --node-index 2 --role full
  python3 "$TOOL" render-node-env --plan "$PLAN" --base "$BASE_NODE_ENV" --output "$NODE3_ENV" --node-index 3 --role full
  for node_env in "$NODE1_ENV" "$NODE2_ENV" "$NODE3_ENV"; do
    python3 "$TOOL" validate-node-env --plan "$PLAN" --node-env "$node_env"
    python3 "$NETWORK_VALIDATOR" --bundle-dir "$BUNDLE_DIR" --node-env "$node_env" --require-runtime --require-bitcoin-runtime
  done
}

pull_and_verify_images() {
  local key reference inspect_file
  for key in usdb_services usdb_chain bitcoin_core; do
    reference=$(plan_value ".images.${key}.execution_reference")
    echo "Pulling ${key}: ${reference}"
    docker pull "$reference" >/dev/null
    inspect_file="$WORK_DIR/${key}-inspect.json"
    docker image inspect "$reference" >"$inspect_file"
    python3 "$TOOL" verify-image-inspect --plan "$PLAN" --image-key "$key" --inspect "$inspect_file"
  done
}

preflight() {
  require_command curl
  require_command docker
  require_command jq
  require_command python3
  require_inputs
  validate_project_prefix
  docker compose version >/dev/null
  KEEP_RUNNING=$(normalize_boolean USDB_E2E_KEEP_RUNNING "$KEEP_RUNNING")
  KEEP_DATA=$(normalize_boolean USDB_E2E_KEEP_DATA "$KEEP_DATA")
  ENABLE_MINING=$(normalize_boolean USDB_E2E_ENABLE_MINING "$ENABLE_MINING")
  START_CONTROL_PLANE=$(normalize_boolean USDB_E2E_START_CONTROL_PLANE "$START_CONTROL_PLANE")
  mkdir -p "$WORK_DIR"

  python3 "$RELEASE_MANIFEST_TOOL" validate \
    --bundle-dir "$BUNDLE_DIR" \
    --manifest "$MANIFEST" \
    --compatibility-lock "$COMPATIBILITY_LOCK"
  local plan_args=(plan --manifest "$MANIFEST" --output "$PLAN")
  if [[ -n "$IMAGE_MIRROR" ]]; then
    plan_args+=(--image-mirror "$IMAGE_MIRROR")
  fi
  python3 "$TOOL" "${plan_args[@]}"
  render_node_envs
  configure_execution_images
  runtime_compose "${PROJECT_PREFIX}-node1" "$NODE1_ENV" config --quiet
  runtime_compose "${PROJECT_PREFIX}-node2" "$NODE2_ENV" config --quiet
  runtime_compose "${PROJECT_PREFIX}-node3" "$NODE3_ENV" config --quiet
  bitcoin_compose config --quiet
  pull_and_verify_images
  echo "Release manifest preflight passed: $PLAN"
}

ensure_network() {
  if ! docker network inspect "$NETWORK_NAME" >/dev/null 2>&1; then
    docker network create --label org.usdb.test-scope=three-node-release-e2e "$NETWORK_NAME" >/dev/null
  fi
}

start_bitcoin() {
  echo "Starting digest-pinned Bitcoin Core"
  bitcoin_compose up -d btc-node
  assert_container_reference \
    "$(bitcoin_compose ps -q btc-node)" \
    "$(plan_value '.images.bitcoin_core.execution_reference')"
  bitcoin_compose exec -T btc-node \
    python3 /opt/usdb/docker/scripts/tools/check_bitcoin_readiness.py \
      --wait-timeout-secs "$DATA_READY_TIMEOUT_SECS" \
      --poll-interval-secs 15
}

start_data_services() {
  local project="${PROJECT_PREFIX}-upstream"
  echo "Starting real balance-history and usdb-indexer services"
  runtime_compose "$project" "$NODE1_ENV" up -d snapshot-loader balance-history
  assert_runtime_service_image "$project" "$NODE1_ENV" balance-history usdb_services
  python3 "$READINESS_CHECKER" \
    --url http://127.0.0.1:28110 \
    --expected-service balance-history \
    --require-consensus-ready \
    --wait-timeout-secs "$DATA_READY_TIMEOUT_SECS"
  runtime_compose "$project" "$NODE1_ENV" up -d usdb-indexer
  assert_runtime_service_image "$project" "$NODE1_ENV" usdb-indexer usdb_services
  python3 "$READINESS_CHECKER" \
    --url http://127.0.0.1:28120 \
    --expected-service usdb-indexer \
    --require-consensus-ready \
    --wait-timeout-secs "$INDEXER_READY_TIMEOUT_SECS"
}

start_chain_node() {
  local index=$1
  local node_env=$2
  local project="${PROJECT_PREFIX}-node${index}"
  local rpc_port=$((18545 + (index - 1) * 10000))
  runtime_compose "$project" "$node_env" run --rm --no-deps usdb-chain-init
  runtime_compose "$project" "$node_env" up -d --no-deps usdb-chain
  assert_runtime_service_image "$project" "$node_env" usdb-chain usdb_chain
  wait_for_rpc "http://127.0.0.1:${rpc_port}"
  assert_chain_identity "http://127.0.0.1:${rpc_port}"
}

start_control_plane() {
  if [[ "$START_CONTROL_PLANE" != "1" ]]; then
    return
  fi
  runtime_compose "${PROJECT_PREFIX}-upstream" "$NODE1_ENV" up -d --no-deps usdb-control-plane
  assert_runtime_service_image "${PROJECT_PREFIX}-upstream" "$NODE1_ENV" usdb-control-plane usdb_services
  local deadline=$((SECONDS + CHAIN_READY_TIMEOUT_SECS))
  while (( SECONDS < deadline )); do
    if curl -fsS http://127.0.0.1:28140/healthz >/dev/null 2>&1; then
      return
    fi
    sleep 2
  done
  echo "Timed out waiting for control-plane health" >&2
  return 1
}

write_report() {
  local node1_height node2_height node3_height node1_peers node2_peers node3_peers
  local report_status=passed
  if [[ "$ENABLE_MINING" != "1" ]]; then
    report_status=bringup-only
  fi
  node1_height=$(block_number http://127.0.0.1:18545)
  node2_height=$(block_number http://127.0.0.1:28545)
  node3_height=$(block_number http://127.0.0.1:38545)
  node1_peers=$(rpc_call http://127.0.0.1:18545 net_peerCount '[]' | jq -er '.result')
  node2_peers=$(rpc_call http://127.0.0.1:28545 net_peerCount '[]' | jq -er '.result')
  node3_peers=$(rpc_call http://127.0.0.1:38545 net_peerCount '[]' | jq -er '.result')
  jq -n \
    --arg release_id "$(plan_value '.release_id')" \
    --arg manifest_sha256 "$(plan_value '.manifest_sha256')" \
    --arg chain_id "$(plan_value '.network.chain_id')" \
    --arg genesis_hash "$(plan_value '.network.genesis_block_hash')" \
    --arg node1_height "$node1_height" \
    --arg node2_height "$node2_height" \
    --arg node3_height "$node3_height" \
    --arg node1_peers "$node1_peers" \
    --arg node2_peers "$node2_peers" \
    --arg node3_peers "$node3_peers" \
    --arg status "$report_status" \
    --slurpfile plan "$PLAN" \
    '{
      schema_version: "usdb-three-node-release-e2e-report:v1",
      status: $status,
      release_id: $release_id,
      manifest_sha256: $manifest_sha256,
      chain_id: ($chain_id | tonumber),
      genesis_block_hash: $genesis_hash,
      images: $plan[0].images,
      nodes: [
        {index: 1, height: $node1_height, peers: $node1_peers},
        {index: 2, height: $node2_height, peers: $node2_peers},
        {index: 3, height: $node3_height, peers: $node3_peers}
      ]
    }' >"$REPORT"
}

show_failure_logs() {
  local project
  for project in upstream node1 node2 node3; do
    runtime_compose "${PROJECT_PREFIX}-${project}" "$NODE1_ENV" logs --tail 100 2>/dev/null || true
  done
  bitcoin_compose logs --tail 100 2>/dev/null || true
}

stop_projects() {
  configure_execution_images 2>/dev/null || true
  local volume_args=()
  if [[ "$KEEP_DATA" != "1" ]]; then
    volume_args=(-v)
  fi
  local index env_file
  for index in 3 2 1; do
    env_file="$WORK_DIR/node${index}.env"
    if [[ -f "$env_file" ]]; then
      runtime_compose "${PROJECT_PREFIX}-node${index}" "$env_file" down --remove-orphans "${volume_args[@]}" || true
    fi
  done
  if [[ -f "$NODE1_ENV" ]]; then
    runtime_compose "${PROJECT_PREFIX}-upstream" "$NODE1_ENV" down --remove-orphans "${volume_args[@]}" || true
    bitcoin_compose down --remove-orphans || true
  fi
  docker network rm "$NETWORK_NAME" >/dev/null 2>&1 || true
}

run_e2e() {
  preflight
  ensure_network
  trap 'if [[ $E2E_COMPLETED != 1 ]]; then show_failure_logs; fi; if [[ $KEEP_RUNNING != 1 ]]; then stop_projects; fi' EXIT
  start_bitcoin
  start_data_services

  echo "Starting node 1"
  start_chain_node 1 "$NODE1_ENV"
  start_control_plane

  echo "Starting node 2"
  start_chain_node 2 "$NODE2_ENV"
  connect_to_node1 http://127.0.0.1:28545
  wait_for_peer_count http://127.0.0.1:18545 1
  wait_for_peer_count http://127.0.0.1:28545 1

  if [[ "$ENABLE_MINING" == "1" ]]; then
    echo "Waiting for miner node 1 to produce a block before the late joiner starts"
    wait_for_height http://127.0.0.1:18545 1 >/dev/null
  fi

  echo "Starting late joiner node 3"
  start_chain_node 3 "$NODE3_ENV"
  connect_to_node1 http://127.0.0.1:38545
  wait_for_peer_count http://127.0.0.1:18545 2
  wait_for_peer_count http://127.0.0.1:38545 1
  local node1_target_hex node1_target
  node1_target_hex=$(block_number http://127.0.0.1:18545)
  node1_target=$((16#${node1_target_hex#0x}))
  wait_for_height http://127.0.0.1:28545 "$node1_target" >/dev/null
  wait_for_height http://127.0.0.1:38545 "$node1_target" >/dev/null

  echo "Restarting node 2 with its existing E2E datadir"
  runtime_compose "${PROJECT_PREFIX}-node2" "$NODE2_ENV" restart usdb-chain
  wait_for_rpc http://127.0.0.1:28545
  assert_chain_identity http://127.0.0.1:28545
  connect_to_node1 http://127.0.0.1:28545
  wait_for_height http://127.0.0.1:28545 "$node1_target" >/dev/null

  write_report
  E2E_COMPLETED=1
  if [[ "$ENABLE_MINING" == "1" ]]; then
    echo "Three-node release manifest E2E passed: $REPORT"
  else
    echo "Three-node release manifest bring-up completed without block replay: $REPORT"
  fi
  if [[ "$KEEP_RUNNING" == "1" ]]; then
    echo "E2E projects remain running under prefix: $PROJECT_PREFIX"
    trap - EXIT
  fi
}

status() {
  docker ps -a --filter "label=com.docker.compose.project=${PROJECT_PREFIX}-bitcoin"
  local project
  for project in upstream node1 node2 node3; do
    docker ps -a --filter "label=com.docker.compose.project=${PROJECT_PREFIX}-${project}"
  done
  if [[ -f "$REPORT" ]]; then
    jq . "$REPORT"
  fi
}

action=${1:-}
case "$action" in
  preflight)
    preflight
    ;;
  run)
    run_e2e
    ;;
  status)
    require_command docker
    status
    ;;
  down)
    require_command docker
    validate_project_prefix
    KEEP_DATA=$(normalize_boolean USDB_E2E_KEEP_DATA "$KEEP_DATA")
    stop_projects
    ;;
  help|--help|-h|"")
    usage
    ;;
  *)
    echo "Unknown action: $action" >&2
    usage >&2
    exit 1
    ;;
esac
