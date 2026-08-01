# SaaS 后台管理平台 第一期开发计划（Admin Console / v0.4）

> **状态（2026-07-31 更新）**：第一期 P0–P6 已全部实现并验收：
> 管理台后端（`internal/adminauth`、`internal/adminquery`、`/api/v1/admin/*`
> 会话鉴权 + RBAC + 审计）与前端（`web/admin`，嵌入 `/admin`）均已落地，
> 迁移 016 就绪；挂起商户/封禁用户链路已打通。真实环境冒烟（Postgres +
> Redis + Edge headless）通过。剩余待办：生产部署（k8s/ingress 暴露 /admin）、
> 管理员 2FA、注册审批流（均属后续版本，见 §六/§七）。
> 与 v0.3 托管前端（面向终端用户）并行，二者是同一平台的两面：托管页是
> 「商店」，管理台是「店堂后台」。本计划只覆盖第一期（能管起来、能监控、
> 有审计），不做超出运营刚需的扩展。

---

## 一、背景与现状盘点

### 现有能力（后端已具备，管理台直接复用）

| 域 | 已有端点 | 鉴权 |
|----|----------|------|
| 商户 | 注册、列表、配置读写、v3 密钥重发、集成配置、回调验证/重置/死信重放 | 列表/配置操作为 admin |
| 事件 | 创建、状态流转、结算（resolve）、列表/详情 | 写操作 admin |
| 市场 | 创建、状态流转、加流动性、作废（void）、订单簿、列表/详情 | 写操作 admin |
| 订单/资金 | v1 订单 CRUD、v2 订单/成交/流水/结算/派彩/日报、钱包余额/入金/出金 | merchant |
| 分析 | 平台聚合 `GET /api/v1/analytics/platform` | admin |
| 体育 | 事件同步 `POST /api/v1/sports/sync` | admin |
| 审计 | 结算审计表 `event_resolution_audits`、结算级联审计 | 内部 |
| 用户（终端） | `platform_users` 表（merchant_id + external_user_id，status active/blocked，locale） | 无管理端点 |

### 关键缺口（第一期必须补的后端）

1. **无管理员身份体系**：后台只有一个静态 `adminAPIKey`，无登录、无会话、
   无角色权限、无管理员操作审计。
2. **商户管理不完整**：`merchants.status` 有字段但无 admin 端点变更
   （挂起/启用）；`fee_rate` 在库中但配置接口不返回也不可改；
   注册是开放端点，无审批/留痕流程。
3. **商户用户管理完全空白**：`platform_users` 无列表/详情/封禁端点。
4. **事件管理不完整**：创建后不可编辑（标题/描述/结算时间），
   无 admin 全局分页搜索。
5. **市场管理不完整**：创建后不可编辑，结算/派彩只能走 v2 商户视角，
   无 admin 全局视图。
6. **监控盲区**：无全局订单/资金流水检索、无管理端审计日志查询、
   无仪表盘聚合端点（analytics/platform 粒度不够）。

---

## 二、目标与设计原则

1. **管理台是内部运营工具**：只给平台运营/超管用，第一期为中文界面，
   结构上预留 i18n（与托管前端同一套 `t()` 模式）。
2. **零构建、嵌入二进制**：与 `web/hosted` 相同路线——vanilla JS + CSS，
   `go:embed` 进 API 二进制，`GET /admin` 托管，无外部脚本/字体/CDN。
3. **权限最小化**：管理台全部走新的会话鉴权；现有 `adminAPIKey` 通道保留
   给自动化/脚本，两个通道都进审计。
4. **所有写操作可追溯**：管理员的每个变更（登录、改状态、结算、重发密钥、
   封禁用户）写入审计日志，管理台可查询。
5. **资金操作双确认**：结算/作废/重发密钥等敏感操作需输入确认词二次提交。
6. **沿用现有约定**：金额字符串定点、`/api/v1` 冻结、错误格式
  `{error:{code,message}}`、分页参数 page/limit。

---

## 三、总体架构

