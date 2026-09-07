# 独立上游多节点故障矩阵

## 目的与边界

`upstream-fault-matrix` 是短专项，每次建立一套新环境，独立于保留 2500
工作轮次的 world-soak。三个 geth 分别连接自己的 Bitcoin Core、Ord、
balance-history 和 usdb-indexer；只有 BTC/geth 的 P2P 连接共享规范链。
三个节点不能通过共用 indexer RPC 或复制数据库获得一致结果。

这项验证检查跨进程隔离、故障时拒绝、回滚恢复与从创世完整执行之间的一致性。
节点运行相同实现，因此它不是独立实现的协议公式 oracle，也不替代发布镜像、
生产规模压力或深度超过保留窗口的重组测试。
本专项直接运行 geth，验证进程内的自动恢复；容器及 runtime guard 的停机、重启策略由各自专项覆盖。

## 必过阶段

| 阶段 | 实际触发 | 门禁 |
| --- | --- | --- |
| baseline | A 挖矿，B 从空上游和空 geth 同步 | B 实际查询历史 profile；完整区块、系统合约历史关键槽位、矿工余额和上游语义状态一致 |
| indexer-crash | SIGKILL B 的 indexer，BTC 推进到新 anchor，A 继续挖矿 | A 高度增长；B 在观察期内停滞；审计代理记录 B 的真实 profile 查询连接错误 |
| crash-recovery | B 复用原 indexer 数据库重启，geth 保持原进程 | 90 秒内自动建立新 RPC 连接，成功重试相同 selector 并导入故障期积压区块；完整历史执行状态一致 |
| balance-crash | SIGKILL B 的 balance-history，保留 indexer 进程 | indexer RPC 仍存活，报告 `UpstreamReadinessUnknown`；A 引用新 anchor 出块，B 实际收到 `-32041` 并停滞 |
| balance-recovery | balance-history 复用原数据库重启 | indexer 与 geth 都保持原进程；90 秒内恢复共识就绪、重试相同 selector 并追平；完整历史与上游状态一致 |
| ord-outage | B 使用 Core 解析铭文时 SIGKILL Ord，并推进 BTC | indexer 保持共识就绪；B 不重启即验证新 anchor 并追平 A；经济状态一致 |
| ord-recovery | Ord 复用原数据库重启、补齐缺失块 | 精确高度及哈希收敛，补做故障期间延后的 Ord owner/content 对照 |
| ord-source-outage | B 改用 Ord 解析铭文后 SIGKILL Ord，并推进 BTC | indexer 存活但报告 `CatchingUp`，同步高度落后；A 继续出块，B 实际查询未就绪错误并停滞 |
| ord-source-recovery | Ord 复用原数据库重启，B 保持 Ord 解析模式 | geth 不重启，90 秒内恢复相同 selector 查询并追平；完整历史和状态一致 |
| ord-event-outage | B 位于独立分支且采用 Ord 解析，SIGKILL Ord 后挖入一笔真实 pass 转移 | 区块中确实包含该交易，铭文输出发给新 owner；推进跨稳定前沿后，indexer 仍因 `CatchingUp` 未处理该事件 |
| ord-event-catchup | Ord 复用原数据库追赶，随后再重启一次 indexer | Ord 与 indexer 的新 owner/satpoint 一致；pass 转为 Dormant，能量按独立数值断言结算并冻结；只有一条转移记录，重启前后完整状态不变 |
| stable-fork | 隔离 B 的 Core，替换跨稳定前沿的分支，替代块排除 A 已确认的 1 BTC top-up | 同高度 BTC 哈希不同、实际余额相差 1 BTC；A 挖矿推进、B 对新 anchor 拒绝 |
| recovery-interrupted | B 返回规范链，故障钩子将恢复停在能量回滚前，再 SIGKILL indexer | 持久化 recovery marker 存在，报告 `ReorgRecoveryPending`，实际 validator 查询返回 `-32041` 且不导入新块；强制退出后 marker 保留 |
| recovery-reinterrupted | 原数据库重启，恢复推进到 transfer tracker 重载前，再 SIGKILL indexer | 第二个钩子确实触发；恢复目标、reorg epoch 保持不变；仍拒绝验证，强制退出后 marker 保留 |
| fork-recovery | 清除故障注入参数，再次重启 B 的 indexer | pending marker 被清除且 epoch 不额外增加；geth 保持原进程，90 秒内重试先前拒绝的 selector 并恢复全历史一致 |
| ord-event-rollback | B 返回包含冲突交易的规范链，撤销停机窗口中的转移 | 旧 owner 与 Active 状态恢复，能量符合规范链投影；新 owner 无残留 pass/余额，孤块 selector 被原生 mismatch 拒绝，未受重组影响的历史 selector 仍可用 |
| steady-progress | 全部故障恢复后再推进 BTC 并产生新的 USDB 区块 | 同一个 B geth 实际查询更高 anchor、导入新块；完整历史和上游状态一致 |
| fresh-replay | C 在上述故障恢复后从全新目录加入 | BTC/Ord/两索引器全部重建；geth 从 genesis 以 full 模式执行；每个实际 anchor 都有成功查询；A/B/C 全量状态一致，C 也通过事件撤销与历史 selector 检查 |

