#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
# shellcheck source=lib/go_toolchain.sh
source "$ROOT_DIR/scripts/usdb/lib/go_toolchain.sh"
UPGRADE_WORK_DIR=${UPGRADE_WORK_DIR:-/tmp/usdb-economic-activation-upgrade-e2e}
USDB_GO_TOOLCHAIN_MODE=${USDB_GO_TOOLCHAIN_MODE:-auto}
DEFAULT_GETH_BIN=${DEFAULT_GETH_BIN:-}
V2_GETH_BIN=${V2_GETH_BIN:-}
V3_GETH_BIN=${V3_GETH_BIN:-}
ECONOMIC_CONFORMANCE_V2_BLOCK=${ECONOMIC_CONFORMANCE_V2_BLOCK:-3}
ECONOMIC_CONFORMANCE_V3_BLOCK=${ECONOMIC_CONFORMANCE_V3_BLOCK:-6}
TARGET_BLOCKS=${TARGET_BLOCKS:-8}

build_geth() {
  local output="$1"
  local tags="$2"
  usdb_build_geth "$ROOT_DIR" "$output" "$tags"
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
  BTC_RPC_PORT="${BTC_RPC_PORT:-28532}" \
  BTC_P2P_PORT="${BTC_P2P_PORT:-28533}" \
  BH_RPC_PORT="${BH_RPC_PORT:-28510}" \
  USDB_INDEXER_RPC_PORT="${USDB_INDEXER_RPC_PORT:-28520}" \
  ORD_RPC_PORT="${ORD_RPC_PORT:-28530}" \
  "$ROOT_DIR/scripts/usdb/run_usdb_profile_e2e.sh"
