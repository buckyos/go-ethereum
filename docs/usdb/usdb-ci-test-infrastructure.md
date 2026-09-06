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
容量、reorg 和 soak 测试由 Go coordinator 的 Nightly / Weekly 层承载，不阻塞普通提交。
Go repo 的普通提交不会重复 checkout/编译 pinned Rust dependency；只有 revision lock、两个 frozen
golden 或相关 runner/workflow 改变时运行 cross-repository lane。手工运行和 release tag gate 不使用
该优化，始终执行完整 cross-repository 校验。

本地统一入口为：

```bash
scripts/usdb/run_fast_ci.sh
```

入口支持单独运行 `go`、`rust`、`golden`、`sourcedao`，也支持通过 `all`
一次运行全部 scope。

Go canonical lane 的 Ethash 定向测试保留名称筛选，但默认构建与三种 conformance
构建都以 `go test -json` 输出执行报告，并通过
`scripts/usdb/check_fast_go_coverage.py` 校验
`scripts/usdb/fast_go_required_tests.json` 中的关键用例实际运行且通过。
清单覆盖 UIP-0012 的 50,405 步独立 K oracle、K 窗口边界/损坏状态、UIP-0013
parent price 状态，以及激活边界 expected version 校验。漏选、skip、包未完成或
测试改名后清单未更新都会使 gate 失败；无需依靠增加 world-sim 轮数覆盖 K 窗口。
新增这些关键测试族的用例时必须同步登记清单；新增其他关键用例也应显式登记。
报告保存在 `USDB_FAST_OUTPUT_DIR/consensus-*.jsonl`，并输出到 CI 日志。

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

## Nightly 与 Weekly

中央入口位于 Go 仓库：

| Workflow | 默认触发 | 内容 |
| --- | --- | --- |
| `usdb-nightly.yml` | 每日 `08:37 UTC`，也可手工运行 | profile、activation/bootstrap、balance-history、indexer protocol/reorg/validator |
| `usdb-weekly.yml` | 每周日 `09:23 UTC`，也可手工运行 | 完整 Nightly，加 world soak、独立上游故障矩阵、economic capacity、balance-history extended、public release E2E |
| `usdb-integration.yml` | reusable only | 校验 lock、准备固定 regtest 工具、按 shard 执行和归档 |

Nightly / Weekly 始终以触发 workflow 的 Go SHA 为 coordinator，并按
`scripts/usdb/ci-revisions.json` checkout 精确的 USDB 与 SourceDAO revision。Bitcoin Core 28.1 archive
checksum 和 ord 0.23.3 commit 固定在 `prepare_regtest_tools.sh`，每次 run 只构建一次工具 artifact。

各 shard 由统一入口运行：

```bash
scripts/usdb/run_long_ci.sh nightly --list
scripts/usdb/run_long_ci.sh weekly --list
scripts/usdb/run_long_ci.sh nightly go-profile
```

每个 shard 使用独立工作目录；stdout/stderr、JSON report 和有限大小的诊断文件分别保留 14 天和 30 天。
Weekly 是 Nightly 的超集，因此一次成功 Weekly run 同时证明基础 Nightly 分片和 Weekly 扩展分片通过。

World-soak 的 seeds `41/42/43` 各用独立 job，单个 seed 保持连续 `2500` tick。
runner 在长跑前执行真实 Bitcoin Core/Ord 钱包回归，覆盖断开 `10/11/13` 个块后的
commit/reveal 恢复与继续铭刻。默认 `ORD_POLLING_INTERVAL=200ms`、
`USDB_UPSTREAM_POLL_INTERVAL_MS=200`，只压缩轮询等待，稳定确认深度与校验频率保持不变。
需要做旧配置对照时，可分别覆盖为 `5s` 和 `5000`。

