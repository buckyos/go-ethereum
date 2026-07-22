# USDB × ETHW 矿工奖励对接备忘

> 历史设计稿：本文记录 UIP 拆分前的 reward-only 方案，不再作为现行协议或实现依据。
> 当前 header selector、economic profile 和 chain-config 边界分别以 UIP-0006、
> UIP-0007、UIP-0008/UIP-0009 及 `internal/usdb` 实现为准。

当前实现状态：

- header 使用 107-byte `ProfileSelectorPayloadV1`，而非本文的旧 reward payload；
- chain config 使用按 ETHW block 生效的完整 activation registry；
- miner 和 validator 均通过同一份历史 `get_pass_economic_profile` 解析结果计算实际难度；
- level/reward multiplier mock 已删除；在 UIP-0011 激活前仍沿用既有 Ethash 静态奖励。

## 1. 需求

当前需要在 ETHW 侧引入一条新的奖励逻辑：

- 矿工出块时，不再只拿当前链上固定的静态区块奖励
- 还要根据 USDB / BTC 侧矿工证系统中的 `energy` 和 `level`
- 计算该块应得的实际奖励

目标不是简单做一个“矿工本地多发钱”的逻辑，而是让这套奖励规则成为 ETHW 节点都能一致验证的共识规则。

## 2. 当前现状

### 2.1 ETHW 当前奖励发放位置

在当前 `go-ethereum` 分支里，区块奖励仍然集中在：

- `consensus/ethash/consensus.go`

关键路径是：

- `Ethash.Finalize(...)`
- `accumulateRewards(...)`

这里目前还是传统的静态 block reward 逻辑：

- 根据分叉高度选择 `5 / 3 / 2 ETH`
- 对 miner 和 uncle 做标准奖励分配
- 最终把奖励直接加到 `header.Coinbase`

也就是说，当前 ETHW 还没有接入任何来自 USDB 的奖励决定因素。

### 2.1.1 当前 static reward 的具体含义

按当前代码，ETHW 这条分支在现阶段实际运行时，矿工主奖励可以近似理解为：

- `staticBlockReward = 2 ETH`

此外还存在 uncle 相关的两类奖励：

- uncle 自己的奖励
- 当前出块矿工因为打包 uncle 获得的 inclusion bonus

因此“当前 static reward”更准确地说是：

- miner 主奖励：`2 ETH`
- 外加可能存在的 uncle 额外奖励

### 2.2 USDB 当前可提供的能力

根据现有 `usdb-indexer` 设计和测试体系，BTC 侧已经具备：

- 历史 `state ref`
- `snapshot_id`
- `system_state_id`
- `get_pass_snapshot`
- `get_pass_energy`
- 带 `ConsensusQueryContext` 的历史回放校验

这意味着 ETHW 如果要对接 USDB，已经有能力在“高度 `H` 的历史上下文”下重建：

- 某张 pass 的 owner / state
- 某张 pass 的 historical energy
- 对应的 BTC 外部状态引用

### 2.3 当前缺口

当前 USDB 侧并没有看到一个已经稳定对外暴露的一等 `level` 概念。

也就是说，奖励规则如果显式依赖：

- `energy`
- `level`

那么 `level` 需要额外定义来源：

1. 在 ETHW 侧根据 `energy` 本地确定性推导
2. 在 USDB 侧补一层 `level` 计算与 RPC

当前建议先采用第 1 种做法：

- 第一版先在 ETHW 本地用 `level = f(energy)` 的 mock 规则推导
- 后续再把 `level` 切换到由 USDB 对外返回

## 3. 关键约束

### 3.1 这是共识规则，不是 miner 本地策略

一旦奖励金额依赖 USDB 状态，这件事就不再只是 miner 的出块辅助，而是区块有效性的一部分。

这意味着：

- 矿工必须能算出奖励
- 验证节点也必须能重算出同一个奖励
- 否则相同区块会在不同节点上得到不同状态根

所以不能做成：

- miner 本地查当前 USDB 状态
- 然后直接给自己多发奖励

验证侧必须有一条可以重放的历史输入链路。

### 3.2 奖励计算必须绑定历史 USDB 状态，而不是当前状态

ETHW validator 在未来重放校验某个旧块时，不能查“当前 USDB 状态”，必须查：

