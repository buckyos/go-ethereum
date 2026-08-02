#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
SOURCE_DAO_REPO=${SOURCE_DAO_REPO:-"$ROOT_DIR/../SourceDAO"}
USDB_REPO=${USDB_REPO:-"$ROOT_DIR/../usdb"}
WORK_DIR=${WORK_DIR:-/tmp/usdb-public-release-candidate-e2e}
SOURCE_ROOT=${SOURCE_ROOT:-"$WORK_DIR/source"}
GO_SOURCE_DIR=${GO_SOURCE_DIR:-"$SOURCE_ROOT/go-ethereum"}
SOURCE_DAO_SOURCE_DIR=${SOURCE_DAO_SOURCE_DIR:-"$SOURCE_ROOT/SourceDAO"}
NETWORK_WORK_DIR=${NETWORK_WORK_DIR:-"$WORK_DIR/network"}
GETH_GO=${GETH_GO:-/home/bucky/.cache/geth-go-1.18.5-linux-amd64/go/bin/go}
GETH_BIN=${GETH_BIN:-"$WORK_DIR/bin/geth"}
GOCACHE=${GOCACHE:-"$WORK_DIR/go-cache"}
PUBLIC_RELEASE_ID=${PUBLIC_RELEASE_ID:-usdb-public-release-e2e-v1}
PUBLIC_RELEASE_SIGNING_KEY_ID=${PUBLIC_RELEASE_SIGNING_KEY_ID:-usdb-public-release-e2e-key-1}
PUBLIC_RELEASE_FEE_SPLIT_BLOCK=${PUBLIC_RELEASE_FEE_SPLIT_BLOCK:-192}
BOOTSTRAP_ACCEPTANCE_CONFIRMATIONS=${BOOTSTRAP_ACCEPTANCE_CONFIRMATIONS:-3}
ALLOW_DIRTY_RELEASE_E2E=${ALLOW_DIRTY_RELEASE_E2E:-0}

require_command() {
  local command=$1
  if ! command -v "$command" >/dev/null; then
    echo "Required command is unavailable: $command" >&2
    exit 1
  fi
}

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
      exit 1
      ;;
  esac
}

assert_release_source_state() {
  local repo=$1
  local label=$2
  local status
  status="$(git -C "$repo" status --short)"
  if [[ -n "$status" && "$ALLOW_DIRTY_RELEASE_E2E" != "1" ]]; then
    echo "$label worktree is dirty; public release builds require reviewed commits:" >&2
    printf '%s\n' "$status" >&2
    exit 1
  fi
  if [[ -n "$status" ]]; then
    echo "$label worktree is dirty; continuing only because ALLOW_DIRTY_RELEASE_E2E=1"
  fi
}

snapshot_worktree() {
  local source_repo=$1
  local target_dir=$2
  mkdir -p "$target_dir"
  (
    cd "$source_repo"
    git ls-files --cached --others --exclude-standard -z |
      tar --null -T - -cf -
  ) | (
    cd "$target_dir"
    tar -xf -
  )
}

prepare_clean_sources() {
  rm -rf "$SOURCE_ROOT"
  mkdir -p "$SOURCE_ROOT"
  echo "Creating clean source snapshots without build outputs"
  snapshot_worktree "$ROOT_DIR" "$GO_SOURCE_DIR"
  snapshot_worktree "$SOURCE_DAO_REPO" "$SOURCE_DAO_SOURCE_DIR"
  if [[ ! -d "$SOURCE_DAO_REPO/node_modules" ]]; then
    echo "SourceDAO node_modules is missing: $SOURCE_DAO_REPO/node_modules" >&2
    exit 1
  fi
  ln -s "$SOURCE_DAO_REPO/node_modules" "$SOURCE_DAO_SOURCE_DIR/node_modules"
}

load_node_toolchain() {
  # nvm is installed per-user and cannot be resolved statically by shellcheck.
  # shellcheck source=/dev/null
  source "$HOME/.nvm/nvm.sh"
  nvm use 24 >/dev/null
}

