# Geth TxPool Call Chain

## Main Path

[TxPool.Add](../core/txpool/txpool.go#L314)
[core/txpool/txpool.go#L314](../core/txpool/txpool.go#L314)
`func (*TxPool) Add`
    |
    v
[LegacyPool.Add](../core/txpool/legacypool/legacypool.go#L935)
[core/txpool/legacypool/legacypool.go#L935](../core/txpool/legacypool/legacypool.go#L935)
`func (*LegacyPool) Add`
    |
    v
[add](../core/txpool/legacypool/legacypool.go#L688)
[core/txpool/legacypool/legacypool.go#L688](../core/txpool/legacypool/legacypool.go#L688)
`func (*LegacyPool) add`
    |
    v
[enqueueTx](../core/txpool/legacypool/legacypool.go#L849)
[core/txpool/legacypool/legacypool.go#L849](../core/txpool/legacypool/legacypool.go#L849)
`func (*LegacyPool) enqueueTx`
    |
    v
[requestPromoteExecutables](../core/txpool/legacypool/legacypool.go#L1145)
[core/txpool/legacypool/legacypool.go#L1145](../core/txpool/legacypool/legacypool.go#L1145)
`func (*LegacyPool) requestPromoteExecutables`
    |
    v
[runReorg](../core/txpool/legacypool/legacypool.go#L1235)
[core/txpool/legacypool/legacypool.go#L1235](../core/txpool/legacypool/legacypool.go#L1235)
`func (*LegacyPool) runReorg`
    |
    v
[promoteExecutables](../core/txpool/legacypool/legacypool.go#L1429)
[core/txpool/legacypool/legacypool.go#L1429](../core/txpool/legacypool/legacypool.go#L1429)
`func (*LegacyPool) promoteExecutables`
    |
    v
[Pending](../core/txpool/legacypool/legacypool.go#L506)
[core/txpool/legacypool/legacypool.go#L506](../core/txpool/legacypool/legacypool.go#L506)
`func (*LegacyPool) Pending`
    |
    v
[miner.fillTransactions](../miner/worker.go#L475)
[newTransactionsByPriceAndNonce](../miner/ordering.go#L101)
[miner/worker.go#L475](../miner/worker.go#L475)
[miner/ordering.go#L101](../miner/ordering.go#L101)

## One-Line Version

`TxPool.Add`
-> `LegacyPool.Add`
-> `add`
-> `enqueueTx`
-> `requestPromoteExecutables`
-> `runReorg`
-> `promoteExecutables`
-> `Pending`
-> `miner`

## Modification Points For Priority Accounts

The "priority account" change is intentionally inserted after promotion and before miner ordering.

Why this location:

- It does not alter transaction validity, nonce progression, replacement rules, or eviction rules.
- It does not mutate the transaction object, hash, or on-chain gas fields.
- It changes only the local ordering view exported from txpool to miner.

The concrete modification points are:

1. [Config.PriorityAccounts / Config.PriorityFeeBoost](../core/txpool/legacypool/legacypool.go#L140)
   `type Config struct`
   Adds the allowlist of privileged senders and the local fee boost value.

2. [LegacyPool.priority](../core/txpool/legacypool/legacypool.go#L232)
   `type LegacyPool struct`
   Stores the privileged account set in memory for fast lookup.

3. [New](../core/txpool/legacypool/legacypool.go#L267)
   `func New`
   Converts `PriorityAccounts` into `pool.priority`.

4. [Pending](../core/txpool/legacypool/legacypool.go#L506)
   `func (*LegacyPool) Pending`
   The real insertion point of the feature.
   When `addr` is privileged, `LazyTransaction.GasFeeCap` and `LazyTransaction.GasTipCap`
   are boosted before being returned to miner.

5. [boostFeeCap](../core/txpool/legacypool/legacypool.go#L563)
   Helper for safe local fee boosting with uint256 overflow handling.

So the modified semantic chain is:

`runReorg -> promoteExecutables -> promoteTx -> pool.pending updated`

then later:

`miner.fillTransactions -> TxPool.Pending -> LegacyPool.Pending`

and inside `LegacyPool.Pending`:

`if sender in PriorityAccounts => boost LazyTransaction fee metadata`

This means the feature is a local scheduling override layered on top of the existing txpool pipeline.

## Expanded Notes

1. [TxPool.Add](../core/txpool/txpool.go#L314)
   `func (*TxPool) Add`
   Entry point for the unified txpool. Splits incoming transactions by subpool type.

2. [LegacyPool.Add](../core/txpool/legacypool/legacypool.go#L935)
   `func (*LegacyPool) Add`
   Filters known and basically invalid transactions. Calls `addTxsLocked`, which eventually calls `add`.

3. [add](../core/txpool/legacypool/legacypool.go#L688)
   `func (*LegacyPool) add`
   Performs stateful validation and replacement / overflow logic. Decides whether the tx replaces a pending tx or should enter the future queue.

4. [enqueueTx](../core/txpool/legacypool/legacypool.go#L849)
   `func (*LegacyPool) enqueueTx`
   Inserts the transaction into the queue structure and updates `all` / `priced` indexes.

5. [requestPromoteExecutables](../core/txpool/legacypool/legacypool.go#L1145)
   `func (*LegacyPool) requestPromoteExecutables`
   Schedules async promotion work through the reorg loop.

6. [runReorg](../core/txpool/legacypool/legacypool.go#L1235)
   `func (*LegacyPool) runReorg`
   Central async maintenance stage. Handles reset, promotion, demotion, truncation, and tx event emission.

7. [promoteExecutables](../core/txpool/legacypool/legacypool.go#L1429)
   `func (*LegacyPool) promoteExecutables`
   Pulls contiguous executable txs out of the future queue and moves them into pending.

8. [Pending](../core/txpool/legacypool/legacypool.go#L506)
   `func (*LegacyPool) Pending`
   Exposes executable txs as account-grouped, nonce-ordered `LazyTransaction`s.
   This is also the modification point for the privileged-account local ordering boost.

9. [miner.fillTransactions](../miner/worker.go#L475)
   `func (*Miner) fillTransactions`
   Reads `Pending(filter)`.

10. [newTransactionsByPriceAndNonce](../miner/ordering.go#L101)
    `func newTransactionsByPriceAndNonce`
    Builds a per-account head heap ordered by effective tip / time, then feeds block assembly.

## `promoteExecutables` vs `Pending`

These are not in a direct caller-callee relationship.

The actual relationship is:

`runReorg`
-> [promoteExecutables](../core/txpool/legacypool/legacypool.go#L1429)
-> [promoteTx](../core/txpool/legacypool/legacypool.go#L873)
-> update `pool.pending`

Later, a different caller reads that state:

[fillTransactions](../miner/worker.go#L475)
-> `miner.txpool.Pending(filter)`
-> [TxPool.Pending](../core/txpool/txpool.go#L362)
-> [LegacyPool.Pending](../core/txpool/legacypool/legacypool.go#L506)

So the semantic relationship is:

- `promoteExecutables` writes executable transactions into `pool.pending`
- `Pending` reads `pool.pending` and exports it to miner / sync logic

This means the chain is stage-based, not a direct function call:

`runReorg -> promoteExecutables -> pool.pending updated`

then later:

`miner.fillTransactions -> TxPool.Pending -> LegacyPool.Pending`

## Validation Script

To test low-gas flooding and observe txpool pruning behavior, use:

- [txpool_low_gas_flood.py](../scripts/txpool_low_gas_flood.py#L1)

Key entrypoints in the script:

- [rpc](../scripts/txpool_low_gas_flood.py#L13)
- [main](../scripts/txpool_low_gas_flood.py#L28)

The script:

- reads the sender pending nonce
- sends many low-gas-price transactions through `eth_sendTransaction`
- prints accepted/rejected results
- reads `txpool_status` at the end
