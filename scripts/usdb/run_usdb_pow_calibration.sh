#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
# shellcheck source=lib/go_toolchain.sh
source "$ROOT_DIR/scripts/usdb/lib/go_toolchain.sh"
OUTPUT_ROOT=${USDB_POW_CALIBRATION_OUTPUT_ROOT:-/tmp/usdb-pow-calibration}
PROFILE=${USDB_POW_CALIBRATION_PROFILE:-local-cpu-single-thread}
TARGET_BLOCK_SECONDS=${USDB_POW_CALIBRATION_TARGET_BLOCK_SECONDS:-13}
SAMPLE_BLOCKS=${USDB_POW_CALIBRATION_SAMPLE_BLOCKS:-256}
CONFIRMATIONS=${USDB_POW_CALIBRATION_CONFIRMATIONS:-12}
DAG_WARMUP_BLOCKS=${USDB_POW_CALIBRATION_DAG_WARMUP_BLOCKS:-64}
MINER_THREADS=${USDB_POW_CALIBRATION_MINER_THREADS:-1}
GENESIS_DIFFICULTY=${USDB_POW_CALIBRATION_GENESIS_DIFFICULTY:-0x2000}
MINIMUM_DIFFICULTY=${USDB_POW_CALIBRATION_MINIMUM_DIFFICULTY:-$GENESIS_DIFFICULTY}
REQUIRE_UNCENSORED=${USDB_POW_CALIBRATION_REQUIRE_UNCENSORED:-1}
REQUIRE_RELEASE_ELIGIBLE=${USDB_POW_CALIBRATION_REQUIRE_RELEASE_ELIGIBLE:-0}
USDB_GO_TOOLCHAIN_MODE=${USDB_GO_TOOLCHAIN_MODE:-auto}
GETH_BIN=${GETH_BIN:-"$OUTPUT_ROOT/geth"}
REUSE_GETH_BIN=${USDB_POW_CALIBRATION_REUSE_GETH_BIN:-0}
ISOLATED_HARDWARE=${USDB_POW_CALIBRATION_ISOLATED_HARDWARE:-0}
ENVIRONMENT_NOTES=${USDB_POW_CALIBRATION_ENVIRONMENT_NOTES:-operator_did_not_declare_isolation}
WORK_DIR=${WORK_DIR:-"$OUTPUT_ROOT/e2e"}
REPORT_FILE=${USDB_POW_CALIBRATION_REPORT:-"$OUTPUT_ROOT/${PROFILE}.json"}
USDB_GOCACHE=${USDB_GOCACHE:-/tmp/usdb-pow-calibration-go-cache}

require_positive_integer() {
  local name="$1"
  local value="$2"
  if [[ ! "$value" =~ ^[1-9][0-9]*$ ]]; then
    echo "${name} must be a positive integer, have: ${value}" >&2
    exit 2
  fi
}

require_positive_integer USDB_POW_CALIBRATION_TARGET_BLOCK_SECONDS "$TARGET_BLOCK_SECONDS"
require_positive_integer USDB_POW_CALIBRATION_SAMPLE_BLOCKS "$SAMPLE_BLOCKS"
require_positive_integer USDB_POW_CALIBRATION_CONFIRMATIONS "$CONFIRMATIONS"
require_positive_integer USDB_POW_CALIBRATION_DAG_WARMUP_BLOCKS "$DAG_WARMUP_BLOCKS"
require_positive_integer USDB_POW_CALIBRATION_MINER_THREADS "$MINER_THREADS"
if ! [[ "$REQUIRE_UNCENSORED" =~ ^[01]$ ]]; then
  echo "USDB_POW_CALIBRATION_REQUIRE_UNCENSORED must be 0 or 1" >&2
  exit 2
fi
if ! [[ "$REQUIRE_RELEASE_ELIGIBLE" =~ ^[01]$ ]]; then
  echo "USDB_POW_CALIBRATION_REQUIRE_RELEASE_ELIGIBLE must be 0 or 1" >&2
  exit 2
fi
if ! [[ "$REUSE_GETH_BIN" =~ ^[01]$ ]]; then
  echo "USDB_POW_CALIBRATION_REUSE_GETH_BIN must be 0 or 1" >&2
  exit 2
fi
if ! [[ "$ISOLATED_HARDWARE" =~ ^[01]$ ]]; then
  echo "USDB_POW_CALIBRATION_ISOLATED_HARDWARE must be 0 or 1" >&2
  exit 2
fi
if [[ -z "$ENVIRONMENT_NOTES" ]]; then
  echo "USDB_POW_CALIBRATION_ENVIRONMENT_NOTES must not be empty" >&2
  exit 2
fi
if [[ -z "$PROFILE" ]]; then
  echo "USDB_POW_CALIBRATION_PROFILE must not be empty" >&2
  exit 2
fi
python3 - "$GENESIS_DIFFICULTY" "$MINIMUM_DIFFICULTY" <<'PY'
import sys

try:
    genesis = int(sys.argv[1], 0)
    minimum = int(sys.argv[2], 0)
except ValueError as error:
    raise SystemExit(f"invalid PoW calibration difficulty: {error}")
if genesis <= 0 or minimum <= 0:
    raise SystemExit("PoW calibration difficulty values must be positive")