build_clean_artifacts() {
  rm -rf "${GOCACHE:?}" "${WORK_DIR:?}/bin"
  mkdir -p "$GOCACHE" "$WORK_DIR/bin"
  echo "Building geth from an isolated source snapshot and empty Go cache"
  (
    cd "$GO_SOURCE_DIR"
    GOCACHE="$GOCACHE" "$GETH_GO" build \
      -trimpath \
      -buildvcs=false \
      -o "$GETH_BIN" \
      ./cmd/geth
  )
  "$GETH_BIN" version >/dev/null

  echo "Building and auditing SourceDAO USDB artifacts from an isolated source snapshot"
  (
    cd "$SOURCE_DAO_SOURCE_DIR"
    load_node_toolchain
    npm run build:usdb
    npm run test:usdb:audit
  )
}

prepare_candidate_config() {
  local source_config="$GO_SOURCE_DIR/tools/config/usdb-local-chain.json"
  local target_config="$WORK_DIR/usdb-public-release-chain.json"
  python3 - "$source_config" "$target_config" "$PUBLIC_RELEASE_FEE_SPLIT_BLOCK" <<'PY'
import json
import pathlib
import sys

source, target, fee_split_block = sys.argv[1:]
value = int(fee_split_block)
if value <= 0:
    raise SystemExit("PUBLIC_RELEASE_FEE_SPLIT_BLOCK must be positive")
config = json.loads(pathlib.Path(source).read_text(encoding="utf-8"))
config["dividendFeeSplitBlock"] = value
pathlib.Path(target).write_text(
    json.dumps(config, indent=2) + "\n",
    encoding="utf-8",
)
PY
  printf '%s\n' "$target_config"
}

prepare_ephemeral_release_key() {
  local private_key=$1
  local trusted_keys=$2
  local public_der="$NETWORK_WORK_DIR/usdb-public-release-signing-key.der"
  local public_key_base64

  mkdir -p "$NETWORK_WORK_DIR"
  openssl genpkey -algorithm ED25519 -out "$private_key" >/dev/null 2>&1
  chmod 600 "$private_key"
  openssl pkey \
    -in "$private_key" \
    -pubout \
    -outform DER \
    -out "$public_der" >/dev/null 2>&1
  public_key_base64="$(tail -c 32 "$public_der" | base64 -w0)"
  python3 - "$trusted_keys" "$PUBLIC_RELEASE_SIGNING_KEY_ID" "$public_key_base64" <<'PY'
import json
import pathlib
import sys

path, key_id, public_key = sys.argv[1:]
trusted = {
    "schema_version": "uip-0010-public-release-trusted-keys:v1",
    "keys": [
        {
            "key_id": key_id,
            "algorithm": "ed25519",
            "public_key_base64": public_key,
        }
    ],
}
pathlib.Path(path).write_text(
    json.dumps(trusted, indent=2) + "\n",
    encoding="utf-8",
)
PY
  echo "Generated an ephemeral E2E-only release signing key: $PUBLIC_RELEASE_SIGNING_KEY_ID"
}

write_e2e_report() {
  local report_file="$WORK_DIR/public-release-e2e-report.json"
  python3 - \
    "$report_file" \
    "$PUBLIC_RELEASE_ID" \
    "$NETWORK_WORK_DIR/usdb-bootstrap-genesis.json" \
    "$NETWORK_WORK_DIR/usdb-bootstrap-acceptance.json" \
    "$NETWORK_WORK_DIR/usdb-public-release-manifest.json" \
    "$NETWORK_WORK_DIR/usdb-public-release-manifest.sig.json" \
    "$GETH_BIN" \
    "$(git -C "$ROOT_DIR" rev-parse HEAD)" \
    "$(git -C "$SOURCE_DAO_REPO" rev-parse HEAD)" \
    "$(git -C "$USDB_REPO" rev-parse HEAD)" <<'PY'
import hashlib
import json
import pathlib
import sys

(
    report_path,
    release_id,
    genesis_path,
    acceptance_path,
    manifest_path,
    signature_path,
    geth_path,
    geth_commit,
    sourcedao_commit,
    usdb_commit,
) = sys.argv[1:]

def digest(path: str) -> str:
    return hashlib.sha256(pathlib.Path(path).read_bytes()).hexdigest()

manifest = json.loads(pathlib.Path(manifest_path).read_text(encoding="utf-8"))
report = {
    "schema_version": "usdb-public-release-e2e-report:v1",
    "status": "passed",
    "release_id": release_id,
    "source_commits": {
        "go_ethereum": geth_commit,
        "source_dao": sourcedao_commit,
        "usdb": usdb_commit,
    },
    "artifacts": {
        "geth_sha256": digest(geth_path),
        "genesis_sha256": digest(genesis_path),
        "acceptance_sha256": digest(acceptance_path),
        "manifest_sha256": digest(manifest_path),
        "signature_sha256": digest(signature_path),
    },
    "chain": {
        "chain_id": manifest["chain_id"],
        "network_id": manifest["network_id"],
        "genesis_hash": manifest["genesis"]["block_hash"],
        "checkpoint": manifest["acceptance"]["checkpoint"],
        "confirmation_depth": manifest["acceptance"]["confirmation_depth"],
        "fee_split_block": manifest["fee_policy"]["activation_block"],
        "bootnodes": manifest["bootnodes"]["enodes"],
    },
}
pathlib.Path(report_path).write_text(
    json.dumps(report, indent=2) + "\n",
    encoding="utf-8",
)
print(f"Public release E2E report: {report_path}")
PY
}

