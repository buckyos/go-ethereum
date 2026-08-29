#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
LIVE_STATE_WORK_DIR=${LIVE_STATE_WORK_DIR:-$(mktemp -d /tmp/usdb-miner-live-state-e2e-XXXXXX)}
OUTAGE_OBSERVE_SECONDS=${OUTAGE_OBSERVE_SECONDS:-6}

echo "[usdb-miner-live-state] Running persistent miner consume/remint and outage/recovery checks"
echo "[usdb-miner-live-state] Isolated work directory: ${LIVE_STATE_WORK_DIR}"
env \
  WORK_DIR="$LIVE_STATE_WORK_DIR" \
  MINER_LIVE_STATE_CHECK=1 \
  OUTAGE_OBSERVE_SECONDS="$OUTAGE_OBSERVE_SECONDS" \
  "$ROOT_DIR/scripts/usdb/run_usdb_profile_e2e.sh"