上游中断及分叉用例必须先证明健康节点继续出块，并让新块引用新的 BTC anchor，防止缓存
旧 profile 使测试失去意义。失败记录必须来自 B 的 geth，而不是单独发送一个
人工 RPC 探针。每个必过阶段成功后才写入 `summary.json`；缺阶段不能全绿。
中断时 validator 可以先在复验父块 profile 时拒绝，报告记录实际拒绝的 anchor；
分叉用例则要求新 anchor 的原生 snapshot/hash/state selector 不匹配错误。
注入前在上游健康时重启 A 的 geth，清除 `miner_stop` 保留的旧 anchor 待封块，
并在 B 仍就绪时等待它通过基线登记的静态 peer 自动重新连接；Ord 依赖已经失效的阶段则通过实际拒绝确认连接尝试。
否则合法的旧上下文块可能先被封出并被 B 接受，干扰本项
对新上下文拒绝行为的判断。矿工运行中刷新 profile 的覆盖仍在原专项 E2E 中。

从 baseline 完成到 fresh-replay 完成，B 的 geth 进程、PID 和 Linux 进程启动时刻必须保持不变，
启动次数必须为 1；脚本在这段期间禁止停止 B 的 geth 或再次调用 `admin_addPeer`。
两次 `ReorgRecoveryPending` 中断也保持同一 geth，仅中断并恢复上游 indexer。
最终清理才解除该约束。A 的挖矿准备重启与 C 的首次启动不属于这个 validator 连续性范围。

四个自动恢复阶段的 90 秒预算从恢复上游之前开始，包含上游 readiness、Ord 追平和 geth 同步等待。
恢复期间 A 停止挖矿，目标 head 固定，B 必须自行重试，不能依靠新块通知或人工连接操作解除停滞。
门禁比较恢复前后的完整 profile 请求参数，要求同一 selector 曾失败而后成功，并要求实际查询目标块的
新 anchor；只恢复 RPC 存活、只查询旧缓存或只报告相同高度均不算通过。

审计代理保留 HTTP keep-alive；上游连接失败时记录真实请求并断开 geth 的 HTTP 连接，
不会将连接故障统一转换成 JSON-RPC 响应。`crash-recovery` 必须证明同一失败请求经新的
连接成功；原生 readiness 和 selector 错误仍原样转发。连接编号和完整请求见 `b-rpc.jsonl`，
PID、启动时刻、恢复耗时、重试请求摘要及状态摘要见 `summary.json`。

