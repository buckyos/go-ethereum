# USDB 新链启动参数与迁链注意事项

## 1. 背景

当前代码基线来自 ETHW fork 过来的 `go-ethereum` 分支，但目标链不是继续兼容正在运行的 ETHW 网络，而是：

- 一条全新的 USDB 链
- 从 genesis 即固定使用 PoW
- 从 genesis 即启用 USDB 奖励规则
- 不考虑兼容当前 ETHW 的分叉迁移语义

因此，迁链时不应只是“替换几个地址和 ChainID”，而应系统性收口掉这条分支里遗留的 ETHW / Merge 过渡逻辑。

## 2. 当前代码现状

### 2.1 难度炸弹与 difficulty 计算

当前 `consensus/ethash/consensus.go` 的 `CalcDifficulty(...)` 仍然保留了两类路径：

1. 传统 Ethereum 的 difficulty 计算器  
   它们经过 `Byzantium / Constantinople / GrayGlacier` 等阶段演化，内部仍然带有 difficulty bomb 或 bomb-delay 机制。

2. ETHW fork 后新增的 `calcDifficultyEthPoW(...)`  
   这条路径本身不再包含 difficulty bomb，更适合作为长期 PoW 链的基础。

当前 ETHW 分支还通过：

- `EthPoWForkBlock`
- `EthPoWForkSupport`
- `ETHWStartDifficulty`
- `ChainID_ALT`

来支持“从原 Ethereum / Merge 前后切出 ETHW”这类兼容逻辑。

对于 USDB 新链，这些历史迁移语义大多并不需要。

### 2.2 默认难度参数

当前全局默认值在 `params/protocol_params.go`：

- `GenesisDifficulty = 131072`
- `MinimumDifficulty = 131072`
- `DifficultyBoundDivisor = 2048`
- `DurationLimit = 13`

这套参数是 Ethereum / ETHW 现有链语境下的默认值，不一定适合一个启动初期算力很低的新链。

### 2.3 网络身份与 fork 兼容字段

当前 `params/config.go` 里的 `ChainConfig` 除了 `ChainID`，还带有：

- `ChainID_ALT`
- `EthPoWForkBlock`
- `EthPoWForkSupport`
- `TerminalTotalDifficulty`
- `TerminalTotalDifficultyPassed`

这些字段大多是为：

- Merge 过渡
- ETHW 从既有 Ethereum 网络中分叉

而存在，不是新链从 genesis 启动时的必需项。

### 2.4 genesis 分配

`core/genesis.go` 中的 `Genesis` 允许通过：

- `Alloc`

做账户预分配；当前新链阶段没有预挖和初始分配诉求，因此不需要默认携带预分配账户。

## 3. 第一阶段建议

## 3.1 难度炸弹：第一版直接移除

结论：

- `v1` 不保留 difficulty bomb
- 不依赖 bomb 做强制升级
- 未来版本升级依赖显式 fork/version 机制，而不是让链自然失效

原因：

- USDB 链确定长期使用 PoW
- bomb 是为促使链离开 PoW 或推动历史阶段升级而设计
- 对新链来说，它只会增加长期维护和参数治理复杂度

### 3.1.1 实现方向建议

不要只是机械地把现有 `GrayGlacierBlock`、`ArrowGlacierBlock` 等高度改成 `0`。

更合理的做法是：

- 让 USDB 链从 genesis 起直接走“无炸弹 difficulty 路径”
- 也就是以当前 ETHW 分支里的 `calcDifficultyEthPoW(...)` 为基础

同时建议：

- 不再使用 `ETHWStartDifficulty` 这种 fork 切换时的 reset 逻辑
- 不再保留 “fork 高度 == next 时 difficulty = 1” 这类迁移特判

因为 USDB 链不是从旧链切换过来，而是从 genesis 启动。

## 3.2 初期难度：显式调低，并通过测试反推合理参数

结论：

- `v1` 应显式降低初始难度
- 同时降低最小难度下限
- 最终值不在文档里硬编码拍板，而通过私链/测试网压测反推

建议第一阶段优先只调整：

- `GenesisDifficulty`
- `MinimumDifficulty`

而暂时不改：

- `DifficultyBoundDivisor`
- `DurationLimit`

原因：

- 前两者直接影响低算力阶段的起步出块能力
- 后两者影响整条链的 retargeting 响应曲线，改动面更大

### 3.2.1 参数选择方法

第一阶段建议先用保守方法：

