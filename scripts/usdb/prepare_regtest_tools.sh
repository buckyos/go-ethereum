#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
USDB_REPO_DIR=${USDB_REPO_DIR:-"$SCRIPT_DIR/../../../usdb"}
OUTPUT_DIR=""

usage() {
  cat <<'HELP'
Usage: scripts/usdb/prepare_regtest_tools.sh --output <directory> [--usdb-repo <directory>]

Build Bitcoin Core and Ord from the release Docker stages in the USDB checkout
pinned by ci-revisions.json. Docker and Python 3.11+ are required. No services start.
HELP
}

while (($# > 0)); do
  case "$1" in
    --output)
      OUTPUT_DIR="${2:?missing --output value}"
      shift 2
      ;;
    --usdb-repo)
      USDB_REPO_DIR="${2:?missing --usdb-repo value}"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

[[ -n "$OUTPUT_DIR" ]] || {
  echo "--output is required" >&2
  exit 2
}
for command in docker git python3; do
  command -v "$command" >/dev/null 2>&1 || {
    echo "Required command is unavailable: $command" >&2
    exit 1
  }
done

# Reject an unpinned checkout or inconsistent release inputs before any build.
python3 "$SCRIPT_DIR/regtest_tools.py" describe --usdb-repo "$USDB_REPO_DIR"
OUTPUT_DIR=$(python3 -c 'import os,sys; print(os.path.abspath(sys.argv[1]))' "$OUTPUT_DIR")
[[ ! -e "$OUTPUT_DIR" ]] || {
  echo "Refusing to replace regtest tool directory: $OUTPUT_DIR" >&2
  exit 1
}
mkdir -p "$(dirname "$OUTPUT_DIR")"
temporary=$(mktemp -d "$(dirname "$OUTPUT_DIR")/.regtest-tools.XXXXXX")
ord_container=""
core_container=""
cleanup() {
  [[ -z "$ord_container" ]] || docker rm "$ord_container" >/dev/null || true
  [[ -z "$core_container" ]] || docker rm "$core_container" >/dev/null || true
  rm -rf "$temporary"
}
trap cleanup EXIT

# Use the production stages, including the reviewed Ord Cargo.lock and the
# Bitcoin checksum/signature verifier, instead of maintaining parallel pins.
docker build --platform linux/amd64 --target ord-builder \
  -f "$USDB_REPO_DIR/docker/Dockerfile.usdb-services" \
  --iidfile "$temporary/ord-image" "$USDB_REPO_DIR"
docker build --platform linux/amd64 --target verifier \
  -f "$USDB_REPO_DIR/docker/Dockerfile.bitcoin-core" \
  --iidfile "$temporary/core-image" "$USDB_REPO_DIR"
ord_container=$(docker create "$(cat "$temporary/ord-image")" /bin/true)
core_container=$(docker create "$(cat "$temporary/core-image")" /bin/true)
mkdir -p "$temporary/output/ord" "$temporary/output/bitcoin/bin"
docker cp "$ord_container:/opt/ord/bin/ord" "$temporary/output/ord/ord"
for executable in bitcoind bitcoin-cli; do
  docker cp "$core_container:/opt/bitcoin/bin/$executable" "$temporary/output/bitcoin/bin/$executable"
done
docker cp "$core_container:/opt/bitcoin/share/core-artifact-provenance.json" \
  "$temporary/output/bitcoin/core-artifact-provenance.json"
chmod +x "$temporary/output/ord/ord" "$temporary/output/bitcoin/bin/"*
python3 "$SCRIPT_DIR/regtest_tools.py" record --usdb-repo "$USDB_REPO_DIR" --tools "$temporary/output"

mv "$temporary/output" "$OUTPUT_DIR"
echo "[usdb-regtest-tools] prepared tools at $OUTPUT_DIR"
