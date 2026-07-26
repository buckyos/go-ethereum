# USDB Chain 手续费分账改造备忘

> 历史设计稿：本文记录 UIP-0010/UIP-0011 落地前的方案比较，不再作为现行实现依据。
> 当前规则是按退款后实际手续费执行 `60% miner / 40% Dividend`，并由 activation、
> `DividendFeeSplitBlock`、冻结 runtime code hash 与
> `Dividend.bootstrapFinalized()` 共同 fail closed。请以 UIP-0010、UIP-0011、
> `docs/usdb/usdb-dividend-bootstrap-implementation.md` 及 `core/state_transition.go`
> 为准。下文 `MinerDAOAddress` 和“第一阶段”内容仅保留为历史记录。

## 1. 需求

当前需要在 USDB chain 侧引入一条新的手续费分账规则：

- 所有交易手续费不再全部归矿工
- 需要按固定比例，把其中一部分划入指定合约地址
- 这个指定地址可以视为分红池 / 奖池 / 协议金库

目标是：

- 尽量以外挂方式实现
- 不大改交易执行主流程
- 仍保持验证节点对状态根的一致重放

## 2. 当前现状

### 2.1 手续费发放位置

当前 USDB chain 分支的手续费发放逻辑主要在：

- `core/state_transition.go`

交易执行结束后，会在 `TransitionDb()` 末尾把手续费记到状态里。

### 2.2 这条分支继承了 legacy ETHW 的手续费特化

当前逻辑并不是完全原始以太坊默认实现。

它已经做了：

- miner 获得 `effectiveTip`
- `gasPrice - effectiveTip` 的另一部分进入 `params.MinerDAOAddress`

也就是说，这条链已经存在“手续费不完全归矿工”的先例。

从改造角度看，这是好事：

- 说明现有代码路径已经接受过手续费定向分配
- 现在更适合把这段逻辑抽象成可配置策略，而不是再硬编码第二套分账

### 2.3 当前真实分账公式

当前代码里的真实公式在 `core/state_transition.go`，可以直接写成：

- `effectiveTip = gasPrice`
- 如果是 London 之后：
  - `effectiveTip = min(gasTipCap, gasFeeCap - baseFee)`

然后手续费分配为：

- `minerFee = gasUsed * effectiveTip`
- 如果已经进入 `EthPoWFork`：
  - `daoFee = gasUsed * max(0, gasPrice - effectiveTip)`

也就是说：

1. miner 先拿走 `gasUsed * effectiveTip`
2. 只有在 `EthPoWFork` 之后，`MinerDAOAddress` 才会拿走剩余部分

在当前主分支配置下，`EthPoWForkBlock` 晚于 `LondonBlock`，所以实际运行时基本可以理解成：

- miner 拿 `priority tip`
- `MinerDAOAddress` 拿 `baseFee` 对应的那部分

换句话说，**当前不是固定比例分账，而是动态分账**：

- miner 占比约等于 `effectiveTip / gasPrice`
- `MinerDAOAddress` 占比约等于 `(gasPrice - effectiveTip) / gasPrice`

### 2.4 当前实现有没有现成说明

目前仓库里并没有一份专门介绍这条 USDB chain 分账规则的正式设计文档。

现有可直接参考的事实来源主要是：

- `core/state_transition.go`
- `params/config.go`
- `core/blockchain_test.go` 里的 EIP-1559 基础测试

因此后续如果这条规则要演进成我们自己的 dividend pool 方案，最好把新的正式公式单独固化成设计文档和测试。

## 3. 关键约束

### 3.1 这是共识行为

手续费怎么分，不只是 miner 本地展示问题，而是状态转换的一部分。

因此：

- 所有节点都必须按同一规则分账
- 不能做成 RPC 层或钱包层的“统计口径”
- 必须在状态变更路径里落账

### 3.2 必须先定义“拆分的基数”

“所有交易手续费按比例拆分”这句话在 London 之后其实有多种解释。

至少有三种口径：

1. 只拆 `effectiveTip`
2. 拆交易支付的总手续费
3. 保留继承代码对 `base fee` / remainder 的处理，只拆 miner 侧那部分

如果这点不先定清，代码很容易改出两层互相叠加的歧义。

## 4. 初步可行方案

## 4.1 方案 A：只拆 miner tip

语义：

- 当前矿工拿到的 `effectiveTip * gasUsed`
- 再按比例拆成：
  - miner
  - dividend pool
- 当前继承代码已有的 `remainGas -> MinerDAOAddress` 保持不变

优点：

- 改动最小
- 不会推翻当前 USDB chain 分支既有 fee 语义
- 最适合先落第一版

缺点：

- “所有手续费都拆分”这个表述不完全严格

这个方案的本质是：

- 保留当前继承代码对 `baseFee -> MinerDAOAddress` 的逻辑
- 只对 miner 当前拿到的 `tip` 做二次拆分

## 4.2 方案 B：把交易总手续费按比例整体拆分

语义：

- 对交易实际支付的整笔 fee 做一次总分账

优点：

- 口径最直观
- 更接近“所有手续费按比例进池子”

缺点：

- 会和当前继承代码已有的 `MinerDAOAddress` 逻辑重叠
- 需要重新定义 London 后 base fee 的处理方式
- 风险高于方案 A

