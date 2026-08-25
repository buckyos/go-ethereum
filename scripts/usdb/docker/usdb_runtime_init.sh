#!/usr/bin/env bash
set -euo pipefail

data_dir="${USDB_CHAIN_DATA_DIR:-/data/usdb-chain}"
genesis_file="${USDB_GENESIS_FILE:-/network/usdb-genesis.json}"
manifest_file="${USDB_GENESIS_MANIFEST_FILE:-/network/usdb-genesis.manifest.json}"
chain_id="${USDB_CHAIN_ID:?USDB_CHAIN_ID is required}"
network_id="${USDB_NETWORK_ID:?USDB_NETWORK_ID is required}"
marker_path="${data_dir}/bootstrap/usdb-init.done.json"

/opt/usdb/scripts/validate_usdb_genesis.py \
  --genesis "${genesis_file}" \
  --manifest "${manifest_file}" \
  --chain-id "${chain_id}" \
  --network-id "${network_id}"

genesis_sha256="$(sha256sum "${genesis_file}" | awk '{print $1}')"
marker_matches="false"
if [[ -f "${marker_path}" ]]; then
  marker_sha256="$(sed -nE 's/^[[:space:]]*"genesis_sha256"[[:space:]]*:[[:space:]]*"([0-9a-f]+)".*/\1/p' "${marker_path}" | head -n 1)"
  marker_chain_id="$(sed -nE 's/^[[:space:]]*"chain_id"[[:space:]]*:[[:space:]]*([0-9]+).*/\1/p' "${marker_path}" | head -n 1)"
  marker_network_id="$(sed -nE 's/^[[:space:]]*"network_id"[[:space:]]*:[[:space:]]*([0-9]+).*/\1/p' "${marker_path}" | head -n 1)"
  if [[ "${marker_sha256}" == "${genesis_sha256}" && "${marker_chain_id}" == "${chain_id}" && "${marker_network_id}" == "${network_id}" ]]; then
    marker_matches="true"
  fi
fi

if [[ "${marker_matches}" == "true" && -f "${data_dir}/geth/chaindata/CURRENT" ]]; then
  echo "USDB chain data already matches the mounted genesis; skipping geth init"
  exit 0
fi

if [[ -d "${data_dir}/geth" ]] && find "${data_dir}/geth" -mindepth 1 -print -quit | grep -q .; then
  echo "Existing USDB chain data does not match the mounted genesis artifact" >&2
  exit 1
fi

mkdir -p "${data_dir}"
geth --datadir "${data_dir}" init "${genesis_file}"

mkdir -p "$(dirname "${marker_path}")"
tmp_marker="${marker_path}.tmp"
cat >"${tmp_marker}" <<EOF
{
  "chain_id": ${chain_id},
  "network_id": ${network_id},
  "genesis_file": "${genesis_file}",
  "genesis_manifest_file": "${manifest_file}",
  "genesis_sha256": "${genesis_sha256}",
  "initialized_at": "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
}
EOF
mv "${tmp_marker}" "${marker_path}"
echo "USDB chain data initialized under ${data_dir}"