main() {
  require_command base64
  require_command git
  require_command openssl
  require_command python3
  require_command tar
  ALLOW_DIRTY_RELEASE_E2E="$(normalize_boolean ALLOW_DIRTY_RELEASE_E2E "$ALLOW_DIRTY_RELEASE_E2E")"

  if [[ ! -x "$GETH_GO" ]]; then
    echo "GETH_GO is not executable: $GETH_GO" >&2
    exit 1
  fi
  for repo in "$ROOT_DIR" "$SOURCE_DAO_REPO" "$USDB_REPO"; do
    if [[ ! -d "$repo/.git" ]]; then
      echo "Missing required git checkout: $repo" >&2
      exit 1
    fi
  done
  assert_release_source_state "$ROOT_DIR" "go-ethereum"
  assert_release_source_state "$SOURCE_DAO_REPO" "SourceDAO"
  assert_release_source_state "$USDB_REPO" "usdb"

  rm -rf "$WORK_DIR"
  mkdir -p "$WORK_DIR"
  prepare_clean_sources
  build_clean_artifacts

  local candidate_config private_key trusted_keys
  candidate_config="$(prepare_candidate_config)"
  private_key="$NETWORK_WORK_DIR/usdb-public-release-signing-key.pem"
  trusted_keys="$NETWORK_WORK_DIR/usdb-public-release-trusted-keys.json"
  prepare_ephemeral_release_key "$private_key" "$trusted_keys"

  echo "Running candidate public release lifecycle with an ephemeral test signer"
  env \
    WORK_DIR="$NETWORK_WORK_DIR" \
    GETH_BIN="$GETH_BIN" \
    SOURCE_DAO_DIR="$SOURCE_DAO_SOURCE_DIR" \
    SOURCE_DAO_FULL_CONFIG="$SOURCE_DAO_SOURCE_DIR/tools/config/sourcedao-bootstrap-full.example.json" \
    USDB_ARTIFACTS="$SOURCE_DAO_SOURCE_DIR/artifacts-usdb" \
    USDB_CONFIG="$candidate_config" \
    RUN_SMOKE=0 \
    RUN_FULL_BOOTSTRAP=1 \
    START_JOINER_AFTER_BOOTSTRAP=1 \
    RESTART_NODE1_AFTER_BOOTSTRAP=1 \
    RUN_PUBLIC_RELEASE_E2E=1 \
    KEEP_RUNNING=0 \
    NODE2_GCMODE=archive \
    BOOTSTRAP_ACCEPTANCE_CONFIRMATIONS="$BOOTSTRAP_ACCEPTANCE_CONFIRMATIONS" \
    PUBLIC_RELEASE_ID="$PUBLIC_RELEASE_ID" \
    PUBLIC_RELEASE_SIGNING_KEY_ID="$PUBLIC_RELEASE_SIGNING_KEY_ID" \
    PUBLIC_RELEASE_SIGNING_KEY_FILE="$private_key" \
    PUBLIC_RELEASE_TRUSTED_KEYS_FILE="$trusted_keys" \
    RPC_WAIT_SECONDS=180 \
    FEE_PROBE_TIMEOUT_MS=900000 \
    "$GO_SOURCE_DIR/scripts/usdb/run_local_two_node_network.sh"

  write_e2e_report
  echo "USDB candidate public release E2E passed."
}

main "$@"
