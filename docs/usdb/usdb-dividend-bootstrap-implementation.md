# USDB Dividend Bootstrap 实现方案

## 1. 目标

本阶段只落最小可运行实现，目标是把 `Dao + Dividend` 冷启动方案接到 `go-ethereum` 的链配置与 bring-up 流程里，为后续手续费分账实现铺路。

第一阶段不直接修改手续费状态转换公式，而是先完成：

- 链配置中的 `DividendAddress`
- 链配置中的 `DividendFeeSplitBlock`
- genesis / bootstrap 对系统合约地址的承载约定
- 对应的配置单测与集成测试骨架

同时，第一阶段要把“测试 helper”再往前推一步，变成开发者可直接使用的固定 bootstrap genesis 生成流程。

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

原因：

- 这两个值会影响交易手续费最终进入哪个地址
- 它们属于状态转换规则的一部分
- 节点间必须一致，不能只放在本地运行配置里

### 2.2 本阶段不纳入共识配置的字段

暂不放入 `ChainConfig`：

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

第一阶段建议：

- 不把 SourceDAO 合约字节码硬编码进内置 `DefaultUSDBGenesisBlock()`
- 先使用外部 genesis JSON 或测试专用 genesis builder 注入 code

这样合约仍在变动时，不会频繁改变内置开发链的 `USDBGenesisHash`。开发期 bootstrap overlay
生成自己的确定性 genesis hash；所有参与同一次测试的节点必须使用同一份生成结果，但该 hash
不要求等于当前内置开发 genesis hash。public network 发布时则必须冻结 spec 和 artifacts，把最终
generated genesis hash 同步绑定到该网络的 `USDBGenesisHash`、chain config 和 release manifest。

### 3.2.1 第一阶段的生成入口

第一阶段推荐的本地 bring-up 流程是：

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
2. 保存 full bootstrap state，并在 node1 上执行 strict validation。
3. 固定 bootstrap 完成高度的 block hash 和 state root。
4. 使用原 datadir 重启 node1，检查固定区块身份和完整合约状态不变。
5. bootstrap 完成后才启动全新 node2，使其从同一 genesis 重放历史。
6. 检查 node2 在固定高度得到相同 block hash/state root，且两端 strict validation 摘要一致。
7. 再次执行 full bootstrap，要求 state 中不存在新的 `completed` 或 `error` operation。

该测试默认使用 fake PoW 和 test-only UIP-0006 indexer fixture，只证明 bootstrap persistence、
historical replay、validator 接线和幂等性，不作为真实 BTC-side state 或 PoW 难度标定证据。

### 3.3 状态转换

文件：

- `/home/bucky/work/go-ethereum/core/state_transition.go`

本阶段只预留接入点，不立即改分账公式。

后续接线原则：

- `if cfg.IsDividendFeeSplit(blockNumber) { ... }`
- 激活前仍走旧逻辑
- 激活后再把目标金额记入 `cfg.DividendAddress`

### 3.4 Bring-up / bootstrap

这部分不建议做成共识代码，而应由测试脚本或部署脚本负责。

建议顺序：

1. 启动使用系统地址 code 的链
2. bootstrap admin 发送：
   - `Dao.initialize()`
   - `Dividend.initialize(cycleMinLength, DaoAddress)`
   - `Dao.setTokenDividendAddress(DividendAddress)`
3. 等到 `DividendFeeSplitBlock`
4. 才开始验证手续费进入 `DividendAddress`

## 4. 第一阶段默认策略

### 4.1 内置 USDB genesis 的默认行为

内置 `USDBChainConfig` 第一阶段建议保持：

- `DividendFeeSplitBlock = nil`
- `DividendAddress = 0x0`

含义：

- 内置链配置先不默认启用手续费分账
- 避免在系统地址和合约代码未最终固定前，把不完整配置变成默认共识行为

开发和联调阶段：

- 使用自定义 genesis / config 覆盖这两个字段

### 4.2 激活块建议

开发阶段可以先用一个很小但大于 bootstrap 所需块数的值，例如：

- `DividendFeeSplitBlock = 16`

这样可以稳定覆盖三种状态：

- 链已启动但未初始化
- 已初始化但未激活 fee split
- 已激活 fee split

## 5. 测试设计

## 5.1 配置单测

文件：

- `/home/bucky/work/go-ethereum/params/config_test.go`

新增测试：

1. `DividendFeeSplitBlock=nil` 时不激活
2. `DividendAddress=0x0` 时不激活
3. 两者都设置后，在阈值块前后返回值正确

## 5.2 Genesis 层测试

位置可放：

- `core/genesis_test.go`

测试目标：

- 测试专用 genesis 可以预置 `DaoAddress` 与 `DividendAddress` 的 code
- `bootstrapAdmin` 初始余额正确

这里先做“状态写入正确”的测试，不必一开始就联 SourceDAO ABI。

## 5.3 Bootstrap 集成测试

建议新建单独 integration/regtest：

1. 启动单节点私链
2. 导入测试 genesis
3. 发送 bootstrap 交易
4. 调用只读方法确认：
   - `Dao` 已初始化
   - `Dividend` 已初始化
   - `Dao.dividend() == DividendAddress`

## 5.4 激活块测试

在 bootstrap 完成后：

1. 激活前发送普通交易
   - `Dividend` 余额不增加
2. 到达激活块后发送普通交易
   - `Dividend` 余额增加

这条测试应在手续费分账逻辑真正接入后补齐。

## 6. 开发顺序

第一批：

1. `ChainConfig` 增加 `DividendAddress` / `DividendFeeSplitBlock`
2. 增加 `IsDividendFeeSplit(...)`
3. 单测覆盖配置行为
4. 文档与 bring-up 约定同步
5. 增加开发期 bootstrap genesis 生成入口

当前第一批完成后，开发者应当可以：

- 从 SourceDAO 本地配置直接导出 `genesis-bootstrap.json`
- 在单机上启动一条带 `Dao` / `Dividend` runtime code 的 USDB 开发链
- 后续再用 bootstrap admin 发送初始化交易

第二批：

1. 测试专用 genesis 清单 / builder
2. 预置系统地址 code 的链级测试
3. bootstrap admin 初始化流程测试

第三批：

1. 手续费分账逻辑接入 `state_transition.go`
2. 激活块前后行为测试
3. 重启一致性测试

## 7. 当前结论

第一阶段最小可落地实现，不是“马上把 fee split 打开”，而是：

- 先把链配置与 cold-start 结构准备好
- 再把 bootstrap bring-up 流程跑通
- 最后接入手续费分账公式

这能把配置正确性、系统合约冷启动、共识状态转换三个风险面分开验证。
