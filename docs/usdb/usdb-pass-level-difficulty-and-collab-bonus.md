# USDB Pass Level 难度调节与协作者奖励方案备忘

## 1. 背景

当前已经落地的 `v1` 方向是：

- 主矿工证 `pass` 的历史 `energy / level`
- 只影响 ETHW 侧矿工主奖励
- 通过 `header.Extra` 中的 `RewardPayloadV1` 绑定到历史 USDB 状态

目前正在讨论的增强方向是：

1. 主 `pass level` 不再影响出块奖励，而是影响 miner 出块难度
   - `level` 越高，目标难度越低
   - 下降方向应从 `1.0` 向下递减，而不是从大于 `1.0` 的放大系数开始
2. 协作者 `eth_collab` 不参与难度，而是为出块奖励提供额外加法增益
   - 主矿工奖励仍由主 `pass` 决定
   - 协作者等级带来一笔额外 bonus

这份文档用于独立收口该方案的：

- 可行性
- 结构约束
- 风险
- 分阶段建议

## 2. 目标中的规则拆分

当前讨论的理想形态可以概括为两条彼此独立的规则：

### 2.1 主 `pass` 影响难度

定义：

- `effectiveDifficulty = baseDifficulty(parent, time) * mainDifficultyFactor(level_main)`

其中：

- `baseDifficulty(parent, time)` 仍是链原有的难度调整主公式
- `mainDifficultyFactor(level_main)` 随 `level_main` 单调递减
- 因子建议落在保守区间，例如：
  - `[1.0, 0.8]`
  - 或 `[1.0, 0.7]`

也就是说：

- 低等级主 `pass`：接近原始难度
- 高等级主 `pass`：相对更容易挖到块

### 2.2 协作者只影响奖励

定义：

- `finalReward = mainReward + collaboratorBonus`

其中：

- `mainReward` 由主 `pass` 的规则决定
- `collaboratorBonus` 是基于协作者等级的额外奖励

推荐第一版采用加法而不是乘法：

- `collaboratorBonus = baseReward * collabBonusFactor(level_collab)`

这样可以避免协作者 bonus 与主奖励规则耦合过深。

## 3. 技术可行性判断

## 3.1 主 `pass level -> difficulty` 技术上可行

从当前实现结构看，这件事是可行的，但会比 `reward-only` 明显更重。

原因是：

- miner 当前会先构造 `header.Extra`
- 然后再调用 `engine.Prepare(...)`
- `SealHash` 又同时承诺了：
  - `header.Difficulty`
  - `header.Extra`

这意味着 validator 可以：

1. 先解析 `header.Extra`
2. 用 payload 固定的历史 USDB 上下文解析主 `pass`
3. 重算 `level_main`
4. 重算该块应有的 `Difficulty`
5. 再校验块头

因此它不是“不可能”，而是：

- 需要把当前 `difficulty` 计算从只依赖 `parent + timestamp`
- 升级成依赖 `header payload + historical USDB state`

## 3.2 协作者奖励也可行，但前提更多

当前 USDB 协议中：

- `eth_collab` 只是一个可选协作者 ETH 地址
- 不是协作者 `pass_id`
- 也不是“协作者 pass 列表”

所以如果协作者 bonus 真的要依赖协作者自己的 `energy / level`，当前还缺一层能力：

- 在历史高度 `H`，根据 `eth_collab` 地址解析它对应的有效协作者矿工证

也就是说，协作者 bonus 不是直接拿现有接口就能完全闭环的。

## 4. 当前结构下的关键约束

## 4.1 难度规则一旦接入，就是 header 有效性的一部分

奖励错误目前主要会体现在：

- `Finalize` 计算状态根时不一致

而 difficulty 不同则更前置：

- 直接影响 header 是否有效
- 影响 PoW target
- 影响 total difficulty
- 影响分叉选择

所以：

- `reward` 错误更多是“状态不一致”
- `difficulty` 错误则是“区块头共识不一致”

这会显著提高 USDB 依赖的共识敏感度。

## 4.2 协作者 bonus 进入共识后，协作者解析必须是历史确定性的

如果协作者奖励进入共识，那么 validator 必须在旧块重放时，稳定得到同一个协作者等级。

这要求以下两种方案之一成立：

1. USDB 提供“按历史高度通过 `eth_collab` 地址解析协作者 active pass”的一等 RPC
2. ETHW payload 显式携带 `collab_pass_id`

如果做不到其中之一，协作者 bonus 就不适合进入共识。

## 5. 主要风险

## 5.1 全网出块节奏可能变得不平稳

这是主 `level -> difficulty` 方案最重要的系统性风险。

当前难度算法默认假设：

- 全网算力变化主要来自外部矿工硬件/在线状态

但如果 difficulty 再额外受 `pass level` 影响，则全网“有效算力”会同时受到：

- 真实算力
- 当前在线 miner 的 `level` 分布

影响结果：

- 某段时间高等级 miner 集中上线时，出块可能突然偏快
- 某段时间低等级 miner 为主时，出块可能突然偏慢
- 难度 retarget 可能出现更复杂的振荡

这不是简单的“单矿工收益变化”，而是：

- 会影响整条链的平均出块时间稳定性

## 5.2 分叉选择和链权重会变复杂

Ethash 下 `Difficulty` 还参与：

- `total difficulty`
- 链权重比较

因此一旦主 `pass` 影响难度：

