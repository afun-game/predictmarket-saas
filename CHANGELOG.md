# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added (Market maker + Parimutuel)
- 流动性池做市（`internal/marketmaker`）：binary 市场的 `liquidity_pool`
  变成真实做市资金——平台按市场一次性注入专用钱包（`__liquidity__`，
  `marketmaker_funds` 记账，追加流动性只补差额），每 10s 在盘口维护双边
  限价单（以盘口中间价 ±5% 报价，两侧各 25% 资金，价格-时间优先、不穿价），
  临近结算 5 分钟停止报价；订单走 mm 通道，结算照常。
- Pari-mutuel 奖池市场（`internal/parimutuel` + 迁移 017）：市场类型新增
  `parimutuel`，管理员创建市场时选择；`POST /api/v1/bets` 下注（商户鉴权，
  下注即扣减钱包入池），结算时总奖池按中奖方各注单占比分配，
  无人中奖全部退还；作废全额退还。
- 管理台：市场列表/详情展示市场类型，创建表单可选择订单簿/奖池并选择
  归属商户，奖池市场显示累计投注；`GET /api/v1/bets` 商户侧注单查询。

### Added (Admin console)
- SaaS 后台管理控制台（第一期）：零构建 SPA 嵌入 API 二进制，`/admin` 托管。
- 管理员身份体系：`admin_accounts` + `admin_action_logs`（迁移 016），
  bcrypt 密码登录、5 次失败锁定 15 分钟、会话 JWT（HttpOnly Cookie，
  复用 `SESSION_JWT_SECRET`）、`super_admin`/`operator` 两级 RBAC、
  `ADMIN_USERNAME`/`ADMIN_PASSWORD` 引导首个超管。
- 商户管理：列表/搜索/详情（含用户/市场/订单/交易量统计）、费率/币种/
  时区配置（解除 `merchants_fee_rate_disabled` 冻结）、挂起/启用（确认词
  双确认 + 审计）；挂起商户在所有 API 鉴权边界立即失效。
- 商户用户管理：用户列表/详情（钱包余额与锁定）、流水查询、封禁/解封；
  封禁用户在 v3 会话创建/换取/下单/撤单链路返回 403。
- 事件管理：列表/搜索、创建/编辑、状态流转、结算操作台（确认词）；
  已结算事件不可再编辑。
- 市场管理：列表/搜索、详情（订单簿 + 事件标题）、状态流转、加流动性、
  作废（确认词）；`/api/v1/admin/markets/{id}/void` 改挂会话鉴权。
- 监控与仪表盘：全局订单/资金流水检索、审计日志查询、平台总览聚合
  （商户/用户/市场/今日订单与交易量/手续费/待结算 + 14 天序列）。
- 每个管理员写操作写入 `admin_action_logs`（操作人/动作/资源/前后状态/IP）。
- 管理台前端 `web/admin`：登录、仪表盘、商户/用户/事件/市场/订单/流水/
  审计 8 个页面，全部中文，复用托管页视觉语言。

### Added (Hosted parimutuel betting)
- 修复托管前端奖池市场误用订单簿模式：`web/hosted` 此前无视市场类型，
  奖池市场下单时读取空盘口报价并报“无报价”失败。现在市场页按
  `type` 展示「奖池模式/订单簿模式」徽标，奖池市场按池内各选项投注额
  推算隐含概率展示，票据标注「奖池投注」，下单走投注额（非份额）。
- 新增用户会话奖池接口：`POST /api/user/bets` 下注（扣减钱包入池，
  失败自动退款，与商户侧 `/api/v1/bets` 同语义）、
  `GET /api/user/markets/{id}/pools` 查询池总量与分选项投注额；
  订单簿市场对二者分别拒绝 400。
- `internal/parimutuel` 新增 `OptionStakes` 聚合（内存/Postgres 双实现），
  为前端提供分选项隐含概率数据。
- 修复 seamless 商户奖池下注报 `could not debit wallet`：seamless 商户
  余额在商户侧，平台钱包无余额。现 `/api/user/bets` 对 seamless 商户走
  签名回调扣款（与订单簿下单同路径）：同步 debit 回调 → 影子钱包入账
  （`parimutuel_bets.wallet_kind='shadow'`，迁移 018）→ 入池；结算/作废
  时通过 `credit` 回调（`payout`/`refund_cancel`/`void`）回款，订单式
  seamless 结算回调逻辑泛化支持无订单 ID 的注单。
