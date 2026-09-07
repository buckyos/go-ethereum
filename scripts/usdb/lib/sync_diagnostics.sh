#!/usr/bin/env bash

# Diagnostics must neither hide the original failure nor contaminate numeric
# output captured by a caller's command substitution. Ten RPCs normally take at
# most ten seconds; enforce a process deadline even if a server trickles bytes.
usdb_dump_sync_diagnostics() {
  local output="$1"
  local reason="$2"
  shift 2
  if ! timeout --kill-after=1s 12s python3 "$ROOT_DIR/scripts/usdb/collect_sync_diagnostics.py" \
    --output "$output" --reason "$reason" "$@" >&2; then
    echo "[usdb-sync-diagnostics] Collection failed or timed out; preserving the original test failure (report=${output})" >&2
  fi
  return 0
}
