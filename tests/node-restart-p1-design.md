# 节点恢复验收：P1 设计

状态：进程层 P1 已实现并在本地验证；真实部署与 VM 层尚未执行。沿用 [P0 恢复验收](node-restart-acceptance.md)
的健康节点 A、故障节点 B、历史状态比较和恢复后新增业务验证。两个 P1 场景可以先独立于
VM 实现；真实 systemd/Docker 验收需要专用 Linux 测试机，VM 是可选的承载环境。

## P1-A：chain、controller、monitor 分别重启

目标是验证局部重启的影响范围、节点身份保持、重复启动保护，以及观测恢复。
每次只中断一个目标，先建立检查点，完成恢复和新增业务验证后再进入下一个子场景。

| 目标 | 注入方式 | 通过条件 |
|---|---|---|
| chain | 正常停止后启动；另一次只 SIGKILL geth | nodekey、角色、矿工参数和历史状态保持；原 peer 配置自动重连；继续执行新块；BH/Indexer/Core 不被重启 |
| controller | 在等待依赖时终止控制进程，再恢复；另测 READY 后重新提交启动 | 操作锁释放并能重新获取；继续原流程；没有重复实例或重复导入；已采用正确资源配置的容器不被再次重建 |
| monitor | 分别正常停止、SIGKILL 后恢复 | 历史告警和事件保留；只有一个采样/发布实例；出现新进程的成功采样；业务容器不重启 |

重启方式与验收层次分开：

- 进程层由测试脚本明确重新拉起被终止进程，验证持久化恢复，不把它记作自动拉起成功。
- 部署层使用已安装包和真实 systemd/Docker，核对实际 restart policy。正常 stop 后的再次
  start 与异常退出后的自动恢复分别测试，不能把人为 stop 当成必须自动重启。
- controller 完成启动后退出并处于 idle 是正常状态。运行期间的崩溃恢复和 READY 后重复
  提交启动分开断言；明确需要人工处理的退出码不能被测试程序循环重启掩盖。
- 普通恢复不得创建 `halted.json`；已有真实重组事件不能因重启被清除。保留现有永久停机
  对照测试。矿工角色覆盖限于隔离 regtest，部署验收继续使用专用 full 节点。

已有基础：

- `tests/node_restart_acceptance.py` 已有真实服务、历史状态/身份比较及新业务验证。
- `tests/test_runtime_deep_reorg.py` 已覆盖实际 chain 入口和 guard，geth 使用受控替身；
  包括 SIGTERM、重复信号、上游恢复、矿工参数保持和永久停机对照。
- USDB 的 `tests/test_node_monitor.py` 已有真实 monitor 子进程重启及单实例检查，采样输入
  使用测试替身；尚不等于完整部署下的 monitor 故障验收。
- USDB 已有 controller 操作锁、monitor 运行锁，以及后台服务幂等启动测试。

本批已将实际 chain 入口接到真实服务夹具，并给 controller/monitor 加上独立进程生命周期
与证据采集。部署层仍需在夹具中声明允许操作的 systemd unit 和 Docker service，并验证归属。
记录 PID 加启动时间、容器 ID 加 StartedAt，避免 PID 复用或同容器内重启被误判。

## P1-B：bootstrap、verify、资源切换途中中断

目标是从中间状态恢复，并证明不会发布未完成的数据或重复执行危险操作。
使用小型可重复快照、固定 regtest 历史及无中断对照结果，不依赖公网下载速度或人工观察百分比。

| 中断点 | 恢复后必须验证 |
|---|---|
| 快照下载已有部分文件，尚未校验完成 | 保留正确的部分文件；服务器支持 Range 时断点续传；最终 SHA-256 一致；损坏文件不能被当成已验证输入 |
| BH 导入批次已提交、回放检查点已提交 | 从持久化状态恢复，不重复计入 UTXO/余额；完整就绪前不开放查询；最终 origin commit 与无中断对照一致 |
| BH 正在 verify、封存后尚未发布 | 未封存时重新完成核验；封存后按现有发布流程恢复；最终承诺、余额和历史一致，不提前报告 READY |
| bitcoin → overlap、overlap → steady 资源切换 | pending 日志先于停服；停止旧分配后才应用新预算；已采用的目标容器不重复重建；未恢复服务继续启动，最终清除 pending/recover_services |

资源切换至少固定两个故障窗口：旧服务已停止但新配置未生效；新容器已采用配置但完成记录
尚未落盘。检查实际 Docker 内存上限、启动参数和活跃服务集合，不能仅凭 node.env 判断成功。
测试配置选用确定的受支持内存档位；不通过制造宿主机 OOM 来触发该场景。

几个恢复语义需要保持准确：

- **BH 导入可以按已提交批次恢复；verify 目前没有持久化扫描游标。** 未封存时允许重做完整
  核验，验收不要求扫描进度单调延续，也不把续扫优化作为 P1 的前置条件。
- **Core 的快照导入与 BH 导入不同。** `loadtxoutset` 响应丢失后先查看已有 chainstate 和
  活跃 RPC；结果仍不确定时保持明确的人工恢复状态，不能为通过测试盲目再次提交导入。
- Core 导入请求去重可先用现有受控 RPC 夹具验证。当前真实 regtest 服务验收通过提交区块
  建立前缀，不包含真实双 chainstate；完整 AssumeUTXO 导入还需要 Core 支持的确定性快照
  与区块夹具，或专用 mainnet 测试数据。增加 VM 本身不能补齐这些数据条件。
