# USDB Dividend Bootstrap 实现方案

## 1. 目标

当前开发实现已经把 `Dao + Dividend` 冷启动、共识 readiness 和 UIP-0011
手续费分账接到 `go-ethereum` 的链配置与状态转换。实现包括：

- 链配置中的 `DividendAddress`
- 链配置中的 `DividendFeeSplitBlock`
- 链配置承诺的 `DividendCodeHash`
- genesis / bootstrap 对系统合约地址和 runtime code 的承载约定
- `Dividend.bootstrapFinalized()` 一向 readiness marker
- 激活前全归矿工、激活后 60%/40% 的实际手续费状态转换
- Dividend native balance 与合约内部 ledger 的显式同步测试
- restart、fresh joiner、历史 state root 和幂等重放测试

## 2. 设计边界

### 2.0 EVM 指令前提

当前 SourceDAO artifact 由 Solidity `0.8.20` 生成，runtime bytecode 包含 `PUSH0`。

因此要在 USDB 链上直接预置并运行这套合约，链配置必须至少满足：

- `ShanghaiBlock = 0`

否则即使地址、code、bootstrap 流程都正确，合约初始化交易也会在 opcode 层失败。

### 2.1 本阶段纳入共识配置的字段

放入 `params.ChainConfig`：

- `DividendAddress`
- `DividendFeeSplitBlock`
- `DividendCodeHash`

原因：

- 这两个值会影响交易手续费最终进入哪个地址
- 它们属于状态转换规则的一部分
- 节点间必须一致，不能只放在本地运行配置里

### 2.2 本阶段不纳入共识配置的字段

不放入 `ChainConfig`：

- `DaoAddress`
- `bootstrapAdmin`
- `cycleMinLength`

原因：

- 这些值服务于 bootstrap 运维流程
- 不直接进入手续费分账状态转换公式
- 更适合作为 genesis 清单或初始化脚本输入

## 3. go-ethereum 实现入口

### 3.1 ChainConfig

文件：

- `/home/bucky/work/go-ethereum/params/config.go`

新增：

- `DividendFeeSplitBlock *big.Int`
- `DividendAddress common.Address`

新增 helper：

- `IsDividendFeeSplit(num *big.Int) bool`

建议语义：

- `DividendFeeSplitBlock == nil` 时表示功能未启用
- `DividendAddress == 0x0` 时表示没有合法分账目标，功能视为未启用
- 只有两者都有效时，`IsDividendFeeSplit` 才返回 `true`

### 3.2 Genesis

文件：

- `/home/bucky/work/go-ethereum/core/genesis.go`

现有 `GenesisAlloc` 已经支持：

- `Balance`
- `Code`
- `Nonce`
- `Storage`

因此系统合约冷启动不需要改状态模型，只需要在 genesis 中预置：

- `DaoAddress` 的 runtime code
- `DividendAddress` 的 runtime code
- `bootstrapAdmin` 的初始余额

开发期与 public release 的约束：

- 不把 SourceDAO 合约字节码硬编码进内置 `DefaultUSDBGenesisBlock()`
- 先使用外部 genesis JSON 或测试专用 genesis builder 注入 code

这样合约仍在变动时，不会频繁改变内置开发链的 `USDBGenesisHash`。开发期 bootstrap overlay
生成自己的确定性 genesis hash；所有参与同一次测试的节点必须使用同一份生成结果，但该 hash
不要求等于当前内置开发 genesis hash。public network 发布时则必须冻结 spec 和 artifacts，把最终
generated genesis hash 同步绑定到该网络的 `USDBGenesisHash`、chain config 和 release manifest。

### 3.2.1 Genesis 生成入口

当前开发期推荐的本地 bring-up 流程是：

1. 使用 `geth dumpgenesis --usdb --usdb.bootstrap.config <path> --usdb.bootstrap.artifacts <dir>`
   读取 versioned public genesis spec 和独立的 SourceDAO artifact root。
2. public spec 显式固定：
   - `schemaVersion`
   - DAO / Dividend 地址、artifact 相对路径、artifact SHA-256 和 runtime code hash
   - `bootstrapAdmin.address` 与初始余额
   - genesis / minimum difficulty
   - `dividendFeeSplitBlock`
3. loader 严格校验 schema、地址、数值编码、artifact 边界和两个 code commitment 后，生成固定的
   `genesis-bootstrap.json`。
4. 用 `geth init genesis-bootstrap.json` 初始化本地节点。

bootstrap signer 私钥不属于 genesis spec，也不参与 genesis hash。SourceDAO 初始化脚本必须通过
`SOURCE_DAO_BOOTSTRAP_PRIVATE_KEY` 等 runtime secret 注入 signer，并校验派生地址与
`bootstrapAdmin.address` 一致。

这样做的好处是：

- 保持“生成自动化，但 genesis 结果固定”
- 避免把当前仍在演进的合约代码直接变成内置链身份
- 单节点、多节点、SourceDAO smoke 都能复用同一份开发期 genesis

开发期的最小使用方式：

