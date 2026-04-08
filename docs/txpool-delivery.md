# Geth 交易池与 P2P 网络: 任务交付版

## 1. 任务目标

本次任务围绕 Geth 的 `core/txpool` 与交易传播路径展开，目标分为三部分：

1. 深度分析交易如何进入内存池、如何被排序、如何被丢弃。
2. 修改 TxPool 排序相关逻辑，实现“特权账户”优先打包机制。
3. 编写脚本模拟大量低 Gas 价格交易，观察 TxPool 的剔除行为。

本次实现以普通交易池 `core/txpool/legacypool` 为核心，P2P 部分补充分析交易广播、同步和拉取路径。

## 2. 深度分析

### 2.1 主调用链

主链路可参考：

- [txpool-call-chain.md](../docs/txpool-call-chain.md)
- [txpool-flow-source.txt](../docs/txpool-flow-source.txt)

核心路径为：

`TxPool.Add -> LegacyPool.Add -> add -> enqueueTx -> requestPromoteExecutables -> runReorg -> promoteExecutables -> Pending -> miner`

需要强调的是，`promoteExecutables` 并不会直接调用 `Pending`。真实关系是：

`runReorg -> promoteExecutables -> promoteTx -> pool.pending updated`

然后在后续构块阶段：

`miner.fillTransactions -> TxPool.Pending -> LegacyPool.Pending`

也就是：

- `promoteExecutables` 负责把可执行交易写入 `pool.pending`
- `Pending` 负责把 `pool.pending` 读出来交给 miner

### 2.2 交易如何进入内存池

入口统一在：

