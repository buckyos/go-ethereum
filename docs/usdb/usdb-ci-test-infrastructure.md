# USDB CI 测试基础设施

## 当前状态

三个关联仓库已经实现最小正式 GitHub Actions Fast CI：

- `go-ethereum/.github/workflows/usdb-fast.yml`：每次运行 Go canonical、
  compatibility 两条 lane，并按变更范围协调跨仓库 golden artifact 校验；
- `usdb/.github/workflows/usdb-fast.yml`：运行 Rust workspace、ShellCheck 和
  world simulator；
- `SourceDAO/.github/workflows/usdb-fast.yml`：运行合约测试、USDB 定向构建和
  bytecode audit。

每次提交的 blocking gate 只包含确定性、无外部服务依赖的 Fast CI。跨进程 E2E、
容量、reorg 和 soak 测试不进入当前 fast workflow，后续由独立 nightly 层承载。
Go repo 的普通提交不会重复 checkout/编译 pinned Rust dependency；只有 revision lock、两个 frozen
golden 或相关 runner/workflow 改变时运行 cross-repository lane。手工运行和 release tag gate 不使用
该优化，始终执行完整 cross-repository 校验。

本地统一入口为：

```bash
scripts/usdb/run_fast_ci.sh
```

入口支持单独运行 `go`、`rust`、`golden`、`sourcedao`，也支持通过 `all`
一次运行全部 scope。

## Go 工具链策略

USDB geth 的正式构建使用项目 canonical Go 1.18.5。由于继承的
`github.com/fjl/memsize` 仍引用 Go runtime 私有符号，现代 Go 当前只作为
compatibility lane。

| 模式 | 接受的工具链 | Linker 策略 | 用途 |
| --- | --- | --- | --- |
| `release` | 必须为 Go 1.18.5 | 不增加兼容参数 | canonical 和正式构建 |
| `compatibility` | linker 支持 `-checklinkname` | 使用 `-checklinkname=0` | 现代 Go 构建和测试 smoke |
| `auto` | canonical Go，或具备兼容 linker 的现代 Go | 按 capability 派生 | 本地 E2E fallback |

`scripts/usdb/lib/go_toolchain.sh` 是该策略的唯一实现入口。E2E 脚本不再硬编码
开发机 Go 路径，也不再无条件传入 linker 参数。脚本优先复用显式 `GETH_BIN`；
未提供时只构建一次 geth 并在后续步骤复用。

`USDB_GOCACHE` 可以显式隔离构建缓存；未设置时，helper 先遵循标准 `GOCACHE`，
再选择可写的临时目录。

compatibility linker 参数不是 release 策略。长期方案仍是升级或移除旧
`memsize` runtime 依赖，随后删除 compatibility workaround。

## 本地 Fast CI

使用显式工具链运行完整本地 gate：

```bash
USDB_CANONICAL_GO_BIN=/path/to/go1.18.5/bin/go \
USDB_COMPAT_GO_BIN=/path/to/go1.26/bin/go \
USDB_NODE_BIN_DIR=/path/to/node24/bin \
USDB_FAST_REQUIRE_COMPAT_GO=1 \
scripts/usdb/run_fast_ci.sh
```

使用逗号分隔的 scope 运行部分检查：

```bash
USDB_FAST_SCOPE=go scripts/usdb/run_fast_ci.sh
USDB_FAST_SCOPE=rust,golden scripts/usdb/run_fast_ci.sh
USDB_FAST_SCOPE=sourcedao scripts/usdb/run_fast_ci.sh
```

默认要求 `../usdb` 和 `../SourceDAO` 为 sibling checkout。必要时可通过
`USDB_REPO_DIR` 和 `SOURCE_DAO_REPO_DIR` 覆盖路径。

### Go 检查范围

- 共享 Go 工具链策略单元测试；
- 维护范围内的 gofmt 检查；
- USDB package vet 和聚焦测试；
- `cmd/utils` CLI 参数绑定与 `eth/ethconfig` 共识引擎配置测试；
- activation、economic conformance build-tag 测试；
- Go 1.18.5 canonical geth 构建；
- 可选的现代 Go compatibility 测试和 geth 构建；
- ShellCheck 及 Python verifier/configuration、bootstrap fixture、deep-reorg
  guard/runtime 测试。