- 高等级 miner 产出的块，不只是更容易找到
- 还可能改变整条链的累计难度演化

如果设计不慎，会导致：

- 分叉选择偏向某类高等级矿工链
- 链权重和真实外部算力不再近似对应

## 5.3 高等级矿工可能过度集中

如果主 `pass` 同时拥有：

- 更低出块难度
- 以及未来仍保留较高主奖励

那么高等级矿工的优势会过强。

即使当前讨论是：

- 主 `pass` 只影响 difficulty
- 协作者才影响额外奖励

也仍需要防止高等级矿工过度聚集出块权。

所以难度因子建议一开始必须保守。

## 5.4 协作者可能出现“一人多协作”放大问题

当前 `eth_collab` 是地址，不是唯一绑定关系。

如果没有额外限制，可能出现：

- 同一个协作者地址
- 同时出现在多个主矿工证里
- 然后在多个 miner 出块时都获得 bonus 贡献

这是否符合协议目标，需要先明确。

否则会出现：

- 协作者 bonus 被重复放大
- 激励结构变得不可控

## 5.5 协作者解析路径当前不够明确

由于当前模型只有：

- `eth_main`
- `eth_collab`

而没有：

- `collab_pass_id`

所以协作者 bonus 真正进入共识之前，需要先定清楚：

- 是按地址反查 active pass
- 还是 payload 明确带协作者 pass

这一步如果不先补，后续很容易把“协议想法”和“可验证实现”混在一起。

## 6. 推荐的分阶段策略

## 6.1 不建议直接一步到位

不推荐直接上线：

- 主 `pass` 影响 difficulty
- 协作者同时影响 bonus

更稳的方式是逐层推进。

## 6.2 建议的阶段拆分

### 阶段 A：继续保持当前 `reward-only`

也就是当前已经在做的：

- 主 `pass energy / level` 影响奖励
- 不改 difficulty

作用：

- 先把历史 payload、历史稳定性、双节点校验打稳

### 阶段 B：单独试验 `difficulty-only`

先只做：

- 主 `pass level -> mainDifficultyFactor`

并且：

- 关闭主奖励乘数放大
- 只观察 difficulty 侧影响

建议：

- 因子区间先非常保守
- 例如只做到 `[1.0, 0.9]` 或 `[1.0, 0.8]`

这样便于先验证：

- header 有效性
- 历史稳定性
- 双节点同步
- 出块节奏振荡

### 阶段 C：再讨论协作者 bonus

只有在以下问题明确后，协作者 bonus 才应进入共识：

1. 协作者的历史 `pass / level` 如何确定
2. 协作者 bonus 是发给：
   - `coinbase`
   - 还是 `eth_collab`
3. 一个协作者是否允许同时协作多个 miner

在这些问题未确定前，不建议让 `eth_collab` 进入共识路径。

## 7. 协作者 bonus 的实现建议

如果后续真要引入协作者 bonus，我更推荐：

- 第一版 bonus 仍然发给 `coinbase`
- 只把协作者当作“额外贡献输入”
- 不急着直接把 bonus 发到协作者地址

原因：

- 这样可以减少 `Finalize` 中的多地址分账复杂度
- 也能避免与 dividend / fee split 逻辑叠加过快

如果未来再做“协作者链上直接分润”，可以另开一个版本。

## 8. 对 payload / USDB RPC 的建议

如果协作者进入共识，建议优先考虑以下两个方向：

### 8.1 方向 A：USDB 新增历史协作者解析 RPC

例如提供一类接口：

- `get_active_pass_by_eth_main_at_height`
- 或更高层的 `get_effective_mining_profile_at_height`

优点：

- ETHW payload 仍可保持较小

缺点：

- USDB 侧需要新增更复杂的历史解析能力

### 8.2 方向 B：payload 显式携带 `collab_pass_id`

即在块里除主 `pass_id` 外，再显式带：

- `collab_pass_id`

优点：

- 共识输入更显式
- validator 不需要按地址做历史反查
- 更容易审计和排错

缺点：

- payload 会变大
- 需要额外校验 `collab_pass_id` 与主 `pass` 中的 `eth_collab` 的一致性

从共识可验证性角度，我更倾向这个方向。

## 9. 测试建议

如果后续进入实现阶段，测试建议至少拆成四层：

1. 单节点：
   - `level` 提升后，新块 difficulty 下降
2. 历史稳定性：
   - BTC 头推进后，旧块仍按旧 difficulty 被接受
3. 双节点：
   - 新 validator 节点同步旧块时，仍按历史 payload 接受区块
4. 稳定性/振荡测试：
   - 模拟不同 `level` 分布下的出块节奏变化

其中第 4 类非常关键，因为这正是该方案相对当前 `reward-only` 最大的新风险来源。

## 10. 当前建议结论

这套“主 `pass` 影响 difficulty、协作者影响 bonus”的结构，本身是有吸引力的：

- 主矿工证控制出块竞争力
- 协作者控制额外收益

它也比“主 `pass` 同时影响 reward + difficulty”更健康。

但从当前工程状态看，建议仍然是：

- 先把它作为一套独立设计方向保留
- 先不要直接替代当前 `reward-only`
- 真正推进时，优先顺序应是：
  1. `difficulty-only`
  2. 历史稳定性和双节点验证
  3. 协作者 bonus

在协作者历史解析和多重协作规则未定前，不建议把协作者直接带入共识主路径。
