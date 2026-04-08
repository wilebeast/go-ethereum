# Geth 交易池排队机制分析

## 入口路径

外部交易进入内存池的主路径是 `TxPool.Add -> LegacyPool.Add -> addTxsLocked -> add`。P2P 网络侧在 `eth/protocols/eth/handlers.go` 接收 `TransactionsMsg` 或 `PooledTransactionsMsg` 后调用 backend `Handle`，最终进入 txpool；本地 RPC 提交也会走相同的 txpool 校验与插入逻辑。

交易插入前先做静态校验：交易类型、大小、签名、gas tip 下限、intrinsic gas、fee cap/tip cap 等。进入 `LegacyPool.add` 后会做状态相关校验：nonce 是否过旧、余额是否足够覆盖已 pending 交易和新交易成本、EIP-7702 授权限制、跨 subpool 地址 reservation。

## 内存结构

LegacyPool 使用三层索引：

1. `all *lookup`：全局 hash -> tx 索引，用于查重、P2P 按 hash 回答 `GetPooledTransactions`、RPC 查询。
2. `pending map[Address]*list`：可执行交易。每个账户内部必须 nonce 连续，`list` 底层是 `SortedMap`，按 nonce 排序。
3. `queue *queue`：future/non-executable 交易。典型原因是 nonce gap，账户内部可以有缺口，也按 nonce 索引。
4. `priced *pricedList`：全局低价堆，用于池满时找淘汰候选。它包含 urgent/floating 两个 heap，urgent 以当前/下一区块 effective tip 为主，floating 以 fee cap 为主。

## 排队与提升

新交易通常先进入 `queue.add`。`requestPromoteExecutables` 触发异步 reorg loop 后，`queue.promoteExecutables` 会检查指定账户：

1. 低于链上 nonce 的交易被丢弃。
2. 余额不足或超过 block gas limit 的交易被丢弃。
3. 从 `pendingNonces` 开始 nonce 连续的交易被 `Ready` 取出并提升到 `pending`。
4. 单账户 future queue 超过 `AccountQueue` 时，按最高 nonce 端裁剪。

`pending` 中每个账户内部仍按 nonce 递增。构块时，miner 调用 `txpool.Pending(filter)` 拿到账户分组，再由 `miner/ordering.go` 的 `transactionsByPriceAndNonce` 在“每个账户当前 head 交易”之间按 effective tip 排序；某账户 head 被选中后才会 `Shift` 到该账户下一 nonce，保证 nonce 约束不被打破。

## 丢弃机制

主要丢弃路径如下：

1. 基础校验失败：`ErrAlreadyKnown`、签名错误、tip 低于 `PriceLimit`、fee cap 低于 basefee、交易过大等。
2. 替换失败：相同 nonce 的新交易必须同时提高 fee cap 和 tip cap，并满足 `PriceBump` 百分比，否则返回 `ErrReplaceUnderpriced`。
3. 池满淘汰：当 `all.Slots()+numSlots(tx) > GlobalSlots+GlobalQueue`，先用 `priced.Underpriced(tx)` 判断新交易是否不如池内最便宜交易；若不是，则 `priced.Discard` 弹出若干低价交易腾空间。
4. future 交易保护：gapped/future 交易不能为了入池而挤掉 pending 交易，否则返回 `ErrFutureReplacePending`。
5. pending 全局限额：`truncatePending` 对超过 `AccountSlots` 的大户做近似公平裁剪，优先剪掉高 nonce 尾部。
6. queue 全局限额：`queue.truncate` 按账户 heartbeat 老旧程度淘汰 future 交易。
7. queue 生命周期：后台 `loop` 每分钟执行一次，超过 `Lifetime` 未活跃的 queued 账户被清理。
8. gas tip 重定价：`SetGasTip` 提高门槛时，所有低于新 tip 的交易被删除。

## P2P 传播

当交易从 queue 提升到 pending 或 pending 替换成功时，`queueTxEvent` 将交易放入 reorg loop 的事件集合，最后通过 `txFeed.Send(NewTxsEvent)` 发出。`eth/handler.go` 的 `txBroadcastLoop` 订阅该事件并执行广播。

对等节点同步时，`syncTransactions` 从 `txpool.Pending(PendingFilter{BlobTxs:false})` 收集 hash，并通过 `NewPooledTransactionHashes` 宣告。远端若缺交易，会发送 `GetPooledTransactions`，本节点再从 `TxPool.GetRLP(hash)` 取 RLP 返回。

## 本次排序修改

本次在 `core/txpool/legacypool/legacypool.go` 增加实验性配置：

```go
PriorityAccounts []common.Address
PriorityFeeBoost uint64
```

`LegacyPool.Pending()` 构造 `LazyTransaction` 时，如果 sender 属于 `PriorityAccounts`，会把返回给 miner 以及其他读取 lazy fee 元数据的调用方的 `GasFeeCap` 和 `GasTipCap` 增加 `PriorityFeeBoost`。这不会改变交易本体、hash、签名、真实 gas price 或链上执行结果，只影响本地候选排序视图。P2P 初始同步同样调用 `Pending()` 收集 hash，但当前同步逻辑只使用 hash，不使用这里的 fee 权重。

代码片段：

```go
gasFeeCap := uint256.MustFromBig(txs[i].GasFeeCap())
gasTipCap := uint256.MustFromBig(txs[i].GasTipCap())
if prioritized {
    gasFeeCap = boostFeeCap(gasFeeCap, pool.config.PriorityFeeBoost)
    gasTipCap = boostFeeCap(gasTipCap, pool.config.PriorityFeeBoost)
}
```

## 低 Gas 攻击/测试脚本

新增脚本：`scripts/txpool_low_gas_flood.py`。

示例：

```bash
python3 scripts/txpool_low_gas_flood.py \
  --rpc http://127.0.0.1:8545 \
  --from 0xYourUnlockedSender \
  --to 0xRecipient \
  --count 500 \
  --gas-price 0x1
```

观察点：

1. `txpool_status` 的 pending/queued 数量变化。
2. geth 日志中的 `Discarding underpriced transaction`、`Removed fairness-exceeding pending transaction`、`Removing cap-exceeding queued transaction`。
3. 调高本地门槛后执行 `txpool.SetGasTip` 对应路径，或重启节点设置更高 `--txpool.pricelimit`，低价交易会被拒绝或清理。

仅在本地 devnet 使用该脚本，不要对公共网络或第三方节点执行洪泛测试。
