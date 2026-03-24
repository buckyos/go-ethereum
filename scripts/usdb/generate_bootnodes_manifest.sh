#!/usr/bin/env bash
set -euo pipefail

NETWORK_ID=${NETWORK_ID:-20260323}
OUTPUT_DIR=${OUTPUT_DIR:-/tmp/usdb-bootnodes}
MANIFEST_JSON=${MANIFEST_JSON:-"$OUTPUT_DIR/bootnodes-manifest.json"}
STATIC_NODES_JSON=${STATIC_NODES_JSON:-"$OUTPUT_DIR/static-nodes.json"}
BOOTNODES_TXT=${BOOTNODES_TXT:-"$OUTPUT_DIR/bootnodes.txt"}

declare -a RPC_URLS=()

usage() {
  cat <<'EOF'
Usage:
  generate_bootnodes_manifest.sh --rpc-url http://127.0.0.1:18545 [--rpc-url ...]

Environment overrides:
  NETWORK_ID
  OUTPUT_DIR
  MANIFEST_JSON
  STATIC_NODES_JSON
  BOOTNODES_TXT
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --rpc-url)
      [[ $# -ge 2 ]] || { echo "--rpc-url requires a value" >&2; exit 1; }
      RPC_URLS+=("$2")
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown argument: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
done

if [[ ${#RPC_URLS[@]} -eq 0 ]]; then
  RPC_URLS=("http://127.0.0.1:18545")
fi

mkdir -p "$OUTPUT_DIR"

fetch_enode() {
  local rpc_url=$1
  local response
  response=$(curl -sf -H 'Content-Type: application/json' \
    --data '{"jsonrpc":"2.0","method":"admin_nodeInfo","params":[],"id":1}' \
    "$rpc_url")
  printf '%s' "$response" | sed -n 's/.*"enode":"\([^"]*\)".*/\1/p'
}

json_escape() {
  local value=$1
  value=${value//\\/\\\\}
  value=${value//\"/\\\"}
  printf '%s' "$value"
}

generated_at=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
declare -a ENODES=()
declare -a ENTRY_LINES=()

for rpc_url in "${RPC_URLS[@]}"; do
  enode=$(fetch_enode "$rpc_url")
  if [[ -z "$enode" ]]; then
    echo "Failed to resolve enode from $rpc_url" >&2
    exit 1
  fi
  ENODES+=("$enode")
  ENTRY_LINES+=("    {\"rpcUrl\":\"$(json_escape "$rpc_url")\",\"enode\":\"$(json_escape "$enode")\"}")
done

{
  printf '{\n'
  printf '  "networkId": %s,\n' "$NETWORK_ID"
  printf '  "generatedAt": "%s",\n' "$generated_at"
  printf '  "bootnodes": [\n'
  for ((i=0; i<${#ENTRY_LINES[@]}; i++)); do
    printf '%s' "${ENTRY_LINES[$i]}"
    if (( i + 1 < ${#ENTRY_LINES[@]} )); then
      printf ','
    fi
    printf '\n'
  done
  printf '  ]\n'
  printf '}\n'
} >"$MANIFEST_JSON"

{
  printf '[\n'
  for ((i=0; i<${#ENODES[@]}; i++)); do
    printf '  "%s"' "$(json_escape "${ENODES[$i]}")"
    if (( i + 1 < ${#ENODES[@]} )); then
      printf ','
    fi
    printf '\n'
  done
  printf ']\n'
} >"$STATIC_NODES_JSON"

(
  IFS=,
  printf '%s\n' "${ENODES[*]}"
) >"$BOOTNODES_TXT"

echo "Generated bootnodes manifest:"
echo "  manifest:      $MANIFEST_JSON"
echo "  static-nodes:  $STATIC_NODES_JSON"
echo "  bootnodes txt: $BOOTNODES_TXT"
