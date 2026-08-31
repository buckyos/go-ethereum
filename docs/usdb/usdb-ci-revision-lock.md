# USDB CI 跨仓库 Revision Lock 规范

## 目的

USDB 的实现横跨以下三个独立仓库：

- `go-ethereum`：USDB chain 共识、构块、验证和 chain config；
- `usdb`：BTC-side indexer、economic state view、activation registry 和 Rust
  golden artifact 生成器；
- `SourceDAO`：冷启动和 dividend 相关合约及其 USDB bytecode artifact。

三个仓库可以独立开发，但跨仓库接口、golden artifact 和工具链必须基于一个明确
组合进行验证。`scripts/usdb/ci-revisions.json` 记录这个“最近一次联合验证通过的
组合”，以下简称 revision lock。

revision lock 的核心目标是：

1. 避免跨仓库 CI 隐式使用 sibling 仓库不断变化的默认分支 HEAD；
2. 让历史 CI 失败能够定位到确定的三仓 commit 组合；
3. 为 integration、nightly 和 release candidate 提供可重放的代码基线；
4. 让共享工具链升级成为显式、可 review 的变更。

## 非目标和边界

revision lock：

- 不是每个仓库的依赖管理文件；
- 不要求每次普通 commit 都更新；
- 不是 USDB 共识输入；
- 不参与 runtime activation、block validation 或节点网络握手；
- 不是 genesis、activation registry 或 release manifest 的替代品；
- 不表示三个仓库记录的一定是各自最新 HEAD。

正式发布仍需由 canonical genesis、chain config、activation binding、release
manifest 和相应签名/验收流程确定。revision lock 只证明某个源码和工具链组合经过
了指定 CI 验证。

## 文件结构

当前 schema 为 `usdb-ci-revisions:v2`，包含以下字段：

| 字段 | 含义 |
| --- | --- |
| `schema_version` | revision lock schema 版本 |
| `coordinator` | Go coordinator 的固定 repository identity 和 checkout 目录，不包含自引用 revision |
| `dependencies` | `usdb`、`SourceDAO` 的固定 identity、checkout 目录和完整 commit SHA |
| `toolchains` | Fast CI 共享 runner、Go、Python、Rust、Node 和 npm 版本 |

repository revision 必须为 40 字符小写完整 commit SHA，不能使用 `main`、
`master`、tag 或短 SHA。校验器还会拒绝：

- 重复 JSON key；
- 未知或缺失字段；
- 非预期 repository identity 或 checkout 目录；
- 浮动或格式错误的工具链版本；
- sibling checkout 与 lock 不一致。

## CI 使用方式

三个 repo-local workflow 始终测试触发 workflow 的当前 checkout。revision lock
主要由 `go-ethereum` 的跨仓库 job 使用：

1. `go-ethereum` 使用当前 PR、push 或手工触发的 checkout；
2. `usdb` 和 `SourceDAO` 按 lock 中的完整 SHA checkout；
3. 校验器要求两个 sibling 精确匹配；
4. golden generator 使用该固定 `usdb` revision 校验当前 Go artifact。

普通 Go push/PR 始终运行 revision-lock schema 校验和 Go canonical/compatibility lane。昂贵的
cross-repository golden lane 只在 revision lock、frozen golden、golden runner/workflow 发生变化时
运行；手工 dispatch 和 release workflow 的 reusable call 显式强制运行。首次 push、无法读取 diff
base 等无法可靠判定变更范围的情况也强制运行，不能以路径过滤掩盖未知状态。

Go revision 由当前 workflow checkout 或 release annotated tag target 确定，不写入其自身
管理的 lock。校验器检查 coordinator checkout 存在，并要求两个 dependency checkout 与
lock 完全一致。

## 更新策略

### 不需要更新的情况

以下变更通常不更新 revision lock：

- 单仓内部重构，外部接口和可观察派生结果不变；
- 普通文档、注释和测试整理；
- 不影响跨仓库消费者的局部 bugfix；
- 当前 coordinator 上的普通连续提交；
- 仅升级 workflow 中的 Action SHA，且共享工具链和三仓组合没有变化。

不要为了“看起来是最新版本”而机械追随三个仓库 HEAD。这样会破坏 last-known-good
语义，并可能把未联合验证的组合写入 lock。

