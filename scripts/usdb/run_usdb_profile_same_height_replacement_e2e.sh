#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
REPLACEMENT_WORK_DIR=${REPLACEMENT_WORK_DIR:-/tmp/usdb-profile-same-height-replacement-e2e}
TARGET_BLOCKS=${TARGET_BLOCKS:-4}

echo "[usdb-profile-same-height] Running stale-selector rejection after BTC same-height replacement"
env \
  WORK_DIR="$REPLACEMENT_WORK_DIR" \
  BTC_STATE_TRANSITION=same-height-replacement \
  TARGET_BLOCKS="$TARGET_BLOCKS" \
  "$ROOT_DIR/scripts/usdb/run_usdb_profile_historical_stability_e2e.sh"
