# USDB CI Test Infrastructure

## Status

The three repositories now provide a minimum blocking GitHub Actions fast gate:

- `go-ethereum/.github/workflows/usdb-fast.yml` runs the canonical and
  compatibility Go lanes and coordinates cross-repository golden checks;
- `usdb/.github/workflows/usdb-fast.yml` runs the Rust workspace, ShellCheck,
  and world-simulator gate;
- `SourceDAO/.github/workflows/usdb-fast.yml` runs contract tests, the USDB
  build, and bytecode audit.

Cross-process E2E, capacity, and soak workloads remain outside the per-change
blocking gate and are planned as a separate nightly layer.

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

Node 24.12.0 and npm 11.6.2 are exact fast-gate inputs. SourceDAO freezes them
through `.nvmrc`, `package.json`, `.npmrc`, and a runtime verifier.
`USDB_NODE_BIN_DIR` can select the exact toolchain without depending on an
interactive `nvm` shell. Existing `node_modules` are reused by default; set
`USDB_FAST_SOURCE_DAO_INSTALL=ci` for a clean `npm ci` installation in a
disposable CI checkout.

## Toolchain And Action Locks

The minimum fast gate freezes these inputs:

| Input | Version |
| --- | --- |
| GitHub-hosted runner | `ubuntu-24.04` |
| canonical Go | `1.18.5` |
| compatibility Go | `1.26.0` |
| Python | `3.13.7` |
| Rust | `1.91.0` |
| Node | `24.12.0` |
| npm | `11.6.2` |

Rust is also pinned by `usdb/src/btc/rust-toolchain.toml`; SourceDAO owns its
Node/npm pins. Third-party Actions are referenced by immutable full commit SHA,
with the reviewed release tag retained in a comment.

`scripts/usdb/ci-revisions.json` is the cross-repository revision lock. It
contains exact 40-character commit IDs for `go-ethereum`, `usdb`, and
`SourceDAO`, plus the shared fast-gate toolchains. The schema validator rejects
duplicate or unknown JSON keys, floating refs, unexpected repositories, and
unversioned toolchains.

The lock is a last-validated baseline, not a self-referential claim that the
current coordinator commit already exists in its own file. In a go-ethereum
workflow, the current go-ethereum checkout may advance from its recorded
baseline; every sibling checkout must exactly match the lock. After coordinated
changes are committed and validated, update sibling revisions and record the
last validated go-ethereum commit in a follow-up lock update.

## Validation Baseline

The component scopes were run independently on 2026-08-21. The Go lane passed
with canonical Go 1.18.5 and compatibility Go 1.26.0, including both geth
builds. The Rust workspace, simulator, and frozen golden checks passed. The
SourceDAO lane passed 266 tests, compiled 29 Solidity files, and audited 42 USDB
artifact files with Node 24.

On 2026-08-22, all three workflow definitions passed actionlint. The exact
repo-local gates were then rerun with Rust 1.91.0 and Node 24.12.0/npm 11.6.2,
including a clean `npm ci`; the Go dual-toolchain and cross-repository golden
lanes also passed. Actual GitHub-hosted execution remains pending until this
uncommitted workflow batch is reviewed, committed, and pushed.

## Remaining CI Work

1. Add a central nightly workflow that checks out the locked three-repository
   baseline and runs deterministic regtest, activation/reorg,
   bootstrap/public-release, capacity, and soak shards.
2. Retain build and service logs plus JSON reports as nightly artifacts.
3. Remove the modern-Go compatibility linker workaround after the inherited
   `memsize` runtime dependency is upgraded or removed.
4. Recheck the minimum Actions runner version before moving these workflows to
   self-hosted infrastructure.
