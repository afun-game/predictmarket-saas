# v0.2.0 版本计划：从「功能完成」到「资金安全可上线」

> **状态（2026-07-30 上线复核）**：2026-07-29 的真库终验结论因 schema
> 漂移而失效：`orders.time_in_force` 被生产仓储读写，却未包含在 001–009 迁移中。
> 已新增 `010_add_time_in_force.sql`；独立全新 PostgreSQL 已验证 Goose 001–010、
> 订单 PostgreSQL 集成测试和 reconciliation 集成测试通过，且 010 可 down/up 往返。
> 发布仍以包含该迁移的提交在 GitHub Actions `Verify` 工作流实际全绿为准。
> 其余遗留事项见文末「终验遗留清单」。

## 一、当前真实基线（已实测，非估算）

| 指标 | 实测值 | 说明 |
|------|--------|------|
| 生产代码 | 10,699 行 | 不含测试与生成代码 |
| 测试代码 | 7,069 行 | 单测 + 集成 + e2e |
| 生成代码 | 11,153 行 | `twill_gen.go` |
| 文件总数 | 148 个 | 不含 vendor |
| `go build ./...` | ✅ 通过 | 需 `-buildvcs=false` |
| `go vet ./...` | ✅ 通过 | 无告警 |
| `go test ./...` | ✅ 全部通过 | 19 个包 |
| `gofmt` 漂移 | ✅ 无 | 格式干净 |
| `panic("not implemented")` | 0 处 | 骨架阶段已结束 |

### 测试覆盖率分布（暴露核心风险）

| 包 | 覆盖率 | 评价 |
|----|--------|------|
| `internal/auth` | 96.8% | 好 |
| `pkg/polymarket` | 73.9% | 好 |
| `internal/httpapi` | 67.9% | 可接受 |
| `internal/eventsync` | 30.2% | 偏低 |
| `internal/currency` | 29.8% | 偏低 |
| `internal/event` | 29.1% | 偏低 |
| `internal/settlement` | 24.8% | **资金路径，偏低** |
| `internal/merchant` | 24.2% | 偏低 |
| `internal/market` | 22.8% | **资金路径，偏低** |
| `internal/order` | 22.6% | **资金路径，偏低** |
| `internal/wallet` | 22.0% | **资金路径，偏低** |
| `internal/settlementworker` | 16.6% | **资金路径，偏低** |
| `internal/sports` | 14.1% | 低 |
| `internal/analytics` | 8.1% | 低 |
| `internal/infra` | 0% | 无测试 |
| `internal/messaging` | 0% | 无测试 |

**根因**：`postgres_repository.go` 平均覆盖率仅 **5.7%**（82 个函数），全项目 **503 个函数 0% 覆盖**。
集成测试写得规范，但全部以 `t.Skip("set INTEGRATION_TEST=1 ...")` 默认跳过，
且**没有 CI 去执行它们**。所以真正的问题不是"没写测试"，而是"测试从未被自动执行"。

---

## 二、已确认的缺陷清单

### P0 — 阻塞上线（资金安全）

#### 0. 撮合价格完全不参与派彩，赔率恒为 2 倍（最严重）

`internal/settlement/postgres_repository.go:145-155` 的结算 SQL 未选取 `o.price`，
`internal/settlement/settlement.go:77-88` 的 `settlementOrder` 结构体也没有 price 字段。
`price` 仅在 `internal/order/postgres_repository.go:297-302` 作为撮合交叉条件使用，
**一旦成交就被永久丢弃**。

派彩是纯 parimutuel（`internal/settlement/settlement.go:111-141`）：
```
payout = totalPool × filled / totalWinningStake
```
其中 `totalPool` 是买卖双方 filled 之和。

**失败场景**：A 以 `price=0.99` 买 YES 100 元，B 以 `price=0.99` 卖 YES 100 元，
按金额 1:1 撮合 → 池 = 200，赢家 stake = 100，payout = 200。
把 price 换成 `0.01`，结果**完全相同**。
即：无论在什么概率下成交，赢家永远拿 2 倍，输家永远归零。
`0.99` 的买方本应只赚约 1%，实际赚 100%——平台或对手方系统性亏损。

**经济模型已定案：份额模型（share model）**——`Amount` = 份额数，`Price` = 每份额价格，
取值 0~1 表示概率。这是标准预测市场语义，也是 Polymarket CLOB 的做法。

**重要认定：这不是引入新方案，而是恢复代码原本的设计意图。**
撮合层已经是份额语义且实现正确（`internal/order/postgres_repository.go:68-75`）：
```go
incomingRemaining := incoming.Amount - incoming.FilledAmount
fillAmount := min(incomingRemaining, maker.Amount-maker.FilledAmount)
```
买卖双方按**数量**配对，价格仅作交叉判定（`queryMatchingOrders` 中买单 `price <= $7`、
卖单 `price >= $7`），排序为价格优先 + 时间优先——标准 CLOB 撮合。
**这只有在 amount 是份额时才成立**：若 amount 是金额，双方金额相同而价格不同时数量无法匹配。

schema 亦印证该意图：`price DECIMAL(10,6)`（概率精度）配 `amount DECIMAL(20,2)`，
price 显然是单价而非金额。

**因此真正的缺陷只有两处语义退化**：

