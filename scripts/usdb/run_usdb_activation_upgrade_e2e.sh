#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
# shellcheck source=lib/go_toolchain.sh
source "$ROOT_DIR/scripts/usdb/lib/go_toolchain.sh"
UPGRADE_WORK_DIR=${UPGRADE_WORK_DIR:-/tmp/usdb-activation-upgrade-e2e}
USDB_GO_TOOLCHAIN_MODE=${USDB_GO_TOOLCHAIN_MODE:-auto}
DEFAULT_GETH_BIN=${DEFAULT_GETH_BIN:-}
TAGGED_GETH_BIN=${TAGGED_GETH_BIN:-}
ACTIVATION_CONFORMANCE_BLOCK=${ACTIVATION_CONFORMANCE_BLOCK:-4}
TARGET_BLOCKS=${TARGET_BLOCKS:-6}

build_geth() {
  local output="$1"
  local tags="$2"
  usdb_build_geth "$ROOT_DIR" "$output" "$tags"
}

mkdir -p "$UPGRADE_WORK_DIR/bin"
if [[ -z "$DEFAULT_GETH_BIN" ]]; then
  DEFAULT_GETH_BIN="$UPGRADE_WORK_DIR/bin/geth-default"
  echo "[usdb-activation-upgrade] Building default geth"
  build_geth "$DEFAULT_GETH_BIN" ""
elif [[ ! -x "$DEFAULT_GETH_BIN" ]]; then
  echo "DEFAULT_GETH_BIN is not executable: ${DEFAULT_GETH_BIN}" >&2
  exit 1
fi
if [[ -z "$TAGGED_GETH_BIN" ]]; then
  TAGGED_GETH_BIN="$UPGRADE_WORK_DIR/bin/geth-activation-conformance"
  echo "[usdb-activation-upgrade] Building activation-conformance geth"
  build_geth "$TAGGED_GETH_BIN" "usdb_activation_conformance"
elif [[ ! -x "$TAGGED_GETH_BIN" ]]; then
  echo "TAGGED_GETH_BIN is not executable: ${TAGGED_GETH_BIN}" >&2
  exit 1
fi

echo "[usdb-activation-upgrade] Running cross-process activation E2E"
env \
  WORK_DIR="$UPGRADE_WORK_DIR/e2e" \
  GETH_BIN="$DEFAULT_GETH_BIN" \
  PRE_ACTIVATION_GETH_BIN="$DEFAULT_GETH_BIN" \
  POST_ACTIVATION_GETH_BIN="$TAGGED_GETH_BIN" \
  ACTIVATION_CONFORMANCE_BLOCK="$ACTIVATION_CONFORMANCE_BLOCK" \
  ACTIVATION_FRESH_VALIDATOR_CHECK=1 \
  TARGET_BLOCKS="$TARGET_BLOCKS" \
  "$ROOT_DIR/scripts/usdb/run_usdb_profile_e2e.sh"
