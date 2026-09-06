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
| stable-fork | 隔离 B 的 Core，替换跨稳定前沿的分支，替代块排除 A 已确认的 1 BTC top-up | 同高度 BTC 哈希不同、实际余额相差 1 BTC；A 挖矿推进、B 对新 anchor 拒绝 |
| fork-recovery | B 撤销测试分支、重新连接 A；A 挖触发块 | B 的原 Core/Ord/balance-history/indexer 数据库恢复规范状态；重启 validator 后全历史一致 |
| fresh-replay | C 在上述故障恢复后从全新目录加入 | BTC/Ord/两索引器全部重建；geth 从 genesis 以 full 模式执行；每个实际 anchor 都有成功查询；A/B/C 全量状态一致 |

故障用例必须先证明健康节点继续出块，并让新块引用新的 BTC anchor，防止缓存
旧 profile 使测试失去意义。失败记录必须来自 B 的 geth，而不是单独发送一个
人工 RPC 探针。每个必过阶段成功后才写入 `summary.json`；缺阶段不能全绿。
中断时 validator 可以先在复验父块 profile 时拒绝，报告记录实际拒绝的 anchor；
分叉用例则要求新 anchor 的原生 snapshot/hash/state selector 不匹配错误。
注入前在上游健康时重启 A 的 geth，清除 `miner_stop` 保留的旧 anchor 待封块，
并等待 B 重新连接。否则合法的旧上下文块可能先被封出并被 B 接受，干扰本项
对新上下文拒绝行为的判断。矿工运行中刷新 profile 的覆盖仍在原专项 E2E 中。

BTC 稳定延迟固定为 10。标准 pass 在分叉点之前铸造；回滚分支移除真实余额
变化，避免只比较空块重组。Ord 使用间隔 1、保留 64 个 savepoint；替代分支
和恢复分支都推进到旧 tip 之上，并检查 `blockcount == BTC height + 1` 及
规范 `blockhash`。全量历史查询等待历史 anchor 回填，只重试明确的
`HISTORY_NOT_AVAILABLE`，不会把永久 selector 错误当作启动延迟。
每次状态对照还比较各自 Ord 的铭文 owner、完整铭文信息和实际 mint 内容，
为 indexer 的 Core 铭文解析路径增加来自 Ord 的交叉验证。

## 运行与证据

CI 作为单独 weekly shard 运行。编译步骤 40 分钟；模拟内部总预算 20 分钟，
CI 执行步骤 22 分钟，额外时间用于清理。Go 和 Rust 的服务二进制均在编译阶段
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

1. 将同一故障生命周期扩展到 balance-history 中断、Ord 落后，分别断言其
   readiness 与拒绝原因，避免用统一的“RPC 不通”替代不同故障语义。
2. 增加重组恢复期间再次中断、进程恢复后仍保留过期本地缓存的组合；继续
   由新节点的完整重放作为收尾对照。
3. 在固定短矩阵稳定后，再加入不同重组深度与故障时点的有限种子。每个种子
   从空环境开始，单个种子内保留故障前后的状态联系。

这些扩展不应通过延长 worldsim 轮次实现；K 的 50400 区块窗口边界由现有
独立 K oracle 覆盖。

## 本地验收记录（2026-09-06）

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
