# USDB Dividend 冷启动与 Bootstrap 设计

## 1. 背景

USDB 新链需要把一部分交易手续费划入 `Dividend` 分红池合约。

当前工作区引入的 SourceDAO 合约中：

- `Dao` 是主合约
- `Dividend` 是手续费归集与分红相关合约

对应文件：

- `/home/bucky/work/SourceDAO/contracts/Dao.sol`
- `/home/bucky/work/SourceDAO/contracts/Dividend.sol`

这类结构与“只向一个固定收款地址转账”的简单分账不同，因为 `Dividend` 并不是一个零依赖的收款账户，而是一个需要初始化并依赖主合约地址的系统模块。

## 2. 问题本质

如果手续费分账目标地址要指向 `Dividend` 合约，会遇到一个冷启动问题：

- 共识规则需要从链启动开始就知道手续费该打到哪个地址
- 但 `Dividend` 又依赖 `Dao` 和初始化流程才能安全工作

如果直接采用“链先启动，后续再部署合约决定地址”的模式，会出现循环依赖：

1. 共识需要先知道 `DividendAddress`
2. 运行时部署才能拿到最终地址
3. 但运行时部署本身又依赖链已经启动和出块

因此 `DividendAddress` 不能依赖运行时普通部署来决定。

## 3. 当前 SourceDAO 合约约束

### 3.1 Dividend 不能在未初始化状态下直接承接分账

`DividendContract` 需要显式调用：

- `initialize(uint256 cycleMinLength, address mainAddr)`

它会完成：

- 设置 `cycleMinLength`
- 设置 `mainContractAddress`
- 初始化 `cycles[0].startBlocktime`

在未初始化状态下：

- `cycleMinLength == 0`
- `mainContractAddress` 未设置
- `receive()` 内部的 cycle 轮转逻辑不可靠

因此，**不应从 block 0 就把手续费直接打入一个尚未初始化的 Dividend 实例**。

### 3.2 Dao 也需要 bootstrap 初始化

`SourceDao.initialize()` 会：

- 设置 `mainContractAddress = address(this)`
- 设置 `bootstrapAdmin = msg.sender`

之后还需要由 `bootstrapAdmin` 完成模块地址注入，例如：

- `setTokenDividendAddress(dividendAddr)`

并且这些 setter 要求目标地址已经有 `code.length > 0`。

这说明：

- `Dao`
- `Dividend`

都更适合以“固定地址 + 已知代码 + bootstrap 交易”方式接入，而不是链启动后再动态决定地址。

## 4. 推荐的 v1 冷启动方案

### 4.1 总体原则

v1 推荐采用：

- **系统保留地址**
- **genesis 预置代码**
- **bootstrap 初始化交易**
- **手续费分账激活高度**

这比“block 0 直接开始向未初始化合约分账”更稳，也比“链起来后再临时部署合约”更清晰。

### 4.2 地址规划

在链配置中预留固定地址，例如：

- `DaoAddress`
- `DividendAddress`

这些地址：

- 不作为普通用户地址使用
- 从协议层视为系统模块地址
- 后续尽量保持不变

### 4.3 Genesis 预置内容

genesis 中建议至少预置：

- `DaoAddress` 的 runtime code
- `DividendAddress` 的 runtime code

是否需要在 genesis 中直接预置初始化后的 storage，v1 不建议优先采用。原因是：

- upgradeable / initializer 合约的 storage 形态更脆弱
- 直接写 storage 的审计成本更高
- 对 bring-up 和调试不友好

因此 v1 更推荐：

- 只预置代码
- 初始化通过 bootstrap 交易完成

### 4.4 开发期与正式网的 genesis 形态

冷启动方案需要区分两个阶段：

- 开发 / 测试阶段
  - 不直接把 `Dao` / `Dividend` 的 runtime code 永久硬编码进内置 `DefaultUSDBGenesisBlock()`
  - 先由 `go-ethereum` 提供一个 genesis 生成入口，基于 SourceDAO artifact 和本地 manifest 生成一份固定的 `genesis-bootstrap.json`
  - 本地单节点、多节点联调、SourceDAO smoke 都统一使用这份 canonical 开发期 genesis
- 正式网络阶段
  - 等 `DaoAddress` / `DividendAddress` / runtime code / `DividendFeeSplitBlock` 冻结后，再确定正式网的 canonical genesis
  - 这时可以继续以外部 `genesis.json` 为准，也可以同步内置到代码里，但事实来源仍应是一份固定、可审计的 genesis 文件

这里的关键点是：

- 生成过程可以自动化
- 生成结果必须固定
- 不能让节点在运行时根据松散配置各自动态装配 genesis

