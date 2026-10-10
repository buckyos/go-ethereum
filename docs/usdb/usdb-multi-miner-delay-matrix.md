# 多矿工独立上游延迟验收

本矩阵验收“已提交高度可以验证”和 Go 延迟导入机制的组合行为。每个节点都有独立
Bitcoin Core、balance-history、indexer 数据库，BTC 节点通过 regtest P2P 同步同一条链。
两名矿工使用不同 BTC owner、不同 USDB 地址和两张真实 commit/reveal 铸造的 pass；
第三个节点作为晚加入的 full validator。测试不接触已有节点或数据目录。

## 场景和判定

| 场景 | 必须观察到的行为 |
| --- | --- |
| 基线 | 两名矿工分别出块、获得收益；独立重放的上游状态一致 |
| BH 落后两个块 | 真实同步读取被暂停，历史 profile 与健康节点一致；落后矿工仍可出块 |
| gossip 连续子块 | 新 anchor 尚不可验证时保留等待；恢复后自动导入目标块及其后继 |
| indexer 单独落后 | BH 已追平、indexer RPC 仍可查询历史；不能以整个上游离线替代 |
| 晚加入 full 同步 | downloader 实际进入等待/重试，已提交历史前缀仍可导入 |
| RPC 中断 | 杀死一个 indexer，观察真实连接失败；从同一数据库重启它后 chain 自动恢复 |
| 同时出块 | 两名矿工均封出块，按正常累计难度和后继块完成分支选择 |
| 不同 anchor 的竞争分支 | 落后矿工在旧高度上生成分支；恢复后切换到更高工作量的规范链，核对被替换块 |
| anchor 使用耗尽 | 达到测试 genesis 的 24 次 age 上限后不再出块；新的 BTC 状态可用后无需重启 mining 自动继续 |
| 真正无效区块 | 经 `admin_importChain` 导入 gasUsed 超过 gasLimit 的区块仍被拒绝、记入 bad-block，且之后合法块仍可导入 |

BH/indexer 延迟通过透明的 Core RPC 代理暂停特定 `getblock` 请求实现；代理不伪造
成功响应，不改高度、hash、profile 或 readiness。历史查询继续由各自真实数据库回答。
恢复前后核对 chain PID、Linux process start time 和启动次数；测试脚本只在初始连接时
调用 `admin_addPeer`，恢复过程不重连 peer、不重启 chain、不调用 `miner_start` 帮助恢复。
为取得可比较的目标链，短时间出块结束后会停止生产者；已封好的在途块可以继续抵达。
判定以目标高度的规范 hash 为准，允许当前 head 已前进到其合法后继。

每个恢复场景必须在 90 秒内完成，必须存在同一 selector 查询先失败、后成功的审计记录。
全程检查 peer 断连日志和坏块记录：上游延迟不得生成坏块，只有最后显式注入的非法块允许
出现在 bad-block 中。最终比较完整规范区块序列、state/receipts/transactions roots、
历史系统合约存储、两名矿工的历史余额，以及上游 pass/energy/owner/history 状态。

## 本地与 CI

```bash
# 准备 canonical Go、Cargo、Bitcoin Core 28.1 和 Ord 0.23.3 后执行：
MATRIX_SCENARIO=multi-miner \
MATRIX_WORK_ROOT=/tmp/usdb-multi-miner \
BITCOIN_BIN_DIR=/path/to/bitcoin/bin ORD_BIN=/path/to/ord \
bash scripts/usdb/run_usdb_upstream_fault_matrix.sh

# 与 CI 相同的入口；分别为一轮和连续三轮：
scripts/usdb/run_long_ci.sh nightly multi-miner-delay
scripts/usdb/run_long_ci.sh weekly multi-miner-soak
```

`MATRIX_PORT_BASE` 默认 22400；每个节点使用一个 20 端口区间，包括 Core 隐式 onion
监听端口和测试代理端口。每次新建 `run-*` 目录，不复用上次测试库。
`MATRIX_CYCLES` 支持 1–5；CI 固定 nightly 1、weekly 3，weekly 在同一批节点和数据库上
连续执行，不以三个独立短跑代替。构建在模拟预算之前完成；默认模拟上限 20 分钟，
weekly workflow 另留清理和上传余量。`MATRIX_SKIP_BUILD=1` 要求 geth、两个 Rust 服务和
`invalidblock` 测试工具均已准备，不隐式使用未知旧二进制。

报告位于 `MATRIX_OUTPUT_DIR`，长 CI 默认归档 `multi-miner-delay/summary.json`，包括
每个故障的已提交高度、readiness、查询失败次数、恢复耗时、进程身份、分叉块 hash 和
最终规范链。`*-rpc.jsonl` 是实际 profile 调用审计，`*-delay.jsonl` 证明同步读取确实被延迟，
`canonical-blocks.json`、`final-*-state.json` 和服务日志用于复查失败。退出时释放代理等待，
停止本次创建的进程；保留临时数据库和诊断文件。

## 边界

- 使用 `--fakepow --fakepow.delay 1200ms` 压缩并稳定封块时间；仍执行 selector、经济规则、
  fork choice、奖励和状态根校验。这不是实际 Ethash 算力或互联网传播延迟基准，不能据此
  确定生产环境 gap 应设为 1 还是 2。
- 本批同时修复 delayed fake PoW 的同步等待：延迟工作改为异步并响应取消，使 miner 的
  task loop 能及时接收新任务或退出，避免人为排队旧封块结果。普通 PoW 路径保持原有行为；
  延迟、非阻塞返回和取消回归加入 Fast 必跑清单。
- 非法块场景覆盖真实执行导入；非法 gossip、light/snap 路径和队列配额、超时、停机取消由
  Fast Go 回归覆盖。长时间断网、队列到期和跨多机网络测量不由本矩阵代替。
- BTC 深重组及中断恢复由既有 `weekly upstream-fault-matrix` 覆盖。本矩阵不更改协议、
  生产 genesis、registry、数据库格式或默认挖矿 gap。

## 本地验收记录

2026-10-10：单轮 14 项、连续三轮 34 项全部通过；最终规范高度分别为 68、136，
完整历史执行状态、收益和上游状态一致。16 次恢复均在约 1.3–20.1 秒内完成，
没有 chain 重启、人工重连或 peer 断连，两个 runner 清理成功并以 0 退出。
该记录来自隔离环境；远端 Nightly / Weekly 及真实节点升级仍需以对应运行的证据为准。