如果采用这个方案，建议直接把当前 `MinerDAOAddress` 逻辑整体替换掉，而不是叠加。

## 4.3 方案 C：保留当前 base-fee 去向，再对矿工侧收益做二次拆分

语义：

- `remainGas` 继续进入当前 `MinerDAOAddress`
- `effectiveTip` 再按比例拆给：
  - miner
  - dividend pool

优点：

- 对现有分支兼容性最好
- 改动面仍然很小

缺点：

- 语义上会出现两类资金池：
  - 现有 `MinerDAOAddress`
  - 新增 dividend pool

如果业务上其实只想保留一个资金池，这个方案会显得绕。

## 4.4 方案 D：直接把 `MinerDAOAddress` 替换成新的 `DividendAddress`

语义：

- 不改当前公式
- 只把原来进入 `MinerDAOAddress` 的金额，改为进入新的 `DividendAddress`

也就是：

- `minerFee = gasUsed * effectiveTip`
- `dividendFee = gasUsed * max(0, gasPrice - effectiveTip)`

优点：

- 改动最小
- 风险最低
- 非常适合作为第一步迁移

缺点：

- 这仍然不是固定比例分账
- 它只是“替换地址”，不是“改成新的比例规则”

因此如果目标真的是“按固定比例拆分”，**只换地址是不够的**。

## 5. 推荐路径

当前更推荐：

- 如果只是想把当前 USDB chain 的 `MinerDAOAddress` 换成我们自己的分红池，优先 `方案 D`
- 如果希望保留继承分支已有经济逻辑，但再从 miner 收益里切一部分进池子，优先 `方案 C`
- 如果希望从现在开始把“给矿工的手续费”抽一部分进池子，且尽量少动旧规则，优先 `方案 A`

不建议第一步直接做 `方案 B`，因为它会把当前分支对 base fee 的处理一起卷进来，讨论面更大。

### 5.1 推荐的两阶段落地方式

如果想降低实施风险，建议分两步：

1. 第一阶段
   - 先采用 `方案 D`
   - 把 `MinerDAOAddress` 替换成我们自己的 `DividendAddress`
   - 先完成“资金池归属切换”
2. 第二阶段
   - 再决定是否从动态分账升级到固定比例分账
   - 如果要升级，再引入显式 `FeeSplitPolicy`

这样可以把：

- “先切换到自己的池子”
- “再升级经济规则”

拆成两个独立变更，降低共识改动风险。

## 6. 推荐实现方式

建议把现有硬编码逻辑抽成一个独立策略，而不是继续在 `state_transition.go` 里堆 `if/else`。

例如新增：

- `FeeSplitPolicy`

输入：

- `blockNumber`
- `gasUsed`
- `gasPrice`
- `effectiveTip`
- `coinbase`

输出：

- `minerAmount`
- `poolAmount`
- `legacyDaoAmount`

如果采用推荐的两阶段路径：

- 第一阶段甚至可以先不引入完整 `FeeSplitPolicy`
- 只需要把 `MinerDAOAddress` 参数化为新的 `DividendAddress`
- 第二阶段再把动态分账抽象成真正的策略对象

这样 `TransitionDb()` 里的主逻辑就仍然很短：

1. 算出 fee 相关基础值
2. 调 `FeeSplitPolicy`
3. 分别 `AddBalance(...)`

## 7. 配置建议

### 7.1 冷启动与分红池地址

SourceDAO / Dividend 这类依赖主合约初始化的冷启动问题，单独整理在：

- `docs/usdb/usdb-dividend-bootstrap-cold-start.md`

这里仅保留结论：

- 不建议依赖“链启动后普通部署再决定地址”
- 第一阶段推荐使用 **系统保留地址 + genesis 预置代码 + bootstrap 初始化交易 + 激活高度**
- 是否从 `block 0` 开始分账，应与合约初始化方式一起设计，不建议和 fee-split 公式讨论混在同一文档里

### 7.2 其他参数

如果后续进入实现，建议把这几个参数显式配置化：

- `activation block`
- `dividend pool address`
- `miner share bps`
- `pool share bps`

并收口到 USDB chain 自己的配置层，而不是继续把地址硬编码在 `params` 里。

## 8. 建议的代码收口位置

建议后续改动主要集中在：

- `core/state_transition.go`
- `params/config.go`
- `eth/ethconfig/config.go`

如果要保持外挂风格，也可以单独新增一个：

- `internal/feesplit`

用于承载：

- 参数结构
- 分账计算
- 激活块判断

## 9. 后续需要单独确认的问题

在真正开始实现前，需要先定清：

1. 到底拆的是 `tip`，还是全部手续费
2. 当前 `MinerDAOAddress` 是保留、替换，还是和新池子并存
3. 系统保留 `DividendAddress` 的具体地址取值
4. 冷启动方案采用“genesis 预置代码 + bootstrap 初始化交易”还是“genesis 直接预置初始化后 storage”
5. 分账比例是否会分叉升级
6. 是否需要补一个专项测试矩阵，覆盖：
   - pre-London
   - post-London
   - USDB chain fee-split activation 前后
   - 不同 fee cap / tip cap 组合

额外需要明确：

7. 第一阶段是否只做“地址替换”
8. 第二阶段是否再升级成固定比例
