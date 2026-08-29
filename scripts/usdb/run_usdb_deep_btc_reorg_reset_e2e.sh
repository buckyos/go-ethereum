#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
WORKSPACE_ROOT=${USDB_E2E_WORKSPACE_ROOT:-$(dirname "$ROOT_DIR")}
USDB_REPO=${USDB_REPO:-"$WORKSPACE_ROOT/usdb"}
E2E_ROOT=${USDB_DEEP_REORG_WORK_DIR:-$(mktemp -d /tmp/usdb-deep-btc-reorg-reset-e2e-XXXXXX)}
SERVICES_WORK_DIR="$E2E_ROOT/services"
GUARD_ROOT="$E2E_ROOT/chain-generations"
GUARD_SCRIPT="$ROOT_DIR/scripts/usdb/docker/usdb_deep_reorg_guard.py"
REORG_SCRIPT="$USDB_REPO/src/btc/usdb-indexer/scripts/regtest_reorg_smoke.sh"
TARGET_HEIGHT_VALUE="${TARGET_HEIGHT:-40}"
STABLE_LAG_VALUE="${BTC_STABLE_LAG_BLOCKS:-10}"
REPLACEMENT_DEPTH=$((STABLE_LAG_VALUE + 1))

for path in "$GUARD_SCRIPT" "$REORG_SCRIPT"; do
  if [[ ! -f "$path" ]]; then
    echo "Required deep-reorg E2E input is missing: $path" >&2
    exit 1
  fi
done

mkdir -p "$E2E_ROOT"

USDB_DEEP_REORG_GUARD_SCRIPT="$GUARD_SCRIPT" \
USDB_DEEP_REORG_GUARD_ROOT="$GUARD_ROOT" \
WORK_DIR="$SERVICES_WORK_DIR" \
TARGET_HEIGHT="$TARGET_HEIGHT_VALUE" \
BTC_STABLE_LAG_BLOCKS="$STABLE_LAG_VALUE" \
bash "$REORG_SCRIPT"

python3 - "$E2E_ROOT" "$TARGET_HEIGHT_VALUE" "$STABLE_LAG_VALUE" "$REPLACEMENT_DEPTH" <<'PY'
import hashlib
import json
import sys
from pathlib import Path

root = Path(sys.argv[1])
target_height = int(sys.argv[2])
stable_lag = int(sys.argv[3])
replacement_depth = int(sys.argv[4])
generations = root / "chain-generations"
nodes = []
for generation in ("old", "new"):
    for index in range(1, 4):
        state_dir = generations / f"{generation}-node{index}"
        baseline_path = state_dir / "baseline.json"
        halted_path = state_dir / "halted.json"
        baseline = json.loads(baseline_path.read_text(encoding="utf-8"))
        incident = (
            json.loads(halted_path.read_text(encoding="utf-8"))
            if halted_path.exists()
            else None
        )
        nodes.append(
            {
                "generation": generation,
                "node": index,
                "baseline_epoch": baseline["upstream_reorg_epoch"],
                "halted": incident is not None,
                "incident_sha256": (
                    hashlib.sha256(halted_path.read_bytes()).hexdigest()
                    if halted_path.exists()
                    else None
                ),
            }
        )

report = {
    "schema_version": "usdb-deep-btc-reorg-reset-e2e-report:v1",
    "status": "guard-and-data-services-passed",
    "target_height": target_height,
    "btc_stable_lag": stable_lag,
    "replacement_depth": replacement_depth,
    "nodes": nodes,
}
report_path = root / "report.json"
report_path.write_text(json.dumps(report, indent=2, sort_keys=True) + "\n", encoding="utf-8")
print(f"USDB deep BTC reorg reset E2E passed: {report_path}")
PY