### 必须或建议更新的情况

| 变更 | 策略 |
| --- | --- |
| RPC schema、payload、error contract 或 capability 变化 | 必须更新并联合验证 |
| activation/release golden artifact 输入或生成规则变化 | 必须更新并联合验证 |
| SourceDAO bootstrap bytecode 或 Go genesis 集成变化 | 必须更新并联合验证 |
| Rust/Go、Go/SourceDAO 或三仓联动批次 | 必须更新 |
| Go、Rust、Python、Node/npm 固定版本变化 | 必须原子更新代码、workflow 和 lock |
| nightly/integration 基线主动前进 | 建议更新 |
| release candidate、预发或正式发布冻结 | 必须更新并运行完整矩阵 |

推荐规则是：**每个跨仓库兼容性批次更新一次，而不是每个 commit 更新一次。**

如果长期只在预发版更新，repo-local Fast CI 仍能发现单仓问题，但 central
cross-repository job 会持续使用旧 sibling，跨仓库集成漂移可能积累到发布阶段才暴露。

## 标准更新流程

v2 不保存 coordinator revision，因此不再需要“已验证前一提交”的自引用模型。标准流程如下：

1. 在 `usdb` 和/或 `SourceDAO` 完成修改、repo-local gate 和独立提交；
2. 将新的 sibling 完整 SHA 写入 `ci-revisions.json`；
3. 在 `go-ethereum` 完成对应实现并运行 repo-local gate；
4. 使用当前 Go commit、locked USDB 和 locked SourceDAO 运行 revision verify 与跨仓库
   golden/integration checks；
5. release 时由 Go annotated tag 固定实际 coordinator revision，并写入 release manifest。

发布协调工具可以从当前 `go-ethereum` checkout 自动推导 sibling workspace：

```bash
# 默认 dry-run；要求 USDB HEAD 已 push 且与 origin/master 完全一致。
python3 scripts/usdb/prepare_release.py sync-lock

# 一次性写入 lock、创建 Go commit，并显式 push。
python3 scripts/usdb/prepare_release.py sync-lock --commit --push

# 也可以分两步执行；第二条只允许续推恰好一个且仅修改 lock 的本地 commit。
python3 scripts/usdb/prepare_release.py sync-lock --commit
python3 scripts/usdb/prepare_release.py sync-lock --push

# 两仓 Fast CI 通过后先预检，再创建和推送同名 annotated tag。
python3 scripts/usdb/prepare_release.py tag --release-id usdb-testnet-v0-r1
python3 scripts/usdb/prepare_release.py tag \
  --release-id usdb-testnet-v0-r1 --create --push
```

默认 workspace root 是承载当前 `go-ethereum` checkout 的上级目录。非标准布局可在子命令前
显式提供 `--workspace-root /path/to/workspace`。工具会校验三仓目录、Git top-level、GitHub
origin、主分支、clean worktree、published HEAD、lock 和 tag 可用性；不会执行 `git pull`、
rebase、tag 覆盖或远端 tag 删除。

## 本地校验命令

校验 schema 和单元测试：

```bash
PYTHONDONTWRITEBYTECODE=1 python3 scripts/usdb/test_ci_revisions.py
PYTHONDONTWRITEBYTECODE=1 python3 scripts/usdb/ci_revisions.py validate
```

在三个仓库位于同一 workspace root 时校验 checkout：

```bash
PYTHONDONTWRITEBYTECODE=1 \
python3 scripts/usdb/ci_revisions.py verify \
  --workspace-root /path/to/workspace
```

校验 Rust-to-Go frozen artifact：

```bash
USDB_FAST_SCOPE=golden \
USDB_REPO_DIR=/path/to/usdb \
RUSTUP_TOOLCHAIN=1.91.0 \
scripts/usdb/run_fast_ci.sh
```

## 评审要求

revision lock 变更应在 review 中明确说明：

1. 为什么需要推进基线；
2. 哪些跨仓库 contract、artifact 或工具链发生变化；
3. 新 SHA 对应的提交内容；
4. 执行了哪些 repo-local 和跨仓库验证；
5. 是否仍有未运行的 nightly、live 或 release-only 测试。

lock 更新不应夹带未解释的 repository identity、工具链或 workflow 信任边界变化。