World-soak 编译独立为 `Build world-soak services` 步骤，限时 40 分钟；
随后模拟执行步骤单独限时 300 分钟，为 360 分钟 job 上限内的其他准备和日志上传留出空间。
两个步骤分别调用 `run_long_ci.sh weekly world-soak --prepare-only` 和 `--run-only`，
共用同一 checkout、工具链和 Cargo target；省略参数的本地入口仍依次执行构建和测试。
超时仍判失败。`seed-*-report.jsonl` 保存逐轮及分阶段耗时，成功的
`seed-*-summary.json` 汇总均值、P95 和最后 100 轮均值，完整耗时为 `duration_seconds`。
每轮含动作块和稳定确认块，不能把 2500 tick 当作只有 2500 个 BTC 块。

Weekly 还运行独立的 [多节点上游故障矩阵](usdb-independent-upstream-matrix.md)：
三个 geth 各用一套 BTC/Ord/balance-history/indexer，覆盖中断、稳定分叉、
恢复与空目录完整重放。该 shard 单独编译，模拟内部预算 20 分钟；不改变
world-soak 的轮次与种子。

等待重组收敛时，模拟器只将精确的 `-32041 / SNAPSHOT_NOT_READY` 视为可重试状态；
持续未就绪仍然超时，高度、哈希和 consensus readiness 校验保持严格。
runner 在真实钱包回归之前执行等待流程的错误注入测试。
历史 Pass 查询让最新事件聚合驱动连接，避免 SQLite 先扫描状态/owner 索引后
反复执行聚合；沿用 `MAX(id)`、历史高度边界和分页顺序，不使用当前状态代替历史结果。

独立的较短 seed 不继承上一条链的累计状态；跨 job 延续同一条链则需要完整的
Bitcoin/钱包、Ord、balance-history、indexer 和 simulator 一致 checkpoint，单独的
recovery JSON 不足以实现。目前保留连续长跑，不做跨 job 状态迁移。

需要启动 Rust RPC 服务的 shard 会按照实际 `cargo run` 的 package/bin 选择，分别
完成 `usdb-indexer` 与 `balance-history` 构建，再开始服务 readiness 计时。这里不能
合并构建两个 package，否则 Cargo 联合解析的 dependency feature set 可能无法被后续
单 package 启动复用。失败时按以下顺序定位：

1. 在失败 job 的 `Run nightly shard` 或 `Run weekly shard` step 中搜索
   `[usdb-long-ci] FAIL`；该行给出失败 case、命令退出码和主日志路径。
2. 查看 job 的 Step Summary；其中记录本次上传的 artifact 名称，以及主日志和
   `diagnostics/` 目录位置。
3. 从 run summary 下载对应的 `usdb-nightly-*` 或 `usdb-weekly-*` artifact。
   根目录的 `<case>.log` 是原始命令输出，`diagnostics/` 保存服务日志和测试报告。

测试脚本退出后打印的 Bitcoin Core、ord 或 RPC 服务 shutdown tail 属于失败清理，
不应当被当作根因。应优先查看 `FAIL` 标记之前的第一条失败信息，以及 artifact
中的对应服务日志。

需要给 release tag 生成更高资格证据时，必须在该 tag 上手工运行，不能借用默认分支其他 SHA 的定时结果：

```bash
gh workflow run usdb-nightly.yml \
  --repo buckyos/go-ethereum \
  --ref usdb-testnet-v0-r1
```

Fast、Nightly、Weekly 只表示证据等级，不改变 GitHub Release 的完整性。Fast-qualified testnet release
仍包含 manifest、node kit 和 release-bound installer；资格由 manifest 冻结，已发布 release 不原地升级。

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

1. 在 GitHub-hosted runner 上观察 Nightly / Weekly 的真实耗时、磁盘峰值和 flake，按证据调整 shard timeout
   与并发；容量超出 hosted runner 时再评估隔离的 self-hosted runner。
2. 升级或移除继承的 `memsize` runtime 依赖，删除现代 Go compatibility linker
   workaround。
3. 若迁移到 self-hosted runner，重新定义 runner hardening、secret/attestation 边界和最低版本要求。