```bash
./build/bin/geth dumpgenesis \
  --usdb \
  --usdb.bootstrap.config /home/bucky/work/go-ethereum/tools/config/usdb-local-chain.json \
  --usdb.bootstrap.artifacts /home/bucky/work/SourceDAO/artifacts-usdb \
  > /tmp/usdb-bootstrap-genesis.json

./build/bin/geth --datadir /tmp/usdb-node-1 init /tmp/usdb-bootstrap-genesis.json
```

或者直接使用仓库脚本，把这两步与 SourceDAO smoke 串起来：

```bash
./scripts/usdb/run_local_bootstrap_smoke.sh
```

该脚本会：

- 从 `go-ethereum/tools/config/usdb-local-chain.json` 生成 bootstrap genesis
- 初始化一个本地 datadir
- 启动单节点 USDB geth
- 调用 `SourceDAO` 的 `npm run test:usdb:smoke`

该入口默认设置 `USDB_BOOTSTRAP_FAKE_POW=1` 和 `USDB_BOOTSTRAP_USE_MOCK_INDEXER=1`。test-only
indexer fixture 返回一份固定、可由真实 Go builder/verifier 校验的 UIP-0006 profile，因此该 smoke
覆盖 selector/registry/profile 接线，但不验证真实 BTC-side 派生状态或 Ethash seal。它不用于评估
PoW difficulty。

禁用 mock 后必须通过 `USDB_INDEXER_RPC_URL` 和 `USDB_BOOTSTRAP_PASS_ID` 指向完整服务；真实 PoW
场景还需将 `USDB_BOOTSTRAP_FAKE_POW=0`。

默认端口约定：

- RPC: `18545`
- P2P: `31303`

这样可以避免和开发机上常见的 `8545` 本地链冲突。

如果需要本地双节点简单组网，也可以直接使用：

```bash
./scripts/usdb/run_local_two_node_network.sh
```

该脚本会：

- 生成同一份 bootstrap genesis
- 初始化两个独立 datadir
- 启动 node1（出块）与 node2（跟随）
- 通过 `admin_addPeer` 手工连通两个节点

与单节点 bootstrap smoke 一样，该入口默认使用 `--fakepow` 和 test-only indexer fixture，以验证：

- miner 与 validator 使用同一个 UIP-0006 profile
- node2 能校验并接受 node1 生成的 selector/profile 元数据
- 两节点能在相同 genesis 上同步并执行 SourceDAO bootstrap

这不是 PoW difficulty 性能证据。关闭 fixture 时必须提供
`USDB_INDEXER_RPC_URL` 与 `USDB_BOOTSTRAP_PASS_ID`；验证真实 Ethash 时还应设置
`USDB_BOOTSTRAP_FAKE_POW=0`。

默认端口：

- node1 RPC: `18545`
- node1 P2P: `31303`
- node2 RPC: `18546`
- node2 P2P: `31304`

### 3.2.2 Full-bootstrap restart/joiner lifecycle

完整生命周期回归使用独立入口：

```bash
./scripts/usdb/run_local_full_bootstrap_restart_joiner.sh
```

该入口复用双节点脚本和 SourceDAO full bootstrap/strict validator，固定执行：

1. 只启动 node1，并执行完整 SourceDAO bootstrap。
2. 写入 `Dividend.bootstrapFinalized()`，保存 state 并执行 strict validation。
3. 跨过 `DividendFeeSplitBlock`，验证 emission、60%/40% fee 和 Dividend ledger sync。
4. 固定 bootstrap 完成高度的 block hash 和 state root。
5. 使用原 datadir 重启 node1，检查固定区块身份和完整合约状态不变。
6. bootstrap 完成后才启动 `--syncmode full` 的全新 node2，使其从同一 genesis 重放历史。
7. 检查 node2 在固定高度得到相同 block hash/state root，且两端 strict validation 摘要一致。
8. 再次执行 full bootstrap，要求 state 中不存在新的 `completed` 或 `error` operation。

该测试默认使用 1 秒 fake-PoW seal interval 和 test-only UIP-0006 indexer fixture。1 秒下限避免
链上时间快于墙钟而使 fresh joiner 以 `block in the future` 拒绝历史。该入口证明 bootstrap
persistence、reward/fee state transition、historical replay、validator 接线和幂等性，但不作为
真实 BTC-side state 或 PoW 难度标定证据。

### 3.3 状态转换

文件：

- `/home/bucky/work/go-ethereum/core/state_transition.go`

当前实现按每笔交易的退款后实际手续费
`gas_used * effective_gas_price` 分账，包含 base fee 和 tip：

- USDB activation 未启用时保留 legacy ETHW 路径。
- `fee_split_policy_version = 0` 时，实际手续费全部归 `header.Coinbase`。
- policy v1 但高度低于 `DividendFeeSplitBlock` 时，实际手续费仍全部归矿工。
- policy v1 且到达 gate 后，validator 必须同时确认 `DividendAddress`、
  `DividendCodeHash` 和 `Dividend.bootstrapFinalized() == true`；任一不满足即 fail closed。
- readiness 满足后，每笔交易 `40%` 向下取整计入 Dividend，剩余 `60%` 加 rounding
  remainder 计入矿工。
