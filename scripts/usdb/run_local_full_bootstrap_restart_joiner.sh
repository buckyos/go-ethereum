#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

export RUN_SMOKE=0
export RUN_FULL_BOOTSTRAP=1
export START_JOINER_AFTER_BOOTSTRAP=1
export RESTART_NODE1_AFTER_BOOTSTRAP=1
export KEEP_RUNNING=0

exec "$SCRIPT_DIR/run_local_two_node_network.sh"
