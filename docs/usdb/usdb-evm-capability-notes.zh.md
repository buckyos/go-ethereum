# USDB 链 EVM 能力说明

[English](./usdb-evm-capability-notes.md) | [中文](./usdb-evm-capability-notes.zh.md)

## 1. 目的

本文用于总结当前这条从 ETHW/go-ethereum 演进而来的 USDB 链，**在执行层实际具备的 EVM 能力边界**。

重点是把以下三件事讲清楚：

- USDB `v1` 当前明确启用了哪些 fork 时代的 EVM 能力；
- 哪些较新的 Ethereum 能力 **目前并不具备**；
- 合约开发者在现阶段应该以什么能力边界为目标，避免部署或运行不兼容。

本文刻意与以下文档分离：

- `usdb-chain-bootstrap-notes.md`
- `usdb-reward-integration.md`
- `usdb-fee-split-integration.md`

因为这里讨论的是 **EVM 能力与合约兼容性**，而不是奖励、手续费分账或冷启动流程。

## 2. 当前 USDB v1 的定位

USDB `v1` 是一条 **从 genesis 启动、始终保持 PoW 的新链**。

目前已经完成了与旧 ETHW 历史迁移语义的主要切割：

- 不再走 Merge / beacon wrapper 执行路径；
- 不再继承 ETHW 的 transition-reset 难度语义；
- 不再继承 ETHW 的 fork 后 `chain id` 切换语义。

在执行层上，当前的实际目标是：

- `LondonBlock = 0`
- `ShanghaiBlock = 0`
- `CancunBlock = nil`

这意味着目前链上实际打算支持的是：

- 作为历史兼容基础保留下来的 pre-London 行为；
- London 时代的执行语义；
- 当前合约实际需要的 Shanghai 子集；
- 但 **不把 Cancun 视为已验证可用的能力目标**。

## 3. 为什么开启 Shanghai

开启 Shanghai 的直接原因，是为了兼容当前 SourceDAO 合约集。

目前 bootstrap 测试中使用的 SourceDAO artifact 由 Solidity `0.8.20` 编译，runtime bytecode 中包含：

- `PUSH0`

而 `PUSH0` 对应的是：

- `EIP-3855`
- Shanghai 执行层能力

如果不开启 Shanghai，那么即使以下条件都正确：

- 系统合约地址正确；
- genesis 预置 code 正确；
- bootstrap 交易顺序正确；

合约仍然会在 opcode 执行阶段直接失败。

所以 USDB 从 genesis 启用 Shanghai，并不是因为“fork 越新越好”，而是因为 **当前合约兼容性明确需要它**。

## 4. 当前真实支持的能力

### 4.1 当前实际生效的 fork 层级

从当前 fork 选择与 VM 路径来看，USDB 已覆盖并继承的能力层大致包括：

- Homestead
- EIP-150 / Tangerine Whistle
- EIP-155 / EIP-158
- Byzantium
- Constantinople / Petersburg
- Istanbul
- Berlin
- London
- Shanghai

对 USDB `v1` 而言，当前**明确启用且实际依赖的最高 fork 层级**是：

- **Shanghai**

### 4.2 当前 VM 中显式接线的 EIP

当前 `core/vm/eips.go` 里明确存在 activator 的 EIP 包括：

- `1344` `CHAINID`
- `1884`
- `2200`
- `2929`
- `3198` `BASEFEE`
- `3529`
- `3855` `PUSH0`

对现阶段合约兼容最关键的一点是：

- `PUSH0` 现在已经可用

### 4.3 Shanghai 的支持边界

USDB 当前已经具备“足以支持现有合约集”的 Shanghai 能力，原因是：

- interpreter 现在会选择 Shanghai jump table；
- Shanghai jump table 已启用 `EIP-3855`；
- chain config 从 genesis 就激活 Shanghai。

这应该理解为：

- **当前 SourceDAO 等现有合约所需的 Shanghai 能力已经具备**

而不应理解为：

- “已经具备完整的最新 Ethereum 执行层能力”。