1. **锁仓少乘价格** —— `internal/order/order.go:161-167` 锁的是 `input.Amount`（份额数），
   份额模型下应锁 `Amount × Price`（金额）。
   买 100 份 @ 0.30 应冻结 30 元，现冻结 100 元。
2. **派彩无成交价可用** —— 结算 SQL 未选 `price`，`settlementOrder` 无该字段。
   份额模型下派彩很简单：**每份获胜份额兑付 1.00**，
   当前的 parimutuel 分池公式应整体替换。

#### 0a. 成交价未被持久化，价格改善退款完全缺失

`orders.price` 是**委托价**，不是**成交价**。撮合时买单 `price <= maker.price`，
实际成交在 maker 价（价格优先原则），两者不等。
买方以 0.35 委托、成交在 maker 的 0.30，差额 0.05/份必须退还。

但当前**没有任何列记录成交价**，撮合完成后该信息即丢失，退款无从计算。

**修复方向**：新增 `trades` 表
（`market_id, taker_order_id, maker_order_id, shares, matched_price, created_at`），
而非在 orders 上加 `avg_matched_price` —— 一个订单可能与多个 maker 在不同价位成交，
单列存不下；且 trades 表对成交流水 API、K 线、审计都是必需的。

**份额模型下的完整资金流**：
```
下单   冻结 = shares × limit_price
成交   实付 = shares × matched_price          （每笔 trade 记录）
       退还 = shares × (limit_price - matched_price)   ← 当前完全缺失
撤单   退还 = 未成交份额 × limit_price
结算   获胜份额 × 1.00，失败份额 × 0
```

#### 0a2. 卖方语义已限定为二元市场

预测市场的「卖」通常指卖出持仓份额（需先有持仓），或等价于买入对立选项。
当前 `orders` 表**无持仓概念**（无 positions 表），
`internal/settlement/settlement.go:143-146` 的 `orderWins` 用
`side == "sell" && option != winningOption` 把卖单当作押注对立面。

这在**二元市场成立**，但多选市场下「卖 A」不等于「买 B」（可能是 B 或 C）。

**已确认**：MVP 只支持二元市场。`market.Create` 拒绝非 `binary` 类型以及非两个
唯一选项；当前 `orderWins` 逻辑可保留。多选市场将来必须引入 `positions` 表，卖单
需校验持仓充足，锁仓改为锁份额而非锁资金。

#### 0b. `market.Settle` 是「假结算」：翻状态、不派彩、丢弃 winningOption

`internal/market/market.go:229-241` 校验了 `winningOption` 非空且必须是合法选项，
但 `:244` 调用 `s.repository.Settle(ctx, marketID, value.Status, s.now().UTC())`
**根本没有把 winningOption 传下去**。仓储层
（`internal/market/postgres_repository.go:202-206`）只执行
`UPDATE markets SET status='settled', settled_at=$3`。

**失败场景**：管理员 `POST /api/v1/markets/{id}/settle` 指定获胜选项 → 返回成功，
market 变 `settled`，但**无任何派彩、无 `market_settlements` 记录、所有 pending 订单仍 pending、
所有 `locked_balance` 仍锁着**。而 `settled` 是终态（`internal/market/market.go:399-408`
的 `canTransition` 不允许再转出）→ 该 market 彻底冻死，
只有对应 event 恰好被 resolve 并走 settlementworker 才可能救回。
**管理员会以为钱已经派完了。**

**修复方向**：删除该端点，或让它委托给 `settlement.Service`，
并校验 winningOption 与 event outcome 一致。

#### 0c. 结算用 INNER JOIN wallets，缺钱包的订单被静默丢弃

`internal/settlement/postgres_repository.go:148-152` 是内连接。
任何匹配不到钱包行的订单**不报错，直接从结算列表消失**：
既不派彩、`locked_balance` 也不释放。而 market 仍被标记 settled 并写入
`market_settlements`，之后 `lockUnsettledMarkets` 会因 `s.market_id IS NULL`
过滤掉它，**永远不会重新结算**。资金静默丢失且不可恢复。

**修复方向**：改 LEFT JOIN，`w.id IS NULL` 时显式报错中止；
并在结算末尾增加「派出总额 == 池内总额」的断言。

#### 0d. 结算 worker 单条毒消息可让整个队列停摆

`internal/settlementworker/worker.go:178-198` 是 `for {}` 无限重试，
**无重试上限、无死信队列**，仅在 `SettleEvent` 成功后才 Ack。
（解码失败会 Ack，这处是对的；但业务错误永久重试。）

叠加两个放大因素：
1. 事务边界是**事件级**而非 market 级（`internal/settlement/postgres_repository.go:39-43`），
   循环内任一 market 失败即整个事件回滚，合法 market 一起结算不了
2. 消费者是**单 goroutine 顺序处理**（`worker.go:148-167`）

**失败场景**：某 market 的 `options` 不含赛事结果（体育源给 `"Draw"`，
商户建的是 binary `["Home","Away"]`）→ `ErrOutcomeNotOption`
（`internal/settlement/postgres_repository.go:92-93`）永久返回错误 →
以最长 30s 间隔无限重试 → **队列后面所有事件的结算全部停摆，
所有用户资金无限期锁定**。

**修复方向**：区分可重试错误（DB 抖动）与不可重试错误（非法结果）；
后者走死信 + 告警 + Ack；事务边界改为 market 级；重试加上限。