- [TxPool.Add](../core/txpool/txpool.go#L314)

普通交易进入 legacy pool 后经过：

- [LegacyPool.Add](../core/txpool/legacypool/legacypool.go#L935)
- [LegacyPool.add](../core/txpool/legacypool/legacypool.go#L688)

主要校验分两层：

1. 静态校验
   - `ValidateTxBasics`
   - 签名、交易类型、大小、最低 tip、intrinsic gas
2. 状态校验
   - `validateTx`
   - nonce、余额、授权限制、替换规则

若交易通过校验：

- 如果是替换现有 pending nonce，则尝试直接替换 pending 交易
- 否则先进入 future queue

对应实现：

- [enqueueTx](../core/txpool/legacypool/legacypool.go#L849)
- [queue.add](../core/txpool/legacypool/queue.go#L117)

### 2.3 内存结构

`LegacyPool` 关键结构：

- [LegacyPool](../core/txpool/legacypool/legacypool.go#L232)

主要字段：

1. `all`
   - 全局 hash 索引
   - 用于查重、按 hash 查询交易、P2P 按 hash 返回交易
2. `pending`
   - 可执行交易集
   - 按账户分组
   - 每个账户内 nonce 必须连续
3. `queue`
   - future / non-executable 交易集
   - 按账户分组
   - 可以有 nonce gap
4. `priced`
   - 全局价格结构
   - 用于池满时淘汰低价交易
5. `pendingNonces`
   - 虚拟 nonce 追踪器
   - 表示链上 nonce 加上 pending 后的下一 nonce

账户内部结构：

- [list](../core/txpool/legacypool/list.go#L272)
- [SortedMap](../core/txpool/legacypool/list.go#L57)

每个账户内永远按 nonce 排序。

### 2.4 交易如何被排序

排序要分两层理解。

#### 账户内排序

账户内永远按 nonce 排：

- [SortedMap](../core/txpool/legacypool/list.go#L57)
- [list.Ready](../core/txpool/legacypool/list.go#L430)

这保证交易执行不会破坏账户 nonce 顺序。

#### 全局淘汰排序

池满时不是简单随机淘汰，而是通过：

- [pricedList](../core/txpool/legacypool/list.go#L543)

`pricedList` 使用两个价格堆：

1. `urgent`
   - 更偏向 effective tip
2. `floating`
   - 更偏向 fee cap

关键逻辑：

- [Underpriced](../core/txpool/legacypool/list.go#L586)
- [Discard](../core/txpool/legacypool/list.go#L619)
- [cmp](../core/txpool/legacypool/list.go#L496)

比较顺序：

1. effective tip
2. GasFeeCap
3. GasTipCap
4. nonce tie-break

这个排序主要服务于“谁被挤出池子”，不是最终 block 打包顺序。

#### 构块排序

真正的打包排序在 miner 侧：

- [fillTransactions](../miner/worker.go#L475)
- [newTransactionsByPriceAndNonce](../miner/ordering.go#L101)

流程：

1. miner 读取 `txpool.Pending(filter)`
2. 拿到 `map[address][]LazyTransaction`
3. 每个账户只取 head tx 进入全局堆
4. 全局堆按 effective tip 和 time 排序
5. 某账户 head 被选中后，才暴露该账户下一个 nonce 交易

因此 Geth 的核心排序思想是：

- 账户内：严格 nonce 顺序
- 账户间：按 head tx 的价格竞争

### 2.5 交易如何被丢弃

主要路径如下：

1. 基础校验失败
   - `ErrAlreadyKnown`
   - 签名错误
   - tip 太低
   - intrinsic gas 不足
   - 交易过大

2. 状态校验失败
   - nonce 过旧
   - 余额不足
   - delegated account inflight 限制
   - authority reserved

3. 替换失败
   - 同 nonce 替换未满足 `PriceBump`
   - `ErrReplaceUnderpriced`

4. 池满拒绝
   - 新交易比池内最便宜交易还差
   - `ErrUnderpriced`

5. 池满淘汰
   - `priced.Discard` 驱逐低价交易腾空间

6. future 保护
   - future tx 不能通过挤掉 pending tx 来入池
   - `ErrFutureReplacePending`

7. queue 生命周期淘汰
   - [queue.evictList](../core/txpool/legacypool/queue.go#L49)

8. queue 超限裁剪
   - [truncateQueue](../core/txpool/legacypool/legacypool.go#L1541)

9. pending 公平性裁剪
   - [truncatePending](../core/txpool/legacypool/legacypool.go#L1446)

10. 链头变化导致降级或移除
   - [demoteUnexecutables](../core/txpool/legacypool/legacypool.go#L1565)

### 2.6 P2P 网络部分

P2P 交易接收与传播的关键点：

1. 接收路径
   - [handleTransactions](../eth/protocols/eth/handlers.go#L479)
   - [handlePooledTransactions](../eth/protocols/eth/handlers.go#L514)
   - 收到交易后交给 backend，再进入 txpool

2. 广播路径
   - [txBroadcastLoop](../eth/handler.go#L517)
   - 订阅 `NewTxsEvent`
   - 新交易提升到 pending 后广播给对等点

3. 交易同步
   - [syncTransactions](../eth/sync.go#L25)
   - 新 peer 连接后，从 `txpool.Pending` 收集 hash 并同步

4. 对等方按 hash 拉取交易
   - [handleGetPooledTransactions](../eth/protocols/eth/handlers.go#L454)
   - [answerGetPooledTransactions](../eth/protocols/eth/handlers.go#L464)

结论：

- TxPool 决定本地交易状态与可执行性
- P2P 决定交易如何被扩散、如何被按 hash 请求和响应
- 两者通过 `NewTxsEvent` 和 `Pending/GetRLP` 接口耦合

## 3. 代码修改: 特权账户插队机制

### 3.1 修改目标

实现一个实验性的“特权账户”本地优先机制：

- 指定地址的交易在 miner 获取 `Pending()` 时拥有更高本地排序权重
- 不改变交易签名
- 不改变 hash
- 不改变链上真实 gas 字段
- 不改变 txpool 的合法性、替换、驱逐规则

### 3.2 改动点

关键改动位置：

1. [Config 新字段](../core/txpool/legacypool/legacypool.go#L140)
   - `PriorityAccounts []common.Address`
   - `PriorityFeeBoost uint64`

2. [LegacyPool.priority](../core/txpool/legacypool/legacypool.go#L232)
   - 维护特权地址集合

3. [New](../core/txpool/legacypool/legacypool.go#L267)
   - 初始化 `priority` map

4. [Pending](../core/txpool/legacypool/legacypool.go#L506)
   - 真正的插队逻辑放在这里

5. [boostFeeCap](../core/txpool/legacypool/legacypool.go#L563)
   - 做 uint256 安全加权

### 3.3 为什么改在 `Pending()`

选择 `Pending()` 而不是 `promoteExecutables()` 或 miner 的原因：

1. 不破坏 txpool 内部状态机
   - queue 和 pending 的转换规则保持原样
2. 不影响合法性判断
   - underpriced、replace、balance、nonce 逻辑不变
3. 不修改交易本体
   - 仅修改导出给 miner 的 `LazyTransaction` 元数据
4. 对实验友好
   - 改动小，边界清晰，容易回滚

### 3.4 修改后的代码片段

代码位于：

- [legacypool.go#L536](../core/txpool/legacypool/legacypool.go#L536)

```go
if len(txs) > 0 {
	lazies := make([]*txpool.LazyTransaction, len(txs))
	_, prioritized := pool.priority[addr]
	for i := 0; i < len(txs); i++ {
		gasFeeCap := uint256.MustFromBig(txs[i].GasFeeCap())
		gasTipCap := uint256.MustFromBig(txs[i].GasTipCap())
		if prioritized {
			gasFeeCap = boostFeeCap(gasFeeCap, pool.config.PriorityFeeBoost)
			gasTipCap = boostFeeCap(gasTipCap, pool.config.PriorityFeeBoost)
		}
		lazies[i] = &txpool.LazyTransaction{
			Pool:      pool,
			Hash:      txs[i].Hash(),
			Tx:        txs[i],
			Time:      txs[i].Time(),
			GasFeeCap: gasFeeCap,
			GasTipCap: gasTipCap,
			Gas:       txs[i].Gas(),
			BlobGas:   txs[i].BlobGas(),
		}
	}
	pending[addr] = lazies
}
```

### 3.5 语义效果

这条修改后的链路是：

`runReorg -> promoteExecutables -> promoteTx -> pool.pending updated`

之后：

`miner.fillTransactions -> TxPool.Pending -> LegacyPool.Pending`

在 `LegacyPool.Pending` 中：

- 如果 sender 在 `PriorityAccounts` 里
- 就提升导出的 `LazyTransaction.GasTipCap` 与 `GasFeeCap`
- miner 排序时会把它视为更“贵”的交易

因此这个特性本质上是：

- 本地调度层优先
- 不是共识层修改
- 不是交易内容修改

### 3.6 为什么 boost 只影响排序而不影响成本

关键原因是：被修改的是 `LazyTransaction` 的导出字段，不是 `types.Transaction` 本体。

相关定义：

- [LazyTransaction](/home/star/go-ethereum/core/txpool/subpool.go#L33)
- [LazyTransaction.Resolve](/home/star/go-ethereum/core/txpool/subpool.go#L52)

`LazyTransaction` 同时持有两类信息：

1. 原始交易对象
   - `Tx *types.Transaction`
2. 供下游快速排序/过滤使用的缓存字段
   - `GasFeeCap *uint256.Int`
   - `GasTipCap *uint256.Int`

本次修改只改了第二类字段：

- [legacypool.go#L546](/home/star/go-ethereum/core/txpool/legacypool/legacypool.go#L546)

这里仍然保留：

- `Tx: txs[i]`

也就是说：

- `LazyTransaction.GasFeeCap/GasTipCap` 是 boost 后的排序视图
- `LazyTransaction.Resolve()` 返回的还是原始 `types.Transaction`

miner 排序为什么会吃到 boost：

- [newTxWithMinerFee](/home/star/go-ethereum/miner/ordering.go#L37)

这里读取的是 `*txpool.LazyTransaction` 上的：

- `tx.GasTipCap`
- `tx.GasFeeCap`

因此排序会受到 boost 影响。

但真实成本、执行和链上计费为什么不受影响：

- 原始交易仍然通过 `Resolve()` 或已有的 `Tx` 字段传递
- `types.Transaction` 的真实 fee/cost 逻辑在：
  - [transaction.go](/home/star/go-ethereum/core/types/transaction.go#L301)
  - [transaction.go](/home/star/go-ethereum/core/types/transaction.go#L304)
  - [transaction.go](/home/star/go-ethereum/core/types/transaction.go#L317)

这些真实字段和 `Cost()` 计算都没有被改。

因此可以确定：

- 影响的是本地排序
- 不影响交易本体
- 不影响 `tx.Cost()`
- 不影响签名和 hash
- 不影响 EVM 执行时的真实 gas 计费

对应验证单测：

- [TestPriorityAccountPendingFeeBoost](/home/star/go-ethereum/core/txpool/legacypool/legacypool_test.go#L2620)

该测试同时断言：

1. boosted `LazyTransaction.GasTipCap` 已经变化
2. `LazyTransaction.Resolve().GasTipCap()` 仍然是原值
3. 原始 `priorityTx.GasTipCap()` 仍然是原值

## 4. 模拟测试

### 4.1 脚本

测试脚本：

- [txpool_low_gas_flood.py](../scripts/txpool_low_gas_flood.py#L1)

关键函数：

- [rpc](../scripts/txpool_low_gas_flood.py#L13)
- [main](../scripts/txpool_low_gas_flood.py#L28)

### 4.2 脚本作用

脚本会：

1. 读取发送账户当前 pending nonce
2. 连续构造大量低 gas price 交易
3. 通过 `eth_sendTransaction` 发送到本地节点
4. 打印每笔交易是 accepted 还是 rejected
5. 最后调用 `txpool_status` 观察池状态

### 4.3 使用方法

示例：

```bash
python3 scripts/txpool_low_gas_flood.py \
  --rpc http://127.0.0.1:8545 \
  --from 0xYourUnlockedSender \
  --to 0xRecipient \
  --count 500 \
  --gas-price 0x1
```

要求：

- 只用于本地开发节点或私链
- `from` 地址必须在本节点已解锁

### 4.4 如何验证“特权账户插队”是否生效

建议测试方案：

1. 准备两个账户
   - 普通账户 A
   - 特权账户 B

2. 给两者都发送可执行交易
   - nonce 连续
   - 实际 gas 参数相近

3. 设置：
   - `PriorityAccounts = [B]`
   - `PriorityFeeBoost > 0`

4. 观察 miner 构块时的选择顺序

预期结果：

- 若 A、B 原始价格接近
- B 在 `Pending()` 导出阶段会得到更高本地排序权重
- miner 会更早选择 B 的 head tx

### 4.5 如何观察低 Gas 剔除机制

重点观察：

1. `txpool_status`
2. geth 日志
3. 新交易返回错误

典型现象：

- 低价交易被直接拒绝
- 池满后更低价交易被淘汰
- queue 超限后旧账户交易被驱逐
- pending 超限后大户尾部交易被裁剪

## 5. 预期现象总结

### 5.1 正常路径

- 新交易先通过静态校验和状态校验
- 非连续 nonce 交易先进 queue
- 一旦 nonce 连续且余额足够，就被 promote 到 pending
- miner 只从 pending 取交易

### 5.2 特权账户路径

- 不改变 queue / pending 的转移规则
- 不改变交易是否能进池
- 只改变 miner 看到的排序权重

### 5.3 低 Gas 攻击路径

- 若 price limit 更高，直接被拒绝
- 若池已满，低价交易被判定 `Underpriced`
- 若 queue 太大，老旧账户 future tx 被驱逐
- 若 pending 太大，大账户高 nonce 交易被裁掉

## 6. 风险与限制

### 6.1 当前修改的限制

1. 这是本地调度优化
   - 不改变网络上其他节点的排序
   - 不改变共识规则

2. 影响的是 `Pending()` 导出的视图
   - 更适合实验、私链、定制节点
   - 不适合直接声称为“全网优先”

3. 若 boost 过大
   - 会明显偏离真实 gas 市场
   - 可能牺牲 fee maximization

### 6.2 P2P 相关限制

1. 交易广播仍按正常 `NewTxsEvent` 驱动
2. 特权机制不改变交易传播协议
3. 远端节点不会自动承认你的本地优先排序

### 6.3 测试限制

1. 脚本依赖 unlocked account
2. `eth_sendTransaction` 更适合 devnet
3. 不应用于公共网络节点或第三方服务

## 7. 交付内容

本次交付包括：

1. 深度分析报告
   - [txpool-queue-analysis.md](../docs/txpool-queue-analysis.md)
   - [txpool-delivery.md](../docs/txpool-delivery.md)

2. 流程与调用链文档
   - [txpool-flow.txt](../docs/txpool-flow.txt)
   - [txpool-flow-source.txt](../docs/txpool-flow-source.txt)
   - [txpool-call-chain.txt](../docs/txpool-call-chain.txt)
   - [txpool-call-chain.md](../docs/txpool-call-chain.md)

3. 修改后的 TxPool 代码
   - [legacypool.go](../core/txpool/legacypool/legacypool.go)

4. 测试脚本
   - [txpool_low_gas_flood.py](../scripts/txpool_low_gas_flood.py)

## 8. 结论

Geth 的交易池机制可以概括为：

- 进入：统一从 `TxPool.Add` 进入，再路由到 `LegacyPool`
- 排队：future 交易先进 queue
- 提升：满足 nonce 与余额条件后进入 pending
- 排序：账户内按 nonce，账户间由 miner 按价格取 head tx
- 驱逐：由 underpriced、容量、公平性、生命周期和链头变化共同决定

本次“特权账户插队机制”没有破坏 txpool 基础状态机，而是在 `Pending()` 导出给 miner 的视图层做本地优先级增强。这种方式实现简单、边界清晰、便于实验验证，适合作为深入理解和修改 Geth TxPool 的一个工程化切入点。
