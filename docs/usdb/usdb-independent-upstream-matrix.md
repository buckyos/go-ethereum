# 独立上游多节点故障矩阵

## 目的与边界

`upstream-fault-matrix` 是短专项，每次建立一套新环境，独立于保留 2500
工作轮次的 world-soak。三个 geth 分别连接自己的 Bitcoin Core、Ord、
balance-history 和 usdb-indexer；只有 BTC/geth 的 P2P 连接共享规范链。
三个节点不能通过共用 indexer RPC 或复制数据库获得一致结果。

这项验证检查跨进程隔离、故障时拒绝、回滚恢复与从创世完整执行之间的一致性。
节点运行相同实现，因此它不是独立实现的协议公式 oracle，也不替代发布镜像、
生产规模压力或深度超过保留窗口的重组测试。

## 必过阶段

| 阶段 | 实际触发 | 门禁 |
| --- | --- | --- |
| baseline | A 挖矿，B 从空上游和空 geth 同步 | B 实际查询历史 profile；完整区块、系统合约历史关键槽位、矿工余额和上游语义状态一致 |
| indexer-crash | SIGKILL B 的 indexer，BTC 推进到新 anchor，A 继续挖矿 | A 高度增长；B 在观察期内停滞；审计代理记录 B 的真实 profile 查询连接错误 |
| crash-recovery | B 复用原 indexer 数据库重启，再重启 validator 重新连接 | BTC、Ord、balance-history、indexer 精确高度与哈希收敛；完整历史执行状态一致 |
| balance-crash | SIGKILL B 的 balance-history，保留 indexer 进程 | indexer RPC 仍存活，报告 `UpstreamReadinessUnknown`；A 引用新 anchor 出块，B 实际收到 `-32041` 并停滞 |
| balance-recovery | balance-history 复用原数据库重启 | indexer 无需重启即恢复共识就绪；重启 validator 后完整历史与上游状态一致 |
| ord-outage | B 使用 Core 解析铭文时 SIGKILL Ord，并推进 BTC | indexer 保持共识就绪；B 不重启即验证新 anchor 并追平 A；经济状态一致 |
| ord-recovery | Ord 复用原数据库重启、补齐缺失块 | 精确高度及哈希收敛，补做故障期间延后的 Ord owner/content 对照 |
| ord-source-outage | B 改用 Ord 解析铭文后 SIGKILL Ord，并推进 BTC | indexer 存活但报告 `CatchingUp`，同步高度落后；A 继续出块，B 实际查询未就绪错误并停滞 |
| ord-source-recovery | Ord 复用原数据库重启，B 保持 Ord 解析模式 | indexer 追平并恢复共识就绪；重启 validator 后完整历史和状态一致 |
| stable-fork | 隔离 B 的 Core，替换跨稳定前沿的分支，替代块排除 A 已确认的 1 BTC top-up | 同高度 BTC 哈希不同、实际余额相差 1 BTC；A 挖矿推进、B 对新 anchor 拒绝 |
| recovery-interrupted | B 返回规范链，故障钩子将恢复停在能量回滚前，再 SIGKILL indexer | 持久化 recovery marker 存在，报告 `ReorgRecoveryPending`，实际 validator 查询返回 `-32041` 且不导入新块；强制退出后 marker 保留 |
| recovery-reinterrupted | 原数据库重启，恢复推进到 transfer tracker 重载前，再 SIGKILL indexer | 第二个钩子确实触发；恢复目标、reorg epoch 保持不变；仍拒绝验证，强制退出后 marker 保留 |
| fork-recovery | 清除故障注入参数，再次重启 B 的 indexer | pending marker 被清除且 epoch 不额外增加；原数据库恢复规范状态，重启 validator 后全历史一致 |
| fresh-replay | C 在上述故障恢复后从全新目录加入 | BTC/Ord/两索引器全部重建；geth 从 genesis 以 full 模式执行；每个实际 anchor 都有成功查询；A/B/C 全量状态一致 |