#### 1. 下单路径跨两个独立事务，存在资金泄漏窗口
`internal/order/order.go:161-209`

`wallets.Lock()` 与 `repository.Place()` 是**两个独立数据库事务**，中间任何失败
（进程崩溃、网络中断、UUID 生成失败）都靠补偿逻辑挽回，而补偿本身**丢弃错误**：

```go
// internal/order/order.go:305-307
func (s *implementation) bestEffortUnlock(ctx context.Context, req *CreateRequest, amount float64) {
	_ = s.wallets.Unlock(ctx, req.MerchantID, req.UserID, req.Currency, amount)
}
```

**失败场景**：用户下单 100，`Lock` 成功（余额 → locked），`Place` 失败，
`bestEffortUnlock` 也失败（Redis/DB 抖动）→ 这 100 永久卡在 `locked_balance`，
无任何对账或补偿任务能发现和回收。用户看到余额少了但没有订单。

同样问题在 `internal/order/order.go:198-207`（IOC 剩余量解锁）
和 `internal/order/order.go:251-259`（取消订单：仓储已提交取消，`Unlock` 独立执行，失败则资金锁死）。

**修复方向**：将钱包锁定与订单写入合并进单个 `*sql.Tx`；
若因组件边界无法合并，则必须新增「滞留资金对账任务」定期扫描
`locked_balance` 与 `pending` 订单的差额并回收。

#### 2. 资金入口用 float64，出口才用精确算术（只做了一半）

| 包 | 金额表示 | 状态 |
|----|----------|------|
| `internal/settlement` | `big.Int` 分单位 + `formatCents()` | ✅ 正确 |
| `internal/currency` | `decimal.go` | ✅ 正确 |
| `internal/wallet` | `float64` | ❌ |
| `internal/order` | `float64` | ❌ |
| `internal/market` | `float64` | ❌ |

`pkg/types/types.go:71-90` 中 `Amount`、`FilledAmount`、`Price`、`Balance`、
`LockedBalance` 全部为 `float64`，而数据库列是 `DECIMAL(20,2)`（精确）。
风险不在存储，而在 **Go 侧计算与读写往返的舍入**。

已定位的具体累加点 `internal/order/postgres_repository.go:73-75`：
```go
maker.FilledAmount += fillAmount
incoming.FilledAmount += fillAmount
```
`fillAmount` 在 Go 侧按 float64 计算，写进 `DECIMAL(20,2)` 时被 PG 舍入，
下一次 Place 又从 PG 读回舍入后的值 → **Go 视图与 DB 视图每笔成交最多偏离 0.005**。
`internal/order/memory_repository.go:195-205` 的 `FilledAmount = Amount` 夹紧
只保证 `filled ≤ amount`，差额那部分锁仓无人释放。

**两处实测确认的校验失效**（`internal/wallet/wallet.go:23`、`internal/order/order.go:25`）：

1. `maxAmount = 999_999_999_999_999.99` 在 float64 中**不可表示**，
   实际值是 `1000000000000000.0`，而 `validateAmount` 对它返回 `true`。
   因为 `amount*100 = 1e17` 远超 2^53（≈9.007e15），该量级下 float64 连整数都不连续，
   所谓「最多两位小数」的检查 `|x*100 - round(x*100)| > 1e-9` 完全失效。

2. `1.10` 减去 11 次累加的 `0.1` 得到 `2.22e-16`，
   而 `validateAmount(2.22e-16)` 返回 `true`。
   `internal/order/order.go:248` 的 `if remaining == 0 { return nil }` 用严格相等判零，
   挡不住这种脏值，于是会向 `Unlock` 传入 `2.22e-16`。
   Postgres 会舍成 `0.00`（生产上仅是无效写入），
   但 memory repository（`internal/wallet/memory_repository.go:169-170`）
   会把脏值真实累加进 `LockedBalance`/`Balance`，使内存态余额逐步偏离——
   这会让单元测试与生产行为不一致。

**份额模型下需要两套精度基准，不能混用同一个 cents 类型**：

| 量 | 建议精度 | 理由 |
|----|----------|------|
| 金额（余额、锁仓、派彩、手续费） | 整分（2 位小数） | 对齐 `DECIMAL(20,2)` 与真实货币最小单位 |
| 份额（amount、filled_amount） | 至少 6 位小数 | 对齐 `price DECIMAL(10,6)`；`shares × price` 才能精确落到分 |
| 价格 | 6 位小数（0~1） | 概率精度，已是 `DECIMAL(10,6)` |

当前 `amount DECIMAL(20,2)` 存份额只有两位小数，偏粗：
100.00 份 × 0.333333 = 33.3333 元，无法精确表示为分。
需要在迁移中把 `orders.amount`/`filled_amount` 提升精度（如 `DECIMAL(28,6)`），
并明确 `shares × price` 的舍入规则（建议对用户有利方向舍入，避免系统性侵占）。

**注**：`internal/currency/decimal.go:43-82` 用 `big.Rat` + `big.Int` 做定点运算
和银行家式进位，实现本身是干净的；但唯一调用者 `internal/currency/currency.go:139`
在出口处立刻退回浮点（`return float64(convertedCents) / 100`），
精确性在边界丢失。settlement 包自己另写了一套 `parseCents`/`formatCents`
（`internal/settlement/settlement.go:148-174`）——**两套精确实现并存但都未被复用**。
改造时应统一为一套公共类型（或直接引入 `shopspring/decimal`）。