- 该块声明的 BTC 高度
- 对应的 `snapshot_id`
- 对应的 `system_state_id`
- 对应的 pass 标识

然后按这份历史上下文重建奖励输入。

### 3.3 尽量减少侵入式修改

当前改造原则是：

- 尽量外挂
- 避免大范围改 header / tx / state transition 主流程
- 尽量把新逻辑收口到独立策略层

## 4. 初步可行方案

## 4.1 方案 A：使用 header extraData 携带 USDB 奖励 payload

思路：

- miner 出块时，把 USDB 历史状态引用编码进 `header.Extra`
- validator 导入块时，从 `header.Extra` 解析出这份 payload
- 本地按 payload 给出的历史上下文查询 USDB
- 重算出该块应得奖励

payload 最小可包含：

- `version`
- `btc_height`
- `snapshot_id`
- `system_state_id`
- `pass_id`

优点：

- 不需要新增交易类型
- 不需要在 block body 里塞特殊交易
- 改动点主要集中在 miner header 构造和 consensus finalize

缺点：

- 当前 ETHW 对 `extraData` 限制是 32 bytes
- 如果要承载完整 payload，需要放宽上限

这是当前最符合“外挂式、少侵入”的方案。

## 4.2 方案 B：使用 block body 中的保留 metadata tx

思路：

- 约定一笔特殊交易作为 USDB 奖励声明载体
- validator 导入块时解析该交易
- 再按历史上下文去 USDB 校验

优点：

- 不需要改变 header `extraData` 长度上限
- payload 空间更宽松

缺点：

- miner 需要额外插入特殊交易
- validator 需要解析并验证该交易格式
- 整体比 `extraData` 更绕

如果后续 payload 变得很大，这个方案才更值得考虑。

## 4.3 方案 C：完全不在块内携带历史引用，只依赖本地当前 USDB 查询

这个方案不建议使用。

原因：

- 验证节点无法稳定重放历史奖励输入
- head 前进、reorg、restart 后会产生不一致
- 本质上不是共识可验证方案

## 5. 推荐路径

当前推荐方案是：

### 5.1 第一阶段

使用 **方案 A：`header.Extra` 携带奖励 payload**。

需要做的事：

1. 扩大 ETHW `extraData` 上限
2. 定义 `UsdbRewardPayloadV1`
3. miner 在出块时构造 payload
4. validator 在导入块时解析 payload
5. `Finalize` 中按历史上下文查询 USDB 并计算奖励

### 5.1.1 第一版奖励规则建议

第一版建议不要完全替换当前 static reward，而是拆成两层：

1. `baseReward(height)`
   - 纯链级货币政策
   - 由 ETHW 自己的 fork / schedule 决定
   - 可以后续演进成类似：
     - 初始 `10 ETH`
     - 到某个高度后减半
     - 继续按高度衰减
     - 最终收敛到某个下限
2. `rewardMultiplier(level)`
   - 纯 USDB / pass 规则
   - 由历史 `energy -> level -> multiplier` 路径决定

第一版推荐最终矿工主奖励定义成：

- `adjustedMinerBaseReward = baseReward(height) * rewardMultiplier(level) / 10000`

这样做的好处是：

- 链的货币发行规则和 USDB 规则解耦
- base reward 的升级可以独立通过 fork 调整
- pass 等级只影响矿工主奖励，不会一开始就把 uncle 规则一起改复杂

当前建议第一版先只让 multiplier 作用于：

- miner 主奖励

而暂时不把它扩到：

- uncle 自身奖励
- uncle inclusion bonus

也就是说，第一版更推荐：

- `finalMinerPayout = adjustedMinerBaseReward + uncleInclusionBonus`
- `uncleReward` 继续保持现有公式

这样第一版的共识规则更简单，也更接近“先在现有 Ethash 奖励上外挂 pass 系数”。

### 5.2 奖励公式建议

奖励公式本身建议放在 ETHW 代码里，不放在 USDB。

也就是：

- USDB 提供历史状态事实
- ETHW 提供奖励规则版本与计算逻辑

这样更容易保证：

- 共识可审计
- 版本切换可控
- 不把经济规则分散到两个系统里

### 5.2.1 `level` 的第一版策略

对 `level` 的当前结论是：