- 新增 chaos 集成用例：seamless 奖池下注扣款/入池/余额不足拒绝。

### Added (V3 hardening)
- Per-merchant seamless circuit breaker: five consecutive callback/webhook
  failures mark a merchant degraded, seamless orders are refused, and a
  healthy delivery or the admin `reset-degraded` endpoint clears the flag.
- Callback URL ownership verification (`verify-callback`): a signed challenge
  must be echoed before seamless orders are accepted; changing the callback
  URL invalidates the proof.
- V2 IP allow-list enforcement (`allowed_ips`, exact IPs or CIDR) on all
  signed merchant requests.
- `merchant_api_audits` table records every state-changing V2 request with
  request ID, idempotency key, client IP, and status code.
- Layered rate limits: per-merchant-key pools for V2 writes and reads plus a
  per-session pool for `/api/user/*` (Redis-backed).
- Market void: admin `POST /api/v1/admin/markets/{id}/void` refunds every
  order in full, emits `order.voided`/`market.voided` webhooks, records
  `settlement_type = "void"` in the pull API, and delivers seamless credits
  with reason `void`.
- Migration 015 (`merchants` hardening columns, `market_settlements`
  settlement type, `merchant_api_audits`).
- Real-time seamless balance callback: `/api/user/me` queries the merchant
  balance on demand and falls back to the last callback mirror.
- Sandbox fake settlement accelerator (`cmd/sandbox-accelerator`) resolves due
  events through the admin API for sandbox settlement testing.
- Embedded hosted UI served at `/launch` when the V3 routes are enabled
  (`web/hosted` is compiled into the API binary).
- Configurable rate limits (`GLOBAL_RATE_LIMIT`, `V3_ORDER_RATE_LIMIT`,
  `V3_QUERY_RATE_LIMIT`, `V3_USER_RATE_LIMIT`) for acceptance runs.
- Integration and end-to-end coverage now exercises market void and the
  1,000-order seamless load path (`make test-e2e`).
- Seamless chaos suite (`internal/callback/seamless_chaos_integration_test.go`)
  drives the coordinator through timeout / 5xx / rollback-before-bet /
  duplicate-delivery / dead-letter-replay faults against the merchant
  simulator; `internal/merchantsim` now hosts the simulator logic shared with
  `cmd/merchant-sim` (adds transient `-fail-count` and `-delay-count`
  injection).
- Fix: rollback callbacks for unknown debits no longer reference the
  never-persisted order, so the outbox insert no longer fails the
  `callback_outbox.order_id` foreign key and the rollback is always delivered.
- Extend the one-time launch token window from 60s to 15 minutes so users can
  open the hosted page comfortably; the token remains single-use, tenant-bound,
  and consumed on exchange.

### Added
- Initial project setup with Twill framework
- Core component interfaces:
  - Merchant service for tenant management
  - Event service for prediction event aggregation
  - Market service for market creation and management
  - Order service for order matching and execution
  - Wallet service for virtual credit management
  - Currency service for exchange rate handling
  - Sports service for sports event integration
  - Analytics service for reporting
- Database schema with PostgreSQL migrations
- Polymarket API client for event synchronization
- Docker Compose setup for local development
- Kubernetes deployment, migration Job, deployment guide, and operations runbook
- OpenAPI contract covering all 37 HTTP operations
- Structured JSON/text application logging
- Reproducible vendored Docker build for the private/local Twill dependency
- Goose-backed versioned migrations with a schema version table
- Redis-locked Cron jobs, readiness/liveness probes, graceful shutdown, and panic recovery
- Prometheus-format `/metrics`, request/trace correlation, and OTLP tracing support
- Comprehensive documentation:
  - Requirements document
  - API documentation
  - Getting started guide
  - MVP implementation plan

### Changed
- Replaced Kafka with NATS JetStream for durable business events
- Limited MVP infrastructure to PostgreSQL, Redis, and NATS JetStream
- Exposed Prometheus-format metrics without requiring Prometheus or Grafana in the MVP runtime