**修复方向**：把 `settlement` 已验证的分单位（cents）表示上推到
`wallet`/`order`/`market`，`pkg/types` 金额字段改为整数分或统一 decimal 类型。

**注**：钱包 SQL 本身写得稳健，用 `balance = balance - $4 WHERE balance >= $4`
原子条件更新（`internal/wallet/postgres_repository.go:154-155,189-192,216-219`），
已避免读-改-写竞态与负余额。这部分**不需要改**。

#### 3. 自动结算链路的源头是断的
`internal/eventsync/eventsync.go:132-141`

同步时固定传 `Closed: &closed`（false），即**只拉取未结束的事件，从不拉取已结算事件的 outcome**。
结算链路下游（outbox → NATS → `settlementworker` → `settlement`）实现质量不错，
但它依赖有人调用 `event.Resolve()` 写入 outcome，而目前**只有管理员手工 HTTP 接口会调**。

**后果**：事件在 Polymarket 已出结果，本平台仍停在 `closed`，用户资金持续锁在
`locked_balance`，直到运营手工介入。这与「自动化结算」的产品决策直接矛盾。

**修复方向**：在 Polymarket DTO 中补充结算字段，新增一个拉取已结算事件并
写入 outcome 的同步任务；保留人工覆盖能力并留审计痕迹。

#### 3b. `fee_rate` 是死字段，平台零收入

`merchants.fee_rate`（`migrations/001_initial_schema.sql:14`）只在 merchant 包内读写，
`settlement` 从未读取。`internal/wallet/wallet.go:363` 允许 `"fee"` 交易类型，
但全项目**没有任何地方创建 `fee` 交易**。
结果：`totalPool` 原封不动全额分给赢家，平台零收入，
而 API 与文档对外暴露了 fee_rate 语义（商户会以为已在计费）。

同时这也是一个权限问题：商户可自行 `PATCH /merchants/{id}/config`
修改自己的 `fee_rate`（`internal/merchant/merchant.go:298-303`），
一旦接入计费即为直接的收入漏洞。fee_rate 属平台商业条款，不应由被计费方写入。

**当前实施策略**：在后台配置能力完成前，商户费和平台费统一固定为 `0`。
市场创建时固化两类费率快照，收入使用独立 `fee_ledger`，绝不写入终端用户
钱包或 `transactions`。商户 API 不能读写费率、也不能修改状态；数据库约束同样
拒绝非零费率。因此结算当前仍保持 `totalPool == 用户派彩`，不生成零金额收入记录。

**后续启用方向**：管理员配置非零费率后，按市场、币种从抵押池扣除，使用整分计算，
写入 `fee_ledger` 的 merchant/platform 两条收入；届时守恒式改为
`总抵押池 = 用户净派彩 + 商户费 + 平台费`。启用迁移必须显式解除当前的零费率约束，
并且不得让商户获得费率或状态的写权限。

#### 3c. 流动性池对定价和成交毫无作用

`liquidity_pool` 只被写入（Create / AddLiquidity 累加），
**撮合与结算从不读取它**。所以风险不是「被抽干」，而是它是个惰性字段：
没有 AMM、没有做市方，订单只能用户对用户撮合。
一个注入了大量流动性但没有对手方的 market，挂单永远不会成交，
而 `POST /markets/{id}/liquidity` 会让商户以为已具备做市能力。

**修复方向**：要么实现 AMM/做市逻辑让该字段真正参与定价，
要么从 API 中移除该端点并在文档中说明当前为纯 P2P 撮合。

#### 4. 缺少幂等键，下单可重复提交
`orders` 表（`migrations/001_initial_schema.sql:102-117`）无幂等列，
`internal/order/order.go` 无幂等校验。客户端超时重试会产生**两笔真实订单、两次扣款**。

对比：结算侧幂等做得对，靠 `settlement_payouts` 的 `PRIMARY KEY (market_id, order_id)`
（`migrations/001_initial_schema.sql:172-182`）和 `event_outbox` 的
`UNIQUE(event_id, event_type)` 保障，重复投递是已提交的空操作。**下单侧需要补齐同等保护。**

Twill 框架已提供 `middleware.RequireIdempotencyKey()`，接线即可。

---

### P1 — 上线前必须补齐（可用性与运维）

#### 5. 无优雅关闭
`cmd/api/` 无 `signal.Notify`、无 SIGTERM 处理、无 `Shutdown`。
K8s 滚动更新会硬切连接，**进行中的下单/结算请求被中断**——对资金类服务是实际风险。

#### 6. HTTP 层无 panic 恢复
`recover()` 仅存在于生成代码 `twill_gen.go`（组件 RPC 层）。
`internal/httpapi/` 全部 handler 无恢复中间件，**单个 handler panic 打挂整个进程**。
另外 `internal/settlementworker/worker.go` 的后台 goroutine 也无 `recover()`。

#### 7. HTTP server 无读写超时
全项目唯一的 `ReadTimeout` 在 `internal/infra/redis_cache.go:31`（Redis 客户端）。
HTTP server 无 `ReadTimeout`/`WriteTimeout`/`IdleTimeout`/`ReadHeaderTimeout`，
慢客户端可长期占用 goroutine（Slowloris）。