1. 将 `GenesisDifficulty` 调低到明显低于当前 ETHW 默认值
2. 将 `MinimumDifficulty` 也同步下调，避免底线过高
3. 在本地私链 / 小规模测试网下做连续出块测试
4. 观察：
   - 平均出块时间
   - 空闲时段出块是否过慢
   - 难度调整是否振荡过大
5. 再确定正式的初始参数

也就是说：

- 当前阶段先确认“要调低”
- 具体数字通过后续测试确定

文档层先不把数值固死。

### 3.2.2 第一阶段临时起步值建议

为了让开发阶段可以直接启动私链和小规模测试网，建议先给出一组“仅用于 v1 内网起步”的临时参数：

- `GenesisDifficulty = 8192`
- `MinimumDifficulty = 8192`

这组数值的定位是：

- 明显低于当前 ETHW 默认值 `131072`
- 便于在早期节点数少、总算力低的情况下快速连续出块
- 仅作为第一阶段 bring-up 和回归测试参数

它不是最终生产值。后续仍应通过实际测试数据决定是否需要上调或下调。

建议的测试回收标准：

- 平均出块时间是否落在预期区间
- 单节点/双节点/少量节点下是否会长时间卡块
- 难度是否在低算力阶段剧烈振荡
- 节点数增加后是否能平滑抬升到更稳的难度水平

如果这组临时值过低或过高，再围绕它做小步调整，例如：

- `4096`
- `16384`

而不是一开始就改 `DifficultyBoundDivisor` 或 `DurationLimit`。

## 3.3 网络身份：整套独立于现有 ETHW

结论：

- 必须使用全新的 `ChainID`
- 必须使用全新的 `NetworkId`
- 必须使用全新的 genesis
- 不应继续复用 ETHW 迁移链路里的 `ChainID_ALT` 语义

对新链来说，建议：

- `ChainID` 直接定义为 USDB 链的唯一标识
- `ChainID_ALT` 在 `v1` 阶段可直接等于 `ChainID`
- 后续再逐步清理与 `ChainID_ALT` 绑定的 ETHW 历史兼容逻辑

同时还应一起调整：

- 默认 bootnodes
- DNS discovery 配置
- 网络名与 banner 展示
- 可能的默认端口

这些组成了完整的“链身份”，不只是交易签名里的 `chainId`。

### 3.3.1 第一阶段网络默认值收口

第一阶段建议只收口最影响日常开发和节点隔离的默认值：

- `P2P Listen Port`
  - `--usdb` 使用独立默认端口
  - 第一阶段固定为 `31303`
  - 目的不是改变链身份，而是避免与本机已有 Ethereum / ETHW 节点默认端口冲突
  - 也避免节点被误连到另一个默认运行在 `30303` 上的网络实例
- `HTTP / WS / Auth`
  - 第一阶段保持现有默认值
  - 这些端口只影响本机运维与调试，不影响链身份与 p2p 隔离
  - 如果后续经常并行运行多条链，再考虑为 `--usdb` 单独提供默认值
- `bootnodes / DNS discovery`
  - 第一阶段保持空集合
  - 单机 bring-up 或少量节点联调时，可直接使用：
    - `--bootnodes`
    - static peers
    - `admin.addPeer(...)`
  - 待进入多机开发或公开测试网阶段后，再补正式 `USDBBootnodes` 和 DNS discovery

### 3.3.2 开发期 bootnodes / static-nodes 生成方式

当前阶段不建议把 bootnodes 直接硬编码进代码，而是先通过外部脚本生成。

开发期建议使用以下工具链：

- [generate_bootnodes_manifest.sh](/home/bucky/work/go-ethereum/scripts/usdb/generate_bootnodes_manifest.sh)
  - 从运行中的 USDB 节点 RPC 读取 `admin_nodeInfo.enode`
  - 生成：
    - `bootnodes-manifest.json`
    - `static-nodes.json`
    - `bootnodes.txt`
- [run_devnet_node.sh](/home/bucky/work/go-ethereum/scripts/usdb/run_devnet_node.sh)
  - 在任意机器上按角色启动一个 USDB devnet 节点
  - 支持：
    - `NODE_ROLE=bootnode`
    - `NODE_ROLE=miner`
    - `NODE_ROLE=full`
  - 并可通过：
    - `BOOTNODES`
    - `BOOTNODES_FILE`
    - `STATIC_NODES_FILE`
    接入外部 bootnodes 列表

典型流程：

1. 在第一台机器上启动发现种子节点：

```bash
NODE_ROLE=bootnode KEEP_RUNNING=1 ./scripts/usdb/run_devnet_node.sh
```

2. 生成 bootnodes / static-nodes 清单：