上游中断及分叉用例必须先证明健康节点继续出块，并让新块引用新的 BTC anchor，防止缓存
旧 profile 使测试失去意义。失败记录必须来自 B 的 geth，而不是单独发送一个
人工 RPC 探针。每个必过阶段成功后才写入 `summary.json`；缺阶段不能全绿。
中断时 validator 可以先在复验父块 profile 时拒绝，报告记录实际拒绝的 anchor；
分叉用例则要求新 anchor 的原生 snapshot/hash/state selector 不匹配错误。
注入前在上游健康时重启 A 的 geth，清除 `miner_stop` 保留的旧 anchor 待封块，
并在 B 仍就绪时等待重新连接；Ord 依赖已经失效的阶段则通过实际拒绝确认连接尝试。
否则合法的旧上下文块可能先被封出并被 B 接受，干扰本项
对新上下文拒绝行为的判断。矿工运行中刷新 profile 的覆盖仍在原专项 E2E 中。

BTC 稳定延迟固定为 10。标准 pass 在分叉点之前铸造；回滚分支移除真实余额
变化，避免只比较空块重组。Ord 使用间隔 1、保留 64 个 savepoint；替代分支
和恢复分支都推进到旧 tip 之上，并检查 `blockcount == BTC height + 1` 及
规范 `blockhash`。全量历史查询等待历史 anchor 回填，只重试明确的
`HISTORY_NOT_AVAILABLE`，不会把永久 selector 错误当作启动延迟。
状态对照还比较各自 Ord 的铭文 owner、完整铭文信息和实际 mint 内容；仅在
`ord-outage` 中暂缓 Ord 对照，恢复时必须补验。B 切换为 Ord 解析后一直保留
该配置，A/C 使用 Core 解析。当前故障窗口主要推进空块；它覆盖上游可用性、
落后追赶及既有铭文的重建，尚不覆盖故障窗口内新增铭文或转移的组合。

恢复中断复用 indexer 已有的 regtest 故障钩子：
`USDB_INDEXER_INJECT_REORG_RECOVERY_ENERGY_FAILURES` 与
`USDB_INDEXER_INJECT_REORG_RECOVERY_TRANSFER_RELOAD_FAILURES`。
分别设置高于短测重试次数的预算，让恢复停在可观察的边界，再杀死真实进程。
必须看到对应钩子的日志、原生 readiness 和 geth 的实际拒绝；同时只读查询
`miner_pass.db` 中的 `upstream_reorg_recovery_pending_height`，在每次强制退出
前后核验。第二次中断位于能量回滚之后、transfer tracker 重载之前。普通重启
及另一钩子的重启都会清除继承的注入参数，最终恢复不得残留 pending marker。

恢复分支编排时先停 B 的 balance-history，完成 Core 的 invalidate/reconsider、
P2P 收敛与 Ord 触发块，再复用原 balance-history 数据库启动。这样它只接收
确定的规范分支，避免两个 Core RPC 之间的短暂低 tip 改变预定回滚目标。
两次中断都发生在同一次规范链恢复中；marker、epoch、validator 高度和完整
重放对照共同约束恢复结果，不通过重建 B 的数据库绕过恢复流程。

## 运行与证据

CI 作为单独 weekly shard 运行。编译步骤 40 分钟；模拟外层总预算 20 分钟，
其中 Python 矩阵预算 15 分钟；CI 执行步骤 22 分钟，额外时间用于清理。
Go 和 Rust 的服务二进制均在编译阶段
准备，节点使用同一套二进制；模拟阶段不因多个节点而重复编译。
`--run-only` 传递 `MATRIX_SKIP_BUILD=1`，缺少预编译二进制会立即失败。

```bash
export BITCOIN_BIN_DIR=/path/to/bitcoin/bin
export ORD_BIN=/path/to/ord
scripts/usdb/run_long_ci.sh weekly upstream-fault-matrix --prepare-only
scripts/usdb/run_long_ci.sh weekly upstream-fault-matrix --run-only
```

本地也可执行 `bash scripts/usdb/run_usdb_upstream_fault_matrix.sh`。
`MATRIX_WORK_ROOT` 指定保存临时运行目录与二进制的位置；每次运行创建新的
`run-*` 子目录。`MATRIX_OUTPUT_DIR` 保存报告，`MATRIX_PORT_BASE` 默认 22400，
全部端口需低于 32768。端口分配包含 Core 隐式使用的 P2P+1 onion 监听端口。
仅测试目录中的新进程被启动或停止；失败时保留目录和日志。