```
┌──────────────┐  /admin（嵌入二进制的零构建 SPA）
│  运营浏览器   │  ──────────────────────────────┐
└──────────────┘                                 ▼
                                        ┌──────────────────┐
                                        │  Admin Console   │
                                        │  (web/admin/*)   │
                                        └────────┬─────────┘
                                                 │ Bearer admin-session JWT
                                                 ▼
                              ┌──────────────────────────────┐
                              │  /api/v1/admin/*（新命名空间）│
                              │  admin_session 中间件 + RBAC  │
                              └──────────────┬───────────────┘
                                             │
                        ┌────────────────────┼────────────────────┐
                        ▼                    ▼                    ▼
                 merchant.Service    event/market.Service   platformuser/wallet
                        │                    │              /audit /callback
                        └────────── 现有 v1 服务层复用 ────────┘
```

- **认证**：`admin_accounts` 表（用户名 + bcrypt + 角色），
  `POST /api/v1/admin/login` 签发短期 HttpOnly JWT（复用 `SESSION_JWT_SECRET`
  机制，独立签发与校验），`admin_session` 中间件；登录失败限流。
- **RBAC（第一期最小集）**：`super_admin`（一切）/ `operator`（日常运营，
  不可：变更商户状态与费率、重发密钥、配置集成、创建管理员）。
- **前端**：`web/admin/`（index.html + app.js + styles.css），
  复用托管页的 shell/卡片/面板视觉语言与 `t()` 模式；
  路由 `#/dashboard`、`#/merchants`、`#/merchants/:id`、
  `#/users`、`#/events`、`#/events/:id`、`#/markets`、`#/markets/:id`、
  `#/orders`、`#/audit`。

---

## 四、第一期模块清单（按优先级）

### P0 管理台骨架 + 管理员身份（先行，1 周）

- 迁移 `016_admin_accounts.sql`：`admin_accounts(id, username, password_hash,
  role, status, created_at, last_login_at)`；`admin_action_logs(action,
  admin_id, resource, resource_id, payload, ip, created_at)`。
- 端点：`POST /api/v1/admin/login`、`POST /api/v1/admin/logout`、
  `GET /api/v1/admin/me`、`GET /api/v1/admin/audit-logs`（分页+筛选）；
  首个管理员由启动参数/环境变量引导创建（adminAPIKey 仅保留给自动化）。
- 前端：登录页、布局壳（侧栏+顶栏）、会话过期自动跳登录。
- **验收**：错误密码连续 5 次锁定 15 分钟；登录/登出/越权尝试全部入审计；
  无会话访问 `/api/v1/admin/*` 返回 401。

### P1 商户管理（第二周）

- 页面：商户列表（分页+关键词搜索：名称/邮箱/ID）、商户详情
  （基本信息、费率、币种、时区、钱包模式、集成配置、回调健康、密钥区）。
- 端点（新增/补齐）：
  - `GET /api/v1/admin/merchants`（现有，补 fee_rate/wallet_mode/集成健康字段）；
  - `GET /api/v1/admin/merchants/{id}` 详情；
  - `PATCH /api/v1/admin/merchants/{id}`（名称/费率/币种/时区，费率变更入审计）；
  - `PATCH /api/v1/admin/merchants/{id}/status`（active/suspended，写操作需双确认）；
  - 复用：v3 密钥重发、集成配置、回调验证/重置/死信重放（改挂 admin 会话鉴权）。
- **验收**：挂起商户后其 v1/v2 请求返回 403（需在鉴权中间件补状态校验）；
  费率变更在审计日志可见 before/after；死信重放页一键重放并可看结果。

### P2 商户用户管理（第二周）

- 页面：用户列表（按商户筛选 + 外部用户 ID 搜索）、用户详情
  （locale、状态、钱包余额/锁定、最近流水、订单数、最近下单时间）。
- 端点（新增）：
  - `GET /api/v1/admin/users?merchant_id=&q=&status=`；
  - `GET /api/v1/admin/users/{merchant_id}/{external_user_id}`（含钱包汇总）；
  - `GET /api/v1/admin/users/{merchant_id}/{external_user_id}/transactions`；
  - `PATCH /api/v1/admin/users/{merchant_id}/{external_user_id}/status`
    （active/blocked，双确认）。
- **验收**：封禁后该用户的 v3 会话换取与下单返回 403（补 platformuser
  状态校验到 session exchange / 下单链路）；解封即时恢复，全程留审计。

### P3 事件管理（第三周）

- 页面：事件列表（分类/状态/时间筛选 + 关键词）、事件详情
  （基本信息、关联市场、结算状态、审计轨迹）、创建/编辑表单、结算操作台
  （选结果→双确认→展示派彩汇总）、体育数据源同步触发。