BTC 稳定延迟固定为 10。标准 pass 在分叉点之前铸造；回滚分支移除真实余额
变化，避免只比较空块重组。Ord 使用间隔 1、保留 64 个 savepoint；替代分支
和恢复分支都推进到旧 tip 之上，并检查 `blockcount == BTC height + 1` 及
规范 `blockhash`。全量历史查询等待历史 anchor 回填，只重试明确的
`HISTORY_NOT_AVAILABLE`，不会把永久 selector 错误当作启动延迟。
状态对照还比较各自 Ord 的铭文 owner、完整铭文信息和实际 mint 内容；仅在
`ord-outage` 中暂缓 Ord 对照，恢复时必须补验。B 切换为 Ord 解析后一直保留
该配置，A/C 使用 Core 解析。前两个 Ord 故障场景检查上游可用性；随后
`ord-event-*` 场景在独立分支上增加真实转移，检查缺失事件追赶、重复处理与撤销。

事件用例直接花费 offset 为 0 的 10,000 sat 铭文输出，将 9,000 sat 发给新
owner、支付 1,000 sat 手续费。交易由 A 的隔离测试钱包签名，仅挖入 B 的分支。
转移导致旧 owner 减少 10,000 sat，但余额的 `floor(balance / 100000)` 保持
不变，所以该夹具不触发余额 unit 减少惩罚。按 UIP-0002/0003，事件高度能量
应等于前一高度能量加一个区块的余额 unit 数，之后进入 Dormant 并保持不变。
检查事件高度及两个区块后的结果，同时检查 active owner 集合、候选集合、
完整历史和能量账本。完整历史必须只新增一条 `state_update` 和一条
`owner_transfer`，顺序固定；同一数据库重启后的完整快照必须完全一致。

返回规范链前，A 会把同一铭文输出花费回原 owner，形成与 B 转移冲突的规范
交易。它保留 Active 状态，并阻止被撤销交易通过 mempool 再次广播或挖入。
规范链费用同样不跨余额 unit 边界，恢复后的能量按重组前规范状态独立投影。
B/C 在原事件高度重新查询规范状态：新 owner 无经济残留、旧 owner 为 Active，
事件分支的历史 selector 必须返回永久 mismatch；`SNAPSHOT_NOT_READY`、
`HISTORY_NOT_AVAILABLE` 或服务不通都不能满足这一拒绝门禁。分叉前保存的
规范历史 selector 必须继续返回相同 profile。

恢复中断复用 indexer 已有的 regtest 故障钩子：
`USDB_INDEXER_INJECT_REORG_RECOVERY_ENERGY_FAILURES` 与
`USDB_INDEXER_INJECT_REORG_RECOVERY_TRANSFER_RELOAD_FAILURES`。
分别设置高于短测重试次数的预算，让恢复停在可观察的边界，再杀死真实进程。
必须看到对应钩子的日志、原生 readiness 和 geth 的实际拒绝；同时只读查询
`miner_pass.db` 中的 `upstream_reorg_recovery_pending_height`，在每次强制退出
前后核验。第二次中断位于能量回滚之后、transfer tracker 重载之前。普通重启
及另一钩子的重启都会清除继承的注入参数，最终恢复不得残留 pending marker。
真实转移的撤销也经过这两次恢复中断，最终由全新节点 C 的完整重放对照收尾。

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

1. 按需扩展新 mint/remint、跨余额 unit 的转移，以及同区块事件排序组合。
2. 在固定短矩阵稳定后，再加入不同重组深度与故障时点的有限种子。每个种子
   从空环境开始，单个种子内保留故障前后的状态联系。

这些扩展不应通过延长 worldsim 轮次实现；K 的 50400 区块窗口边界由现有
独立 K oracle 覆盖。

更新到 USDB `afd21a7` 后，原子 snapshot anchor 发布可能在启动时使
`get_readiness` 的 `btc_synced_block_height` 查询短暂返回 SQLite
`database is locked`。矩阵仅对这个明确的 readiness 错误沿用有时限的重试，
其他内部错误或 profile 查询中的同类错误仍立即失败，不扩大历史 selector
拒绝门禁的允许错误集合。

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

## 本地验收记录（2026-09-06，v3 故障窗口真实转移）

使用 CI lock 中的 USDB `afd21a7` 隔离源码，以 Rust 1.91.0 编译两索引器，
搭配 Go 1.18.5、Core 28.1、Ord 0.23.3 和本地 Python 3.11.2，通过真实
`run_long_ci.sh weekly upstream-fault-matrix --run-only` 入口验收。报告 schema
为 `usdb-independent-upstream-matrix:v3`，17 个阶段全部通过，矩阵主体及清理
耗时 307.67 秒，不含编译和 A 的初始化。

