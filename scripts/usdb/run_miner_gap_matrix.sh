#!/usr/bin/env bash
set -euo pipefail
ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
tier=${1:-nightly}
case "$tier" in nightly | weekly) ;; *) echo "Expected nightly or weekly" >&2; exit 2 ;; esac
: "${MATRIX_WORK_ROOT:?Prepared matrix binaries are required}"
: "${MATRIX_OUTPUT_DIR:?An isolated output directory is required}"
output_root=$MATRIX_OUTPUT_DIR
run_gap_case() {
  local name=$1 kind=$2 gap=$3 lag=$4 delay=$5
  env MATRIX_SCENARIO=anchor-gap MATRIX_SKIP_BUILD=1 MATRIX_GAP_CASE="$kind" \
    MATRIX_GAP="$gap" MATRIX_GAP_LAG="$lag" MATRIX_GAP_DELAY_SECONDS="$delay" \
    MATRIX_OUTPUT_DIR="$output_root/$name" \
    bash "$ROOT_DIR/scripts/usdb/run_usdb_upstream_fault_matrix.sh"
}
if [[ "$tier" == nightly ]]; then
  # Every arm receives a newly minted fixture and independent stores, with no
  # preceding high-anchor producer to contaminate the policy comparison.
  for gap in 0 1 2; do
    for lag in 1 2; do
      run_gap_case "gap-$gap-lag-$lag" control "$gap" "$lag" 10
    done
  done
  run_gap_case gap-2-lag-3 control 2 3 10
  run_gap_case progressive-exhaustion exhaustion 2 2 10
else
  run_gap_case mixed-gap mixed 2 2 10
  for delay in 10 30 60 120; do
    run_gap_case "burst-lag-3-delay-$delay" control 2 3 "$delay"
  done
  # Exercise actual competing branches, both independent sync gates, transport
  # outage and automatic fork-choice/reward convergence with different policies.
  env MATRIX_SCENARIO=multi-miner MATRIX_SKIP_BUILD=1 MATRIX_CYCLES=1 \
    MATRIX_MINER_GAPS=0,2,1 MATRIX_RETRY_EXPIRY=none \
    MATRIX_OUTPUT_DIR="$output_root/mixed-faults" \
    bash "$ROOT_DIR/scripts/usdb/run_usdb_upstream_fault_matrix.sh"
fi