#### 8. 健康检查形同虚设，且 liveness/readiness 不分
`cmd/api/main.go:67` 只注册了 Twill 自带的 `HealthzHandler`，无条件返回 200，
不检查 DB/Redis/NATS 可达性。`k8s/deployment.yaml:50-65` 两个探针指向同一路径。
**后果**：DB 连接池已死的 Pod 仍留在 Service 端点里持续返回 500；
卡死但仍监听的进程永远不会被重启。

#### 9. 无法水平扩容（cron 无分布式锁）
项目**从未注册 cron provider**，因此回退到 Twill 的 `NewMemoryCron()`——
框架源码 `runtime/resource/cron.go:38-39` 明确注明「不适合生产用」。
这是进程内定时器，多副本会各跑一份，导致 Polymarket 同步重复调用、事件重复处理。

这正是 `k8s/deployment.yaml:9` 的 `replicas: 1` 与 `k8s/configmap.yaml:22-23`
的 `max_replicas = 1` 被硬钉死的原因，与 SaaS 定位矛盾。

**低成本解法**：`internal/infra/` 已有 `RegisterRedisCacheProvider()` 和
`RegisterNATSPubSubProvider()` 的成熟范式，唯独漏了 cron。
Twill 已提供 `resource.NewLockedCron(inner, lock, prefix, holder)`，Redis 也已接好，
补一个 `RegisterLockedCronProvider()` 即可解锁多副本。

#### 10. 无限流，公开注册端点可被滥刷
`POST /api/v1/merchants/register`（`internal/httpapi/merchant_handler.go:52`）
是唯一无认证入口，且无限流——可被无限创建商户与 API key。
全项目无任何限流代码。Twill 已提供 `middleware.RateLimit(limit, window)`。

#### 11. 请求体大小限制只覆盖 1/8 的 handler
`MaxBytesReader` 仅出现在 `internal/httpapi/merchant_handler.go:205`。
其余 7 个 handler（含 order、wallet、market 等**资金相关**）全部为 0，
可被超大 JSON body 打爆内存。

#### 12. 可观测性缺口
- **无任何指标**：全项目零 `prometheus` 引用，但 `prometheus.yml` 已配置抓取 `/metrics` —— 抓的是一个 404
- **otel 是死依赖**：`go.mod` 引入 otel，但业务代码零使用，无 trace/span
- **日志无追踪关联**：`cmd/api/logging.go` 只附加静态 `service`/`environment`，
  无 `request_id`/`trace_id`，一次请求的跨组件日志无法串联
- Twill 已提供 `middleware.RequestID()`，未接

#### 13. 无 CI，导致 82 个仓储函数长期未被验证
无 `.github/workflows`。所有质量门禁都是手工 Makefile 目标。
集成测试因默认 skip 而从未自动执行，这是覆盖率 5.7% 的直接原因。

---

### P2 — 应当改进（性能与工程化）

#### 14. 只有一个迁移文件，无版本管理
`migrations/` 仅 `001_initial_schema.sql`，无迁移工具、无版本表、无回滚脚本。
已出现将变更硬塞进初始文件的迹象（`migrations/001_initial_schema.sql:119-120`
用 `ALTER TABLE ... ADD COLUMN IF NOT EXISTS` 补 `filled_amount`/`time_in_force`，
而这两列在 `:110,113` 已定义）。**未来对已有表的任何变更都无法表达。**

#### 14b. 并发结算的钱包加锁顺序不确定，可能死锁

`internal/settlement/postgres_repository.go:153-155` 按 `ORDER BY o.id` 加锁，
但 `FOR UPDATE OF o, w` 锁住的 wallets 行顺序**跟随 order id，与 wallet id 无关**。
两个不同 event 的结算若涉及同一批用户钱包，加锁顺序可能相反 → 死锁，一方回滚。
叠加 #0d 的无限重试，表现为周期性结算失败刷日志。

**修复方向**：先收集需要的 wallet id，`ORDER BY w.id` 单独 `SELECT ... FOR UPDATE` 预锁，
再处理订单。

#### 14c. Outbox 在持有行锁的事务内做网络发布

`internal/settlementworker/postgres_repository.go:31-47`：
`FOR UPDATE SKIP LOCKED` 取 20 条后，在事务内逐条 `publisher.Publish`。
NATS 变慢会把 DB 事务和行锁一起拖长。
（publish 成功但 commit 失败会导致重投，但因结算幂等而无害——
重投时 `lockUnsettledMarkets` 返回空集，安全空转。所以这只是性能与锁竞争问题。）

#### 14d. memory repository 两处复合操作瑕疵

- `internal/wallet/memory_repository.go:76-85`：重复 transactionID 的检查在钱包 upsert
  **之后**，重复提交会留下一个新建的空钱包才报错，不是原子的
- `internal/order/memory_repository.go:106-125`：`Cancel` 在锁内改状态并返回，
  解锁资金发生在锁外的 service 层，与 #1 同构

`Lock`/`Unlock`/`Debit` 本身的锁覆盖是正确的（`walletForUpdate` 全程持写锁，
检查与扣减在同一临界区）。

