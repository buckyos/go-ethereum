# Canonical USDB block economics RPC

`eth_getUSDBBlockEconomics` accepts one full block hash and returns schema
`usdb-block-economics:v1`. It is registered with the existing `eth` namespace.
Use it through a private archive read endpoint. Explorer exposes its own limited
GET API and deliberately does not add this expensive method to public `/rpc`.

The method opens retained parent state, replays each transaction with the normal
execution path, and finalizes rewards through the configured consensus engine.
It never commits state or imports blocks. Fee routes use pre-transaction state,
refund-adjusted gas and effective gas price; v1 splits round per transaction.
Finalization's miner credit is measured separately from fees, ordinary transfers
and contract effects. An issued-counter change during transactions is rejected.

A `verified` result requires matching state root, receipt root, bloom and total
gas, plus reconciliation of the issuance-counter increment and emission credit.
It includes the block/parent hashes, decimal block number, roots, active versions,
selector-pinned Miner Pass and BTC identities, miner and Dividend addresses,
aggregate amounts and per-transaction fees/status. All amounts and gas quantities
are **decimal strings**, including effective gas price in atoms. The native
currency has 18 decimal places. The issued counter includes genesis allocations
and emission; it is not circulating supply or a sum of live account balances.

Genesis returns `genesis_not_applicable` without amounts, versions or a selector.
Empty mined blocks can verify normally. Failed EVM calls still contribute fees.
The report is this node's replay evidence, not independent consensus validation
or qualification of all historical blocks and future economic policies.

Supported rules: payload, BTC anchor, difficulty, reward, emission and price v1;
fee split and collaboration v0/v1; quote v0 and auxiliary pool v0. Reward replay
resolves the historical profile committed in `header.Extra`, never the current
indexer head. Unknown combinations return an unsupported-policy error.

| RPC error | Meaning |
| --- | --- |
| -32060 | Block not found |
| -32061 | Hash is no longer canonical |
| -32062 | Required parent/history trie state unavailable |
| -32063 | Economic policy not supported by this report version |
| -32064 | Execution, historical profile resolution or commitment verification failed |
| -32065 | Replay deadline/cancellation |
| -32066 | Interactive replay limit exceeded |
| -32067 | Replay slots occupied |

Errors expose stable codes, not indexer endpoints or raw upstream errors.
At most two replays run concurrently; each block is limited to 2000 transactions
and 60 million gas used. The EVM deadline is ten seconds; a running historical
indexer request also follows the resolver's configured timeout. Successful
reports are cached for sixteen hashes in memory. Canonical identity is checked
on lookup and after fresh replay; old branch cache entries are not served after
a reorg. Clients should re-query if they need current canonical status.

No chain reset, storage migration or configuration change is required. Replace
the node runtime to acquire the method. An existing upstream method allowlist
may need an operator adjustment. Enabling archive cannot recreate pruned state;
missing history requires a retained archive or an independently rebuilt node.

Regression tests: `go test ./core ./eth -run 'TestUSDBEconomics'`, plus the existing
USDB fee/reward suites. The modern Go compatibility lane uses the repository's
linker compatibility policy; the canonical release toolchain remains Go 1.18.5.