if genesis < minimum:
    raise SystemExit("PoW calibration genesis difficulty must not be below minimum")
PY

mkdir -p "$OUTPUT_ROOT" "$USDB_GOCACHE"
if [[ "$REUSE_GETH_BIN" == "1" && -x "$GETH_BIN" ]]; then
  echo "[usdb-pow-calibration] reusing geth binary ${GETH_BIN}"
  reused_geth_binary=true
else
  echo "[usdb-pow-calibration] building geth from current source"
  usdb_build_geth "$ROOT_DIR" "$GETH_BIN"
  reused_geth_binary=false
fi

source_commit=$(git -C "$ROOT_DIR" rev-parse HEAD)
if [[ -n "$(git -C "$ROOT_DIR" status --porcelain)" ]]; then
  source_dirty=true
else
  source_dirty=false
fi
binary_sha256=$(sha256sum "$GETH_BIN" | awk '{print $1}')
if [[ "$reused_geth_binary" == "true" ]]; then
  build_command="reused binary=${GETH_BIN}; sha256=${binary_sha256}"
else
  build_command="$(usdb_geth_build_description "$GETH_BIN"); sha256=${binary_sha256}"
fi
cpu_model=$(lscpu | awk -F: '/Model name/ {gsub(/^[ \t]+/, "", $2); print $2; exit}')
virtualization=$(lscpu | awk -F: '/Hypervisor vendor/ {gsub(/^[ \t]+/, "", $2); print $2; exit}')
miner_hardware="${cpu_model}; logical_cpus=$(nproc); hypervisor=${virtualization:-none}; kernel=$(uname -sr)"
if [[ "$ISOLATED_HARDWARE" == "1" ]]; then
  isolated_hardware=true
else
  isolated_hardware=false
fi
target_blocks=$((DAG_WARMUP_BLOCKS + SAMPLE_BLOCKS + CONFIRMATIONS))

echo "[usdb-pow-calibration] profile=${PROFILE}, blocks=${target_blocks}, threads=${MINER_THREADS}, genesis_difficulty=${GENESIS_DIFFICULTY}, minimum_difficulty=${MINIMUM_DIFFICULTY}"
env \
  WORK_DIR="$WORK_DIR" \
  GETH_BIN="$GETH_BIN" \
  TARGET_BLOCKS="$target_blocks" \
  BLOCK_WAIT_SECONDS="${BLOCK_WAIT_SECONDS:-7200}" \
  USDB_CHAIN_MINER_THREADS="$MINER_THREADS" \
  POW_CALIBRATION_PROFILE="$PROFILE" \
  POW_CALIBRATION_TARGET_BLOCK_SECONDS="$TARGET_BLOCK_SECONDS" \
  POW_CALIBRATION_SAMPLE_BLOCKS="$SAMPLE_BLOCKS" \
  POW_CALIBRATION_CONFIRMATIONS="$CONFIRMATIONS" \
  POW_CALIBRATION_DAG_WARMUP_BLOCKS="$DAG_WARMUP_BLOCKS" \
  POW_CALIBRATION_OUTPUT="$REPORT_FILE" \
  POW_CALIBRATION_SOURCE_COMMIT="$source_commit" \
  POW_CALIBRATION_SOURCE_DIRTY="$source_dirty" \
  POW_CALIBRATION_BUILD_COMMAND="$build_command" \
  POW_CALIBRATION_MINER_HARDWARE="$miner_hardware" \
  POW_CALIBRATION_GENESIS_DIFFICULTY="$GENESIS_DIFFICULTY" \
  POW_CALIBRATION_MINIMUM_DIFFICULTY="$MINIMUM_DIFFICULTY" \
  POW_CALIBRATION_ISOLATED_HARDWARE="$isolated_hardware" \
  POW_CALIBRATION_ENVIRONMENT_NOTES="$ENVIRONMENT_NOTES" \
  "$ROOT_DIR/scripts/usdb/run_usdb_profile_e2e.sh"

if [[ "$REQUIRE_UNCENSORED" == "1" ]]; then
  python3 - "$REPORT_FILE" <<'PY'
import json
import sys

with open(sys.argv[1], "r", encoding="utf-8") as stream:
    report = json.load(stream)
if report["quality"]["timestampResolutionLimited"]:
    candidate = report["candidateDifficulty"]["hex"]
    raise SystemExit(
        "PoW sample is limited by one-second block timestamps; "
        f"rerun with USDB_POW_CALIBRATION_GENESIS_DIFFICULTY={candidate} "
        f"and an explicit minimum difficulty (report: {sys.argv[1]})"
    )
PY
fi
if [[ "$REQUIRE_RELEASE_ELIGIBLE" == "1" ]]; then
  python3 - "$REPORT_FILE" <<'PY'
import json
import sys

with open(sys.argv[1], "r", encoding="utf-8") as stream:
    report = json.load(stream)
if not report["quality"]["releaseEligible"]:
    blockers = ", ".join(report["quality"]["releaseBlockers"])
    raise SystemExit(
        f"PoW sample is not release eligible: {blockers} (report: {sys.argv[1]})"
    )
PY
fi

echo "[usdb-pow-calibration] report: ${REPORT_FILE}"