#### 15. 深分页与全量 COUNT
6 个仓储用 `OFFSET` 分页（`event`/`market`/`merchant`/`order`/`sports`/`wallet`），
`page` 无上限，且每次列表查询前都跑一次无界 `COUNT(*)`，即两趟全表扫描。

#### 16. 缺少 HMAC 签名校验（文档声称有，实际不存在）
README 与需求文档明确写有 `X-Signature: HMAC-SHA256`，但全项目**零 HMAC 代码**。
`internal/merchant/merchant.go:194-212` 的 `ValidateAPIKey` 只校验 api_key，
`api_secret` 生成后哈希入库却**从未在任何认证路径使用**——是死代码。

#### 17. api_key 明文存储
`internal/merchant/merchant.go:138` 对 `api_secret` 做了 SHA-256，
但 `api_key` 明文入库、明文查询（`internal/merchant/postgres_repository.go:82`
`WHERE api_key = $1`）。api_key 是实际认证凭据，**数据库泄露即全部商户身份泄露**。
另外无盐 SHA-256 本身也不适合凭据哈希（应用 bcrypt/argon2）。

**注**：`internal/auth/middleware.go` 质量好——恒定时间比较（`crypto/subtle`）、
空 admin key 拒绝、Bearer 解析严谨，这解释了它 96.8% 的覆盖率。**不需要改。**

#### 18. 文档严重失实，需要清理
- `DELIVERY_REPORT.md`、`SUMMARY.md`、`PROJECT_STATUS.md` 声称「940 行代码 / 28 文件 / 脚手架阶段」，
  实际是 **10,699 行 / 148 文件 / 功能已实现**，相差 11 倍
- 4 份文档（`CHANGELOG.md`、`PROJECT_STATUS.md`、`DELIVERY_REPORT.md`、`SUMMARY.md`、`docs/DEPLOYMENT.md`）
  仍写 **Kafka**，而代码与 `docker-compose.yml` 已改用 **NATS**
- `go.mod:1` 仍是 `github.com/afun-game/predictmarket-saas`
- `go.mod` 末行 `replace github.com/nxsky/twill => /mnt/c/works/solgame/twill`
  是宿主机绝对路径，仅因 `vendor/` 已提交才能在别处构建

---

## 三、v0.2.0 迭代计划（6 周）

### Sprint 0（第 1 周）：定价语义定案 + CI 门禁

目标：**先把经济模型和测试基础设施定下来，否则后续改造无依据**

| # | 任务 | 对应缺陷 | 验收标准 |
|---|------|----------|----------|
| 0.1 | ~~定义 `Amount` 语义~~ | #0 | ✅ **已完成**：采用份额模型，见 #0 |
| 0.2 | 限定 MVP 只支持二元市场（不引入 `positions` 表） | #0a2 | ✅ 已完成：创建时拒绝多选市场；卖单表示所选项的互补结果 |
| 0.3 | 建立 GitHub Actions CI：build / vet / gofmt / 单测 + **带 Postgres/Redis/NATS service 跑集成测试与 e2e** | #13 | ✅ 已完成：`.github/workflows/verify.yml` 执行全链路验证；`INTEGRATION_TEST=1` 在 CI 中生效 |
| 0.4 | 补「赔率断言」测试：买 100 份 @0.30 获胜应得 100.00，@0.60 获胜也应得 100.00，但**成本不同** | #0 | ✅ 已完成：`v2regression` 回归测试验证获胜份额按 1.00 兑付 |
| 0.5 | 补「价格改善退款」测试：以 0.35 委托、成交在 0.30，应退还 `shares × 0.05` | #0a | ✅ 已完成：`v2regression` 回归测试验证价格改善退款 |

**0.2 已决策并完成**。份额模型下卖方语义为「卖 = 押注所选项的对立面」；
多选市场仍必须引入持仓表，当前 MVP 不支持。

### Sprint 1（第 2-3 周）：派彩正确性 + 资金完整性

目标：**让钱算对、不丢、不重复**

| # | 任务 | 对应缺陷 | 验收标准 |
|---|------|----------|----------|
| 1.1 | **锁仓改为 `shares × limit_price`**（`internal/order/order.go:161-167`） | #0 | ✅ 已完成：买单锁定 `shares × price`，卖单锁定互补结果风险 `shares × (1-price)` |
| 1.2 | **新增 `trades` 表 + 迁移**，撮合时写入每笔成交的 `matched_price` 与 `shares` | #0a | ✅ 已完成：每笔成交持久化 maker/taker、份额与实际成交价 |
| 1.3 | **实现价格改善退款**：成交时按 `shares × (limit_price - matched_price)` 退还差额 | #0a | ✅ 已完成：买卖方向均按成交价计算并立即退款 |
| 1.4 | **派彩改为 `winning_shares × 1.00`**，删除 parimutuel 分池公式；price 进入结算 SQL 与 `settlementOrder` | #0 | ✅ 已完成：按实际成交抵押归集，胜方份额按 1.00 兑付 |
| 1.5 | 删除或改造 `market.Settle`，禁止「翻状态不派彩」 | #0b | ✅ 已完成：删除服务、仓储与 HTTP 手工结算路径；仅 settlement 服务可在同一事务内派彩并将市场置为 settled |
| 1.6 | 结算 JOIN 改 LEFT JOIN + 缺钱包显式报错；末尾加「派出总额 == 池额」断言 | #0c | ✅ 已完成：缺钱包触发回滚；按币种校验实际抵押与派彩相等；集成测试覆盖删除钱包场景 |
| 1.7 | **份额精度提升**：`orders.amount`/`filled_amount` 改 `DECIMAL(28,6)`；金额统一整分（把 settlement 的 cents 方案提为公共类型）；明确 `shares × price` 舍入规则 | #2 | ✅ 已完成：迁移 `003` 将订单、成交与成交量提升至 6 位份额；`pkg/fixed` 统一整数分/份额/价格计算；半入向上舍入到分；1000 次部分成交精确填满 |
| 1.8 | 下单锁定与订单写入合并为单事务；`Cancel`、IOC 剩余量解锁同理 | #1 | ✅ 已完成：PostgreSQL 路径将锁定、撮合与退款/IOC 解锁原子化；取消订单同事务解锁；冲突写入集成测试验证无残留锁仓 |
| 1.9 | 新增滞留资金对账任务（作为 1.8 的兜底） | #1 | ✅ 已完成：每 10 分钟扫描无挂单但仍有锁定余额的钱包，回收资金并写入 reconciliation 交易记录 |
| 1.10 | 接入 `middleware.RequireIdempotencyKey`，`orders` 与 `transactions` 加幂等唯一约束 | #4 | ✅ 已完成：订单按商户、充值按钱包约束幂等键；重复同一 key 只返回原订单或余额，不重复锁仓/加款 |

