#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
OUTPUT_ROOT=${USDB_POW_LADDER_OUTPUT_ROOT:-/tmp/usdb-pow-calibration-ladder}
PROFILE_PREFIX=${USDB_POW_LADDER_PROFILE_PREFIX:-local-cpu-pilot}
INITIAL_DIFFICULTY=${USDB_POW_LADDER_INITIAL_DIFFICULTY:-0x2000}
MINIMUM_DIFFICULTY=${USDB_POW_LADDER_MINIMUM_DIFFICULTY:-$INITIAL_DIFFICULTY}
MAX_ROUNDS=${USDB_POW_LADDER_MAX_ROUNDS:-5}
TARGET_BLOCK_SECONDS=${USDB_POW_LADDER_TARGET_BLOCK_SECONDS:-13}
SAMPLE_BLOCKS=${USDB_POW_LADDER_SAMPLE_BLOCKS:-16}
CONFIRMATIONS=${USDB_POW_LADDER_CONFIRMATIONS:-2}
DAG_WARMUP_BLOCKS=${USDB_POW_LADDER_DAG_WARMUP_BLOCKS:-4}
MINER_THREADS=${USDB_POW_LADDER_MINER_THREADS:-1}
GETH_BIN=${GETH_BIN:-"$OUTPUT_ROOT/geth"}

require_positive_integer() {
  local name="$1"
  local value="$2"
  if [[ ! "$value" =~ ^[1-9][0-9]*$ ]]; then
    echo "${name} must be a positive integer, have: ${value}" >&2
    exit 2
  fi
}

for pair in \
  "USDB_POW_LADDER_MAX_ROUNDS:$MAX_ROUNDS" \
  "USDB_POW_LADDER_TARGET_BLOCK_SECONDS:$TARGET_BLOCK_SECONDS" \
  "USDB_POW_LADDER_SAMPLE_BLOCKS:$SAMPLE_BLOCKS" \
  "USDB_POW_LADDER_CONFIRMATIONS:$CONFIRMATIONS" \
  "USDB_POW_LADDER_DAG_WARMUP_BLOCKS:$DAG_WARMUP_BLOCKS" \
  "USDB_POW_LADDER_MINER_THREADS:$MINER_THREADS"; do
  require_positive_integer "${pair%%:*}" "${pair#*:}"
done
python3 - "$INITIAL_DIFFICULTY" "$MINIMUM_DIFFICULTY" <<'PY'
import sys

try:
    initial = int(sys.argv[1], 0)
    minimum = int(sys.argv[2], 0)
except ValueError as error:
    raise SystemExit(f"invalid PoW ladder difficulty: {error}")
if initial <= 0 or minimum <= 0:
    raise SystemExit("PoW ladder difficulty values must be positive")
if initial < minimum:
    raise SystemExit("PoW ladder initial difficulty must not be below minimum")
PY

mkdir -p "$OUTPUT_ROOT"
current_difficulty="$INITIAL_DIFFICULTY"
round=1
while ((round <= MAX_ROUNDS)); do
  round_dir="${OUTPUT_ROOT}/round-${round}"
  report="${round_dir}/${PROFILE_PREFIX}-round-${round}.json"
  profile="${PROFILE_PREFIX}-round-${round}"
  echo "[usdb-pow-ladder] round=${round}, genesis_difficulty=${current_difficulty}"

  env \
    GETH_BIN="$GETH_BIN" \
    WORK_DIR="${round_dir}/e2e" \
    USDB_POW_CALIBRATION_OUTPUT_ROOT="$round_dir" \
    USDB_POW_CALIBRATION_PROFILE="$profile" \
    USDB_POW_CALIBRATION_REPORT="$report" \
    USDB_POW_CALIBRATION_TARGET_BLOCK_SECONDS="$TARGET_BLOCK_SECONDS" \
    USDB_POW_CALIBRATION_SAMPLE_BLOCKS="$SAMPLE_BLOCKS" \
    USDB_POW_CALIBRATION_CONFIRMATIONS="$CONFIRMATIONS" \
    USDB_POW_CALIBRATION_DAG_WARMUP_BLOCKS="$DAG_WARMUP_BLOCKS" \
    USDB_POW_CALIBRATION_MINER_THREADS="$MINER_THREADS" \
    USDB_POW_CALIBRATION_GENESIS_DIFFICULTY="$current_difficulty" \
    USDB_POW_CALIBRATION_MINIMUM_DIFFICULTY="$MINIMUM_DIFFICULTY" \
    USDB_POW_CALIBRATION_REQUIRE_UNCENSORED=0 \
    USDB_POW_CALIBRATION_REQUIRE_RELEASE_ELIGIBLE=0 \
    USDB_POW_CALIBRATION_REUSE_GETH_BIN=1 \
    USDB_POW_CALIBRATION_ISOLATED_HARDWARE=0 \
    USDB_POW_CALIBRATION_ENVIRONMENT_NOTES="pilot_ladder_not_release_evidence" \
    "$ROOT_DIR/scripts/usdb/run_usdb_pow_calibration.sh"

  read -r limited candidate mean_numerator mean_denominator <<<"$(
    python3 - "$report" <<'PY'
import json
import sys

with open(sys.argv[1], "r", encoding="utf-8") as stream:
    report = json.load(stream)
intervals = report["observed"]["blockIntervalSeconds"]
print(
    int(report["quality"]["timestampResolutionLimited"]),
    report["candidateDifficulty"]["hex"],
    intervals["meanNumerator"],
    intervals["meanDenominator"],
)
PY
  )"
  echo "[usdb-pow-ladder] round=${round}, mean_interval=${mean_numerator}/${mean_denominator}s, candidate=${candidate}, timestamp_limited=${limited}"

  if [[ "$limited" == "0" ]]; then
    cp "$report" "${OUTPUT_ROOT}/accepted-pilot.json"
    echo "[usdb-pow-ladder] accepted uncensored pilot: ${OUTPUT_ROOT}/accepted-pilot.json"
    exit 0
  fi
  current_difficulty="$candidate"
  round=$((round + 1))
done

echo "[usdb-pow-ladder] all ${MAX_ROUNDS} rounds remained timestamp-resolution limited" >&2
exit 1