测试 genesis 使用正数难度 256 缩短 PoW 等待，保留真实 PoW、USDB header
校验和 EVM 执行。这个配置仅用于临时 regtest，不应用于测试网创世配置。

产物包括：每阶段门禁结果、节点端口及目录、geth 的 profile RPC 审计 JSONL、
各节点服务日志、完整区块列表、上游全量快照与 pass/energy/balance 历史账本。
完整状态比较复用 USDB `9744ade` 引入的 replay helper；CI revision lock
必须包含该版本或后续版本，并在运行前确保依赖提交已发布到远端。

## 后续扩展顺序

1. 在 Ord 停机窗口中加入实际新铭文或转移，覆盖缺失事件的追赶与回滚。
2. 增加上游恢复后 validator 不重启的缓存恢复专项，明确自动恢复保证。
3. 在固定短矩阵稳定后，再加入不同重组深度与故障时点的有限种子。每个种子
   从空环境开始，单个种子内保留故障前后的状态联系。

这些扩展不应通过延长 worldsim 轮次实现；K 的 50400 区块窗口边界由现有
独立 K oracle 覆盖。

## 本地验收记录（2026-09-06，v1 六阶段基线）

使用 Go 1.18.5、Bitcoin Core 28.1、从官方 `0.23.3` tag 构建的 Ord，
以及 USDB `9744ade` 的隔离源码和固定二进制，通过真实
`run_long_ci.sh weekly upstream-fault-matrix --run-only` 入口完成全部六阶段。
入口总耗时约 173 秒，其中矩阵主体与清理约 153 秒，均不含编译。

- A 的 USDB 高度依次达到 2、4、6；故障期 B 分别停在 2、4，并记录实际拒绝。
- 分叉深度 23，规范链与故障分支的 owner 余额精确相差 100000000 sat。
- C 从全新数据目录完整执行 6 个区块，验证了 144、147、160 三个实际历史
  anchor；A/B/C 的最终经济状态、历史账本和 Ord 铭文信息全部一致。
- 9 项新门禁测试、16 项 long-CI 测试、6 项 revision-lock 测试通过，全部
  USDB shell 脚本的 ShellCheck 通过；没有遗留本次测试服务进程。

本轮没有重新执行完整的 2500 轮 weekly。恢复阶段明确重启 B 的 geth 并复用
其原数据库；不将这些结果解读为无需重启 validator 的自动恢复保证。

## 本地验收记录（2026-09-06，v2 十四阶段扩展）

沿用上述固定二进制与 CI 锁定的 USDB `9744ade`，从真实 `--run-only` 入口
运行，报告 schema 为 `usdb-independent-upstream-matrix:v2`。最终版本全部
14 阶段通过，矩阵主体及清理耗时 300.07 秒，不含 A 的初始化及编译。

- balance-history 中断后 B 返回 `-32041`，恢复时原 indexer PID 保持不变。
- Core 解析模式下 Ord 停机，B 继续验证新 anchor 并追平至高度 8；Ord 解析
  模式下 B 停在高度 8、A 达到 10，B 报告 `CatchingUp` 并实际返回 `-32041`。
- 分叉深度 23，移除 1 BTC top-up 后 B 对新 anchor 返回 `-32042`。
- 能量回滚前、transfer tracker 重载前分别再次 SIGKILL indexer；两个钩子
  各观察到 9 次触发，各有 3 次真实 validator 拒绝。两次退出前后 pending
  高度均为 156，reorg epoch 均为 2，B 高度保持 10。最终正常恢复清除 marker，
  epoch 保持不变，B 追平至高度 12。
- C 从空目录执行全部 12 个 USDB 区块，26 次成功 profile 查询覆盖
  `144、147、150、153、156、169` 六个实际历史 anchor；A/B/C 最终经济状态、
  历史账本及 Ord 铭文信息一致。
- 14 项矩阵门禁和生命周期测试、16 项 long-CI 测试、6 项 revision-lock
  测试，以及按 Fast CI 参数执行的 ShellCheck 均通过。测试服务无残留进程。

2500 轮 world-soak 和时间预算保持不变，本轮没有重跑完整 weekly。上游故障
与重组恢复阶段仍明确重启 validator；仅 Core 解析模式的 Ord 停机阶段验证了
validator 无需重启即可继续出块。上述结果不扩展为所有故障的自动恢复保证。
