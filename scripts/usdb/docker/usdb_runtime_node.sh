#!/usr/bin/env bash
set -euo pipefail

data_dir="${USDB_CHAIN_DATA_DIR:-/data/usdb-chain}"
genesis_file="${USDB_GENESIS_FILE:-/network/usdb-genesis.json}"
manifest_file="${USDB_GENESIS_MANIFEST_FILE:-/network/usdb-genesis.manifest.json}"
chain_id="${USDB_CHAIN_ID:?USDB_CHAIN_ID is required}"
network_id="${USDB_NETWORK_ID:?USDB_NETWORK_ID is required}"
indexer_url="${USDB_INDEXER_RPC_URL:?USDB_INDEXER_RPC_URL is required}"
role="${USDB_NODE_ROLE:-full}"
marker_path="${data_dir}/bootstrap/usdb-init.done.json"
genesis_validator="${USDB_GENESIS_VALIDATOR:-/opt/usdb/scripts/validate_usdb_genesis.py}"
geth_bin="${USDB_GETH_BIN:-geth}"

"${genesis_validator}" \
  --genesis "${genesis_file}" \
  --manifest "${manifest_file}" \
  --chain-id "${chain_id}" \
  --network-id "${network_id}" >/dev/null

genesis_sha256="$(sha256sum "${genesis_file}" | awk '{print $1}')"
if [[ ! -f "${data_dir}/geth/chaindata/CURRENT" || ! -f "${marker_path}" ]]; then
  echo "USDB chain data is not initialized" >&2
  exit 1
fi
if ! grep -Fq "\"genesis_sha256\": \"${genesis_sha256}\"" "${marker_path}"; then
  echo "USDB chain init marker does not match the mounted genesis" >&2
  exit 1
fi
if ! grep -Fq "\"chain_id\": ${chain_id}," "${marker_path}" || ! grep -Fq "\"network_id\": ${network_id}," "${marker_path}"; then
  echo "USDB chain init marker does not match the configured chain/network identity" >&2
  exit 1
fi

case "${role}" in
  bootnode|full|miner)
    ;;
  *)
    echo "Unsupported USDB_NODE_ROLE=${role}; expected bootnode, full, or miner" >&2
    exit 1
    ;;
esac

args=(
  --datadir "${data_dir}"
  --networkid "${network_id}"
  --syncmode full
  --port "${USDB_P2P_PORT:-31303}"
  --discovery.port "${USDB_DISCOVERY_PORT:-${USDB_P2P_PORT:-31303}}"
  --maxpeers "${USDB_MAX_PEERS:-50}"
  --http
  --http.addr "${USDB_HTTP_ADDR:-0.0.0.0}"
  --http.port "${USDB_HTTP_PORT:-8545}"
  --http.api "${USDB_HTTP_APIS:-eth,net,web3,admin,miner,txpool}"
  --http.vhosts "${USDB_HTTP_VHOSTS:-localhost,127.0.0.1,usdb-chain}"
  --ws
  --ws.addr "${USDB_WS_ADDR:-0.0.0.0}"
  --ws.port "${USDB_WS_PORT:-8546}"
  --ws.api "${USDB_WS_APIS:-eth,net,web3}"
  --ws.origins "${USDB_WS_ORIGINS:-http://localhost}"
  --authrpc.addr "${USDB_AUTHRPC_ADDR:-127.0.0.1}"
  --authrpc.port "${USDB_AUTHRPC_PORT:-8551}"
  --ethash.usdb-indexer.rpcurl "${indexer_url}"
  --ethash.usdb-indexer.timeout "${USDB_INDEXER_QUERY_TIMEOUT:-5s}"
)

if [[ -n "${USDB_NAT:-}" ]]; then
  args+=(--nat "${USDB_NAT}")
fi
if [[ -n "${USDB_BOOTNODES:-}" ]]; then
  args+=(--bootnodes "${USDB_BOOTNODES}")
fi

