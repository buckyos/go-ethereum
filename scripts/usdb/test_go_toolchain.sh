#!/usr/bin/env bash
# Toolchain policy cases intentionally isolate exported variables in subshells.
# shellcheck disable=SC2030,SC2031
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
# shellcheck source=lib/go_toolchain.sh
source "$ROOT_DIR/scripts/usdb/lib/go_toolchain.sh"

TEST_DIR=$(mktemp -d "${TMPDIR:-/tmp}/usdb-go-toolchain-test.XXXXXX")
FAKE_GO="$TEST_DIR/go"

cleanup() {
  rm -rf "$TEST_DIR"
}
trap cleanup EXIT

cat >"$FAKE_GO" <<'FAKE'
#!/usr/bin/env bash
set -euo pipefail

case "${1:-}" in
  env)
    if [[ "${2:-}" == "GOVERSION" ]]; then
      printf '%s\n' "$FAKE_GO_VERSION"
      exit 0
    fi
    ;;
  tool)
    if [[ "${2:-}" == "link" && "${3:-}" == "-h" ]]; then
      if [[ "$FAKE_GO_SUPPORTS_CHECKLINKNAME" == "1" ]]; then
        echo "  -checklinkname"
      else
        echo "usage: link [options]"
      fi
      exit 2
    fi
    ;;
  build)
    printf '%q ' "$@" >>"$FAKE_GO_LOG"
    printf '\n' >>"$FAKE_GO_LOG"
    output=""
    while (( $# > 0 )); do
      if [[ "$1" == "-o" ]]; then
        output="$2"
        break
      fi
      shift
    done
    if [[ -n "$output" ]]; then
      mkdir -p "$(dirname "$output")"
      printf '#!/usr/bin/env bash\nexit 0\n' >"$output"
      chmod +x "$output"
    fi
    exit 0
    ;;
esac

echo "unexpected fake go invocation: $*" >&2
exit 1
FAKE
chmod +x "$FAKE_GO"

run_policy_case() {
  local version="$1"
  local supports_checklinkname="$2"
  local mode="$3"
  local expected_ldflags="$4"

  (
    export FAKE_GO_VERSION="$version"
    export FAKE_GO_SUPPORTS_CHECKLINKNAME="$supports_checklinkname"
    export FAKE_GO_LOG="$TEST_DIR/fake-go.log"
    export USDB_GO_BIN="$FAKE_GO"
    export USDB_GO_TOOLCHAIN_MODE="$mode"
    export USDB_GOCACHE="$TEST_DIR/cache-$version-$mode"
    unset USDB_GO_TOOLCHAIN_INITIALIZED
    usdb_init_go_toolchain
    [[ "$USDB_GETH_LDFLAGS" == "$expected_ldflags" ]]
  )
}

assert_policy_rejected() {
  local version="$1"
  local supports_checklinkname="$2"
  local mode="$3"

  if (
    export FAKE_GO_VERSION="$version"
    export FAKE_GO_SUPPORTS_CHECKLINKNAME="$supports_checklinkname"
    export FAKE_GO_LOG="$TEST_DIR/fake-go.log"
    export USDB_GO_BIN="$FAKE_GO"
    export USDB_GO_TOOLCHAIN_MODE="$mode"
    export USDB_GOCACHE="$TEST_DIR/cache-rejected-$version-$mode"
    unset USDB_GO_TOOLCHAIN_INITIALIZED
    usdb_init_go_toolchain >/dev/null 2>&1
  ); then
    echo "expected policy rejection: version=$version mode=$mode" >&2
    exit 1
  fi
}

run_policy_case go1.18.5 0 release ""
run_policy_case go1.18.5 0 auto ""
run_policy_case go1.26.0 1 compatibility "-checklinkname=0"
run_policy_case go1.26.0 1 auto "-checklinkname=0"
assert_policy_rejected go1.26.0 1 release
assert_policy_rejected go1.20.0 0 auto
assert_policy_rejected go1.18.5 0 compatibility

(
  export FAKE_GO_VERSION=go1.18.5
  export FAKE_GO_SUPPORTS_CHECKLINKNAME=0
  export FAKE_GO_LOG="$TEST_DIR/release-build.log"
  export USDB_GO_BIN="$FAKE_GO"
  export USDB_GO_TOOLCHAIN_MODE=release
  export USDB_GOCACHE="$TEST_DIR/release-build-cache"
  unset USDB_GO_TOOLCHAIN_INITIALIZED
  usdb_build_geth "$ROOT_DIR" "$TEST_DIR/geth-release"
  grep -q -- '-trimpath' "$FAKE_GO_LOG"
  grep -q -- '-buildvcs=false' "$FAKE_GO_LOG"
  if grep -q -- '-ldflags=' "$FAKE_GO_LOG"; then
    echo "canonical release build unexpectedly used linker compatibility flags" >&2
    exit 1
  fi
  [[ -x "$TEST_DIR/geth-release" ]]
)

(
  export FAKE_GO_VERSION=go1.26.0
  export FAKE_GO_SUPPORTS_CHECKLINKNAME=1
  export FAKE_GO_LOG="$TEST_DIR/build.log"
  export USDB_GO_BIN="$FAKE_GO"
  export USDB_GO_TOOLCHAIN_MODE=compatibility
  export USDB_GOCACHE="$TEST_DIR/build-cache"
  unset USDB_GO_TOOLCHAIN_INITIALIZED
  usdb_build_geth "$ROOT_DIR" "$TEST_DIR/geth" "usdb_test_tag"
  grep -q -- '-ldflags=-checklinkname=0' "$FAKE_GO_LOG"
  grep -q -- '-tags usdb_test_tag' "$FAKE_GO_LOG"
  [[ -x "$TEST_DIR/geth" ]]
)

echo "USDB Go toolchain policy tests passed"