```bash
./scripts/usdb/generate_bootnodes_manifest.sh --rpc-url http://127.0.0.1:28545
```

3. 在其他机器上引用生成出来的 `bootnodes.txt` 启动跟随节点或矿工节点：

```bash
NODE_ROLE=miner \
BOOTNODES_FILE=/tmp/usdb-bootnodes/bootnodes.txt \
KEEP_RUNNING=1 \
./scripts/usdb/run_devnet_node.sh
```

如果希望使用静态邻居列表，也可以直接把生成器输出的 `static-nodes.json` 注入节点：

```bash
NODE_ROLE=full \
STATIC_NODES_FILE=/tmp/usdb-bootnodes/static-nodes.json \
KEEP_RUNNING=1 \
./scripts/usdb/run_devnet_node.sh
```

这样做的好处是：

- 本地与多机联调都复用同一份 bootstrap genesis
- bootnodes 仍是外部配置，不污染链代码
- 到正式 testnet / mainnet 阶段，再决定是否收进内置 `USDBBootnodes`

也就是说，当前阶段的最小可行网络默认值策略是：

- `ChainID / NetworkId / genesis hash` 已独立
- `P2P` 默认端口独立
- `HTTP / WS / Auth` 暂沿用通用默认值
- `bootnodes / DNS` 暂不内置

## 3.4 genesis：默认不做预挖与预分配

结论：

- `v1` genesis 默认 `alloc = {}`
- 不做初始余额分配
- 不做预挖

例外情况：

- 如果后续 dividend pool 或系统组件必须以 genesis 预置合约形式存在
- 那时再单独讨论某个特定地址的 code/storage 注入

但就当前阶段而言，不需要普通账户预分配。

## 3.5 奖励与手续费规则都从 genesis 生效

既然这是全新的 USDB 链，建议：

- USDB 奖励规则从 genesis 生效
- 手续费分账规则也从 genesis 生效

不建议为了“兼容旧 ETHW 规则”再人为保留一段过渡高度。

## 4. 除上述之外，建议同步处理的项

## 4.1 收口 EthPoW / Merge 遗留逻辑

当前分支仍有不少和 ETHW fork 迁移相关的特殊判断，包括：

- `EthPoWForkSupport`
- `EthPoWForkBlock`
- `ChainID_ALT`
- `TerminalTotalDifficulty`
- `TerminalTotalDifficultyPassed`
- 若干 `IsEthPoWFork(...)` 分支

对 USDB 新链来说，建议逐步收口成：

- 单一 PoW 链语义
- 从 genesis 固定生效

避免长期维护一套“明明不会再走到的迁移代码”。

## 4.2 现代 EVM 规则建议从 genesis 固定开启

对于：

- `London`
- `Berlin`
- `Istanbul`
- `Constantinople`

等历史升级，建议新链不要重走一遍升级时间线，而是直接从 genesis 固定在当前要采用的现代规则集上。

这样更简单，也更适合一条全新链。

## 4.3 明确新增默认 genesis / chain config

建议后续新增独立的：

- `USDBChainConfig`
- `DefaultUSDBGenesisBlock()`
- `USDBGenesisHash`

这样就不需要继续把新链挂靠在现有 `MainnetChainConfig` 或 ETHW 主网配置之上。

## 5. 第一阶段建议清单

迁链第一阶段建议按下面顺序推进：

1. 新增 USDB 独立 chain config 与 genesis
2. 让 difficulty 从 genesis 起走无炸弹路径
3. 去掉 fork-reset 型难度特判
4. 降低 `GenesisDifficulty` 与 `MinimumDifficulty`
5. 清空 genesis `alloc`
6. 替换 `ChainID / NetworkId / bootnodes / network name`
7. 用私链/测试网反推合理的初始难度参数

## 6. 当前阶段的明确结论

- 难度炸弹：`v1` 直接移除
- 初期难度：显式调低，具体值通过测试反推
- 新链身份：整套独立于现有 ETHW
- 预挖与分配：默认移除
- USDB 奖励规则：从 genesis 固定生效
- 手续费分账规则：建议也从 genesis 固定生效

## 7. 后续文档关系

本说明文档关注的是“USDB 新链如何从 ETHW fork 代码基线上收口成一条独立链”。

和它配套的功能级文档是：

- `docs/usdb/usdb-reward-integration.md`
- `docs/usdb/usdb-fee-split-integration.md`

前者关注：

- USDB 奖励 payload / verifier / multiplier

后者关注：

- 交易手续费如何分账到 dividend address