- USDB policy 路径不会再叠加 legacy `MinerDAOAddress` 分账。

readiness slot 固定为：

```text
keccak256("sourcedao.dividend.bootstrap-finalized:v1")
= 0x7d8bb76c5e489191d3f481f0b7ade016df922a8ec91d3eb9c93c07ee5a337054
```

共识层直接增加 Dividend native balance，不执行 Solidity `receive()`。合约 ledger 通过显式
`updateTokenBalance(0)` 同步；同步交易自身产生的新 DAO fee 留作下一轮 pending。

### 3.4 Bring-up / bootstrap

这部分不建议做成共识代码，而应由测试脚本或部署脚本负责。

建议顺序：

1. 启动使用系统地址 code 的链
2. bootstrap admin 发送：
   - `Dao.initialize()`
   - `Dividend.initialize(cycleMinLength, DaoAddress)`
   - `Dao.setTokenDividendAddress(DividendAddress)`
   - 部署并 wiring full scope 的其他模块
   - `Dividend.finalizeBootstrap()`
3. strict validator 同时检查 full wiring 和 `bootstrapFinalized()`
4. 等到 `DividendFeeSplitBlock`
5. 验证手续费进入 `DividendAddress`，并执行一次 ledger sync

## 4. 当前默认策略

### 4.1 内置 USDB genesis 的默认行为

内置 development `USDBChainConfig` 保持：

- `DividendFeeSplitBlock = nil`
- `DividendAddress = 0x0`

含义：

- 内置链配置先不默认启用手续费分账
- 避免在系统地址和合约代码未最终固定前，把不完整配置变成默认共识行为

开发和联调阶段：

- 使用严格 public spec 生成 bootstrap overlay；overlay 绑定 address、height、code hash，
  并把 activation 的 `fee_split_policy_version` 设置为 `1`

### 4.2 激活块建议

当前开发 spec 使用：

- `DividendFeeSplitBlock = 256`

该值只用于开发测试，不是 public network 参数。当前测试覆盖三种实际分账状态：

- 链已启动但未初始化
- 已初始化但未激活 fee split
- 已激活 fee split

## 5. 测试覆盖

## 5.1 配置单测

文件：

- `/home/bucky/work/go-ethereum/params/config_test.go`

当前配置测试覆盖：

1. fee gate 配置必须同时具备正数 `DividendFeeSplitBlock`、非零
   `DividendAddress` 和非零 `DividendCodeHash`。
2. fee policy v1 不得早于 UIP-0010/bootstrap 及 UIP-0011 reward activation。
3. 已生效 gate 的高度、地址或 code hash 变化会触发 `CheckCompatible`。

## 5.2 Genesis 层测试

文件：

- `core/genesis_test.go`

当前测试覆盖：

- bootstrap overlay 预置 `DaoAddress` 与 `DividendAddress` runtime code。
- `bootstrapAdmin` 初始余额正确。
- `DividendCodeHash` 从冻结 artifact 派生并绑定到 chain config。
- issued supply 与 UIP-0011 至 UIP-0013 reserved system slots 初始化正确。

## 5.3 Bootstrap 集成测试

当前 SourceDAO 与 geth integration/regtest 覆盖：

1. 启动单节点私链并导入冻结 bootstrap genesis。
2. 执行 full bootstrap 和 `Dividend.finalizeBootstrap()`。
3. strict validation 确认：
   - `Dao` 已初始化
   - `Dividend` 已初始化
   - `Dao.dividend() == DividendAddress`
   - runtime code hash 与 finalized marker 均匹配

## 5.4 激活块测试

在 bootstrap 完成后：

1. 激活前发送普通交易，实际手续费全部归 miner。
2. 到达激活块后但 readiness 不满足时，区块执行 fail closed。
3. readiness 满足后发送普通交易，实际手续费按 `60% / 40%` 分账。

`scripts/usdb/run_local_full_bootstrap_restart_joiner.sh` 已执行该测试，并额外校验：

- sender 扣款等于退款后实际手续费。
- reward recipient 增量等于 UIP-0011 emission 加 miner fee。
- Dividend 增量等于 DAO fee。
- ledger sync 吸收同步前 pending，且同步交易的新 DAO fee 成为下一轮 pending。

## 6. 开发顺序

已完成：

1. ChainConfig、genesis public spec、artifact/code commitment。
2. DAO/Dividend direct predeploy 和 full bootstrap。
3. 单向 readiness marker 与 strict validator。
4. UIP-0011 fee policy v1 状态转换和负向测试。
5. fee/ledger、restart、fresh joiner 和幂等 live E2E。

public release 前仍需冻结最终地址、artifact、genesis hash、activation height、manifest
signature 和 bootstrap-admin custody。

## 7. 当前结论

开发实现已经完成从 chain config、cold start、on-chain readiness 到 fee state transition 的闭环。
本地 marker 和 RPC 健康状态不参与共识；validator 只信任 chain config、Dividend runtime code 和
state-root 承诺的 finalized slot。剩余工作属于 public release 参数冻结和更大规模网络演练。
