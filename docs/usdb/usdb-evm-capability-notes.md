# USDB Chain EVM Capability Notes

[English](./usdb-evm-capability-notes.md) | [中文](./usdb-evm-capability-notes.zh.md)

## 1. Purpose

This document summarizes the **actual execution-layer capability** of the current USDB chain forked from ETHW/go-ethereum.

The goal is to make three boundaries explicit:

- which fork-era EVM features are intentionally enabled on USDB v1,
- which newer Ethereum features are **not** available yet,
- what contract authors should target today to avoid deployment/runtime incompatibility.

This document is intentionally separate from:

- `usdb-chain-bootstrap-notes.md`
- `usdb-ethw-reward-integration.md`
- `usdb-ethw-fee-split-integration.md`

because it focuses on **EVM capability and contract compatibility**, not reward, fee split, or chain cold-start flow.

## 2. Current USDB v1 Position

USDB v1 is a **genesis-start, pure PoW chain**.

It has already been migrated away from:

- Merge/beacon wrapper execution path
- ETHW transition-reset difficulty semantics
- ETHW post-fork chain-id switching semantics

At the execution layer, the current practical target is:

- `LondonBlock = 0`
- `ShanghaiBlock = 0`
- `CancunBlock = nil`

This means the chain is currently intended to support:

- pre-London historical EVM behavior only as inherited compatibility,
- London-era execution semantics,
- the subset of Shanghai that is required by current contract artifacts,
- but **not** Cancun as a validated target.

## 3. Why Shanghai Was Enabled

The immediate reason was compatibility with the current SourceDAO contracts.

The SourceDAO artifacts used by bootstrap testing are compiled by Solidity `0.8.20`, and their runtime bytecode contains:

- `PUSH0`

`PUSH0` is introduced by:

- `EIP-3855`
- Shanghai execution layer

Without Shanghai support, the preloaded SourceDAO runtime code would fail at opcode execution time even if:

- the system contract address is correct,
- genesis preloading is correct,
- bootstrap transactions are correct.

USDB therefore enables Shanghai from genesis for a concrete compatibility reason, not simply because it is a newer fork name.

## 4. What Is Actually Supported Today

### 4.1 Fork-era capability actually exercised by USDB

The current fork selection and VM path are consistent with:

- Homestead
- EIP-150 / Tangerine Whistle
- EIP-155 / EIP-158
- Byzantium
- Constantinople / Petersburg
- Istanbul
- Berlin
- London
- Shanghai

For USDB v1, the highest fork that is intentionally activated and currently relied on is:

- **Shanghai**

### 4.2 Execution-layer EIPs explicitly wired in the current VM

The current `core/vm/eips.go` activator table explicitly contains:

- `1344` `CHAINID`
- `1884`
- `2200`
- `2929`
- `3198` `BASEFEE`
- `3529`
- `3855` `PUSH0`

For USDB contract compatibility, the key practical point is:

- `PUSH0` is now available

### 4.3 Shanghai support boundary

USDB currently has enough Shanghai support for the present contract set because:

- the interpreter now selects a Shanghai jump table,
- the Shanghai jump table enables `EIP-3855`,
- the chain config activates Shanghai from genesis.

This should be understood as:

- **Shanghai support sufficient for current contract execution**

not as:

- "full latest Ethereum execution environment with every post-London feature".

## 5. Why Cancun Is Not Enabled Yet

`ChainConfig` still contains:

- `CancunBlock`

but that **does not mean USDB currently has validated Cancun support**.

The current codebase does not yet show a complete, audited, and tested Cancun execution path for USDB.

At minimum, the current VM activator list does **not** show explicit support for the Cancun-era execution features people usually expect, such as:

- `EIP-1153` transient storage
- `EIP-5656` `MCOPY`
- `EIP-7516` `BLOBBASEFEE`
- EIP-4844-related blob transaction execution surface

So the practical rule is:

- **having a fork field in config is not the same as having complete fork support**

For USDB v1, enabling Cancun now would create a dangerous situation:

- chain config would claim a newer capability level,
- but VM, transaction types, and execution details may still be incomplete or unverified.

That is worse than intentionally stopping at a lower but proven fork level.

## 6. Current Gap vs Latest Ethereum

Compared with a modern Ethereum execution client target, USDB v1 currently has a deliberate gap in at least these areas:

- no Cancun-era validated execution target
- no blob-transaction target for contracts or tooling
- no expectation that contracts may safely rely on transient storage
- no expectation that contracts may safely rely on `MCOPY`
- no expectation that contracts may safely rely on `BLOBBASEFEE`
- no merge/beacon-era execution environment assumptions

In other words:

- USDB v1 is **not** “latest Ethereum feature-complete”
- it is a **purpose-built PoW chain with a controlled execution baseline**

## 7. Practical Contract Guidance

For contract authors targeting USDB v1, the recommended target is:

- compiler/toolchain compatible with `Shanghai`

Recommended discipline:

- explicitly target `Shanghai`-compatible bytecode
- do not assume `Cancun` features
- do not ship contracts that require:
  - transient storage
  - blob-related opcodes or fee context
  - Cancun-only codegen assumptions

For the current SourceDAO contract set:

- Solidity `0.8.20` is acceptable because the chain now supports `PUSH0`

But the safe interpretation is still:

- `solc 0.8.20` output that only needs Shanghai-era execution is acceptable
- `solc` output that relies on Cancun semantics is **not yet a supported deployment target**

## 8. Recommended Versioning Policy

For USDB v1:

- execution target: **Shanghai**
- do not enable `CancunBlock`
- treat Cancun as a future, separately audited upgrade topic

If USDB later wants a `v2` execution upgrade:

1. audit the required Cancun-era EIPs one by one
2. implement the missing VM/runtime support explicitly
3. add targeted tests for contract compatibility and fork transition
4. only then decide whether to activate `CancunBlock`

## 9. Current Recommendation

The current recommendation for USDB v1 remains:

- keep `ShanghaiBlock = 0`
- keep `CancunBlock = nil`
- document USDB as a **Shanghai-level PoW chain**, not a latest-Ethereum-equivalent chain

This is the most honest and least risky baseline for:

- reward integration
- dividend/bootstrap system contracts
- future fee-split integration
- external contract developer expectations

## 10. Follow-up Work

If Cancun support becomes necessary later, the follow-up should be tracked as a dedicated upgrade topic:

- `USDB EVM Capability Upgrade: Shanghai -> Cancun`

That work should include:

- opcode/gas-rule audit
- transaction-type audit
- test-matrix update
- contract compatibility validation
- explicit fork activation design
