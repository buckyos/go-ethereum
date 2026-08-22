#!/usr/bin/env bash

# Shared Go toolchain policy for USDB development, tests, and release builds.
# This file is sourced by other scripts and intentionally has no entrypoint.

USDB_CANONICAL_GO_VERSION=go1.18.5
USDB_GO_TOOLCHAIN_INITIALIZED=0
USDB_GO_TOOLCHAIN_RESOLVED_MODE=""

usdb_go_toolchain_error() {
  echo "[usdb-go-toolchain] $*" >&2
}

usdb_go_toolchain_log() {
  echo "[usdb-go-toolchain] $*" >&2
}

usdb_resolve_go_binary() {
  if [[ -z "${USDB_GO_BIN:-}" ]]; then
    USDB_GO_BIN="$(command -v go || true)"
  fi
  if [[ -z "$USDB_GO_BIN" ]]; then
    usdb_go_toolchain_error "USDB_GO_BIN is unset and go is not available on PATH"
    return 1
  fi
  if [[ ! -x "$USDB_GO_BIN" ]]; then
    usdb_go_toolchain_error "USDB_GO_BIN is not executable: $USDB_GO_BIN"
    return 1
  fi
}

usdb_go_linker_supports_checklinkname() {
  local help
  help="$("$USDB_GO_BIN" tool link -h 2>&1 || true)"
  [[ "$help" == *"-checklinkname"* ]]
}

usdb_init_go_toolchain() {
  local requested_mode="${USDB_GO_TOOLCHAIN_MODE:-auto}"

  if [[ "${USDB_GO_TOOLCHAIN_INITIALIZED:-0}" == "1" ]]; then
    if [[ "$requested_mode" != "$USDB_GO_TOOLCHAIN_RESOLVED_MODE" ]]; then
      usdb_go_toolchain_error \
        "toolchain already initialized in mode $USDB_GO_TOOLCHAIN_RESOLVED_MODE, requested $requested_mode"
      return 1
    fi
    return 0
  fi

  usdb_resolve_go_binary
  USDB_GO_VERSION="$("$USDB_GO_BIN" env GOVERSION 2>/dev/null || true)"
  if [[ -z "$USDB_GO_VERSION" ]]; then
    usdb_go_toolchain_error "failed to read GOVERSION from $USDB_GO_BIN"
    return 1
  fi

  USDB_GETH_LDFLAGS=""
  case "$requested_mode" in
    release)
      if [[ "$USDB_GO_VERSION" != "$USDB_CANONICAL_GO_VERSION" ]]; then
        usdb_go_toolchain_error \
          "release mode requires $USDB_CANONICAL_GO_VERSION, have $USDB_GO_VERSION from $USDB_GO_BIN"
        return 1
      fi
      ;;
    compatibility)
      if ! usdb_go_linker_supports_checklinkname; then
        usdb_go_toolchain_error \
          "compatibility mode requires linker support for -checklinkname, have $USDB_GO_VERSION"
        return 1
      fi
      USDB_GETH_LDFLAGS="-checklinkname=0"
      ;;
    auto)
      if [[ "$USDB_GO_VERSION" == "$USDB_CANONICAL_GO_VERSION" ]]; then
        :
      elif usdb_go_linker_supports_checklinkname; then
        USDB_GETH_LDFLAGS="-checklinkname=0"
      else
        usdb_go_toolchain_error \
          "unsupported Go toolchain $USDB_GO_VERSION: use $USDB_CANONICAL_GO_VERSION or a linker with -checklinkname"
        return 1
      fi
      ;;
    *)
      usdb_go_toolchain_error \
        "USDB_GO_TOOLCHAIN_MODE must be release, compatibility, or auto; have $requested_mode"
      return 1
      ;;
  esac

  USDB_GOCACHE="${USDB_GOCACHE:-${GOCACHE:-${TMPDIR:-/tmp}/usdb-go-cache-${UID:-0}}}"
  mkdir -p "$USDB_GOCACHE"
  USDB_GO_TOOLCHAIN_RESOLVED_MODE="$requested_mode"
  USDB_GO_TOOLCHAIN_INITIALIZED=1

  usdb_go_toolchain_log \
    "mode=$requested_mode go=$USDB_GO_VERSION binary=$USDB_GO_BIN gocache=$USDB_GOCACHE ldflags=${USDB_GETH_LDFLAGS:-<none>}"
}

usdb_go() {
  usdb_init_go_toolchain
  env GOCACHE="$USDB_GOCACHE" "$USDB_GO_BIN" "$@"
}

usdb_go_with_geth_linker_compat() {
  local subcommand="$1"
  shift
  local -a args=("$subcommand")

  usdb_init_go_toolchain
  if [[ -n "$USDB_GETH_LDFLAGS" ]]; then
    args+=("-ldflags=$USDB_GETH_LDFLAGS")
  fi
  args+=("$@")
  usdb_go "${args[@]}"
}

usdb_build_geth() {
  local root_dir="$1"
  local output="$2"
  local build_tags="${3:-}"
  local -a args=(build -trimpath -buildvcs=false -o "$output")

  usdb_init_go_toolchain
  if [[ -n "$USDB_GETH_LDFLAGS" ]]; then
    args+=("-ldflags=$USDB_GETH_LDFLAGS")
  fi
  if [[ -n "$build_tags" ]]; then
    args+=(-tags "$build_tags")
  fi
  args+=(./cmd/geth)

  mkdir -p "$(dirname "$output")"
  usdb_go_toolchain_log \
    "building geth output=$output tags=${build_tags:-<none>} go=$USDB_GO_VERSION"
  (
    cd "$root_dir" || exit 1
    usdb_go "${args[@]}"
  )
}

usdb_prepare_geth_binary() {
  local target_variable="$1"
  local root_dir="$2"
  local default_output="$3"
  local build_tags="${4:-}"
  local resolved="${!target_variable:-}"

  if [[ -n "$resolved" ]]; then
    if [[ ! -x "$resolved" ]]; then
      usdb_go_toolchain_error "$target_variable is not executable: $resolved"
      return 1
    fi
    usdb_go_toolchain_log "using prebuilt geth binary $resolved"
  else
    resolved="$default_output"
    usdb_build_geth "$root_dir" "$resolved" "$build_tags"
  fi
  printf -v "$target_variable" '%s' "$resolved"
}

usdb_geth_build_description() {
  local output="$1"
  local build_tags="${2:-}"

  usdb_init_go_toolchain
  printf 'go=%s; mode=%s; ldflags=%s; tags=%s; output=%s' \
    "$USDB_GO_VERSION" \
    "$USDB_GO_TOOLCHAIN_RESOLVED_MODE" \
    "${USDB_GETH_LDFLAGS:-<none>}" \
    "${build_tags:-<none>}" \
    "$output"
}