- B 在 Ord 停机期间将真实转移挖入高度 181，indexer 停在 170；Ord 恢复后
  追赶到稳定高度 183，新 owner/satpoint 与 Ord 一致。
- 转移时 raw energy 按独立断言从 45000 增至 46000，Dormant 后保持 46000；
  `state_update`、`owner_transfer` 各一条且顺序正确。原数据库重启后全量状态
  摘要保持不变，未重复处理或结算。
- 返回规范链期间的两次强制退出均保留高度 156 的恢复标记，epoch 保持 2。
  最终恢复清除标记，规范链同 owner 冲突交易排除了孤块转移及其 mempool 复活。
- B/C 在高度 181 的规范历史状态完全一致：旧 owner 为 Active，raw energy
  独立投影为 60000，新 owner 无残留 pass/余额；孤块 selector 均返回 `-32042`，
  分叉前的历史 selector 仍返回原 profile。
- C 从空目录重放全部 12 个 USDB 区块，实际验证 6 个 payload anchor，另外
  核验事件高度 181 的历史状态和 selector。最终稳定 BTC 高度 184 上的
  A/B/C 全量状态、历史账本与 Ord 铭文信息一致。
- 19 项矩阵门禁/生命周期测试、16 项 long-CI 测试、6 项 revision-lock 测试
  和 ShellCheck 通过；无残留测试服务进程。未修改 2500 轮配置或执行完整 weekly。

这条确定性用例覆盖真实 transfer；新 mint/remint 与跨余额 unit 惩罚组合仍按
上述后续计划补充。validator 在故障恢复后仍显式重启，自动恢复保证不在本轮扩展内。

## 本地验收记录（2026-09-07，v4 validator 自动恢复）

使用 Go `3bebdb7c7` 的服务代码和本次矩阵改动，搭配 CI lock 中的 USDB `335d9b7`
（包含 SQLite WAL 修复），以 Go 1.18.5 / Rust 1.91.0 编译固定服务二进制；
Core 28.1、Ord 0.23.3、本地 Python 3.11.2。经真实 `--prepare-only` / `--run-only`
入口完成全部 18 阶段，矩阵主体与清理耗时 471.92 秒，不含编译和 A 的初始化。

| 自动恢复场景 | 恢复耗时 | 验证结果 |
| --- | --- | --- |
| indexer SIGKILL 后恢复 | 19.587 秒 | HTTP 连接 4 被断开，相同 selector 经新连接重试成功，USDB 高度 2 → 4 |
| balance-history SIGKILL 后恢复 | 27.607 秒 | indexer 和 geth 都不重启，原 `-32041` 请求成功，USDB 高度 4 → 6 |
| Ord 解析依赖停机后恢复 | 34.926 秒 | 原进程成功重试两个 selector，USDB 高度 8 → 10 |
| 重组恢复连续中断两次后恢复 | 35.124 秒 | pending 期间拒绝验证，清除 pending 后原进程追平，USDB 高度 10 → 12 |

- 四个恢复时限均为 90 秒；从开始恢复上游到导入固定目标 head 计时。
- B 全程 PID 为 `3208488`，Linux start ticks 为 `86567510`，启动次数为 1；
  geth 日志也只出现一次 P2P 启动。基线之后没有手工 peer 重连。
- 两次恢复中断保留 pending 高度 156、epoch 2；真实转移撤销与孤块 selector 拒绝仍通过。
- 恢复后推进到 BTC stable height 187，B 实际验证新 anchor 并继续至 USDB 高度 14。
- C 从空目录完整重放 14 个区块，成功查询覆盖 7 个历史 anchor；A/B/C 全量状态及历史一致。
- 24 项矩阵门禁测试、16 项 long-CI 测试、6 项 revision-lock 测试，以及发布片段校验通过；
  测试服务均已清理。本轮没有执行完整 weekly，2500 轮 world-soak 配置保持不变。