**任务顺序有依赖**：1.2（trades 表）必须先于 1.3（退款）与 1.4（派彩），
因为后两者都需要 `matched_price`。1.7 的精度改造应在 1.1~1.4 之后，
避免在语义变动中同时改精度。

**出口条件**：CI 中集成测试全绿，`postgres_repository` 覆盖率 5.7% → 60%+，
资金守恒断言（池内 == 派出 + 手续费）在 e2e 中通过

### Sprint 2（第 3-4 周）：自动结算 + 运维就绪

目标：**打通自动结算，让服务能被安全地部署和重启**

| # | 任务 | 对应缺陷 | 验收标准 |
|---|------|----------|----------|
| 2.0 | **毒消息隔离**：错误分类（可重试/不可重试）、死信队列、重试上限；事务边界改 market 级 | #0d | ✅ 已完成：永久错误或 5 次重试耗尽后写入 NATS 死信主题并 Ack；结算逐市场提交，非法 outcome/缺钱包不阻塞其他市场或后续事件 |
| 2.0b | 结算钱包预锁改为 `ORDER BY w.id` 消除死锁 | #14b | ✅ 已完成：结算前按钱包 ID 排序并 `FOR UPDATE` 预锁；两个事件共享钱包的并发集成测试通过 |
| 2.0c | 市场固化商户/平台费率快照，建立独立 `fee_ledger`；`fee_rate`/`status` 移出商户可写字段 | #3b | 🔄 当前两个费率均强制为 `0`，不扣费、不写收入；后台配置完成后再启用非零费率与收入入账 |
| 2.1 | Polymarket DTO 补结算字段 + 新增已结算事件同步任务 | #3 | 上游出结果后无人工介入即完成派彩 |
| 2.2 | 事件超期未结算的告警与对账任务 | #3 | 超过 `resolution_time` 未结算的事件被上报 |
| 2.3 | 优雅关闭：SIGTERM → 停止接新请求 → 等待在途完成 | #5 | 滚动更新期间零请求中断 |
| 2.4 | HTTP panic 恢复中间件 + 后台 goroutine `recover()` | #6 | 注入 panic，进程存活且返回 500 |
| 2.5 | HTTP server 四个超时 | #7 | 慢客户端不再占用 goroutine |
| 2.6 | readiness 检查 DB/Redis/NATS；与 liveness 分离 | #8 | 断开 DB 后 Pod 从 Service 端点摘除 |
| 2.7 | 注册 `RegisterLockedCronProvider()`（Redis 锁），放开 `replicas` | #9 | 3 副本下 Polymarket 同步只执行一次 |

**出口条件**：多副本部署，滚动更新零中断，自动结算端到端跑通

### Sprint 3（第 5-6 周）：防护 + 可观测性 + 文档校正

目标：**上生产前的最后一层防护与可运维性**

| # | 任务 | 对应缺陷 | 验收标准 |
|---|------|----------|----------|
| 3.1 | 全局限流；注册端点独立更严策略 | #10 | 超阈值返回 429 |
| 3.2 | 所有 handler 统一请求体上限 | #11 | 超大 body 返回 413 |
| 3.3 | 接 `middleware.RequestID()`，日志与 trace 注入 request_id | #12 | 一次请求的全部日志可用 request_id 串联 |
| 3.4 | 暴露 `/metrics`，埋点核心指标（下单量/延迟/错误率/结算滞后/滞留资金额） | #12 | Prometheus 抓取成功，非 404 |
| 3.5 | 打通 otel trace 导出，补 k8s 的 `OTEL_*` 配置 | #12 | 可查看跨组件调用链 |
| 3.6 | 引入迁移工具（goose/golang-migrate）+ 版本表，整理 001 中的补丁式 ALTER | #14 | 可前滚可回滚 |
| 3.7 | api_key 改为哈希存储（bcrypt/argon2）+ 前缀查找；实现 HMAC 签名校验或**从文档中删除该承诺** | #16, #17 | 二者取一，代码与文档一致 |
| 3.8 | 分页上限 + 键集分页改造热路径；补缺失索引 | #15 | 深分页不再全表扫描 |
| 3.9 | **删除** `DELIVERY_REPORT.md`/`SUMMARY.md`/`PROJECT_STATUS.md`；修正 Kafka→NATS；修正 module 路径 | #18 | 文档与代码零偏差 |

