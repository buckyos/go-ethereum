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
  "$ROOT_DIR/scripts/usdb/run_usdb_profile_e2e.sh"
