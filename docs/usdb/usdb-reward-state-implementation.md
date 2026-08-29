# USDB Reward State Implementation

本文记录 UIP-0011、UIP-0012 和 UIP-0013 当前 development v1 实现边界。

## Consensus Inputs

每个 reward block 只消费 UIP-0007 selector 解析出的同一份历史 profile：

- `pass.usdb_main` 作为唯一 reward recipient，并强制等于 `header.Coinbase`。
- `miner_aggregate.total_miner_btc_sats` 作为 target supply 的 BTC 总额。
- `pass.collab_contribution` 作为 UIP-0012 当前 `CE` sample。

builder 和 validator 使用同一个 profile codec。字段缺失、负数、非 canonical decimal、
owner aggregate 不一致或服务不可用都 fail closed。

## Reward Order

区块 `N` 的执行顺序固定为：

1. 从 parent state 校验 reserved system account schema。
2. 从 parent price slots 读取 UIP-0011 reward price。
3. 用 parent K window 和当前 `CE_N` 计算 `k_bps_N`。
4. 计算 target supply、remaining target 和 emission。
5. 校验 `header.Coinbase == profile.pass.usdb_main`。
6. credit emission，并累加 `ISSUED_USDB_ATOMS_SLOT`。
7. 写入 K ring/sum/count/cursor 和 last audit slots。
8. 按区块 `N` activation 写入 child fixed-price policy state。

所有待写值在修改 StateDB 前完成校验。unsupported policy、uint256 overflow、损坏的 K
窗口、错误 price range 或 recipient mismatch 不得留下部分状态。

## UIP-0011

v1 emission 使用整数公式：

```text
target = floor(total_miner_btc_sats * price_atoms_per_btc / 100_000_000)
remaining = max(0, target - issued)
emission = floor(remaining * k_bps / (157_680 * 10_000))
```

`issued` 包含 genesis alloc 和此前 emission，不因 burn 或转账回减。reward v1 完全禁用
uncle/ommer；builder 只产生 empty uncle list，validator 拒绝任何非空 uncle body/hash。

## UIP-0012

v1 使用 50,400 个 reward block 的 rolling window：

- warmup 未填满时 `k_bps = 10000`。
- 动态 K 只使用当前块之前的 window average。
- 当前 `CE` 在 reward 计算后写入 ring。
- full window 覆盖 cursor 指向的旧 sample，并同步更新 checked sum。
- count、cursor、sum、ring 和 last audit slots 任一不一致时 fail closed。

## UIP-0013

v1 固定：

```text
price = real_price = 100000000000000000000000 atoms/BTC
price_policy_version = 1
price_source_kind = 1
```

reward 使用 parent price；当前 activation 的 price state 写入 child，因此 activation
边界的新 policy 最早影响下一块 reward。v1 range ID 为：

```text
keccak256(
  UTF8("usdb.price.policy.range:v1") || 0x00 ||
  uint256_be(chain_id) ||
  uint64_be(start_block) ||
  uint32_be(price_policy_version) ||
  uint32_be(price_source_kind) ||
  uint256_be(const_price_atoms_per_btc)
)
```

chain ID `20260323`、start block `0` 的 golden value：

```text
0x2ae45cafae84cc892d1d4354f02a0869f97dfd6ca2c757ba511c57680b8bfaf4
```

v1 constant 编译进 policy implementation。未来调整 fixed price 必须激活新的
`price_policy_version`，不能修改历史 v1 常量或复用 v1 range identity。

## Verification

当前覆盖：

- Rust/Go formula 和 fixed-range golden vectors。
- target exhausted、zero BTC、uint64/uint128/uint256 边界和 overflow。
- K warmup、full-window replacement、上下界和损坏状态拒绝。
- parent-price/child-price activation 边界。
- reward recipient mismatch 原子失败。
- uncle header/body/finalization 拒绝。
- parent-root reorg 后 balance、issued、K 和 price range 恢复。
- 独立 producer/validator 数据库通过 `BlockChain.InsertChain` 重放 reward；profile
  reward aggregate 不一致时由 importer 的 state-root 校验拒绝且不移动 canonical head。
- 真实签名 legacy/dynamic-fee 交易覆盖 policy/gate、base fee + tip、SSTORE refund、
  Dividend readiness fail-closed、逐笔 rounding 和 fee 总量守恒。
- 独立 50,405-step oracle 从空窗口跨过完整 warmup，并逐步交叉校验 K 公式、
  sum/count/cursor、ring 覆盖和 last audit slots。
- selector-bound real regtest reward smoke。
- fee gate、Dividend ledger sync、restart、fresh joiner 和 historical state-root live E2E。
- BTC same-height replacement 后 stale selector chain 拒绝。
