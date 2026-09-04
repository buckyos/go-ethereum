#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
FAILURE_WORK_DIR=${FAILURE_WORK_DIR:-/tmp/usdb-profile-failure-matrix-e2e}
TARGET_BLOCKS=${TARGET_BLOCKS:-4}

echo "[usdb-profile-failure-matrix] Running indexer outage/recovery and selector tamper checks"
env \
  WORK_DIR="$FAILURE_WORK_DIR" \
  INDEXER_OUTAGE_CHECK=1 \
  SELECTOR_TAMPER_CHECK=1 \
  TARGET_BLOCKS="$TARGET_BLOCKS" \
  BTC_RPC_PORT="${BTC_RPC_PORT:-28332}" \
  BTC_P2P_PORT="${BTC_P2P_PORT:-28333}" \
  BH_RPC_PORT="${BH_RPC_PORT:-28310}" \
  USDB_INDEXER_RPC_PORT="${USDB_INDEXER_RPC_PORT:-28320}" \
  ORD_RPC_PORT="${ORD_RPC_PORT:-28330}" \
  "$ROOT_DIR/scripts/usdb/run_usdb_profile_e2e.sh"
