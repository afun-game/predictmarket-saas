# v0.3.0 方案设计：商户站点集成 API（Launch / 无缝钱包 / 结算 / 对账）

> **状态（2026-07-31 更新）**：Phase 1–2 完成；Phase 3 功能、并发验收与
> 混沌验收均已落地（含 void 事件、熔断、IP 白名单、回调验证、审计、分层限流与
> 实时余额回调）；Phase 4 的配套资产（`merchant-sim`、沙箱结算加速器、托管前端
> 原型、认证清单）齐备，仅剩真实环境联调与商务决策项（见 §十一）。
> 实施与验收记录见 `docs/V3_ACCEPTANCE_CHECKLIST.md`。

---

## 一、背景与目标

### 现状（v0.2.x）

| 能力 | 现状 |
|------|------|
| 商户认证 | `Authorization: Bearer <api_key>`（bcrypt + 前缀查找） |
| 用户概念 | `user_id` 自由字符串，无用户表、无会话 |
| 钱包 | 平台记账（转账式），仅 `POST /wallets/{userID}/credit` 单向入金 |
| 下注 | 商户服务器代下单 `POST /api/v1/orders` |
| 结算通知 | 无。商户只能轮询订单状态 |
| 托管页面 | 无 |
| 平台→商户回调 | 无。`api_secret` 是死字段，HMAC 承诺已从文档移除 |

### 目标

对标体育博彩供应商的四件套：

1. **Launch（认证换 URL）**：站点服务器用自己的用户体系向平台换取一次性启动 URL，
   用户浏览器打开即进入平台托管的交易页面（iframe 或跳转），无需在平台二次注册。
2. **下注（双钱包模式）**：
   - **转账钱包（transfer）**：沿用现有平台记账，补齐出金与转账对账；
   - **单一/无缝钱包（seamless）**：余额的唯一权威在商户侧，
     平台在下注/退款/派彩时实时回调商户扣款/加款。
3. **结算**：结算结果主动推送（webhook）+ 可拉取（pull API），at-least-once + 幂等。
4. **查询/对账**：订单、成交、结算、资金流水的游标分页拉取 + 日报表汇总。

### 设计原则（继承 v0.2 的教训）

- **资金守恒不动摇**：无缝钱包不绕过现有 `PlaceWithLockedCollateral` 原子路径与
  守恒断言，而是在其外围加「影子账本 + 回调」层（见 §4.3）。
- **金额一律字符串定点**：API 边界金额 2 位小数、份额/价格 6 位小数，
  内部继续用 `pkg/fixed`，杜绝 float。
- **一切跨系统消息幂等**：回调带平台事务 ID，商户按 ID 去重；
  webhook 走 outbox，at-least-once。
- **失败必须可见**：回调重试耗尽进 DLQ + 告警 + 人工 runbook，绝不静默丢钱。

---

## 二、总体架构

```
┌──────────────┐  ①POST /v2/sessions（换launch_url）   ┌──────────────────┐
│  商户站点     │ ────────────────────────────────────→ │                  │
│  服务器       │  ②服务器代下单/查询/对账 (v2 REST)     │     平台          │
│              │ ────────────────────────────────────→ │                  │
│              │  ③无缝钱包回调 debit/credit (HMAC)     │  ┌────────────┐  │
│  callback    │ ←──────────────────────────────────── │  │ 影子账本    │  │
│  endpoint    │  ④结算/异常 webhook (HMAC)             │  │ + outbox   │  │
│              │ ←──────────────────────────────────── │  └────────────┘  │
└──────────────┘                                        └──────────────────┘
       ↑ 登录态                                                  ↑
┌──────────────┐  ⑤launch_url 打开托管页，token 换 session        │
│  用户浏览器   │ ────────────────────────────────────────────────┘
└──────────────┘      /api/user/*（会话态：行情、下单、我的订单）
```

三条信道，三套认证：

