#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
UPGRADE_WORK_DIR=${UPGRADE_WORK_DIR:-/tmp/usdb-economic-activation-upgrade-e2e}
GO_BIN=${GO_BIN:-/usr/local/go/bin/go}
DEFAULT_GETH_BIN=${DEFAULT_GETH_BIN:-}
V2_GETH_BIN=${V2_GETH_BIN:-}
V3_GETH_BIN=${V3_GETH_BIN:-}
ECONOMIC_CONFORMANCE_V2_BLOCK=${ECONOMIC_CONFORMANCE_V2_BLOCK:-3}
ECONOMIC_CONFORMANCE_V3_BLOCK=${ECONOMIC_CONFORMANCE_V3_BLOCK:-6}
TARGET_BLOCKS=${TARGET_BLOCKS:-8}

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
    env GOCACHE="${GOCACHE:-/tmp/usdb-economic-activation-go-cache}" \
      "$GO_BIN" "${args[@]}"
  )
}

mkdir -p "$UPGRADE_WORK_DIR/bin"
if [[ -z "$DEFAULT_GETH_BIN" ]]; then
  DEFAULT_GETH_BIN="$UPGRADE_WORK_DIR/bin/geth-default"
  echo "[usdb-economic-activation] Building default geth"
  build_geth "$DEFAULT_GETH_BIN" ""
elif [[ ! -x "$DEFAULT_GETH_BIN" ]]; then
  echo "DEFAULT_GETH_BIN is not executable: ${DEFAULT_GETH_BIN}" >&2
  exit 1
fi
if [[ -z "$V2_GETH_BIN" ]]; then
  V2_GETH_BIN="$UPGRADE_WORK_DIR/bin/geth-economic-conformance-v2"
  echo "[usdb-economic-activation] Building fake-v2 geth"
  build_geth "$V2_GETH_BIN" "usdb_economic_conformance_v2"
elif [[ ! -x "$V2_GETH_BIN" ]]; then
  echo "V2_GETH_BIN is not executable: ${V2_GETH_BIN}" >&2
  exit 1
fi
if [[ -z "$V3_GETH_BIN" ]]; then
  V3_GETH_BIN="$UPGRADE_WORK_DIR/bin/geth-economic-conformance-v3"
  echo "[usdb-economic-activation] Building fake-v3 geth"
  build_geth "$V3_GETH_BIN" "usdb_economic_conformance_v3"
elif [[ ! -x "$V3_GETH_BIN" ]]; then
  echo "V3_GETH_BIN is not executable: ${V3_GETH_BIN}" >&2
  exit 1
fi

echo "[usdb-economic-activation] Running default -> fake v2 -> fake v3 E2E"
rm -rf "$UPGRADE_WORK_DIR/e2e"
env \
  WORK_DIR="$UPGRADE_WORK_DIR/e2e" \
  GETH_BIN="$DEFAULT_GETH_BIN" \
  PRE_ACTIVATION_GETH_BIN="$DEFAULT_GETH_BIN" \
  MID_ACTIVATION_GETH_BIN="$V2_GETH_BIN" \
  POST_ACTIVATION_GETH_BIN="$V3_GETH_BIN" \
  ECONOMIC_CONFORMANCE_V2_BLOCK="$ECONOMIC_CONFORMANCE_V2_BLOCK" \
  ECONOMIC_CONFORMANCE_V3_BLOCK="$ECONOMIC_CONFORMANCE_V3_BLOCK" \
  ACTIVATION_FRESH_VALIDATOR_CHECK=1 \
  TARGET_BLOCKS="$TARGET_BLOCKS" \
  "$ROOT_DIR/scripts/usdb/run_usdb_profile_e2e.sh"
