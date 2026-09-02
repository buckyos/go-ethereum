#!/usr/bin/env bash
set -euo pipefail

OUTPUT_DIR=""

BITCOIN_VERSION=28.1
BITCOIN_ARCH=x86_64-linux-gnu
BITCOIN_ARCHIVE_SHA256=07f77afd326639145b9ba9562912b2ad2ccec47b8a305bd075b4f4cb127b7ed7
ORD_VERSION=0.23.3
ORD_REVISION=ba60f87b530c01b15f6f8645e2ed4ef52f3f9f74

usage() {
  cat <<'EOF'
Usage: scripts/usdb/prepare_regtest_tools.sh --output <directory>

Download the pinned Bitcoin Core release and build the pinned ord revision used
by deterministic USDB nightly and weekly regtest jobs.
EOF
}

while (($# > 0)); do
  case "$1" in
    --output)
      OUTPUT_DIR="${2:?missing --output value}"
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
for command in cargo curl git python3 sha256sum tar; do
  command -v "$command" >/dev/null 2>&1 || {
    echo "Required command is unavailable: $command" >&2
    exit 1
  }
done

OUTPUT_DIR=$(python3 -c 'import os,sys; print(os.path.abspath(sys.argv[1]))' "$OUTPUT_DIR")
[[ ! -e "$OUTPUT_DIR" ]] || {
  echo "Refusing to replace regtest tool directory: $OUTPUT_DIR" >&2
  exit 1
}
mkdir -p "$(dirname "$OUTPUT_DIR")"
temporary=$(mktemp -d "$(dirname "$OUTPUT_DIR")/.regtest-tools.XXXXXX")
cleanup() {
  rm -rf "$temporary"
}
trap cleanup EXIT

archive="bitcoin-${BITCOIN_VERSION}-${BITCOIN_ARCH}.tar.gz"
release_url="https://bitcoincore.org/bin/bitcoin-core-${BITCOIN_VERSION}/${archive}"
echo "[usdb-regtest-tools] downloading Bitcoin Core ${BITCOIN_VERSION}"
curl --fail --location --silent --show-error "$release_url" --output "$temporary/$archive"
printf '%s  %s\n' "$BITCOIN_ARCHIVE_SHA256" "$temporary/$archive" | sha256sum --check --strict
tar -xzf "$temporary/$archive" -C "$temporary"
mkdir -p "$temporary/output/bitcoin/bin"
install -m 0755 \
  "$temporary/bitcoin-${BITCOIN_VERSION}/bin/bitcoind" \
  "$temporary/bitcoin-${BITCOIN_VERSION}/bin/bitcoin-cli" \
  "$temporary/output/bitcoin/bin/"

echo "[usdb-regtest-tools] building ord ${ORD_VERSION}@${ORD_REVISION}"
git init --quiet "$temporary/ord-src"
git -C "$temporary/ord-src" remote add origin https://github.com/ordinals/ord.git
git -C "$temporary/ord-src" fetch --quiet --depth=1 origin "$ORD_REVISION"
git -C "$temporary/ord-src" checkout --quiet --detach FETCH_HEAD
[[ "$(git -C "$temporary/ord-src" rev-parse HEAD)" == "$ORD_REVISION" ]] || {
  echo "Fetched ord revision does not match the pinned commit" >&2
  exit 1
}
(cd "$temporary/ord-src" && cargo build --release --locked --bin ord)
mkdir -p "$temporary/output/ord"
install -m 0755 "$temporary/ord-src/target/release/ord" "$temporary/output/ord/ord"
[[ "$("$temporary/output/ord/ord" --version)" == "ord ${ORD_VERSION}" ]] || {
  echo "Built ord binary does not report the pinned version ${ORD_VERSION}" >&2
  exit 1
}

python3 - \
  "$temporary/output/toolchain-manifest.json" \
  "$BITCOIN_VERSION" \
  "$BITCOIN_ARCHIVE_SHA256" \
  "$ORD_VERSION" \
  "$ORD_REVISION" \
  "$temporary/output/bitcoin/bin/bitcoind" \
  "$temporary/output/bitcoin/bin/bitcoin-cli" \
  "$temporary/output/ord/ord" <<'PY'
import hashlib
import json
import pathlib
import sys

output, bitcoin_version, bitcoin_archive_sha256, ord_version, ord_revision, *files = sys.argv[1:]
root = pathlib.Path(output).parent
entries = {}
for name in files:
    path = pathlib.Path(name)
    entries[path.relative_to(root).as_posix()] = hashlib.sha256(path.read_bytes()).hexdigest()
manifest = {
    "schema_version": "usdb-regtest-tools:v1",
    "bitcoin_core": {
        "version": bitcoin_version,
        "archive_sha256": bitcoin_archive_sha256,
    },
    "ord": {"version": ord_version, "revision": ord_revision},
    "files": entries,
}
pathlib.Path(output).write_text(
    json.dumps(manifest, indent=2, sort_keys=True) + "\n", encoding="utf-8"
)
PY

mv "$temporary/output" "$OUTPUT_DIR"
echo "[usdb-regtest-tools] prepared tools at $OUTPUT_DIR"