gofmt gate 排除 `crypto/secp256k1/libsecp256k1/**`。其中被跟踪的
`dummy.go` 是上游 vendoring workaround，旧 gofmt 会对其产生无意义机械改写。

### Rust 和 golden 检查范围

- workspace `cargo fmt`；
- workspace Clippy，并以 `-D warnings` 拒绝 warning；
- workspace tests；
- 全部 indexer 和 balance-history shell scripts 的 ShellCheck；
- world simulator Python tests；
- balance-history oracle/Core UTXO audit、snapshot distribution/release wrapper
  的纯 Python 测试；
- BTC activation 和 cross-chain release manifest 的 Rust-to-Go golden check。

依赖外部 bitcoind、ord 或 electrs 的测试必须明确标为 ignored/manual live test，
不能进入 GitHub-hosted Fast CI 的默认测试集合。

### SourceDAO 检查范围

- 完整 Hardhat tests；
- USDB 定向 Solidity build；
- USDB bytecode audit。

Fast CI 精确使用 Node 24.12.0 和 npm 11.6.2。SourceDAO 通过 `.nvmrc`、
`package.json`、`.npmrc` 和 runtime verifier 冻结版本。

`USDB_NODE_BIN_DIR` 可以显式选择工具链，不依赖交互式 `nvm` shell。默认复用现有
`node_modules`；在一次性 CI checkout 中可设置
`USDB_FAST_SOURCE_DAO_INSTALL=ci` 执行 clean `npm ci`。

## 工具链和 Action 锁定

当前 Fast CI 冻结以下输入：

| 输入 | 版本 |
| --- | --- |
| GitHub-hosted runner | `ubuntu-24.04` |
| canonical Go | `1.18.5` |
| compatibility Go | `1.26.0` |
| Python | `3.13.7` |
| Rust | `1.91.0` |
| Node | `24.12.0` |
| npm | `11.6.2` |

Rust 同时由 `usdb/src/btc/rust-toolchain.toml` 固定；SourceDAO 自行维护 Node/npm
固定文件。第三方 GitHub Actions 使用完整不可变 commit SHA，旁边保留经 review
的 release tag 注释。

`scripts/usdb/ci-revisions.json` 记录两个外部 dependency 最近一次联合验证通过的 revision 和共享
工具链版本；当前 Go revision 由 workflow checkout 或 release tag 确定。它不是“每次提交自动追随
HEAD”的文件，也不是 release manifest。
详细语义、更新条件和提交流程见
[USDB CI 跨仓库 Revision Lock 规范](./usdb-ci-revision-lock.md)。

## 已验证基线

2026-08-21 分别运行了全部 component scope：

- Go 1.18.5 canonical 与 Go 1.26.0 compatibility 测试和 geth build 通过；
- Rust workspace、simulator 和 frozen golden checks 通过；
- SourceDAO 在 Node 24 下通过 266 tests、29 个 Solidity 文件构建和 42 个
  USDB artifact bytecode audit。

2026-08-22，三份 workflow 均通过 actionlint，并使用 Rust 1.91.0、
Node 24.12.0/npm 11.6.2 重新运行 repo-local gate，其中 SourceDAO 包含 clean
`npm ci`。Go 双工具链和跨仓库 golden lane 同样通过。

workflow 和 revision lock 已提交并推送到三个仓库，当前由 GitHub Actions 执行
repo-local Fast CI；具体运行结果以各仓库 Actions 页面为准。

## 后续 CI 工作

1. 增加 central nightly workflow，按锁定的三仓基线运行 deterministic regtest、
   activation/reorg、bootstrap/public-release、capacity 和 soak shards。
2. 将 build/service logs 和 JSON reports 作为 nightly artifacts 保留。
3. 升级或移除继承的 `memsize` runtime 依赖，删除现代 Go compatibility linker
   workaround。
4. 若迁移到 self-hosted runner，重新确认 Actions 对 runner 的最低版本要求。