- 原 verify 临时目录依靠 Rust Drop 清理，SIGKILL 不会执行 Drop。本批已增加启动恢复清理，
  持有同一 bootstrap 工作区锁，只清理私有 staging 内格式匹配的废弃临时目录；遇到符号
  链接拒绝继续，不删除无关文件。测试覆盖连续两次 verify 崩溃和临时目录不再累积。

已有基础位于 USDB 仓库：`tests/assumeutxo_native.rs` 覆盖取消后的导入/回放恢复、verify
取消和封存发布；`tests/run_assumeutxo_p65_services.py` 提供真实 Core/BH/Indexer 小型夹具；
`docker/scripts/tools/test_resource_controller.py` 已有基于容器模型的切换故障窗口；
`tests/test_bitcoin_assumeutxo.py` 已有下载恢复和 Core 导入结果不确定的对照。

本批已增加**真正跨进程的中断**，重新打开数据库、重新获取锁并读取持久化日志。
controller 的 Docker 外部操作仍由持久化容器模型承接；实际 cgroup 限制和 Docker 重启
策略留在部署验收中核对，不能根据模型测试声称已完成真实容器资源验收。

## 故障定位与证据

优先使用现有 RPC 代理、阶段文件和测试包装器。容易一闪而过的提交窗口使用测试专用握手：
被测阶段发出标记，测试程序确认已到达后暂停/中断，再启动恢复；不依赖固定 sleep 猜时机。
Rust 内部持久化边界需要精确钩子时，放在测试构建或独立测试驱动中，正式发布包不启用。
阶段文件只是观察证据，不能代替数据库检查点作为恢复依据。

每个子场景记录固定 ID、注入点、进程身份、退出状态、恢复耗时和前后历史状态。
沿用 P0 的 summary/timeline/服务日志；部署层额外保留 systemd 状态、容器生命周期、资源配置
和 monitor 事件。只保存配置/身份摘要，不导出私钥、认证信息或完整容器环境。

通过条件包括故障确实命中、相应进程确实替换、恢复后的新鲜观察、历史一致和新业务进展。
monitor 需验证连续成功采样及既有恢复窗口，不能把停机时间算作连续健康时间。
缺少必需子场景、超时、夹具不满足条件不能记作通过；失败后保留证据，不自动清数据重跑。

## 实施顺序与 VM 边界

1. Fast CI 补充 controller/monitor 进程生命周期和资源切换边界检查，保留现有单元测试。
2. 独立 Nightly 分片 `node-restart-p1` 运行真实 chain 重启。小型 BH bootstrap/verify 进程
   测试耗时很短，直接进入 USDB Rust Fast gate；无需等待完整 regtest 栈启动。
3. 扩展现有手动部署验收，在专用 Linux runner 上检查真实 systemd/Docker 的局部重启和资源
   切换。已有 P0 部署入口要求 READY；冷启动 P1-B 应另设明确允许中间状态的新夹具描述，
   保留 P0 的 READY 前置条件。不得隐式接管或清空正在使用的测试节点。
4. 后续 P2 增加 VM 整机重启和强制断电，只替换故障执行与重新连接方式，复用历史/新业务
   验证。控制端必须在 VM 外，记录开机身份并在 SSH/Docker 恢复后继续验收。

进程 SIGKILL 不等于断电：操作系统缓存仍在，不能证明设备写入、文件系统恢复或开机启动
顺序正确。P1 不依赖 VM；P2 才用 VM 验证这类边界，并明确虚拟磁盘缓存与 flush 的配置。
当前实际 `usdb-node down/up` 部署入口已存在，但专用夹具验收尚未在本轮执行；不能据本地
regtest 结果宣称部署层或 VM 层已经通过。

## 已落地的进程层入口

| 仓库 / 入口 | 真实执行范围 | 测试替身边界 |
|---|---|---|
| Go：`tests/node_restart_p1.py` | 真实运行入口、guard、geth、Core、Ord、BH、Indexer；正常停启与 geth SIGKILL 后重新拉起 | 低难度隔离 regtest；故障后由测试脚本显式重新启动，不验证 Docker policy |
| USDB：`tests/test_node_process_recovery.py` | controller/up 状态机、资源切换日志、文件锁、monitor 守护进程、SQLite、实际 HTTPS 下载及 Core 导入观察进程 | 外部 Docker 操作、生命周期观察和 monitor 输入由夹具提供；Core 导入使用受控 RPC 服务 |
| USDB：`tests/assumeutxo_process_recovery.rs` | 独立 Rust 测试进程、真实快照解析、RocksDB 导入/回放/核验/封存与发布 | Core 历史使用已保存的 regtest 区块；故障握手只编入测试二进制 |

USDB 进程测试运行：

```bash
PYTHONDONTWRITEBYTECODE=1 python3 tests/test_node_process_recovery.py
cargo test --locked --manifest-path src/btc/Cargo.toml -p balance-history native_bootstrap_process_recovers
```

Go 真实服务测试运行（外部工具变量与 P0 相同）：

```bash
scripts/usdb/run_long_ci.sh nightly node-restart-p1 --prepare-only
scripts/usdb/run_long_ci.sh nightly node-restart-p1 --run-only
```

Python 进程组有 10 个测试方法；BH 验收包含导入、回放、核验、封存发布四个中断点。
CI 对 Python/小型 Rust 测试保留选定日志、阶段标记和结果，不上传临时 TLS 密钥或完整
配置。`USDB_TEST_ARTIFACT_DIR` 可指定证据目录；不设置时使用 `/tmp` 中的隔离目录。
Go 场景沿用 P0 summary/timeline/RPC 审计证据，每次恢复后验证新增 100,000 sats 入账和新块执行。