**出口条件**：安全与可观测性达标，文档与实现一致

---

## 四、关键取舍建议

1. **份额模型已定案，改造是「修复退化」而非「重新设计」**。
   撮合层原本就是份额语义且实现正确，只有锁仓（少乘 price）和派彩（错用 parimutuel）
   两处退化。这意味着不需要重写撮合引擎——那是项目里质量最高的部分之一。
   MVP 已限定为二元市场；多选支持须在引入持仓账本后另行设计。

1b. **`trades` 表是份额模型的必需品，不是可选优化**。
   没有成交价记录就无法计算价格改善退款，而委托价与成交价必然不等
   （价格优先原则下成交在 maker 价）。当前每笔限价单成交都在少退用户的钱，
   金额为 `shares × (limit_price - matched_price)`——这是系统性侵占，不是舍入误差。

2. ~~**当前系统不具备任何真实交易的正确性**~~ → **已解除**：
   #0（赔率恒 2 倍）、#0b（假结算冻死 market）、#0c（静默丢单）均已修复并有
   回归/集成测试覆盖。真库验证：`postgres_repository` 平均覆盖率 5.7% → 77.9%，
   资金三包（order/settlement/wallet）仓储平均 78.2%，超出 Sprint 1 出口条件（60%）。

3. **CI 已建立**（`.github/workflows/verify.yml`）：race 单测 + 真库集成 + HTTP e2e
   全部纳入门禁。「赔率断言」与「价格改善退款」回归测试
   （`internal/{order,settlement}/share_model_regression_test.go`）已转绿。

4. **毒消息（#0d）已隔离**：永久错误或重试耗尽走 NATS 死信主题并 Ack；
   结算改为逐市场提交，非法 outcome / 缺钱包不再阻塞其他市场或后续事件。
   死信主题目前**只有生产者没有消费者**——进入死信的事件需要人工处理，
   Sprint 2 的 2.2（超期未结算告警）是它的配套监控，未完成前死信可能无人察觉。

5. **HMAC 二选一**：要么实现，要么从文档删除承诺。
   现状（文档声称有、代码没有、`api_secret` 是死代码）是最坏的一种——
   会让接入方以为有额外防护。

6. **replicas=1 仍是当前的正确选择**，不要在 2.7 完成前放开。
   在无分布式锁的情况下扩容会导致 Polymarket 重复调用和事件重复处理。

7. **值得肯定、不要动的部分**：`internal/auth` 中间件、钱包层原子条件 SQL、
   结算侧的 outbox + 表约束幂等、`pkg/fixed` 的整数分/份额定点计算、
   集成测试的写法、k8s 清单的安全加固。这些是本项目质量较高的部分，
   改造时应以它们为范式向外推广，而非重写。

8. **两处已知的有意取舍**（记录在案，非缺陷）：
   - memory repository 未实现 `PlaceWithLockedCollateral`，非 PG 路径仍走
     「先锁后放 + 补偿」两段式——单测环境无跨事务崩溃问题，可接受，
     但要求单测不能作为资金原子性的证据，原子性必须由集成测试守护
   - 迁移仍是 `psql` 顺序重放（无版本表），`005` 用 `migration_markers`
     手工防重放是过渡方案——3.6 引入迁移工具后应移除该标记机制

---

## 五、终验遗留清单（2026-07-29）

Sprint 0–3 验收通过后仍在册的事项，均不阻塞上线，但应进入 v0.3 排期：

1. **迁移过渡机制未清理**：Goose 已接入（`cmd/migrate`，`goose_db_version` 版本表生效），
   但 `005` 的 `migration_markers` 手工防重放表仍在——原计划 3.6 完成后移除。

2. **卖单初始锁仓的份额精度**：撮合与派彩已整分/份额定点化，
   但卖单 `shares × (1-price)` 的舍入在极小份额（0.000001 份级）下的边界
   行为未见专项测试，建议补一组 property-based 测试。

3. **死信有监控无自动处理**：`settlementmonitor` 每轮巡检 DLQ 深度并告警
   （`predictmarket_settlement_lag_seconds` / `dead_letter_size`），
   但死信消息的重放仍需人工执行，无 runbook 化的重放命令。

4. **`hashSecret`（api_secret）仍是无盐 SHA-256**：api_key 已升级 bcrypt + 前缀查找，
   但 api_secret 的哈希未同步升级。由于 secret 当前不参与任何认证路径
   （HMAC 承诺已从文档移除），风险为零，但字段本身应考虑直接删除。

5. **单测覆盖率总量 34.7%**：真库集成把仓储层拉到 74.4%，
   但 handler 分支、错误路径的单测密度仍有提升空间。

6. **流动性端点的产品语义不准确**：`POST /markets/{id}/liquidity` 仍暴露，
   但不会影响纯 P2P 撮合。上线前应移除该端点，或在面向商户的文档中明确其不提供做市能力。