### 4.5 Bootstrap 交易顺序

启动后由一个 genesis 预置的小额 bootstrap admin 账户发送初始化交易。

建议顺序：

1. 调用 `Dao.initialize()`
2. 调用 `Dividend.initialize(cycleMinLength, DaoAddress)`
3. 调用 `Dao.setTokenDividendAddress(DividendAddress)`

如果后续还要接其他模块，再继续：

- `setDevTokenAddress(...)`
- `setNormalTokenAddress(...)`
- `setCommitteeAddress(...)`
- `setProjectAddress(...)`
- `setTokenLockupAddress(...)`
- `setAcquiredAddress(...)`

但这不应该阻塞手续费分账功能本身的冷启动设计。

### 4.6 激活高度

手续费分账建议增加一个独立激活高度，例如：

- `DividendFeeSplitBlock`

在该高度之前：

- 手续费仍走旧逻辑
- 或全部归矿工

在该高度之后：

- 手续费才开始打到 `DividendAddress`

这样做的意义是：

- 避免合约未初始化时就开始收款
- 把“链启动”和“分账功能启用”拆成两个阶段
- 更便于回归测试和问题定位

### 4.7 后续新节点的加入方式

带系统合约的 bootstrap genesis 只是在网络定义阶段“确定一次”，不是每个新节点都要重新做一遍部署。

更准确地说：

- 后续新节点仍必须使用同一份 bootstrap genesis
  - 因为它们需要认同相同的 genesis hash
- 但它们不需要重新执行 bootstrap 运维动作
  - `Dao.initialize()`
  - `Dividend.initialize(...)`
  - `Dao.setTokenDividendAddress(...)`
  这些都会作为早期链上交易被同步和重放

因此：

- genesis 预置 code 是网络身份的一部分
- bootstrap 初始化交易只需要在网络早期实际执行一次

## 5. 为什么不推荐其他方案

### 5.1 不推荐“普通地址先收款，后续再升级成合约”

原因：

- EOA 不是一种可以平滑“升级”为合约的稳定系统方案
- 未来若要复用同地址，需要额外设计部署方式
- 容易把系统地址和普通用户地址语义混在一起

### 5.2 不推荐“运行时部署后再确定地址”

原因：

- 会形成冷启动循环依赖
- 节点启动时无法先验知道共识目标地址
- 运维流程和链配置会产生耦合

### 5.3 不推荐 v1 就直接预置初始化后的复杂 storage

原因：

- 需要精确控制 upgradeable 合约的 storage 布局
- 调试复杂
- 一旦初始化字段设计变化，genesis 也要联动重做

这条路线可以作为后续优化，但不应是 v1 首选。

## 6. 建议的测试方式

## 6.1 合约级测试

这部分 SourceDAO 已基本具备。

继续重点确认：

- `Dao.initialize()` 成功
- `Dividend.initialize(...)` 成功
- `Dao.setTokenDividendAddress(...)` 成功
- 初始化后向 `Dividend` 转入原生币不会异常

## 6.2 链级 bootstrap 集成测试

在 USDB 链侧增加一条专项测试：

1. 使用带系统地址代码的 genesis 启动单节点
2. 用 bootstrap admin 发送初始化交易
3. 验证：
   - `Dao` 已初始化
   - `Dividend` 已初始化
   - `Dao.dividend()` 指向 `DividendAddress`

如果采用激活高度：

4. 在激活高度前发送普通交易
   - 验证 `Dividend` 余额不增加
5. 到达激活高度后发送普通交易
   - 验证 `Dividend` 收到分账

## 6.3 重启一致性测试

再补一条恢复类测试：

1. 完成 bootstrap
2. 让链进入 fee-split 激活后运行若干块
3. 重启节点
4. 再发送交易
5. 验证手续费仍正确进入 `DividendAddress`

这可以保证：

- genesis 预置代码
- bootstrap 初始化状态
- fee-split 激活判断

在重启后仍保持一致。

## 7. v1 推荐结论

对于当前 SourceDAO `Dao + Dividend` 架构，v1 建议采用：

- 固定系统保留地址
- genesis 预置 `Dao` / `Dividend` 代码
- bootstrap admin 在前几块完成初始化
- 通过 `DividendFeeSplitBlock` 在 bootstrap 完成后再启用手续费分账

这是目前最稳、最容易审计、也最适合先落地的冷启动方式。

## 8. 后续可选演进

后续如果确实希望从 `block 0` 就开始分账，可以再评估：

- genesis 直接预置初始化后的 storage
- 或更复杂的系统合约固化方案

但这应在 v1 跑通后再考虑，而不是作为第一阶段前置条件。