| 信道 | 认证 | 说明 |
|------|------|------|
| 站点服务器 → 平台（v2 REST） | Bearer api_key + **HMAC-SHA256 请求签名**（复活 api_secret） | 幂等键必带 |
| 平台 → 站点（回调/webhook） | 独立 `callback_secret` 的 HMAC 签名 | 商户注册回调地址时下发 |
| 用户浏览器 → 平台（/api/user/*） | 一次性 launch token 换短期会话 JWT | 与商户 key 完全隔离 |

版本策略：新套件挂 `/api/v2`，现有 `/api/v1` 冻结为兼容层（仅修 bug），
商户迁移完成后废弃 v1 中与 v2 重叠的端点。

---

## 三、模块一：用户认证与启动（Launch）

### 3.1 创建会话（站点服务器调用）

```
POST /api/v2/sessions
Authorization: Bearer <api_key>
X-PM-Signature / X-PM-Timestamp        （HMAC，见 §7.1）
Idempotency-Key: <uuid>

{
  "user_id": "site-user-8801",        // 商户侧用户唯一 ID
  "currency": "USD",
  "balance": "100.00",                // seamless 必填；进入游戏时的余额快照
  "locale": "zh-CN",
  "return_url": "https://site.com/lobby",   // 页面内「返回」跳转
  "ip": "1.2.3.4",                    // 终端用户 IP（风控用，可选）
  "meta": {"vip_level": "3"}          // 透传，平台不解释
}
```

```
201 Created
{
  "session_id": "ps_9f2…",
  "launch_url": "https://play.<platform>/launch?token=lt_one_time_…",
  "expires_at": "2026-07-30T15:00:00Z"     // launch token 有效期 15 分钟
}
```

平台侧动作：
1. **影子用户 upsert**：`platform_users(merchant_id, external_user_id)` 唯一约束，
   首次出现即建档（当前 `user_id` 自由字符串正式收编为外键）。
2. 签发一次性 launch token（Redis，TTL 15 分钟，**单次使用**，绑定 merchant+user+currency）。
3. 返回托管页 URL。

### 3.2 浏览器兑换会话

用户打开 `launch_url` → 托管前端调
`POST /api/user/session/exchange {token}` → 平台校验并**作废** token，
签发会话 JWT（TTL 2h，滑动续期上限 12h），JWT claims：
`merchant_id, user_id, currency, wallet_mode, locale`。
兑换响应同时返回启动请求中的余额快照，托管页首屏无需再查询余额。

### 3.3 会话管理

| 端点 | 用途 |
|------|------|
| `DELETE /api/v2/sessions/{session_id}` | 商户强制踢线（用户在站点登出/被封） |
| `GET /api/v2/sessions/{session_id}` | 查询会话状态 |
| `POST /api/user/session/refresh` | 浏览器续期 |

踢线实现：会话 JWT 短 TTL + Redis 黑名单（session_id → revoked），
`/api/user/*` 中间件每请求查一次（Redis 已在依赖里）。

### 3.4 托管页面（独立工作流，API 先行）

托管前端（行情列表 / 市场详情 / 下单面板 / 我的订单）是独立交付物，本期只定其依赖的 API：

| 端点（会话态） | 说明 |
|------|------|
| `GET  /api/user/me` | 用户信息 + 余额（seamless 模式为镜像值，见 §4.3） |
| `GET  /api/user/markets` `GET /api/user/markets/{id}` `GET /api/user/markets/{id}/orderbook` | 行情（复用 v1 只读逻辑，加会话租户过滤） |
| `POST /api/user/orders` | 下单（走 §4 的钱包路径） |
| `DELETE /api/user/orders/{id}` | 撤单 |
| `GET  /api/user/orders` `GET /api/user/orders/{id}/trades` | 我的订单/成交 |

iframe 集成时托管页通过 `postMessage` 向父页面发事件：
`pm:ready / pm:bet_placed / pm:balance_changed / pm:session_expired / pm:navigate_home`，
商户据此刷新站点侧余额显示。品牌化（logo/主题色/隐藏元素）挂在商户配置，launch 时下发。

---

## 四、模块二：下注与双钱包模式

商户配置 `wallet_mode: "transfer" | "seamless"`（默认 transfer，向后兼容）。
**同一商户只能选一种模式**，切换需人工审批 + 余额清算。

### 4.1 转账钱包（transfer，现有模式补齐）

余额在平台。站点负责把钱转进/转出平台：

| 端点 | 说明 |
|------|------|
| `POST /api/v2/users/{user_id}/deposits` | 入金（替代 v1 credit，请求体带商户流水号，`(merchant_id, merchant_txn_id)` 唯一） |
| `POST /api/v2/users/{user_id}/withdrawals` | **出金（新增）**：`balance` 充足才允许，原子扣减 |
| `GET  /api/v2/transfers/{merchant_txn_id}` | 转账终态查询（超时后先查再重试，防重复入金） |
| `GET  /api/v2/users/{user_id}/balance` | 余额 |

下注路径不变：锁仓、撮合、退款、结算全部走现有 `internal/order` / `internal/settlement`。

### 4.2 无缝钱包（seamless）——回调契约

余额唯一权威在**商户侧**。平台每次资金变动实时回调商户：

```
POST {merchant.callback_url}
X-PM-Signature: hex(HMAC-SHA256(callback_secret, timestamp + "." + raw_body))
X-PM-Timestamp: 1769836800
X-PM-Merchant-Id: <uuid>
Content-Type: application/json

{
  "callback_id": "cb_7c1…",           // 本次投递 ID（重试不变）
  "type": "debit" | "credit" | "rollback",
  "transaction_id": "ptx_a41…",       // 平台事务 ID = 幂等键
  "user_id": "site-user-8801",
  "currency": "USD",
  "amount": "30.00",                  // 字符串定点，2 位小数
  "reason": "bet" | "refund_price_improvement" | "refund_cancel"
          | "refund_ioc" | "payout" | "void",
  "ref": {                            // 业务引用，商户入账备注用
    "order_id": "…", "trade_id": "…",
    "market_id": "…", "event_id": "…"
  },
  "created_at": "2026-07-30T12:00:00Z"
}
```

商户应答（HTTP 200）：

```
{ "status": "ok", "balance": "70.00" }                 // 成功，回报最新余额
{ "status": "insufficient_funds", "balance": "5.00" } // 仅对 debit 合法，也返回最新余额
{ "status": "duplicate", "balance": "70.00" }           // 幂等重放，等同成功
{ "status": "user_not_found" | "user_blocked" }         // permanent 失败
```

**契约要点**（写入商户接入文档并纳入认证测试）：

1. 商户必须按 `transaction_id` 去重——同一 ID 重复投递只入账一次。
2. `rollback` 引用原 `transaction_id`（`ref.original_transaction_id`），
   对未见过的原事务先记账再冲正（体育博彩惯例：rollback-before-bet 也要能处理）。
3. 超时（平台侧 3s）与 5xx 视为未知态 → 平台以**相同 transaction_id** 重试。
4. 金额恒为正数，方向由 `type` 决定。
5. `ok`、`duplicate`、`insufficient_funds` 必须返回定点字符串 `balance`；
   平台把该余额透传给托管页，商户无需为每次下注额外处理余额查询。

### 4.3 无缝钱包的内部实现：影子账本，不绕过现有资金安全层

v0.2 用血换来的守恒断言、单事务锁仓、对账任务全部保留。无缝模式实现为
「回调驱动的自动入金/出金」包在现有钱包外面：

```
下注（同步，用户请求路径内）:
  ① 计算所需抵押 C = shares×price（买）或 shares×(1-price)（卖）
  ② 回调商户 debit(C)，transaction_id = 预生成的 order intent ID
     ├─ insufficient_funds → 直接拒单，无任何平台侧写入
     ├─ 超时/5xx → 拒单（unknown 态），并投递 rollback(相同ID) 直至终态确认
     └─ ok ↓
  ③ 单 DB 事务：影子钱包 credit C → 现有 PlaceWithLockedCollateral（锁仓+撮合+价格改善退款）
     └─ 失败 → 回调 rollback(C)，rollback 本身进重试队列直至成功
  ④ 事务内产生的即时退款（价格改善/IOC 剩余）不再实时回调，
     并入「credit 出账队列」异步推送（见下）

credit 出账（异步，outbox 驱动）:
  结算派彩 / 撤单退款 / void 退款 → 同一 DB 事务内：
    影子钱包记减 + 写入 callback_outbox(transaction_id, type=credit, reason, amount)
  → 投递 worker（复用 settlementworker 的重试/DLQ 范式）：
    HMAC 回调商户 → ok/duplicate 则标记 delivered
    → 5 次指数退避（1s→5m）失败进 DLQ + 告警（欠商户用户的钱，人工 runbook 处理）
```

不变量（进对账任务与 `/metrics`）：

- **影子钱包余额 ≡ 未完成订单锁仓 + 待投递 credit 之和**。
  偏差即事故，`reconciliation` 任务扩展巡检此式。
- debit 成功但 ③ 失败的 rollback 未终态确认前，该 intent ID 冻结不可复用。

这样 settlement、守恒断言、trades 表、滞留资金对账**零改动**，
无缝模式只是把「用户手动充值/提现」替换成「回调自动化」。

### 4.4 服务器代下单（两种模式通用）

部分商户不用托管页、纯 API 集成（自建前端），保留服务器代下单：

```
POST /api/v2/orders          （字段同 v1 下单 + user_id；seamless 模式同样触发 debit 回调）
DELETE /api/v2/orders/{id}
```

---

## 五、模块三：结算接口

### 5.1 结算 webhook（推）

事件源：现有 settlement 完成点（含 void）。同一 DB 事务写 `webhook_outbox`，
worker at-least-once 投递到商户 `webhook_url`（可与 callback_url 相同，事件独立签名）：

```
{
  "webhook_id": "wh_…",               // 幂等键
  "type": "order.settled" | "order.voided" | "market.settled" | "market.voided",
  "data": {
    "market_id": "…", "event_id": "…", "winning_option": "yes",
    "order_id": "…", "user_id": "…",
    "stake": "30.00", "payout": "100.00", "currency": "USD",
    "settled_at": "…"
  }
}
```

- `market.settled` 一条 + 每单一条 `order.settled`，商户可按需订阅（配置事件掩码）。
- 无缝模式下 `order.settled` 与派彩 credit 回调**都发**：
  回调是记账指令（必须处理），webhook 是通知（可选消费）——与体育博彩惯例一致。
- 重试策略同 §4.3；DLQ 深度已有 `dead_letter_size` 指标，webhook 复用。

### 5.2 结算拉取（拉，兜底 + 对账）

| 端点 | 说明 |
|------|------|
| `GET /api/v2/settlements?from=&to=&cursor=&limit=` | 结算记录（market 粒度） |
| `GET /api/v2/settlements/{market_id}/payouts?cursor=` | 单市场派彩明细（order 粒度） |

推送只是加速，**商户不收 webhook 也必须能靠拉取达到最终一致**——认证测试项之一。

---

## 六、模块四：查询与对账

全部游标（键集）分页：`cursor` 为 `(created_at,id)` 编码，`limit ≤ 500`，
按 `created_at DESC, id DESC`（已有 `idx_orders_merchant_created_id` 支持）。

| 端点 | 说明 |
|------|------|
| `GET /api/v2/orders?user_id=&market_id=&status=&from=&to=&cursor=` | 订单拉取 |
| `GET /api/v2/orders/{order_id}` | 单订单（含成交明细、退款明细） |
| `GET /api/v2/trades?from=&to=&cursor=` | 成交流水（trades 表直出） |
| `GET /api/v2/transactions?user_id=&type=&from=&to=&cursor=` | 资金流水（transfer 模式：平台账本；seamless 模式：回调事务流水及其投递终态） |
| `GET /api/v2/reports/daily?date=&currency=` | 日报表：bets/refunds/payouts/GGR/手续费（预留）汇总，商户与平台各自算一遍然后对数 |

对账建议流程写入接入文档：商户每日拉 `transactions` 与自身账本按
`transaction_id` 逐笔核对，差异走 `GET /api/v2/callbacks/{transaction_id}` 查投递历史。

---

## 七、安全设计

### 7.1 请求签名（站点→平台，复活 api_secret）

v0.2 遗留：`api_secret` 无盐 SHA-256 且是死代码。本期正式启用：

- 重新生成，**仅创建时明文展示一次**；平台侧 AES-256-GCM 加密存储
  （HMAC 验证需要明文，不能只存哈希；主密钥走环境变量/KMS）。
- 签名：`X-PM-Signature = hex(HMAC-SHA256(api_secret, timestamp + "." + raw_body))`，
  `X-PM-Timestamp` 偏差 > 300s 拒绝；nonce（Idempotency-Key 兼任）Redis 去重防重放。
- **轮换**：primary/secondary 双密钥并存窗口（≤7 天），双验签，商户切完删旧。
- v2 全端点强制验签；v1 维持 Bearer-only 直至下线。
- **防重放实现决策（已定）**：v2 变更类端点的幂等由主库唯一约束承担——
  入金/出金以 `(merchant_id, merchant_txn_id)`、下单以 `idempotency_key`
  唯一去重，因此这些端点不再占用 Redis nonce（`RequireSignedMerchantWithoutReplay`）；
  `POST /v2/sessions` 与 `DELETE /v2/sessions/{id}` 仍走 Redis nonce 防重放。
  效果与 §7.1「nonce Redis 去重」等价（重复业务操作无法造成重复记账），
  但省去一次 Redis 往返。

### 7.2 回调签名（平台→站点）

独立 `callback_secret`（与 api_secret 隔离，泄露面不同），同一 HMAC 方案。
商户配置回调地址时平台发**验证挑战**（随机串回显）确认地址所有权，防误配打到第三方。

### 7.3 其他

- launch token：一次性、15 分钟、绑定 merchant+user，兑换即焚。
- 商户级 IP 白名单（v2 REST 可选强制）；回调目标仅允许 HTTPS + 公网域名（禁内网 IP，防 SSRF）。
- `/api/user/*` 与商户 API 物理隔离限流：用户会话 per-session 限流，商户 per-key 限流分层（下单类/查询类分池）。
- 审计：v2 全部变更类请求落审计表（复用 `event_resolution_audits` 范式）。

---

## 八、数据模型变更（迁移 011+）

| 表 | 变更 |
|----|------|
| `platform_users`（新） | `(merchant_id, external_user_id)` 唯一；locale/status/created_at。现有 orders/wallets 的 user_id 字符串在过渡期共存，新写入强制建档 |
| `merchants` | + `wallet_mode`、`api_secret_enc`、`callback_url`、`callback_secret_enc`、`webhook_url`、`webhook_events`、`allowed_ips`。**全部仅管理员可写**（沿用 fee_rate 教训：集成参数不给商户自助改） |
| `wallet_transfers`（新） | transfer 模式出入金：`(merchant_id, merchant_txn_id)` 唯一、direction、status 终态机 |
| `seamless_transactions`（新） | 无缝回调事务：transaction_id 主键、type/reason/amount/ref、投递状态、重试计数、商户应答快照——既是重试状态机也是对账数据源 |
| `callback_outbox` / `webhook_outbox`（新） | 沿用 `event_outbox` 模式（也可合并为一张带 channel 列的表） |
| `wallets` | + `kind`（`user` / `shadow`），影子钱包复用现有原子 SQL 与守恒断言 |
| `orders` | + `channel`（`api` / `hosted`），报表用 |

会话与 launch token 只进 Redis，不落库。

---

## 九、分阶段实施

### Phase 1（2 周）：认证底座 + Launch
HMAC 签名中间件（双向）+ 密钥加密存储与轮换、`platform_users`、
sessions/launch/exchange/踢线、`/api/user/*` 骨架（行情只读 + 会话中间件）。
**出口**：e2e 走通「服务器换 URL → 浏览器换会话 → 会话查行情」，重放/过期 token 用例全绿。

### Phase 2（2 周）：transfer 补齐 + 查询对账套件
deposits/withdrawals/transfers 终态机、v2 orders 代下单、
全部游标分页查询端点、daily report。
**出口**：模拟商户完成一轮「入金→下注→结算→拉流水对账平账」。

### Phase 3（3 周）：无缝钱包 + 结算推送（本方案核心，风险最高）
seamless_transactions 状态机、下注同步 debit + rollback 补偿、
credit outbox worker（重试/DLQ/告警/runbook）、影子账本守恒巡检、
结算 webhook、`GET /callbacks/{txn_id}` 投递历史。
**出口**：
- 注入商户端故障（超时/5xx/乱序/重复投递/rollback-before-bet）的混沌集成测试全绿；
- 影子账本守恒式在 1000 单并发 e2e 后偏差为 0；
- DLQ 消息有 runbook 化重放命令（补上 v0.2 遗留 #4）。

### Phase 4（1-2 周）：接入配套
沙箱环境（独立 DB + 假结算加速器）、托管前端联调仍待实施；本仓库已提供
**商户回调模拟器**（`cmd/merchant-sim`）、认证清单、OpenAPI v2 路由校验和
Go/Python/JavaScript 签名示例。

---

## 十、关键取舍与风险

1. **无缝钱包的 debit 是同步网络调用在下单临界路径上**（行业标配，无法避免）。
   缓解：3s 超时硬上限、商户回调 P99 纳入 SLA、按商户熔断
   （连续失败自动置 `degraded`，拒新单保存量，避免 rollback 风暴）。
2. **影子账本方案** 用一层间接换来了 v0.2 全部资金安全资产的复用。
   代价是多一个账本要巡检——巡检式子已在 §4.3 固定，必须与功能同 PR 交付。
3. **rollback 的未知态**（debit 超时既可能已扣也可能没扣）是无缝钱包最深的坑。
   方案：unknown 态一律拒单 + 以原 transaction_id 投 rollback 直至商户给出终态应答；
   商户侧「先见 rollback 后见 bet」必须按契约处理——纳入认证测试，不通过不给生产 key。
4. **双钱包模式并存**增加测试矩阵。用同一套 `WalletProvider` 接口收敛
   （transfer = 现有实现，seamless = 回调适配器），业务层不感知模式。
5. **v1 冻结而非立改**：现有商户零迁移压力；v2 稳定一个季度后再排 v1 下线。
6. **托管前端是新工种**（本仓库目前纯后端）。Phase 1-3 的 API 不依赖前端交付，
   纯 API 商户（§4.4）可先行接入，前端并行开发。

## 十一、待评审确认项

1. `/api/user/*` 行情是否需要 WS/SSE 实时推送（orderbook 变动）？建议 Phase 4 后单独排期。
2. 无缝模式采用 slots 风格余额：启动请求传入余额，debit/credit/rollback
   应答（包括余额不足）返回最新余额。`balance` 查询回调保留为兜底，仅在页面重新
   获得焦点或 60 秒没有余额更新时调用。
3. 日报表的 GGR 口径（含未结算敞口与否）需与商务对齐。
4. 沙箱是否对外自助开通，还是商务开通制。