## 5. 为什么现在不启用 Cancun

虽然 `ChainConfig` 中仍然保留了：

- `CancunBlock`

但这 **不等于当前这条链已经具备完整、可验证的 Cancun 支持**。

从当前代码看，USDB 还没有形成完整审计和测试过的 Cancun 执行路径。

至少从当前 VM activator 列表来看，还**没有看到**大家通常会预期的那批 Cancun 时代执行能力已经显式接线，例如：

- `EIP-1153` transient storage
- `EIP-5656` `MCOPY`
- `EIP-7516` `BLOBBASEFEE`
- 与 `EIP-4844` 相关的 blob 交易执行面

所以这里有一条非常重要的工程原则：

- **配置里存在 fork 字段，不等于该 fork 已经完整支持**

如果 USDB `v1` 现在直接开启 Cancun，会出现一种危险状态：

- 配置层宣称链能力更高；
- 但 VM、交易类型和执行细节并未完全实现或验证。

这比“明确停在一个已验证的较低 fork 层级”风险更大。

## 6. 与最新 Ethereum 的差距

相比一个现代 Ethereum 执行客户端目标，USDB `v1` 目前至少在以下方面存在明确差距：

- 没有已验证的 Cancun 级执行目标；
- 没有 blob transaction 这一层能力目标；
- 不能假定 transient storage 可用；
- 不能假定 `MCOPY` 可用；
- 不能假定 `BLOBBASEFEE` 可用；
- 不具备 merge / beacon 时代那套执行环境前提。

换句话说：

- USDB `v1` **不是** “最新 Ethereum 完整特性链”；
- 它是一个 **为特定用途裁剪后的 PoW 链，且执行层基线是受控的**。

## 7. 当前对合约开发的建议

对当前要部署到 USDB `v1` 的合约，推荐目标应为：

- 与 `Shanghai` 兼容的编译与执行能力

建议遵循以下约束：

- 明确以 `Shanghai` 兼容 bytecode 为目标；
- 不要假设 `Cancun` 能力可用；
- 不要发布依赖以下能力的合约：
  - transient storage
  - blob 相关 opcode 或 fee context
  - 仅在 Cancun 时代才成立的 codegen 假设

对于当前 SourceDAO 合约集：

- Solidity `0.8.20` 是可以接受的，因为链已经支持 `PUSH0`

但更准确的理解仍然应是：

- 如果 `solc 0.8.20` 产物只依赖 Shanghai 时代执行能力，那么可部署；
- 如果合约实际依赖 Cancun 语义，则 **当前还不属于支持范围**。

## 8. 版本策略建议

对 USDB `v1`：

- 执行层目标定为：**Shanghai**
- 不开启 `CancunBlock`
- 把 Cancun 视为未来单独审计与升级的主题

如果未来 USDB 想推进到 `v2` 执行层升级，建议顺序是：

1. 逐项审计所需的 Cancun 时代 EIP；
2. 显式补齐缺失的 VM / runtime 支持；
3. 增加针对合约兼容和 fork 过渡的测试；
4. 在这些前提都满足后，再决定是否开启 `CancunBlock`。

## 9. 当前建议结论

对 USDB `v1`，当前最稳妥的结论仍然是：

- 保持 `ShanghaiBlock = 0`
- 保持 `CancunBlock = nil`
- 对外明确把 USDB 描述为一条 **Shanghai-level PoW chain**，而不是“等同于最新 Ethereum 的链”

这对以下事项都是当前最诚实、最稳妥的基线：

- 奖励集成
- dividend / bootstrap 系统合约
- 后续手续费分账集成
- 给外部合约开发者提供清晰预期

## 10. 后续工作

如果未来确实需要支持 Cancun，建议把它单独作为一个升级主题来推进：

- `USDB EVM Capability Upgrade: Shanghai -> Cancun`

该主题至少应包含：

- opcode / gas rule 审计
- transaction type 审计
- 测试矩阵补充
- 合约兼容性验证
- 明确的 fork 激活设计