- validator / miner 都强依赖本地 USDB 服务
- 但第一版 payload 不直接写 `level`
- 第一版先由 ETHW 在本地根据历史 `energy` 推导 `level`
- 后续当 USDB 对外返回 `level` 时，再把本地 mock 替换掉

因此第一版推荐做法是：

- payload 只锁定历史状态引用和 `pass_id`
- `energy`、`level`、最终 reward 都在 validator 本地重算

### 5.3 `level` 建议

当前更建议：

- 先在 ETHW 本地定义 `level = f(energy)`

而不是第一步就要求 USDB 扩 RPC。

这样接入面更小，耦合也更低。

### 5.3.1 第一阶段 `level` mock 公式

第一阶段开发可以先固定使用几何级数阈值模型：

- `E(L) = E0 * (q^L - 1) / (q - 1)`

当前讨论版参数：

- `E0 = 1_000_000`
- `q = 1.18`

按 energy 反解 level：

- `level(energy) = floor(log_q(1 + (q - 1) * energy / E0))`

这意味着：

- `energy` 是历史查询得到的共识输入
- `level` 是 ETHW 本地根据统一公式推导出的确定性派生值

后续如果 USDB 正式对外返回 `level`，第一版实现可以再把这层 mock 替换掉，但在接口和 payload 上不必做破坏性变化。

### 5.3.2 第一阶段 multiplier 曲线

第一阶段可以先把奖励倍数定义成按 `level` 在线性区间内变化：

- `level` 范围：`1 .. 50`
- 倍数范围：`[M, N]`
- 测试阶段参数：
  - `M = 0.5`
  - `N = 2.0`

也就是：

- 最低奖励：`baseReward * 0.5`
- 最高奖励：`baseReward * 2.0`
- 中间 level 按线性插值增长

这样做的好处是：

- 第一版实现简单
- 后续只需调整常量或切换到新的规则版本
- 不影响 payload 结构

### 5.4 validator / miner 对 USDB 的依赖结论

当前结论是：

- ETHW miner 强依赖本地 USDB 服务
- ETHW validator 也强依赖本地 USDB 服务

也就是说：

- 没有 USDB，miner 不能正确构造奖励 payload
- 没有 USDB，validator 也不能独立完成奖励校验

因此后续设计应把 USDB 视为：

- 共识验证依赖的本地 companion service

而不是一个“可选远程查询服务”。

这也意味着：

- USDB 不 ready 时，奖励校验必须 fail-closed
- 不能在缺少 USDB 的情况下继续容忍新区块导入

### 5.5 第一版 payload 建议

为了做强一致历史校验，第一版 payload 建议只保存“固定历史状态引用”的最小集合。

推荐字段：

- `payload_version`
- `btc_height`
- `snapshot_id`
- `system_state_id`
- `pass_id`

可选增强字段：

- `stable_block_hash`

第一版不建议直接放进 payload 的字段：

- `energy`
- `level`
- `reward`
- `owner`
- `state`

这些值都应由 validator 按历史 context 本地重算，而不是信任块里抄写的副本。

这里有两个容易混淆的点需要单独说明：

1. `snapshot_id` 是否冗余
   - 从哈希依赖上说，`system_state_id` 已经包含 `upstream_snapshot_id`
   - 所以纯理论上，只靠 `system_state_id` 也足以锁定上游 snapshot
   - 但第一版仍建议保留 `snapshot_id`
   - 原因是它能提供更直接的交叉校验和排错信息，尤其是在区分：
     - 上游 snapshot 变化
     - 本地 system state 变化
2. `pass_id` 是否可以省略
   - 当前不建议省略
   - `pass_id` 不应依赖 `coinbase ETH address + system_state` 这类隐式推导
   - 原因是：
     - 当前 USDB 对外稳定查询主键仍是 `pass_id / inscription_id`
     - `eth_main` / `eth_collab` 只是 pass 内容字段，不是稳定唯一反查键
     - 多 pass / candidate-set 场景下，按 ETH 地址隐式选 pass 会产生歧义
   - 因此第一版更推荐显式携带 `pass_id`
   - 但编码上应尽量紧凑，例如使用固定长度的 outpoint 二进制编码，而不是直接放可变长字符串

### 5.6 `header.Extra` 长度建议

如果第一版 payload 使用紧凑二进制编码，而不是直接存 JSON / hex 字符串，则长度大致可控制在：

