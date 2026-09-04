#!/usr/bin/env bash

# Shared Node.js toolchain policy for USDB integration and release scripts.
# This file is sourced by other scripts and intentionally has no entrypoint.

USDB_REQUIRED_NODE_MAJOR=${USDB_REQUIRED_NODE_MAJOR:-24}

usdb_node_toolchain_error() {
  echo "[usdb-node-toolchain] $*" >&2
}

usdb_node_toolchain_log() {
  echo "[usdb-node-toolchain] $*" >&2
}

usdb_current_node_major() {
  node -p 'process.versions.node.split(".")[0]' 2>/dev/null
}

usdb_node_toolchain_is_ready() {
  local actual_major

  command -v node >/dev/null 2>&1 || return 1
  command -v npm >/dev/null 2>&1 || return 1
  actual_major="$(usdb_current_node_major || true)"
  [[ "$actual_major" == "$USDB_REQUIRED_NODE_MAJOR" ]]
}

usdb_load_node_toolchain() {
  local nvm_dir="${NVM_DIR:-$HOME/.nvm}"
  local path_node_version="unavailable"

  if usdb_node_toolchain_is_ready; then
    usdb_node_toolchain_log "using Node $(node --version) from $(command -v node)"
    return 0
  fi
  if command -v node >/dev/null 2>&1; then
    path_node_version="$(node --version 2>/dev/null || echo unreadable)"
  fi

  if [[ -s "$nvm_dir/nvm.sh" ]]; then
    # nvm is installed per-user and cannot be resolved statically by shellcheck.
    # shellcheck source=/dev/null
    source "$nvm_dir/nvm.sh"
    if nvm use "$USDB_REQUIRED_NODE_MAJOR" >/dev/null && usdb_node_toolchain_is_ready; then
      usdb_node_toolchain_log "using Node $(node --version) from nvm"
      return 0
    fi
  fi

  usdb_node_toolchain_error \
    "Node ${USDB_REQUIRED_NODE_MAJOR}.x with npm is required; PATH has ${path_node_version}, and nvm has no usable matching version"
  return 1
}
