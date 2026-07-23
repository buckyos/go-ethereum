#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
UPGRADE_WORK_DIR=${UPGRADE_WORK_DIR:-/tmp/usdb-activation-upgrade-e2e}
GO_BIN=${GO_BIN:-/usr/local/go/bin/go}
DEFAULT_GETH_BIN=${DEFAULT_GETH_BIN:-}
TAGGED_GETH_BIN=${TAGGED_GETH_BIN:-}
ACTIVATION_CONFORMANCE_BLOCK=${ACTIVATION_CONFORMANCE_BLOCK:-4}
TARGET_BLOCKS=${TARGET_BLOCKS:-6}

if [[ -z "${GETH_BUILD_LDFLAGS+x}" ]]; then
  go_link_help=$("$GO_BIN" tool link -h 2>&1 || true)
  if grep -q -- "-checklinkname" <<<"$go_link_help"; then
    GETH_BUILD_LDFLAGS=-checklinkname=0
  else
    GETH_BUILD_LDFLAGS=
  fi
  unset go_link_help
fi

build_geth() {
  local output="$1"
  local tags="$2"
  local -a args=(build -o "$output")
  if [[ -n "$GETH_BUILD_LDFLAGS" ]]; then
    args+=(-ldflags="$GETH_BUILD_LDFLAGS")
  fi
  if [[ -n "$tags" ]]; then
    args+=(-tags "$tags")
  fi
  args+=(./cmd/geth)
  (
    cd "$ROOT_DIR"
    "$GO_BIN" "${args[@]}"
  )
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
  TARGET_BLOCKS="$TARGET_BLOCKS" \
  "$ROOT_DIR/scripts/usdb/run_usdb_profile_e2e.sh"