- `payload_version`：`1B`
- `btc_height`：`4B`
- `snapshot_id`：`32B`
- `system_state_id`：`32B`
- `pass_id`：约 `36B`（固定长度 outpoint 编码）
- `stable_block_hash`：`32B`（可选）

也就是说：

- 不带 `stable_block_hash`：约 `105B`
- 带 `stable_block_hash`：约 `137B`

因此当前建议：

- 把 `header.Extra` 上限从 `32` 提高到 `160 bytes`

这样既够第一版使用，也给后续小幅扩展留出余量。

### 5.7 版本策略建议

这里建议把两类版本分开：

1. `payload_version`
   - 写进 payload
   - 只用于描述 payload 的编码 / 结构版本
2. `reward rule version`
   - 不直接写进 payload
   - 由链上 fork 高度和 `ChainConfig` 推导

推荐第一版做法：

- `payload_version = 1`
- `reward_rule_version = UsdbV1`
- `UsdbV1` 从 genesis 固定生效

当前这是一条全新的 USDB 链，不考虑兼容已经运行中的 ETHW 链，因此第一版不需要再单独引入一个“从某个高度才开始生效”的切换块高。

也就是说：

- 第一版奖励规则从 `block 0 / genesis` 就固定启用
- 第一版可以把 `UsdbV1Block` 视为概念上的版本名，而不是必须存在的运行时 fork 开关
- 如果未来只升级奖励公式，但 payload 结构不变
  - 不必修改 `payload_version`
  - 可以在未来引入 `UsdbV2Block / UsdbV3Block`
- 只有 payload 本身编码格式变化
  - 才升级 `payload_version`

## 6. 建议的代码收口位置

如果后续开始实现，建议把逻辑收口成独立模块，而不是散落在 `core` / `miner` / `consensus`。

当前更推荐直接使用：

- `internal/usdb`

而不是顶层同级的：

- `usdb/`

原因是：

- 这套逻辑当前是 USDB 为 ETHW / USDB 链提供的内部共识扩展
- 它需要被 `miner`、`consensus`、`cmd/geth` 等仓库内部代码共同使用
- 但它还不是一个需要对外稳定暴露的公共 Go SDK

放在 `internal/usdb` 的好处是：

- 名字足够直接，能清楚表达这是 USDB 相关扩展
- 仓库内部任意需要的模块都可以 import
- 同时不会过早承诺顶层公共包的兼容性边界

内部拆成：

- `payload`：编码 / 解码 `UsdbRewardPayloadV1`
- `client`：USDB RPC client
- `policy`：奖励公式
- `verifier`：按 payload 历史上下文重放 USDB 状态并生成 reward input

主流程改动点则保持最少：

- `miner/worker.go`
- `consensus/ethash/consensus.go`
- `params/protocol_params.go`

## 7. 后续需要单独确认的问题

在真正进入实现前，还需要把这几个问题定清：

1. 第一版 `level = f(energy)` mock 公式是否按当前几何级数参数固化
2. 第一版 multiplier 线性区间 `[M, N]` 是否按 `0.5 .. 2.0` 固化
3. `header.Extra` 是否直接扩到 `160 bytes`
4. 第一版是否明确不带 `stable_block_hash`
5. `baseReward(height)` 在第一阶段是否先固定返回常量 `5 ETH`
6. `baseReward(height)` 的衰减节奏和下限，是后续按 block height 自动切换，还是通过未来 `UsdbV2Block / UsdbV3Block` 升级实现

## 8. 第一阶段默认约定

如果没有新的设计变更，第一阶段开发可以先按以下默认约定落地：

1. `reward_rule_version = UsdbV1`
2. `UsdbV1` 从 genesis 固定生效
3. `level` 先使用本地几何级数 mock 公式
4. multiplier 使用 `level 1..50 -> [0.5, 2.0]` 线性增长
5. `header.Extra` 预留 `160 bytes`
6. 第一版 payload 不带 `stable_block_hash`
7. `baseReward(height)` 第一阶段先返回常量 `5 ETH`

后续如需演进：

- 奖励公式升级可通过 `UsdbV2Block / UsdbV3Block` 这类后续版本机制引入
- payload 编码升级则通过提升 `payload_version` 实现
