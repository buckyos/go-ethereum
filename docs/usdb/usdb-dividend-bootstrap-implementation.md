# USDB Dividend Bootstrap 实现方案

## 1. 目标

本阶段只落最小可运行实现，目标是把 `Dao + Dividend` 冷启动方案接到 `go-ethereum` 的链配置与 bring-up 流程里，为后续手续费分账实现铺路。

第一阶段不直接修改手续费状态转换公式，而是先完成：

- 链配置中的 `DividendAddress`
- 链配置中的 `DividendFeeSplitBlock`
- genesis / bootstrap 对系统合约地址的承载约定
- 对应的配置单测与集成测试骨架

## 2. 设计边界

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

这样合约仍在变动时，不会频繁改变内置 `USDBGenesisHash`。

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