### Deprecated
- N/A

### Removed
- N/A

### Fixed
- seamless 奖池下注/结算回调的 `order_id` 引用缺失导致商户拒收（400
  invalid_request / 502 debit_unknown）：下注扣款前预生成注单 ID 并作为
  回调 `ref.order_id` 随注单落库，奖池 payout/refund/void 的 credit 回调
  同样携带注单 ID，rollback 回调携带扣款引用的 order_id；迁移 019 移除
  `callback_outbox.order_id` 外键（回滚引用的订单/注单在扣款失败时未落库，
  奖池 credit 引用 `parimutuel_bets`），merchant-sim 对齐真实商户契约
  （回调必须携带非空 order_id，否则 400）。
- 市场创建允许挂在已过结算时间的事件上（做市商永不报价导致盘口为空、
  前端无法下单）：创建校验事件 `resolution_time` 必须晚于当前时间，
  否则返回 422 `event_expired`。
- 开发环境 RDS 主密码被自动轮换后凭据未同步：部署工作流改用
  `predictmarket/dev/database` secret，并关闭 RDS 托管轮换。
- 托管页刷新即丢失会话（一次性 Launch token 换出 access token 后仅存
  内存）：access token 持久化到 sessionStorage，刷新页面时调用
  `POST /api/user/session/refresh` 续期并重新拉取数据；会话过期/被吊销
  时清除本地会话并提示从商户站点重新进入。
- 奖池市场展示回报率：`GET …/pools` 每个选项新增 `odds`（返还倍数，
  `(total_stake - total_fees) / option_stake`，两位小数）；`POST
  /api/user/bets` 响应 `meta.pool` 携带下注后的池快照，托管页下注前展示
  各选项回报率、下注后即时刷新，无需二次请求。
- 奖池结算死信修复：`seamless_transactions.order_id` 外键（指向 orders）
  拒绝奖池结算 credit 携带注单 ID，导致结算 5 次重试后进死信；迁移 020
  移除该外键（与 019 同理由），并新增 seamless 奖池结算端到端测试
  （TestSeamlessChaosSettleParimutuelPaysShadowBets）。
- 奖池下注未累计 `markets.total_volume`：下注事务内按注单金额累加（累计
  不回退，与订单簿成交额语义一致），存量奖池市场已回填；托管页与商户
  接口的 `total_volume` 恢复真实投注量。
- `GET /api/user/orders` 并入用户的奖池注单：注单以 `type: "bet"`、
  `amount: stake` 的订单形态混排返回（按时间倒序），刷新后前端历史页
  能完整展示用户参与过的订单与注单；支持 `market_id`/`status` 过滤。
- 市场新增分类字段（`markets.category`，迁移 034）：创建市场时未指定分类
  则自动继承事件分类（可显式覆盖）；`GET /api/user/markets`、`GET
  /api/v1/markets` 与管理员市场接口均返回 `category`，并支持
  `?category=` 过滤；管理台创建表单/列表/详情展示分类，托管页列表卡片
  优先使用市场自身分类、回退事件分类。
- 市场列表/详情接口（`GET /api/user/markets` 与 `/{id}`）内嵌行情摘要：
  奖池市场携带 `pool`（与 `GET …/pools` 同构，含每选项 `stake`/`odds`），
  订单簿市场携带 `book`（每选项最优 bid/ask）；批量聚合一次查询完成，
  列表页无需逐市场请求。
- 奖池结算未写 `settlement_payouts`，`GET /api/v2/settlements/{id}/payouts`
  对奖池市场返回空：结算时按注单逐笔写入审计行（中奖/未中奖均记录，
  `order_id` 为注单 ID，迁移 033 移除 orders 外键），商户可查询到完整
  派彩记录。

### Security
- Merchant API keys are bcrypt-hashed and located by a non-secret prefix

## [0.1.0] - 2024-07-28

### Added
- Initial project scaffold
- Core architecture design
- Component interface definitions
- Database schema design
- Development environment setup

[Unreleased]: https://github.com/afun-game/predictmarket-saas/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/afun-game/predictmarket-saas/releases/tag/v0.1.0
