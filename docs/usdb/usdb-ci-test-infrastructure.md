# USDB CI Test Infrastructure

## Status

The repository currently provides local fast-check infrastructure. GitHub Actions
workflows and the cross-process nightly matrix are intentionally deferred to the
next batch so that CI will invoke commands already proven locally.

The entrypoint is:

```bash
scripts/usdb/run_fast_ci.sh
```

It can run `go`, `rust`, `golden`, and `sourcedao` components independently or
as one `all` scope.

## Go Toolchain Policy

USDB geth release output is built with the repository's canonical Go 1.18.5
toolchain. Modern Go is a compatibility lane only while the inherited
`github.com/fjl/memsize` dependency still references private runtime symbols.

| Mode | Accepted toolchain | Linker policy | Purpose |
| --- | --- | --- | --- |
| `release` | exactly Go 1.18.5 | no compatibility flag | canonical and release builds |
| `compatibility` | linker exposes `-checklinkname` | `-checklinkname=0` | modern-Go build/test smoke |
| `auto` | canonical Go, otherwise compatible linker | derived from capability | local E2E fallback |

`scripts/usdb/lib/go_toolchain.sh` owns this policy. E2E scripts no longer
hard-code a developer-machine Go path or unconditionally pass a linker flag.
They prefer an explicit `GETH_BIN`; otherwise they build geth once and reuse the
result. `USDB_GOCACHE` can isolate a lane explicitly; otherwise the helper honors
the standard `GOCACHE` before choosing a writable temporary default.

The compatibility flag is not a release policy. It exists to test the current
fork on modern Go. The long-term cleanup is to remove or update the old
`memsize` runtime dependency and then delete this compatibility path.

## Local Fast Checks

Run the complete local gate with explicit toolchain locations:

```bash
USDB_CANONICAL_GO_BIN=/path/to/go1.18.5/bin/go \
USDB_COMPAT_GO_BIN=/path/to/go1.26/bin/go \
USDB_NODE_BIN_DIR=/path/to/node24/bin \
USDB_FAST_REQUIRE_COMPAT_GO=1 \
scripts/usdb/run_fast_ci.sh
```

Run selected components with a comma-separated scope:

```bash
USDB_FAST_SCOPE=go scripts/usdb/run_fast_ci.sh
USDB_FAST_SCOPE=rust,golden scripts/usdb/run_fast_ci.sh
USDB_FAST_SCOPE=sourcedao scripts/usdb/run_fast_ci.sh
```

The runner expects sibling `../usdb` and `../SourceDAO` checkouts by default.
Override them with `USDB_REPO_DIR` and `SOURCE_DAO_REPO_DIR` when needed.

### Go

- shared toolchain policy unit tests
- maintained-source gofmt check
- USDB package vet and focused tests
- activation and economic conformance build-tag tests
- canonical Go 1.18.5 geth build
- optional modern-Go compatibility tests and geth build
- ShellCheck and Python verifier/configuration tests

The gofmt gate excludes `crypto/secp256k1/libsecp256k1/**`, whose tracked
`dummy.go` files are upstream vendoring workarounds that old gofmt rewrites
mechanically.

### Rust and Golden Artifacts

- workspace `cargo fmt`, Clippy with warnings denied, and tests
- all indexer shell scripts through ShellCheck with shared-source resolution
- world-simulator Python tests
- BTC activation and cross-chain release-manifest Rust-to-Go golden checks

### SourceDAO

- full Hardhat tests
- USDB-targeted Solidity build
- USDB bytecode audit

Node 22.13 or newer is required. `USDB_NODE_BIN_DIR` can select an explicit
toolchain without depending on an interactive `nvm` shell. Existing
`node_modules` are reused by default; set `USDB_FAST_SOURCE_DAO_INSTALL=ci` for
a clean `npm ci` installation in a disposable CI checkout.

## Validation Baseline

The component scopes were run independently on 2026-08-21. The Go lane passed
with canonical Go 1.18.5 and compatibility Go 1.26.0, including both geth
builds. The Rust workspace, simulator, and frozen golden checks passed. The
SourceDAO lane passed 266 tests, compiled 29 Solidity files, and audited 42 USDB
artifact files with Node 24.

## Next Batch

The formal CI layer should remain thin:

1. repository-local fast workflows invoke the relevant runner scopes;
2. a central nightly workflow checks out all three repositories at pinned full
   commit IDs;
3. nightly jobs add deterministic regtest, activation/reorg, bootstrap/public
   release, capacity, and soak shards;
4. build and service logs plus JSON reports are retained as job artifacts.
