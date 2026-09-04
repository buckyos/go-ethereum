#!/usr/bin/env bash
# Toolchain policy cases intentionally isolate exported variables in subshells.
# shellcheck disable=SC2030,SC2031
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
# shellcheck source=lib/node_toolchain.sh
source "$ROOT_DIR/scripts/usdb/lib/node_toolchain.sh"

TEST_DIR=$(mktemp -d "${TMPDIR:-/tmp}/usdb-node-toolchain-test.XXXXXX")
ORIGINAL_PATH=$PATH

cleanup() {
  rm -rf "$TEST_DIR"
}
trap cleanup EXIT

write_fake_node() {
  local target_dir=$1
  local major=$2
  mkdir -p "$target_dir"
  cat >"$target_dir/node" <<FAKE
#!/bin/bash
if [[ "\${1:-}" == "-p" ]]; then
  printf '%s\n' '$major'
else
  printf 'v%s.0.0\n' '$major'
fi
FAKE
  cat >"$target_dir/npm" <<'FAKE'
#!/bin/bash
exit 0
FAKE
  chmod +x "$target_dir/node" "$target_dir/npm"
}

# actions/setup-node exposes the selected Node version on PATH without installing it in nvm.
write_fake_node "$TEST_DIR/setup-node/bin" 24
(
  export HOME="$TEST_DIR/setup-node/home"
  export PATH="$TEST_DIR/setup-node/bin:$ORIGINAL_PATH"
  usdb_load_node_toolchain
  [[ "$(command -v node)" == "$TEST_DIR/setup-node/bin/node" ]]
)

# Local development may start with another Node version and rely on an nvm fallback.
write_fake_node "$TEST_DIR/path-node/bin" 22
write_fake_node "$TEST_DIR/nvm-node/bin" 24
mkdir -p "$TEST_DIR/nvm"
cat >"$TEST_DIR/nvm/nvm.sh" <<'FAKE'
nvm() {
  [[ "${1:-}" == "use" && "${2:-}" == "24" ]] || return 1
  export PATH="$FAKE_NVM_NODE_BIN:$PATH"
}
FAKE
(
  export PATH="$TEST_DIR/path-node/bin:$ORIGINAL_PATH"
  export NVM_DIR="$TEST_DIR/nvm"
  export FAKE_NVM_NODE_BIN="$TEST_DIR/nvm-node/bin"
  usdb_load_node_toolchain
  [[ "$(command -v node)" == "$TEST_DIR/nvm-node/bin/node" ]]
)

# A mismatched PATH version must fail closed when no matching nvm version exists.
if (
  export HOME="$TEST_DIR/missing/home"
  export PATH="$TEST_DIR/path-node/bin:$ORIGINAL_PATH"
  unset NVM_DIR
  usdb_load_node_toolchain >/dev/null 2>&1
); then
  echo "expected Node toolchain mismatch to be rejected" >&2
  exit 1
fi

echo "USDB Node toolchain policy tests passed"