- 端点（新增/补齐）：
  - `PATCH /api/v1/admin/events/{id}`（标题/描述/结算时间，状态流转除外）；
  - `GET /api/v1/admin/events`（admin 全局分页搜索）；
  - 复用：`POST /api/v1/events`、`PATCH .../status`、`POST .../resolve`、
    `POST /api/v1/sports/sync`（改挂 admin 会话鉴权）。
- **验收**：已结算事件不可再编辑/结算（409）；结算后派彩金额与
  `settlement_payouts` 对账一致；每次结算产生审计与 outbox 记录。

### P4 市场管理（第三周）

- 页面：市场列表（事件/商户/状态筛选 + 关键词）、市场详情
  （问题、选项、报价（订单簿）、交易量、流动性池、结算/派彩、作废操作）。
- 端点（新增/补齐）：
  - `GET /api/v1/admin/markets`（admin 全局分页搜索）；
  - `PATCH /api/v1/admin/markets/{id}`（仅编辑元信息，问题/选项不可改）；
  - 复用：创建、状态流转、加流动性、作废、订单簿、派彩列表（改挂 admin 鉴权）。
- **验收**：作废市场二次确认；作废/结算后订单侧与钱包侧资金变动入流水；
  订单簿页实时可看，报价与 v2 商户视角一致。

### P5 平台总览仪表盘（第四周）

- 页面：指标卡（商户数/活跃商户、终端用户数、今日/累计交易量、手续费收入、
  活跃市场数、待结算市场）、趋势图（近 14 天交易量/订单数，纯 CSS/SVG，
  无图表库）、回调健康（降级商户数、死信队列计数）、最新审计动态。
- 端点：`GET /api/v1/admin/overview`（聚合一次查询，含时间序列）。
- **验收**：仪表盘数据与 analytics/platform 口径一致（复用同一服务层）；
  首屏在管理台 1s 内可交互。

### P6 运营监控（第四周，收尾）

- 页面：全局订单检索（商户/用户/市场/状态/时间）、全局资金流水检索、
  待处理异常（回调 DLQ、降级商户、结算失败）工作台。
- 端点：`GET /api/v1/admin/orders`、`GET /api/v1/admin/transactions`
  （只读，游标分页）。
- **验收**：可按商户+用户定位任意一笔订单与资金链路（订单→成交→流水→派彩
  四段可跳转）。

---

## 五、里程碑与排期（4 周，1 人后端 + 1 人前端）

| 里程碑 | 内容 | 时长 |
|--------|------|------|
| M1 | P0 骨架+身份、P1 商户管理、仪表盘占位 | 第 1–2 周 |
| M2 | P2 商户用户管理、P3 事件管理 | 第 2–3 周 |
| M3 | P4 市场管理、P5 仪表盘、P6 监控 | 第 3–4 周 |
| 收尾 | 全量验收（对照本节验收项）、`docs/ADMIN_ACCEPTANCE_CHECKLIST.md`、真实环境联调 | 第 4 周 |

每里程碑结束都跑现有测试套件 + 管理台浏览器冒烟（沿用托管页的验证方式）。

## 六、明确不做（第一期非目标）

- 商户自助入驻审批流（注册仍开放，管理台先做「事后挂起/审计」）；
- 多级代理/分销体系、返佣结算；
- 深度财务对账报表（沿用 v2 日报，后续版本做账期/对账单）；
- 自动化风控/反作弊（封禁先靠人工，预留接口位）；
- 管理员 2FA/密码策略合规（第一期仅 bcrypt + 锁定，后续加固）；
- 管理台多语言（第一期中文，结构预留）。

## 七、风险与开放问题

1. **商户挂起/用户封禁的链路覆盖**：需逐一确认 v1 下单、v2 会话/下单、
   v3 exchange、结算派彩各链路的状态校验点，遗漏会造成「封了还能交易」。
   第一期以鉴权中间件 + 服务层统一校验兜底。
2. **adminAPIKey 与会话双通道并存**：自动化脚本仍走 API Key，
   管理台只走会话；两通道同一套 admin 服务层，避免行为分叉。
3. **结算/作废是资金操作**：第一期依赖双确认 + 审计 + 现有幂等结算，
   不引入审批流；若运营要求多级审批，列入第二期。
4. **仪表盘口径**：`analytics/platform` 现有口径与「手续费收入」需确认
   是否含 v2 无缝钱包模式下的影子账本数据（fee_ledger 已建，第一期只读展示）。