if [[ "${role}" == "miner" ]]; then
  miner_address="${USDB_MINER_ADDRESS:?USDB_MINER_ADDRESS is required for miner role}"
  args+=(
    --mine
    --miner.threads "${USDB_MINER_THREADS:-1}"
    --miner.etherbase "${miner_address}"
    --miner.usdb-indexer.rpcurl "${indexer_url}"
    --miner.usdb-indexer.timeout "${USDB_INDEXER_QUERY_TIMEOUT:-5s}"
  )
fi

if [[ -n "${USDB_CHAIN_EXTRA_ARGS:-}" ]]; then
  read -r -a extra_args <<<"${USDB_CHAIN_EXTRA_ARGS}"
  args+=("${extra_args[@]}")
fi

echo "Starting USDB node role=${role}, chain_id=${chain_id}, network_id=${network_id}"
if [[ "${USDB_DEEP_REORG_GUARD_ENABLED:-0}" != "1" ]]; then
  exec "${geth_bin}" "${args[@]}"
fi

guard_script="${USDB_DEEP_REORG_GUARD_SCRIPT:-/opt/usdb/scripts/usdb_deep_reorg_guard.py}"
guard_state_dir="${USDB_DEEP_REORG_GUARD_STATE_DIR:-${data_dir}/recovery/deep-btc-reorg}"
guard_poll_interval="${USDB_DEEP_REORG_GUARD_POLL_INTERVAL_SECS:-5}"
guard_request_timeout="${USDB_DEEP_REORG_GUARD_REQUEST_TIMEOUT_SECS:-5}"
guard_max_errors="${USDB_DEEP_REORG_GUARD_MAX_CONSECUTIVE_ERRORS:-3}"
guard_args=(
  --state-dir "${guard_state_dir}"
  --indexer-rpc-url "${indexer_url}"
  --chain-rpc-url "http://127.0.0.1:${USDB_HTTP_PORT:-8545}"
  --poll-interval-secs "${guard_poll_interval}"
  --request-timeout-secs "${guard_request_timeout}"
  --max-consecutive-errors "${guard_max_errors}"
)

hold_halted() {
  echo "USDB node remains halted; preserve ${guard_state_dir} and replace this network generation before clearing chain data" >&2
  trap 'exit 0' TERM INT
  while true; do
    sleep 3600 &
    wait $! || true
  done
}

if ! python3 "${guard_script}" check "${guard_args[@]}"; then
  hold_halted
fi

geth_pid=""
guard_pid=""
# shellcheck disable=SC2317  # Invoked indirectly by the signal trap below.
stop_children() {
  if [[ -n "${guard_pid}" ]] && kill -0 "${guard_pid}" 2>/dev/null; then
    kill -TERM "${guard_pid}" 2>/dev/null || true
  fi
  if [[ -n "${geth_pid}" ]] && kill -0 "${geth_pid}" 2>/dev/null; then
    kill -TERM "${geth_pid}" 2>/dev/null || true
  fi
}
trap 'stop_children; wait || true; exit 0' TERM INT

"${geth_bin}" "${args[@]}" &
geth_pid=$!
python3 "${guard_script}" watch "${guard_args[@]}" &
guard_pid=$!

while kill -0 "${geth_pid}" 2>/dev/null && kill -0 "${guard_pid}" 2>/dev/null; do
  sleep 1
done

if ! kill -0 "${guard_pid}" 2>/dev/null; then
  set +e
  wait "${guard_pid}"
  guard_status=$?
  set -e
  echo "USDB deep-reorg guard stopped the chain process: status=${guard_status}" >&2
  kill -TERM "${geth_pid}" 2>/dev/null || true
  wait "${geth_pid}" 2>/dev/null || true
  hold_halted
fi

set +e
wait "${geth_pid}"
geth_status=$?
set -e
kill -TERM "${guard_pid}" 2>/dev/null || true
wait "${guard_pid}" 2>/dev/null || true
exit "${geth_status}"
