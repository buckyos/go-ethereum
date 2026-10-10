# 节点恢复验收：第一批

本批分为真实进程 regtest 与发布部署两层。前者自动接入 Nightly 的
`node-restart` 分片；后者使用独立、已配置并追平的发布测试节点，手动触发。
不改动正式网络定义、签名校验、就绪门槛，也不使用生产矿工作为故障夹具。

## 四个 P0 场景

| 场景 | 实际注入 | 必须验证 |
|---|---|---|
| 整组重启 | B 节点 Core、Ord、BH、Indexer、geth 全部正常退出，再用原目录启动 | 全部进程更换，配置/nodekey 不变，原历史可查询，静态 peer 自动重连 |
| Core 恢复 | 真正重启 Core，再暂时隐藏最后一个确认块，恢复相同区块 | BH 保留稳定历史，目标降低进入等待；Indexer epoch 不变，不产生永久停机记录；下游 PID 不变 |
| 服务崩溃 | 分别 SIGKILL BH、Indexer；故障期间 A 继续产生 BTC 和 USDB 区块 | B 实际拒绝不可用外部状态，只重启故障服务后自动补齐；历史、余额和执行结果一致 |
| 依赖逆序启动 | 全部停止后先启动 geth、Indexer、BH，再恢复 Core/Ord | 观察真实上游不可用；依赖恢复后下游不需要额外手工重启，继续同步和验证 |

A 为健康矿工，B 为独立全节点，各自有 Core、Ord、BH、Indexer 和 geth 数据库。
使用真实 PoW（仅降低测试 genesis 难度）、真实 RPC/P2P 和真实索引服务；RPC 审计代理仅记录/转发。
沿用现有挖矿夹具，A 在 BTC 锚点推进后重启 geth 以清除旧 sealing work；这不计入 B 的恢复。
对 B 的自动恢复检查只轮询状态，不重启 geth，也不再调用 `admin_addPeer`。
CI 固定的 Ord 0.23.3 会缓存 RPC cookie，本夹具仅对 optional Ord 使用固定的 regtest `rpcauth`；
Core 重启仍会轮换 cookie，BH/Indexer 使用该真实 cookie。Ord 自身认证轮换恢复不在本批 P0 范围内。
每个场景结束后新增一笔 100,000 sats 的矿工证所有者入账，确认后检查余额精确增长，
再产生 USDB 区块并检查 B 实际调用经济状态校验、执行区块、历史系统存储及矿工余额与 A 一致。
同一 BTC 高度上的 BH/Indexer 状态身份、历史事件、余额/能量记录也参与比较。

Core 目标降低使用 `invalidateblock`/`reconsiderblock` 控制**未进入 BH 稳定历史的末端块**。
这是可重复的启动恢复边界模拟，不等同于真实双 chainstate 的 `loadtxoutset` 或磁盘断电。
guard 能读取未改变的 epoch 时返回 0，即使业务 readiness 仍在等待；RPC 不可读才是暂时失败。
已有 `indexer-reorg`、Weekly `upstream-fault-matrix` 和 deep-reorg guard 测试继续覆盖真实历史分叉，
不能以本批“应恢复”测试替代“应停机”的对照测试。

SIGKILL 场景从已核实的持久化检查点中断，故障期间追加工作；本批不声称覆盖每一条
数据库内部写入指令或存储设备断电语义。后续可在现有持久化边界故障钩子上扩展。
逆序启动中的 BH 使用实际部署入口同款 `wait_for_tcp.sh` 等待 Core，再 `exec` 进入服务，
不会由测试脚本循环重启。裸 BH 二进制的启动预检与部署入口的等待行为分开看待。

运行：

```bash
export USDB_REPO_DIR=/absolute/path/to/usdb
export BITCOIN_BIN_DIR=/absolute/path/to/bitcoin/bin
export ORD_BIN=/absolute/path/to/ord
scripts/usdb/run_long_ci.sh nightly node-restart --prepare-only
scripts/usdb/run_long_ci.sh nightly node-restart --run-only
```

编译和场景运行分开计时，测试过程上限 20 分钟。服务只监听回环接口；临时数据目录由
`mktemp` 创建；退出时清理本次启动的进程，保留诊断文件。summary 缺少任意 P0 场景即失败。
证据包括 `summary.json`、阶段 `timeline.jsonl`、进程日志、实际验证 RPC、历史状态快照和区块。

## 实际 `usdb-node down/up` 部署验收

入口为 `tests/node_deployment_restart.py`。它运行**已安装发布包的实际 CLI**，包括真实
Docker 服务、release 校验、正常启动检查和已配置 controller 的后台流程，不替换实现或服务。
要求专用 full 节点预先 READY、已连接 peer，链上有矿工持续出块。首次 BTC 同步/快照准备不在
本次停启的时间预算内。执行账户须预先具有 Docker 和所需非交互 sudo 权限。

1. 按 `tests/common/node-deployment-fixture.example.json` 创建本机私有夹具描述。
   使用实际 hostname、数据目录、版本目录、node.env、nodekey 和保存的 RPC 绑定端口。
   `dedicated_test_node: true` 表示该节点专用于验收，可被停服。
2. 默认只查看计划：

   ```bash
   python3 tests/node_deployment_restart.py \
     --fixture /absolute/path/to/private-fixture.json \
     --output /absolute/path/to/new-result-directory
   ```

3. 执行一次停启：

   ```bash
   python3 tests/node_deployment_restart.py \
     --fixture /absolute/path/to/private-fixture.json \
     --output /absolute/path/to/new-result-directory --execute
   ```

验收先检查主机、目录及 Docker 挂载隔离、节点角色和 RPC 端口归属；保存区块、BH/Indexer
状态身份、epoch 和配置/身份文件哈希；执行 down 并确认服务已停；执行 up；等待两次 READY，
检查真实 RPC、新的已确认 BTC 高度和新的 USDB 区块，再验证历史状态未变化及容器已重建。
只恢复 RPC、使用旧观察结果或没有新业务进展都不能通过。

默认上限 90 分钟，包含停服、恢复和等待新 BTC 确认；无新块或超时返回失败并保留证据。
脚本不会清数据或强杀节点容器。中断只结束 CLI 等待，后台启动/优雅退出可能仍在进行，
需先检查夹具状态再重试。输出不含 node.env、私钥内容或容器环境变量。

CI 入口 `.github/workflows/usdb-deployment-restart.yml`：使用标签
`usdb-deployment-acceptance` 的专用 self-hosted runner，并设置仓库变量
`USDB_DEPLOYMENT_FIXTURE` 指向该描述文件。手动 workflow_dispatch 才执行；未提供夹具不会
退化成 mock 或记录为通过。artifact 保留 14 天。

VM 整机重启、强制断电、fsync/块设备故障、mainnet AssumeUTXO 导入中断留到后续层次。
