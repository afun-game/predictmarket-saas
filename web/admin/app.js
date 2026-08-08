"use strict";

// PredictMarket 管理后台 — Phase-1 admin console.
// Zero-build vanilla JS: hash routing, fetch with same-origin session cookie.

const app = document.querySelector("#app");

/* ============================== 全局状态 ============================== */

let me = null; // { id, username, role } — 来自 GET /api/v1/admin/me

// 列表页视图状态。哈希变化时整体重置；筛选 / 翻页时原地更新后重渲染。
const view = {
  page: 1,
  limit: 20,
  q: "",
  merchantId: "",
  userId: "",
  marketId: "",
  status: "",
  category: "",
  sourceType: "",
  eventId: "",
  type: "",
  userTxPage: 1,
  showCreateEvent: false,
  showCreateMarket: false,
  showCreateMerchant: false,
  merchantCredentials: null, // 开户成功的一次性凭据 { id, api_key, api_secret, ... }
  reissuedSecret: null, // 重发后的 { id, secret }
  testToken: null, // 商户详情生成的测试链接 { launch_url, token, ... }
  translations: [], // 市场创建表单的其他语言行 { locale, question, options }
};

let renderSeq = 0;

/* ============================== 文案 / 映射 ============================== */

const STATUS_LABELS = {
  active: "活跃",
  open: "进行中",
  filled: "已成交",
  suspended: "已暂停",
  blocked: "已封禁",
  voided: "已作废",
  pending: "待开始",
  inactive: "停用",
  cancelled: "已取消",
  canceled: "已取消",
  expired: "已过期",
  closed: "已关闭",
  partial: "部分成交",
  resolved: "已结算",
  settled: "已结算",
  success: "成功",
  failed: "失败",
};

const STATUS_KIND = {
  active: "positive",
  open: "positive",
  filled: "positive",
  success: "positive",
  suspended: "danger",
  blocked: "danger",
  voided: "danger",
  failed: "danger",
  pending: "neutral",
  inactive: "neutral",
  cancelled: "neutral",
  canceled: "neutral",
  expired: "neutral",
  closed: "warning",
  partial: "warning",
  resolved: "info",
  settled: "info",
};

const CATEGORY_LABELS = {
  hot: "热门",
  football: "足球",
  basketball: "篮球",
  baseball: "棒球",
  boxing: "拳击",
  weather: "天气",
  bitcoin: "比特币",
  other: "其它",
};

const MARKET_TYPE_LABELS = {
  binary: "订单簿",
  parimutuel: "奖池",
};

const ORDER_TYPE_LABELS = { buy: "买入", sell: "卖出" };

const ACTION_LABELS = {
  "create.event": "创建事件",
  "update.event": "更新事件",
  "status.event": "修改事件状态",
  "resolve.event": "结算事件",
  "create.market": "创建市场",
  "update.market": "更新市场",
  "status.market": "修改市场状态",
  "liquidity.market": "注入流动性",
  "void.market": "作废市场",
  "update.merchant": "更新商户",
  "status.merchant": "修改商户状态",
  "update.user": "更新用户",
  "status.user": "修改用户状态",
};

const RESOURCE_LABELS = {
  merchant: "商户",
  user: "用户",
  event: "事件",
  market: "市场",
  order: "订单",
  transaction: "流水",
  audit_log: "审计日志",
};

const NAV_ITEMS = [
  ["dashboard", "仪表盘"],
  ["merchants", "商户"],
  ["users", "用户"],
  ["events", "事件"],
  ["markets", "市场"],
  ["orders", "订单"],
  ["transactions", "流水"],
  ["audit", "审计"],
];

const NAV_ICONS = {
  dashboard: "◧",
  merchants: "▣",
  users: "◉",
  events: "▤",
  markets: "◫",
  orders: "▦",
  transactions: "⇄",
  audit: "☰",
};

const PAGE_TITLES = {
  dashboard: "仪表盘",
  merchants: "商户管理",
  users: "用户管理",
  events: "事件管理",
  markets: "市场管理",
  orders: "订单管理",
  transactions: "流水管理",
  audit: "审计日志",
};

/* ============================== 基础工具 ============================== */

function escapeHTML(value) {
  return String(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#039;");
}

function parseRoute() {
  const route = window.location.hash.replace(/^#/, "") || "/dashboard";
  return route.split("/").filter(Boolean);
}

function qs(params) {
  const search = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    if (value !== undefined && value !== null && value !== "") search.set(key, String(value));
  }
  const text = search.toString();
  return text ? `?${text}` : "";
}

function formatTime(value) {
  if (!value) return "—";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return String(value);
  const pad = (n) => String(n).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

function formatMoney(value) {
  if (value === null || value === undefined || value === "") return "—";
  const number = Number(value);
  if (!Number.isFinite(number)) return String(value);
  return number.toLocaleString("zh-CN", { maximumFractionDigits: 6 });
}

function toRFC3339(value) {
  if (!value) return "";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "" : date.toISOString();
}

function toDateTimeLocal(value) {
  if (!value) return "";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  const pad = (n) => String(n).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

function statusBadge(status) {
  const label = STATUS_LABELS[status] ?? status ?? "—";
  const kind = STATUS_KIND[status] ?? "neutral";
  return `<span class="badge badge--${kind}">${escapeHTML(label)}</span>`;
}

function marketTypeBadge(type) {
  const label = MARKET_TYPE_LABELS[type] ?? type ?? "—";
  const kind = type === "parimutuel" ? "info" : "neutral";
  return `<span class="badge badge--${kind}">${escapeHTML(label)}</span>`;
}

function actionLabel(action) {
  return ACTION_LABELS[action] ?? action ?? "—";
}

function resourceLabel(resource) {
  return RESOURCE_LABELS[resource] ?? resource ?? "—";
}

function categoryLabel(category) {
  return CATEGORY_LABELS[category] ?? category ?? "—";
}

function statusOptions(statuses, current) {
  const options = ['<option value="">全部</option>'];
  for (const status of statuses) {
    options.push(
      `<option value="${status}" ${status === current ? "selected" : ""}>${escapeHTML(STATUS_LABELS[status] ?? status)}</option>`
    );
  }
  return options.join("");
}

function categoryOptions(current) {
  const options = ['<option value="">全部</option>'];
  for (const [id, label] of Object.entries(CATEGORY_LABELS)) {
    options.push(`<option value="${id}" ${id === current ? "selected" : ""}>${label}</option>`);
  }
  return options.join("");
}

function toast(message, kind = "success") {
  const el = document.createElement("div");
  el.className = `toast toast--${kind}`;
  el.textContent = message;
  document.body.appendChild(el);
  window.setTimeout(() => el.remove(), 2600);
}

/* ============================== API 封装 ============================== */

async function apiFetch(path, options = {}) {
  const headers = new Headers(options.headers ?? {});
  headers.set("Accept", "application/json");
  if (options.body !== undefined) headers.set("Content-Type", "application/json");
  const response = await fetch(path, { ...options, headers, credentials: "same-origin" });
  let payload = null;
  try {
    payload = await response.json();
  } catch {
    /* 204 / 空响应 */
  }
  if (response.status === 401) {
    me = null;
    if (parseRoute()[0] !== "login") window.location.hash = "#/login";
    throw new Error(payload?.error?.message ?? "登录已过期，请重新登录");
  }
  if (!response.ok) {
    throw new Error(payload?.error?.message ?? `请求失败（HTTP ${response.status}）`);
  }
  return payload?.data ?? payload;
}

/* ============================== 页面骨架 ============================== */

function shell(content, root) {
  const nav = NAV_ITEMS.map(([id, label]) => {
    const current = root === id ? ' aria-current="page"' : "";
    return `<a href="#/${id}"${current}><span class="nav-icon" aria-hidden="true">${NAV_ICONS[id]}</span>${label}</a>`;
  }).join("");
  const title = PAGE_TITLES[root] ?? "管理后台";
  return `
  <div class="layout">
    <aside class="sidebar">
      <a class="sidebar__brand" href="#/dashboard">PredictMarket<small>管理后台</small></a>
      <nav class="sidebar__nav">${nav}</nav>
    </aside>
    <div class="main">
      <header class="topbar">
        <h1 class="topbar__title">${escapeHTML(title)}</h1>
        <div class="topbar__right">
          <span class="topbar__role">${me.role === "super_admin" ? "超级管理员" : "运营员"}</span>
          <span class="topbar__who">${escapeHTML(me.username)}</span>
          <button class="btn btn--ghost btn--sm" type="button" data-action="logout">退出</button>
        </div>
      </header>
      <main class="content">${content}</main>
    </div>
  </div>`;
}

function loginPage() {
  return `
  <div class="login">
    <form class="login__card" data-action="login">
      <div class="login__brand">
        <span class="login__mark" aria-hidden="true">P</span>
        <div><h1>PredictMarket</h1><p>管理后台</p></div>
      </div>
      <div class="field">
        <label for="login-username">用户名</label>
        <input class="input" id="login-username" name="username" autocomplete="username" required>
      </div>
      <div class="field">
        <label for="login-password">密码</label>
        <input class="input" id="login-password" name="password" type="password" autocomplete="current-password" required>
      </div>
      <button class="btn btn--primary btn--block" type="submit">登录</button>
      <p class="login__error" data-login-error hidden></p>
    </form>
  </div>`;
}

function notFoundPage() {
  return `<section class="page"><div class="empty"><p>页面不存在</p><a class="btn btn--primary" href="#/dashboard">返回仪表盘</a></div></section>`;
}

function errorPage(err) {
  return `<section class="page"><div class="error-state"><h2>加载失败</h2><p>${escapeHTML(err.message)}</p><button class="btn btn--primary" type="button" data-action="retry">重试</button></div></section>`;
}

/* ============================== 通用组件 ============================== */

function kvList(entries) {
  return entries
    .map(([label, value]) => `<div class="kv__item"><div class="kv__label">${escapeHTML(label)}</div><div class="kv__value">${value}</div></div>`)
    .join("");
}

function credentialRow(label, value, sourceKey) {
  const safe = escapeHTML(value);
  const copyBtn =
    value && value !== "—"
      ? `<button class="btn btn--ghost btn--sm" type="button" data-action="copy" data-copy-value="${safe}" data-copy-source="${escapeHTML(sourceKey)}">复制</button>`
      : "";
  return `<div class="kv__item">
    <div class="kv__label">${escapeHTML(label)}</div>
    <div class="kv__value"><div class="cred-line">
      <code class="td-mono" data-copy-source="${escapeHTML(sourceKey)}">${safe}</code>
      ${copyBtn}
    </div></div>
  </div>`;
}

function credentialsPanel(cred) {
  const callbackRow =
    cred.callback_secret
      ? credentialRow("Callback Secret", cred.callback_secret, "callbackSecret")
      : "";
  return `
  <div class="card cred-panel">
    <div class="section-heading"><h2>开户凭据</h2></div>
    <div class="cred-alert">凭据仅显示一次，请立即保存</div>
    <div class="kv">
      ${credentialRow("商户 ID", cred.id ?? "", "merchantId")}
      ${credentialRow("API Key", cred.api_key ?? "", "apiKey")}
      ${credentialRow("API Secret", cred.api_secret ?? "", "apiSecret")}
      ${callbackRow}
    </div>
    <div class="cred-actions">
      <button class="btn btn--primary" type="button" data-action="dismiss-credentials" data-id="${escapeHTML(cred.id ?? "")}">我已保存</button>
    </div>
  </div>`;
}

function tableCard(headers, rows, emptyText, colspan) {
  const head = headers.map((h) => `<th>${escapeHTML(h)}</th>`).join("");
  const body = rows || `<tr><td colspan="${colspan}"><div class="empty">${escapeHTML(emptyText)}</div></td></tr>`;
  return `<div class="table-card"><table><thead><tr>${head}</tr></thead><tbody>${body}</tbody></table></div>`;
}

function paginationBar(total, page, target = "list") {
  const pages = Math.max(1, Math.ceil((total ?? 0) / view.limit));
  const prev = `<button class="btn btn--ghost btn--sm" type="button" data-action="page" data-target="${target}" data-page="${page - 1}" ${page <= 1 ? "disabled" : ""}>上一页</button>`;
  const next = `<button class="btn btn--ghost btn--sm" type="button" data-action="page" data-target="${target}" data-page="${page + 1}" ${page >= pages ? "disabled" : ""}>下一页</button>`;
  return `<div class="pagination"><span class="pagination__total">共 ${escapeHTML(total ?? 0)} 条</span><div class="pagination__btns">${prev}<span class="pagination__current">第 ${page} / ${pages} 页</span>${next}</div></div>`;
}

/* ============================== 页面：仪表盘 ============================== */

async function dashboardPage() {
  const overview = await apiFetch("/api/v1/admin/overview");
  let recent = [];
  try {
    const audit = await apiFetch("/api/v1/admin/audit-logs?limit=5");
    recent = audit.items ?? [];
  } catch {
    /* 最近操作提示区为可选项 */
  }

  const merchants = overview.merchants ?? {};
  const users = overview.users ?? {};
  const markets = overview.markets ?? {};
  const orders = overview.orders ?? {};
  const fees = overview.fees ?? {};
  const settlements = overview.settlements ?? {};

  const statCards = [
    ["商户总数", formatMoney(merchants.total), `活跃 ${formatMoney(merchants.active)} · 暂停 ${formatMoney(merchants.suspended)}`],
    ["活跃商户", formatMoney(merchants.active), null],
    ["终端用户", formatMoney(users.total), null],
    ["活跃市场", formatMoney(markets.active), `市场总数 ${formatMoney(markets.total)}`],
    ["今日订单", formatMoney(orders.today), null],
    ["今日交易量", formatMoney(orders.volume_today), null],
    ["今日手续费", formatMoney(fees.today), null],
    ["待结算", formatMoney(settlements.pending), null],
  ]
    .map(
      ([label, value, sub]) => `
    <div class="stat-card">
      <div class="stat-card__label">${escapeHTML(label)}</div>
      <div class="stat-card__value">${value}</div>
      ${sub ? `<div class="stat-card__sub">${escapeHTML(sub)}</div>` : ""}
    </div>`
    )
    .join("");

  const series = Array.isArray(overview.series) ? overview.series : [];
  const maxOrders = Math.max(1, ...series.map((day) => Number(day.orders) || 0));
  const bars = series
    .map((day) => {
      const height = Math.max(3, Math.min(78, Math.round(((Number(day.orders) || 0) / maxOrders) * 100)));
      const tip = `${day.date} · 订单 ${day.orders ?? 0} · 交易量 ${formatMoney(day.volume)}`;
      return `<div class="chart__col" title="${escapeHTML(tip)}"><span class="chart__value">${escapeHTML(day.orders ?? 0)}</span><span class="chart__bar" style="height:${height}%"></span><span class="chart__date">${escapeHTML(String(day.date ?? "").slice(5))}</span></div>`;
    })
    .join("");

  const recentRows = recent
    .map(
      (entry) => `
    <div class="audit-line">
      <span class="audit-line__text">${escapeHTML(entry.admin_username ?? "—")} · ${escapeHTML(actionLabel(entry.action))} ${escapeHTML(resourceLabel(entry.resource))}${entry.resource_id != null && entry.resource_id !== "" ? ` #${escapeHTML(entry.resource_id)}` : ""}</span>
      <time class="audit-line__time">${formatTime(entry.created_at)}</time>
    </div>`
    )
    .join("");

  return `
    <section class="page">
      <div class="stat-grid">${statCards}</div>
      <div class="card chart-card">
        <div class="section-heading"><h2>近 14 天订单趋势</h2><span class="hint">每日订单量</span></div>
        <div class="chart">${bars || `<div class="empty">暂无数据</div>`}</div>
      </div>
      <div class="card">
        <div class="section-heading"><h2>最近操作</h2><a class="text-link" href="#/audit">查看全部</a></div>
        ${recentRows || `<div class="empty">暂无操作记录</div>`}
      </div>
    </section>`;
}

/* ============================== 页面：商户 ============================== */

async function merchantsPage() {
  const data = await apiFetch(`/api/v1/admin/merchants${qs({ q: view.q, page: view.page, limit: view.limit })}`);
  const isSuper = me.role === "super_admin";
  const rows = (data.items ?? [])
    .map(
      (item) => `
    <tr class="clickable" data-nav="#/merchants/${escapeHTML(item.id)}">
      <td class="td-strong">${escapeHTML(item.name)}</td>
      <td>${escapeHTML(item.email ?? "—")}</td>
      <td>${statusBadge(item.status)}</td>
      <td>${escapeHTML(item.currency ?? "—")}</td>
      <td>${escapeHTML(item.fee_rate ?? "—")}</td>
      <td class="td-muted">${formatTime(item.created_at)}</td>
    </tr>`
    )
    .join("");
  const createForm =
    isSuper && !view.merchantCredentials && view.showCreateMerchant
      ? `
    <form class="create-form" data-action="create-merchant">
      <div class="form-grid">
        <div class="field"><label>名称 *</label><input class="input" name="name" required></div>
        <div class="field"><label>邮箱 *</label><input class="input" name="email" type="email" required></div>
        <div class="field"><label>币种</label><input class="input" name="currency" value="USD"></div>
        <div class="field"><label>时区</label><input class="input" name="timezone" value="UTC"></div>
        <div class="field"><label>钱包模式</label><select class="input" name="wallet_mode" data-wallet-toggle>
          <option value="transfer" selected>转账模式 transfer</option>
          <option value="seamless">无缝模式 seamless</option>
        </select></div>
        <div class="field" data-callback-field hidden><label>回调地址（seamless 必填）</label><input class="input" name="callback_url" placeholder="https://merchant.example.com/hooks" autocomplete="off"></div>
        <div class="field form-field--actions"><button class="btn btn--primary" type="submit">创建商户</button><button class="btn btn--ghost" type="button" data-action="toggle-create-merchant">取消</button></div>
      </div>
    </form>`
      : "";
  const credArea = view.merchantCredentials ? credentialsPanel(view.merchantCredentials) : "";
  return `
    <section class="page">
      <form class="filter-bar" data-action="filter">
        <div class="field field--grow"><input class="input" name="q" value="${escapeHTML(view.q)}" placeholder="搜索商户名称 / 邮箱" autocomplete="off"></div>
        <div class="field form-field--actions"><button class="btn btn--primary" type="submit">搜索</button>${view.q ? `<button class="btn btn--ghost" type="button" data-action="reset-filters">重置</button>` : ""}</div>
      </form>
      <div class="section-heading list-heading"><h2>商户列表</h2>${isSuper && !view.merchantCredentials ? `<button class="btn btn--primary" type="button" data-action="toggle-create-merchant">${view.showCreateMerchant ? "收起表单" : "新增商户"}</button>` : ""}</div>
      ${createForm}
      ${credArea}
      ${tableCard(["名称", "邮箱", "状态", "币种", "费率", "创建时间"], rows, "暂无商户", 6)}
      ${paginationBar(data.total, view.page)}
    </section>`;
}

async function merchantDetailPage(id) {
  const merchant = await apiFetch(`/api/v1/admin/merchants/${id}`);
  const stats = merchant.stats ?? {};
  const isSuper = me.role === "super_admin";
  const actions = [];
  if (isSuper) {
    actions.push(`<button class="btn btn--ghost" type="button" data-action="merchant-test-token" data-id="${escapeHTML(id)}">测试链接</button>`);
    if (merchant.status === "active") {
      actions.push(`<button class="btn btn--danger" type="button" data-action="merchant-status" data-id="${escapeHTML(id)}" data-status="suspended">暂停商户</button>`);
    } else if (merchant.status === "suspended" || merchant.status === "inactive") {
      actions.push(`<button class="btn btn--primary" type="button" data-action="merchant-status" data-id="${escapeHTML(id)}" data-status="active">恢复商户</button>`);
    }
  }
  const kv = kvList([
    ["名称", escapeHTML(merchant.name ?? "—")],
    ["邮箱", escapeHTML(merchant.email ?? "—")],
    ["状态", statusBadge(merchant.status)],
    ["币种", escapeHTML(merchant.currency ?? "—")],
    ["时区", escapeHTML(merchant.timezone ?? "—")],
    ["钱包模式", escapeHTML(merchant.wallet_mode ?? "—")],
    ["费率", escapeHTML(merchant.fee_rate ?? "—")],
    ["创建时间", formatTime(merchant.created_at)],
  ]);
  const statCards = [
    ["用户数", formatMoney(stats.user_count)],
    ["市场数", formatMoney(stats.market_count)],
    ["订单数", formatMoney(stats.order_count)],
    ["总交易量", formatMoney(stats.total_volume)],
  ]
    .map(
      ([label, value]) => `<div class="stat-card"><div class="stat-card__label">${escapeHTML(label)}</div><div class="stat-card__value">${value}</div></div>`
    )
    .join("");
  const editForm = isSuper
    ? `
    <div class="card">
      <div class="section-heading"><h2>编辑商户</h2></div>
      <form class="form-grid" data-action="edit-merchant" data-id="${escapeHTML(id)}">
        <div class="field"><label>名称</label><input class="input" name="name" value="${escapeHTML(merchant.name ?? "")}"></div>
        <div class="field"><label>币种</label><input class="input" name="currency" value="${escapeHTML(merchant.currency ?? "")}" placeholder="CNY"></div>
        <div class="field"><label>时区</label><input class="input" name="timezone" value="${escapeHTML(merchant.timezone ?? "")}" placeholder="Asia/Shanghai"></div>
        <div class="field"><label>费率</label><input class="input" name="fee_rate" value="${escapeHTML(merchant.fee_rate ?? "")}" placeholder="0.005"></div>
        <div class="field"><label>钱包模式</label><select class="input" name="wallet_mode" data-wallet-toggle>
          <option value="transfer" ${(merchant.wallet_mode ?? "transfer") === "transfer" ? "selected" : ""}>转账模式 transfer</option>
          <option value="seamless" ${merchant.wallet_mode === "seamless" ? "selected" : ""}>无缝模式 seamless</option>
        </select></div>
        <div class="field" data-callback-field ${merchant.wallet_mode === "seamless" ? "" : "hidden"}><label>回调地址（seamless 必填）</label><input class="input" name="callback_url" value="${escapeHTML(merchant.callback_url ?? "")}" placeholder="https://merchant.example.com/hooks" autocomplete="off"></div>
        <div class="field form-grid__actions"><button class="btn btn--primary" type="submit">保存</button></div>
      </form>
    </div>`
    : "";
  const reissuePanel = view.reissuedSecret && view.reissuedSecret.id === id
    ? `
    <div class="card cred-panel">
      <div class="section-heading"><h2>${view.reissuedSecret.callback ? "回调密钥（Callback Secret）" : "重发的 API Secret"}</h2></div>
      <div class="cred-alert">凭据仅显示一次，请立即保存</div>
      <div class="kv">${credentialRow(view.reissuedSecret.callback ? "Callback Secret" : "API Secret", view.reissuedSecret.api_secret ?? "", "reissuedSecret")}</div>
      <div class="cred-actions"><button class="btn btn--primary" type="button" data-action="dismiss-credentials">我已保存</button></div>
    </div>`
    : "";
  const testTokenPanel = view.testToken && view.testToken.merchant_id === id
    ? `
    <div class="card cred-panel">
      <div class="section-heading"><h2>测试链接</h2><span class="label">15 分钟有效 · 一次性</span></div>
      <div class="cred-alert">把链接交给前端同事直接打开或嵌入 iframe 即可进入托管页</div>
      <div class="kv">
        ${credentialRow("Launch URL", view.testToken.launch_url ?? "", "testLaunchUrl")}
        ${credentialRow("Token", view.testToken.token ?? "", "testTokenValue")}
        ${credentialRow("用户 ID", view.testToken.user_id ?? "—", "testUserId")}
        ${credentialRow("钱包模式", view.testToken.wallet_mode ?? "—", "testWalletMode")}
      </div>
      <div class="cred-actions"><button class="btn btn--primary" type="button" data-action="dismiss-credentials">关闭</button></div>
    </div>`
    : "";
  const credentialsCard = `
    <div class="card">
      <div class="section-heading"><h2>API 凭据</h2>${isSuper ? `<button class="btn btn--ghost" type="button" data-action="merchant-reissue-secret" data-id="${escapeHTML(id)}">重发 API Secret</button>` : ""}</div>
      <div class="kv">
        ${credentialRow("商户 ID", id, "merchantId")}
        ${credentialRow("API Key 前缀", merchant.api_key_prefix ?? "—", "apiKeyPrefix")}
        ${credentialRow("Wallet 模式", merchant.wallet_mode ?? "—", "walletMode")}
      </div>
      <p class="field-hint">API Secret 出于安全不在此展示，重发后可查看一次。</p>
    </div>`;
  return `
    <section class="page">
      <div class="breadcrumb"><a href="#/merchants">商户管理</a><span>/</span><span>${escapeHTML(merchant.name ?? id)}</span></div>
      <div class="detail-head">
        <div><h2>商户详情</h2><p class="detail-head__id">ID：${escapeHTML(id)}</p></div>
        <div class="detail-head__actions">${actions.join("")}</div>
      </div>
      <div class="card"><div class="section-heading"><h2>基本信息</h2></div><div class="kv">${kv}</div></div>
      ${credentialsCard}
      ${reissuePanel}
      ${testTokenPanel}
      <div class="stat-grid">${statCards}</div>
      ${editForm}
    </section>`;
}

/* ============================== 页面：用户 ============================== */

async function usersPage() {
  const data = await apiFetch(
    `/api/v1/admin/users${qs({ merchant_id: view.merchantId, q: view.q, status: view.status, page: view.page, limit: view.limit })}`
  );
  const rows = (data.items ?? [])
    .map(
      (item) => `
    <tr class="clickable" data-nav="#/users/${escapeHTML(item.merchant_id)}/${escapeHTML(item.external_user_id)}">
      <td class="td-mono">${escapeHTML(item.merchant_id)}</td>
      <td class="td-mono">${escapeHTML(item.external_user_id)}</td>
      <td>${statusBadge(item.status)}</td>
      <td class="td-num">${formatMoney(item.balance)}</td>
      <td class="td-num">${formatMoney(item.locked_balance)}</td>
      <td class="td-num">${escapeHTML(item.order_count ?? "—")}</td>
    </tr>`
    )
    .join("");
  return `
    <section class="page">
      <form class="filter-bar" data-action="filter">
        <div class="field"><label>商户 ID</label><input class="input" name="merchant_id" value="${escapeHTML(view.merchantId)}" autocomplete="off"></div>
        <div class="field field--grow"><label>搜索</label><input class="input" name="q" value="${escapeHTML(view.q)}" placeholder="外部用户 ID" autocomplete="off"></div>
        <div class="field"><label>状态</label><select class="input" name="status">${statusOptions(["active", "blocked"], view.status)}</select></div>
        <div class="field form-field--actions"><button class="btn btn--primary" type="submit">筛选</button>${view.q || view.merchantId || view.status ? `<button class="btn btn--ghost" type="button" data-action="reset-filters">重置</button>` : ""}</div>
      </form>
      ${tableCard(["商户 ID", "外部用户 ID", "状态", "余额", "锁定余额", "订单数"], rows, "暂无用户", 6)}
      ${paginationBar(data.total, view.page)}
    </section>`;
}

async function userDetailPage(merchantId, userId) {
  const user = await apiFetch(`/api/v1/admin/users/${merchantId}/${userId}`);
  const isSuper = me.role === "super_admin";
  const actions = [];
  if (isSuper) {
    if (user.status === "active") {
      actions.push(`<button class="btn btn--danger" type="button" data-action="user-status" data-merchant="${escapeHTML(merchantId)}" data-user="${escapeHTML(userId)}" data-status="blocked">封禁用户</button>`);
    } else if (user.status === "blocked") {
      actions.push(`<button class="btn btn--primary" type="button" data-action="user-status" data-merchant="${escapeHTML(merchantId)}" data-user="${escapeHTML(userId)}" data-status="active">解封用户</button>`);
    }
  }
  const kv = kvList([
    ["商户 ID", escapeHTML(merchantId)],
    ["外部用户 ID", escapeHTML(userId)],
    ["语言", escapeHTML(user.locale ?? "—")],
    ["状态", statusBadge(user.status)],
    ["订单数", formatMoney(user.order_count)],
    ["最近下单", formatTime(user.last_order_at)],
    ["创建时间", formatTime(user.created_at)],
  ]);
  const walletRows = (user.wallets ?? [])
    .map(
      (wallet) => `
    <tr><td class="td-strong">${escapeHTML(wallet.currency)}</td><td class="td-num">${formatMoney(wallet.balance)}</td><td class="td-num">${formatMoney(wallet.locked_balance)}</td></tr>`
    )
    .join("");
  const txData = await apiFetch(`/api/v1/admin/users/${merchantId}/${userId}/transactions${qs({ page: view.userTxPage, limit: view.limit })}`);
  const txRows = (txData.items ?? [])
    .map(
      (tx) => `
    <tr>
      <td class="td-mono">${escapeHTML(tx.id)}</td>
      <td>${escapeHTML(tx.type ?? "—")}</td>
      <td class="td-num">${formatMoney(tx.amount)}</td>
      <td>${escapeHTML(tx.currency ?? "—")}</td>
      <td>${statusBadge(tx.status)}</td>
      <td class="td-muted">${formatTime(tx.created_at)}</td>
    </tr>`
    )
    .join("");
  return `
    <section class="page">
      <div class="breadcrumb"><a href="#/users">用户管理</a><span>/</span><span>${escapeHTML(userId)}</span></div>
      <div class="detail-head">
        <div><h2>用户详情</h2><p class="detail-head__id">商户 ${escapeHTML(merchantId)} · 用户 ${escapeHTML(userId)}</p></div>
        <div class="detail-head__actions">${actions.join("")}</div>
      </div>
      <div class="card"><div class="section-heading"><h2>基本信息</h2></div><div class="kv">${kv}</div></div>
      <div class="card"><div class="section-heading"><h2>钱包</h2></div>${tableCard(["币种", "余额", "锁定余额"], walletRows, "暂无钱包", 3)}</div>
      <div class="card">
        <div class="section-heading"><h2>交易流水</h2></div>
        ${tableCard(["ID", "类型", "金额", "币种", "状态", "时间"], txRows, "暂无流水", 6)}
        ${paginationBar(txData.total, view.userTxPage, "tx")}
      </div>
    </section>`;
}

/* ============================== 页面：事件 ============================== */

async function eventsPage() {
  const data = await apiFetch(
    `/api/v1/admin/events${qs({ q: view.q, category: view.category, status: view.status, source_type: view.sourceType, page: view.page, limit: view.limit })}`
  );
  const rows = (data.items ?? [])
    .map(
      (item) => `
    <tr class="clickable" data-nav="#/events/${escapeHTML(item.id)}">
      <td class="td-strong">${escapeHTML(item.title)}</td>
      <td>${escapeHTML(categoryLabel(item.category))}</td>
      <td class="td-mono">${escapeHTML(item.source_type ?? "—")}</td>
      <td>${statusBadge(item.status)}</td>
      <td class="td-muted">${formatTime(item.resolution_time)}</td>
    </tr>`
    )
    .join("");
  const createForm = view.showCreateEvent
    ? `
    <form class="create-form" data-action="create-event">
      <div class="form-grid">
        <div class="field field--span2"><label>标题 *</label><input class="input" name="title" required></div>
        <div class="field field--span2"><label>描述</label><textarea class="input" name="description" rows="3"></textarea></div>
        <div class="field"><label>分类 *</label><select class="input" name="category" required>${categoryOptions("")}</select></div>
        <div class="field"><label>结束时间 *</label><input class="input" type="datetime-local" name="end_time" required></div>
        <div class="field field--span2"><label>结算时间 *</label><input class="input" type="datetime-local" name="resolution_time" required></div>
        <div class="field form-field--actions"><button class="btn btn--primary" type="submit">创建事件</button><button class="btn btn--ghost" type="button" data-action="toggle-create-event">取消</button></div>
      </div>
    </form>`
    : "";
  return `
    <section class="page">
      <form class="filter-bar" data-action="filter">
        <div class="field field--grow"><label>搜索</label><input class="input" name="q" value="${escapeHTML(view.q)}" placeholder="事件标题" autocomplete="off"></div>
        <div class="field"><label>分类</label><select class="input" name="category">${categoryOptions(view.category)}</select></div>
        <div class="field"><label>状态</label><select class="input" name="status">${statusOptions(["pending", "active", "closed", "resolved"], view.status)}</select></div>
        <div class="field"><label>来源</label><select class="input" name="source_type"><option value="">全部来源</option><option value="custom"${view.sourceType === "custom" ? " selected" : ""}>custom</option><option value="polymarket"${view.sourceType === "polymarket" ? " selected" : ""}>polymarket</option><option value="lmb"${view.sourceType === "lmb" ? " selected" : ""}>lmb</option><option value="boxrec"${view.sourceType === "boxrec" ? " selected" : ""}>boxrec</option></select></div>
        <div class="field form-field--actions"><button class="btn btn--primary" type="submit">筛选</button>${view.q || view.category || view.status || view.sourceType ? `<button class="btn btn--ghost" type="button" data-action="reset-filters">重置</button>` : ""}</div>
      </form>
      <div class="section-heading list-heading"><h2>事件列表</h2><button class="btn btn--primary" type="button" data-action="toggle-create-event">${view.showCreateEvent ? "收起表单" : "新建事件"}</button></div>
      ${createForm}
      ${tableCard(["标题", "分类", "来源", "状态", "结算时间"], rows, "暂无事件", 5)}
      ${paginationBar(data.total, view.page)}
    </section>`;
}

async function eventDetailPage(id) {
  const event = await apiFetch(`/api/v1/admin/events/${id}`);
  const actions = [];
  if (event.status === "pending") {
    actions.push(`<button class="btn btn--primary" type="button" data-action="event-status" data-id="${escapeHTML(id)}" data-status="active">激活事件</button>`);
  } else if (event.status === "active" || event.status === "closed") {
    if (event.status === "active") {
      actions.push(`<button class="btn btn--warning" type="button" data-action="event-status" data-id="${escapeHTML(id)}" data-status="closed">关闭事件</button>`);
    }
    actions.push(`<button class="btn btn--primary" type="button" data-action="event-resolve" data-id="${escapeHTML(id)}">结算事件</button>`);
  }
  const marketRows = (event.markets ?? [])
    .map(
      (market) => `
    <tr class="clickable" data-nav="#/markets/${escapeHTML(market.id)}">
      <td class="td-strong">${escapeHTML(market.question)}</td>
      <td>${statusBadge(market.status)}</td>
      <td class="td-num">${formatMoney(market.total_volume)}</td>
    </tr>`
    )
    .join("");
  const kv = kvList([
    ["标题", escapeHTML(event.title ?? "—")],
    ["描述", escapeHTML(event.description ?? "—")],
    ["分类", escapeHTML(categoryLabel(event.category))],
    ["状态", statusBadge(event.status)],
    ["结果", escapeHTML(event.outcome ?? "—")],
    ["结束时间", formatTime(event.end_time)],
    ["结算时间", formatTime(event.resolution_time)],
    ["创建时间", formatTime(event.created_at)],
  ]);
  const editForm = `
    <div class="card">
      <div class="section-heading"><h2>编辑事件</h2></div>
      <form class="form-grid" data-action="edit-event" data-id="${escapeHTML(id)}">
        <div class="field field--span2"><label>标题</label><input class="input" name="title" value="${escapeHTML(event.title ?? "")}"></div>
        <div class="field field--span2"><label>描述</label><textarea class="input" name="description" rows="3">${escapeHTML(event.description ?? "")}</textarea></div>
        <div class="field"><label>结算时间</label><input class="input" type="datetime-local" name="resolution_time" value="${escapeHTML(toDateTimeLocal(event.resolution_time))}"></div>
        <div class="field form-grid__actions"><button class="btn btn--primary" type="submit">保存</button></div>
      </form>
    </div>`;
  return `
    <section class="page">
      <div class="breadcrumb"><a href="#/events">事件管理</a><span>/</span><span>${escapeHTML(event.title ?? id)}</span></div>
      <div class="detail-head">
        <div><h2>事件详情</h2><p class="detail-head__id">ID：${escapeHTML(id)}</p></div>
        <div class="detail-head__actions">${actions.join("")}</div>
      </div>
      <div class="card"><div class="section-heading"><h2>基本信息</h2></div><div class="kv">${kv}</div></div>
      <div class="card"><div class="section-heading"><h2>关联市场</h2></div>${tableCard(["问题", "状态", "交易量"], marketRows, "暂无关联市场", 3)}</div>
      ${editForm}
    </section>`;
}

/* ============================== 页面：市场 ============================== */

async function marketsPage() {
  const data = await apiFetch(
    `/api/v1/admin/markets${qs({ q: view.q, status: view.status, merchant_id: view.merchantId, event_id: view.eventId, page: view.page, limit: view.limit })}`
  );
  let merchantOptions = "";
  if (view.showCreateMarket) {
    try {
      const merchants = await apiFetch("/api/v1/admin/merchants?limit=100");
      merchantOptions = (merchants.items ?? [])
        .map(
          (m) => `<option value="${escapeHTML(m.id)}">${escapeHTML(m.name ?? "")}${m.email ? `（${escapeHTML(m.email)}）` : ""}</option>`
        )
        .join("");
    } catch {
      merchantOptions = "";
    }
  }
  const rows = (data.items ?? [])
    .map(
      (item) => `
    <tr class="clickable" data-nav="#/markets/${escapeHTML(item.id)}">
      <td class="td-strong">${escapeHTML(item.question)}</td>
      <td>${marketTypeBadge(item.type)}</td>
      <td>${categoryLabel(item.category)}</td>
      <td class="td-mono">${escapeHTML(item.merchant_id)}</td>
      <td class="td-mono">${escapeHTML(item.event_id)}</td>
      <td>${statusBadge(item.status)}</td>
      <td class="td-num">${formatMoney(item.total_volume)}</td>
      <td class="td-num">${formatMoney(item.liquidity_pool)}</td>
    </tr>`
    )
    .join("");
  const createForm = view.showCreateMarket
    ? `
    <form class="create-form" data-action="create-market">
      <div class="form-grid">
        <div class="field"><label>商户 *</label><select class="input" name="merchant_id" required><option value="">请选择商户</option>${merchantOptions}</select></div>
        <div class="field"><label>事件 ID *</label><input class="input" name="event_id" required></div>
        <div class="field"><label>类型 *</label><select class="input" name="type"><option value="binary" selected>订单簿</option><option value="parimutuel">奖池</option></select></div>
        <div class="field"><label>分类</label><select class="input" name="category">${categoryOptions("")}</select></div>
        <div class="field"><label>结算时间</label><input class="input" name="resolution_time" type="datetime-local"><p class="field-hint">留空则继承事件结算时间；填写事件 ID 后自动带入</p></div>
        <div class="field" data-liquidity-field><label>初始流动性</label><input class="input" name="liquidity_pool" type="number" min="0" step="any"></div>
        <div class="field" data-liquidity-hint hidden><label>初始流动性</label><p class="field-hint">奖池市场无需初始流动性</p></div>
        <div class="field field--span2"><label>问题 *</label><input class="input" name="question" placeholder="例如：BTC 将在 7 月收于 6 万美元以上吗？" required></div>
        <div class="field field--span2"><label>选项 *（逗号分隔）</label><input class="input" name="options" placeholder="是, 否" required></div>
        <div class="field"><label>商户手续费率</label><input class="input" name="merchant_fee_rate" type="number" min="0" max="1" step="0.001" placeholder="0.005（0.5%）"></div>
        <div class="field"><label>平台手续费率</label><input class="input" name="platform_fee_rate" type="number" min="0" max="1" step="0.001" placeholder="0.01（1%）"></div>
        <div class="field field--span2">
          <label>多语言配置（可选）</label>
          <div data-translation-rows>
            ${view.translations.map((entry, index) => `
            <div class="translation-row" data-translation-row="${index}">
              <input class="input" name="translation_locale" placeholder="语言代码（如 en、zh-CN）">
              <input class="input" name="translation_question" placeholder="该语言的问题">
              <input class="input" name="translation_options" placeholder="该语言的选项（逗号分隔，数量与默认一致）">
              <button class="btn btn--ghost" type="button" data-action="remove-translation" data-index="${index}">删除</button>
            </div>`).join("")}
          </div>
          <button class="btn btn--ghost" type="button" data-action="add-translation">+ 添加语言</button>
        </div>
        <div class="field form-field--actions"><button class="btn btn--primary" type="submit">创建市场</button><button class="btn btn--ghost" type="button" data-action="toggle-create-market">取消</button></div>
      </div>
    </form>`
    : "";
  return `
    <section class="page">
      <form class="filter-bar" data-action="filter">
        <div class="field field--grow"><label>搜索</label><input class="input" name="q" value="${escapeHTML(view.q)}" placeholder="市场问题" autocomplete="off"></div>
        <div class="field"><label>商户 ID</label><input class="input" name="merchant_id" value="${escapeHTML(view.merchantId)}" autocomplete="off"></div>
        <div class="field"><label>事件 ID</label><input class="input" name="event_id" value="${escapeHTML(view.eventId)}" autocomplete="off"></div>
        <div class="field"><label>状态</label><select class="input" name="status">${statusOptions(["active", "suspended", "closed", "settled", "voided"], view.status)}</select></div>
        <div class="field form-field--actions"><button class="btn btn--primary" type="submit">筛选</button>${view.q || view.status || view.merchantId || view.eventId ? `<button class="btn btn--ghost" type="button" data-action="reset-filters">重置</button>` : ""}</div>
      </form>
      <div class="section-heading list-heading"><h2>市场列表</h2><button class="btn btn--primary" type="button" data-action="toggle-create-market">${view.showCreateMarket ? "收起表单" : "新建市场"}</button></div>
      ${createForm}
      ${tableCard(["问题", "类型", "分类", "商户", "事件", "状态", "交易量", "流动性"], rows, "暂无市场", 8)}
      ${paginationBar(data.total, view.page)}
    </section>`;
}

async function marketDetailPage(id) {
  const market = await apiFetch(`/api/v1/admin/markets/${id}`);
  const actions = [];
  if (market.status === "active") {
    actions.push(`<button class="btn btn--danger" type="button" data-action="market-status" data-id="${escapeHTML(id)}" data-status="suspended">暂停市场</button>`);
    actions.push(`<button class="btn btn--warning" type="button" data-action="market-status" data-id="${escapeHTML(id)}" data-status="closed">关闭市场</button>`);
  } else if (market.status === "suspended") {
    actions.push(`<button class="btn btn--primary" type="button" data-action="market-status" data-id="${escapeHTML(id)}" data-status="active">恢复市场</button>`);
    actions.push(`<button class="btn btn--warning" type="button" data-action="market-status" data-id="${escapeHTML(id)}" data-status="closed">关闭市场</button>`);
  }
  if (market.status !== "settled" && market.status !== "voided") {
    if (market.type !== "parimutuel") {
      actions.push(`<button class="btn btn--primary" type="button" data-action="market-liquidity" data-id="${escapeHTML(id)}">注入流动性</button>`);
    }
    actions.push(`<button class="btn btn--success" type="button" data-action="market-settle" data-id="${escapeHTML(id)}" data-options="${escapeHTML((market.options ?? []).join(","))}">立即结算</button>`);
    actions.push(`<button class="btn btn--danger" type="button" data-action="market-void" data-id="${escapeHTML(id)}">作废市场</button>`);
  }
  const options = Array.isArray(market.options) ? market.options.join(" / ") : market.options ?? "—";
  const kv = kvList([
    ["问题", escapeHTML(market.question ?? "—")],
    ["所属事件", escapeHTML(market.event_title ?? market.event_id ?? "—")],
    ["商户 ID", escapeHTML(market.merchant_id ?? "—")],
    ["事件 ID", escapeHTML(market.event_id ?? "—")],
    ["类型", marketTypeBadge(market.type)],
    ["分类", categoryLabel(market.category)],
    ["结算时间", formatTime(market.resolution_time)],
    ["选项", escapeHTML(options)],
    ["状态", statusBadge(market.status)],
    ["交易量", formatMoney(market.total_volume)],
    ...(market.type !== "parimutuel" ? [["流动性", formatMoney(market.liquidity_pool)]] : []),
    ["商户手续费率", market.merchant_fee_rate != null ? `${market.merchant_fee_rate * 100}%` : "0%"],
    ["平台手续费率", market.platform_fee_rate != null ? `${market.platform_fee_rate * 100}%` : "0%"],
    ...(market.translations && Object.keys(market.translations).length > 0
      ? [["多语言", Object.entries(market.translations)
          .map(([locale, translation]) => `${escapeHTML(locale)}: ${escapeHTML(translation.question)}（${escapeHTML((translation.options ?? []).join(" / "))}）`)
          .join("<br>")]]
      : []),
    ["创建时间", formatTime(market.created_at)],
    ["结算时间", formatTime(market.settled_at)],
  ]);
  let bookCard;
  if (market.type === "parimutuel") {
    const poolRows = (market.pools ?? [])
      .map(
        (row) => `<tr><td>${escapeHTML(row.currency ?? "—")}</td><td class="td-num">${formatMoney(row.total_stake)}</td></tr>`
      )
      .join("");
    bookCard = `<div class="card"><div class="section-heading"><h2>奖池</h2></div>${tableCard(["币种", "累计投注"], poolRows, "暂无奖池数据", 2)}</div>`;
  } else {
    const book = market.orderbook ?? {};
    const bookRows = [
      ...(book.bids ?? []).map(
        (row) => `<tr><td><span class="badge badge--positive">买入</span></td><td>${escapeHTML(row.option ?? "—")}</td><td class="td-num">${escapeHTML(row.price)}</td><td class="td-num">${escapeHTML(row.amount)}</td></tr>`
      ),
      ...(book.asks ?? []).map(
        (row) => `<tr><td><span class="badge badge--danger">卖出</span></td><td>${escapeHTML(row.option ?? "—")}</td><td class="td-num">${escapeHTML(row.price)}</td><td class="td-num">${escapeHTML(row.amount)}</td></tr>`
      ),
    ].join("");
    bookCard = `<div class="card"><div class="section-heading"><h2>订单簿</h2></div>${tableCard(["方向", "选项", "价格", "数量"], bookRows, "暂无挂单", 4)}</div>`;
  }
  return `
    <section class="page">
      <div class="breadcrumb"><a href="#/markets">市场管理</a><span>/</span><span>${escapeHTML(market.question ?? id)}</span></div>
      <div class="detail-head">
        <div><h2>市场详情 ${marketTypeBadge(market.type)}</h2><p class="detail-head__id">ID：${escapeHTML(id)}</p></div>
        <div class="detail-head__actions">${actions.join("")}</div>
      </div>
      <div class="card"><div class="section-heading"><h2>基本信息</h2></div><div class="kv">${kv}</div></div>
      ${bookCard}
    </section>`;
}

/* ============================== 页面：订单 ============================== */

async function ordersPage() {
  const data = await apiFetch(
    `/api/v1/admin/orders${qs({ merchant_id: view.merchantId, user_id: view.userId, market_id: view.marketId, status: view.status, page: view.page, limit: view.limit })}`
  );
  const rows = (data.items ?? [])
    .map(
      (item) => `
    <tr>
      <td class="td-mono">${escapeHTML(item.id)}</td>
      <td class="td-mono">${escapeHTML(item.merchant_id)}</td>
      <td class="td-mono">${escapeHTML(item.user_id)}</td>
      <td class="td-mono">${escapeHTML(item.market_id)}</td>
      <td>${escapeHTML(ORDER_TYPE_LABELS[item.type] ?? item.type ?? "—")}</td>
      <td>${escapeHTML(item.option ?? "—")}</td>
      <td class="td-num">${formatMoney(item.amount)}</td>
      <td class="td-num">${formatMoney(item.filled_amount)}</td>
      <td class="td-num">${formatMoney(item.price)}</td>
      <td>${statusBadge(item.status)}</td>
      <td class="td-muted">${formatTime(item.created_at)}</td>
    </tr>`
    )
    .join("");
  return `
    <section class="page">
      <form class="filter-bar" data-action="filter">
        <div class="field"><label>商户 ID</label><input class="input" name="merchant_id" value="${escapeHTML(view.merchantId)}" autocomplete="off"></div>
        <div class="field"><label>用户 ID</label><input class="input" name="user_id" value="${escapeHTML(view.userId)}" autocomplete="off"></div>
        <div class="field"><label>市场 ID</label><input class="input" name="market_id" value="${escapeHTML(view.marketId)}" autocomplete="off"></div>
        <div class="field"><label>状态</label><select class="input" name="status">${statusOptions(["open", "partial", "filled", "cancelled"], view.status)}</select></div>
        <div class="field form-field--actions"><button class="btn btn--primary" type="submit">筛选</button>${view.merchantId || view.userId || view.marketId || view.status ? `<button class="btn btn--ghost" type="button" data-action="reset-filters">重置</button>` : ""}</div>
      </form>
      <div class="table-scroll">${tableCard(["ID", "商户", "用户", "市场", "类型", "选项", "金额", "已成交", "价格", "状态", "创建时间"], rows, "暂无订单", 11)}</div>
      ${paginationBar(data.total, view.page)}
    </section>`;
}

/* ============================== 页面：流水 ============================== */

async function transactionsPage() {
  const data = await apiFetch(
    `/api/v1/admin/transactions${qs({ merchant_id: view.merchantId, user_id: view.userId, type: view.type, page: view.page, limit: view.limit })}`
  );
  const rows = (data.items ?? [])
    .map(
      (item) => `
    <tr>
      <td class="td-mono">${escapeHTML(item.id)}</td>
      <td class="td-mono">${escapeHTML(item.wallet_id ?? "—")}</td>
      <td>${escapeHTML(item.type ?? "—")}</td>
      <td class="td-num">${formatMoney(item.amount)}</td>
      <td>${escapeHTML(item.currency ?? "—")}</td>
      <td>${statusBadge(item.status)}</td>
      <td class="td-muted">${formatTime(item.created_at)}</td>
    </tr>`
    )
    .join("");
  return `
    <section class="page">
      <form class="filter-bar" data-action="filter">
        <div class="field"><label>商户 ID</label><input class="input" name="merchant_id" value="${escapeHTML(view.merchantId)}" autocomplete="off"></div>
        <div class="field"><label>用户 ID</label><input class="input" name="user_id" value="${escapeHTML(view.userId)}" autocomplete="off"></div>
        <div class="field field--grow"><label>类型</label><input class="input" name="type" value="${escapeHTML(view.type)}" placeholder="例如：deposit / withdraw / trade" autocomplete="off"></div>
        <div class="field form-field--actions"><button class="btn btn--primary" type="submit">筛选</button>${view.merchantId || view.userId || view.type ? `<button class="btn btn--ghost" type="button" data-action="reset-filters">重置</button>` : ""}</div>
      </form>
      ${tableCard(["ID", "钱包 ID", "类型", "金额", "币种", "状态", "时间"], rows, "暂无流水", 7)}
      ${paginationBar(data.total, view.page)}
    </section>`;
}

/* ============================== 页面：审计 ============================== */

function stateChange(before, after) {
  const parts = [];
  if (before !== undefined && before !== null) parts.push(`前:${JSON.stringify(before)}`);
  if (after !== undefined && after !== null) parts.push(`后:${JSON.stringify(after)}`);
  return parts.length ? escapeHTML(parts.join(" → ")) : "—";
}

async function auditPage() {
  const data = await apiFetch(`/api/v1/admin/audit-logs${qs({ page: view.page, limit: view.limit })}`);
  const rows = (data.items ?? [])
    .map(
      (item) => `
    <tr>
      <td class="td-muted">${formatTime(item.created_at)}</td>
      <td>${escapeHTML(item.admin_username ?? "—")}</td>
      <td>${escapeHTML(actionLabel(item.action))}</td>
      <td>${escapeHTML(resourceLabel(item.resource))}</td>
      <td class="td-mono">${escapeHTML(item.resource_id ?? "—")}</td>
      <td class="td-state">${stateChange(item.before_state, item.after_state)}</td>
      <td class="td-mono">${escapeHTML(item.client_ip ?? "—")}</td>
    </tr>`
    )
    .join("");
  return `
    <section class="page">
      <div class="table-scroll">${tableCard(["时间", "管理员", "动作", "资源", "资源 ID", "状态变化", "IP"], rows, "暂无审计记录", 7)}</div>
      ${paginationBar(data.total, view.page)}
    </section>`;
}

/* ============================== 渲染分发 ============================== */

async function render() {
  const seq = ++renderSeq;
  const route = parseRoute();
  const root = route[0];

  if (!me) {
    if (root !== "login") {
      if (window.location.hash !== "#/login") window.location.hash = "#/login";
      return;
    }
    app.innerHTML = loginPage();
    return;
  }

  if (root === "login") {
    if (window.location.hash !== "#/dashboard") window.location.hash = "#/dashboard";
    return;
  }

  try {
    let content;
    switch (root) {
      case "dashboard":
        content = await dashboardPage();
        break;
      case "merchants":
        content = route[1] ? await merchantDetailPage(route[1]) : await merchantsPage();
        break;
      case "users":
        content = route[1] && route[2] ? await userDetailPage(route[1], route[2]) : await usersPage();
        break;
      case "events":
        content = route[1] ? await eventDetailPage(route[1]) : await eventsPage();
        break;
      case "markets":
        content = route[1] ? await marketDetailPage(route[1]) : await marketsPage();
        break;
      case "orders":
        content = await ordersPage();
        break;
      case "transactions":
        content = await transactionsPage();
        break;
      case "audit":
        content = await auditPage();
        break;
      default:
        content = notFoundPage();
    }
    if (seq !== renderSeq) return;
    app.innerHTML = shell(content, root);
  } catch (err) {
    if (seq !== renderSeq) return;
    app.innerHTML = shell(errorPage(err), root);
  }
}

/* ============================== 事件委托 ============================== */

document.addEventListener("click", (event) => {
  const target = event.target.closest("[data-action], [data-nav]");
  if (!target) return;
  if (target.dataset.nav) {
    window.location.hash = target.dataset.nav;
    return;
  }
  const action = target.dataset.action;
  if (action) handleAction(action, target);
});

async function handleAction(action, target) {
  try {
    switch (action) {
      case "logout":
        return await logout();
      case "page": {
        const page = Number(target.dataset.page);
        if (!Number.isInteger(page) || page < 1) return;
        if (target.dataset.target === "tx") view.userTxPage = page;
        else view.page = page;
        return render();
      }
      case "reset-filters":
        Object.assign(view, { q: "", merchantId: "", userId: "", marketId: "", status: "", category: "", sourceType: "", eventId: "", type: "" });
        view.page = 1;
        return render();
      case "toggle-create-event":
        view.showCreateEvent = !view.showCreateEvent;
        return render();
      case "toggle-create-market":
        view.showCreateMarket = !view.showCreateMarket;
        if (!view.showCreateMarket) view.translations = [];
        return render();
      case "add-translation":
        view.translations.push({ locale: "", question: "", options: "" });
        return render();
      case "remove-translation": {
        const index = Number(target.dataset.index);
        if (Number.isInteger(index) && index >= 0 && index < view.translations.length) {
          view.translations.splice(index, 1);
        }
        return render();
      }
      case "toggle-create-merchant":
        view.showCreateMerchant = !view.showCreateMerchant;
        return render();
      case "dismiss-credentials":
        view.merchantCredentials = null;
        view.reissuedSecret = null;
        view.testToken = null;
        if (target.dataset.id) window.location.hash = `#/merchants/${encodeURIComponent(target.dataset.id)}`;
        return render();
      case "copy":
        return copyText(target);
      case "merchant-reissue-secret":
        return reissueMerchantSecret(target);
      case "merchant-test-token":
        return merchantTestToken(target);
      case "merchant-status":
        return merchantStatus(target);
      case "user-status":
        return userStatus(target);
      case "event-status":
        return eventStatus(target);
      case "event-resolve":
        return eventResolve(target);
      case "market-status":
        return marketStatus(target);
      case "market-liquidity":
        return marketLiquidity(target);
      case "market-settle":
        return marketSettle(target);
      case "market-void":
        return marketVoid(target);
      case "retry":
        return render();
      default:
        break;
    }
  } catch (err) {
    toast(err.message, "error");
  }
}

document.addEventListener("change", async (event) => {
  const target = event.target;
  if (target?.name === "event_id" && target instanceof HTMLInputElement) {
    const eventId = target.value.trim();
    const form = target.closest('form[data-action="create-market"]');
    const resolutionField = form?.querySelector('input[name="resolution_time"]');
    if (eventId && form && resolutionField && !resolutionField.value) {
      try {
        const eventInfo = await apiFetch(`/api/v1/admin/events/${encodeURIComponent(eventId)}`);
        if (eventInfo?.resolution_time) {
          resolutionField.value = eventInfo.resolution_time.slice(0, 16);
        }
      } catch {
        /* 事件不存在时保持留空，创建时后端会继承 */
      }
    }
  }
  if (!(target instanceof HTMLSelectElement)) return;
  // 市场类型切换：奖池市场隐藏初始流动性。
  if (target.name === "type") {
    const form = target.closest('form[data-action="create-market"]');
    if (form) {
      const liquidityField = form.querySelector("[data-liquidity-field]");
      const liquidityHint = form.querySelector("[data-liquidity-hint]");
      if (liquidityField && liquidityHint) {
        const parimutuel = target.value === "parimutuel";
        liquidityField.hidden = parimutuel;
        liquidityHint.hidden = !parimutuel;
      }
    }
  }
  // 钱包模式切换：无缝模式显示回调地址输入。
  if (target.hasAttribute("data-wallet-toggle")) {
    const form = target.closest("form");
    const callbackField = form?.querySelector("[data-callback-field]");
    if (callbackField) {
      callbackField.hidden = target.value !== "seamless";
    }
  }
});

document.addEventListener("submit", (event) => {
  const form = event.target.closest("form[data-action]");
  if (!form) return;
  event.preventDefault();
  handleFormSubmit(form).catch((err) => toast(err.message, "error"));
});

async function handleFormSubmit(form) {
  switch (form.dataset.action) {
    case "login":
      return handleLogin(form);
    case "filter":
      return applyFilters(form);
    case "create-event":
      return createEvent(form);
    case "create-merchant":
      return createMerchant(form);
    case "create-market":
      return createMarket(form);
    case "edit-merchant":
      return editMerchant(form);
    case "edit-event":
      return editEvent(form);
    default:
      break;
  }
}

/* ============================== 认证 ============================== */

async function logout() {
  try {
    await apiFetch("/api/v1/admin/logout", { method: "POST" });
  } catch {
    /* 会话可能已失效，忽略 */
  }
  me = null;
  if (window.location.hash !== "#/login") window.location.hash = "#/login";
  else render();
}

async function handleLogin(form) {
  const username = String(form.username.value ?? "").trim();
  const password = String(form.password.value ?? "");
  const errorBox = form.querySelector("[data-login-error]");
  errorBox.hidden = true;
  if (!username || !password) {
    errorBox.hidden = false;
    errorBox.textContent = "请输入用户名和密码";
    return;
  }
  try {
    const data = await apiFetch("/api/v1/admin/login", { method: "POST", body: JSON.stringify({ username, password }) });
    me = data;
    toast("登录成功");
    if (window.location.hash !== "#/dashboard") window.location.hash = "#/dashboard";
    else render();
  } catch (err) {
    errorBox.hidden = false;
    errorBox.textContent = err.message;
  }
}

/* ============================== 敏感操作（确认词） ============================== */

async function merchantStatus(target) {
  const id = target.dataset.id;
  const status = target.dataset.status;
  if (status !== "active" && status !== "suspended") return;
  const label = status === "suspended" ? "暂停" : "恢复";
  const input = window.prompt(`${label}该商户？\n请输入确认词：${status}`);
  if (input !== status) return;
  await apiFetch(`/api/v1/admin/merchants/${id}/status`, { method: "PATCH", body: JSON.stringify({ status, confirm: status }) });
  toast(`${label}成功`);
  render();
}

async function userStatus(target) {
  const merchantId = target.dataset.merchant;
  const userId = target.dataset.user;
  const status = target.dataset.status;
  if (status !== "active" && status !== "blocked") return;
  const label = status === "blocked" ? "封禁" : "解封";
  const input = window.prompt(`${label}该用户？\n请输入确认词：${status}`);
  if (input !== status) return;
  await apiFetch(`/api/v1/admin/users/${merchantId}/${userId}/status`, { method: "PATCH", body: JSON.stringify({ status, confirm: status }) });
  toast(`${label}成功`);
  render();
}

async function eventStatus(target) {
  const id = target.dataset.id;
  const status = target.dataset.status;
  const labels = { active: "激活", closed: "关闭" };
  const label = labels[status] ?? status;
  const input = window.prompt(`${label}该事件？\n请输入确认词：${status}`);
  if (input !== status) return;
  await apiFetch(`/api/v1/admin/events/${id}/status`, { method: "PATCH", body: JSON.stringify({ status }) });
  toast(`${label}成功`);
  render();
}

async function eventResolve(target) {
  const id = target.dataset.id;
  const outcome = window.prompt("请输入结算结果（outcome）：");
  if (outcome === null || outcome.trim() === "") return;
  const input = window.prompt("确认结算该事件？\n请输入确认词：resolve");
  if (input !== "resolve") return;
  await apiFetch(`/api/v1/admin/events/${id}/resolve`, { method: "POST", body: JSON.stringify({ outcome: outcome.trim(), confirm: "resolve" }) });
  toast("结算成功");
  render();
}

async function marketStatus(target) {
  const id = target.dataset.id;
  const status = target.dataset.status;
  const labels = { active: "恢复", suspended: "暂停", closed: "关闭" };
  const label = labels[status] ?? status;
  const input = window.prompt(`${label}该市场？\n请输入确认词：${status}`);
  if (input !== status) return;
  await apiFetch(`/api/v1/admin/markets/${id}/status`, { method: "PATCH", body: JSON.stringify({ status }) });
  toast(`${label}成功`);
  render();
}

async function marketLiquidity(target) {
  const id = target.dataset.id;
  const amount = window.prompt("请输入注入的流动性金额：");
  if (amount === null) return;
  const number = Number(amount);
  if (!Number.isFinite(number) || number <= 0) {
    toast("金额无效", "error");
    return;
  }
  const input = window.prompt("确认注入流动性？\n请输入确认词：liquidity");
  if (input !== "liquidity") return;
  await apiFetch(`/api/v1/admin/markets/${id}/liquidity`, { method: "POST", body: JSON.stringify({ amount: number, confirm: "liquidity" }) });
  toast("流动性注入成功");
  render();
}

async function marketSettle(target) {
  const id = target.dataset.id;
  const options = (target.dataset.options ?? "").split(",").filter(Boolean);
  const option = window.prompt(`选择胜出方（${options.join(" / ")}）：`);
  if (!option || !options.includes(option)) {
    toast("胜出方必须来自市场选项", "error");
    return;
  }
  const input = window.prompt(`确认立即结算该市场（胜出方：${option}）？\n请输入确认词：settle`);
  if (input !== "settle") return;
  await apiFetch(`/api/v1/admin/markets/${id}/settle`, { method: "POST", body: JSON.stringify({ winning_option: option, confirm: "settle" }) });
  toast("市场已结算");
  render();
}

async function marketVoid(target) {
  const id = target.dataset.id;
  const input = window.prompt("确认作废该市场？\n请输入确认词：void");
  if (input !== "void") return;
  await apiFetch(`/api/v1/admin/markets/${id}/void`, { method: "POST", body: JSON.stringify({ confirm: "void" }) });
  toast("市场已作废");
  render();
}

/* ============================== 表单处理 ============================== */

function applyFilters(form) {
  const data = new FormData(form);
  Object.assign(view, {
    q: String(data.get("q") ?? "").trim(),
    merchantId: String(data.get("merchant_id") ?? "").trim(),
    userId: String(data.get("user_id") ?? "").trim(),
    marketId: String(data.get("market_id") ?? "").trim(),
    status: String(data.get("status") ?? "").trim(),
    category: String(data.get("category") ?? "").trim(),
    sourceType: String(data.get("source_type") ?? "").trim(),
    eventId: String(data.get("event_id") ?? "").trim(),
    type: String(data.get("type") ?? "").trim(),
  });
  view.page = 1;
  render();
}

// createMerchant opens a merchant account from the console and stores the
// one-time cleartext credentials for the save-now panel.
async function createMerchant(form) {
  const data = new FormData(form);
  const body = {
    name: String(data.get("name") ?? "").trim(),
    email: String(data.get("email") ?? "").trim(),
    currency: String(data.get("currency") ?? "USD").trim().toUpperCase() || "USD",
    timezone: String(data.get("timezone") ?? "UTC").trim() || "UTC",
  };
  if (!body.name || !body.email) {
    toast("请填写名称与邮箱", "error");
    return;
  }
  const walletMode = String(data.get("wallet_mode") ?? "transfer").trim() || "transfer";
  body.wallet_mode = walletMode;
  if (walletMode === "seamless") {
    const callbackUrl = String(data.get("callback_url") ?? "").trim();
    if (!callbackUrl) {
      toast("无缝模式必须填写回调地址", "error");
      return;
    }
    body.callback_url = callbackUrl;
  }
  const created = await apiFetch("/api/v1/admin/merchants", { method: "POST", body: JSON.stringify(body) });
  view.merchantCredentials = created;
  view.showCreateMerchant = false;
  render();
}

// copyText copies a credential value with a clipboard fallback.
async function copyText(target) {
  const value = target.dataset.copyValue ?? "";
  if (!value) return;
  try {
    await navigator.clipboard.writeText(value);
  } catch {
    const source = target.dataset.copySource
      ? document.querySelector(`[data-copy-source="${CSS.escape(target.dataset.copySource)}"]`)
      : null;
    if (source) {
      const range = document.createRange();
      range.selectNodeContents(source);
      const selection = window.getSelection();
      selection.removeAllRanges();
      selection.addRange(range);
      document.execCommand("copy");
      selection.removeAllRanges();
    }
  }
  toast("已复制");
}

// reissueMerchantSecret rotates the V3 signing secret after the confirm word.
async function reissueMerchantSecret(target) {
  const id = target.dataset.id;
  if (!id) return;
  const word = window.prompt("输入确认词 reissue 以重发 API Secret（旧密钥将立即失效）");
  if (word !== "reissue") return;
  const result = await apiFetch(`/api/v1/admin/merchants/${encodeURIComponent(id)}/api-secret/reissue`, {
    method: "POST",
    body: JSON.stringify({ confirm: "reissue" }),
  });
  view.reissuedSecret = { id, api_secret: result.api_secret };
  render();
  toast("API Secret 已重发，请立即保存");
}

// merchantTestToken generates a one-time launch link for the merchant's
// hosted page, so frontend engineers can test without merchant credentials.
async function merchantTestToken(target) {
  const id = target.dataset.id;
  if (!id) return;
  const userID = window.prompt("生成测试链接的用户 ID（留空使用 test-user）", "test-user");
  if (userID === null) return;
  const body = { merchant_id: id };
  if (String(userID).trim()) body.user_id = String(userID).trim();
  const result = await apiFetch("/api/v1/admin/test-token", { method: "POST", body: JSON.stringify(body) });
  view.testToken = result;
  render();
  toast("测试链接已生成（15 分钟有效）");
}

async function createEvent(form) {
  const data = new FormData(form);
  const title = String(data.get("title") ?? "").trim();
  const description = String(data.get("description") ?? "").trim();
  const category = String(data.get("category") ?? "").trim();
  const endTime = toRFC3339(String(data.get("end_time") ?? ""));
  const resolutionTime = toRFC3339(String(data.get("resolution_time") ?? ""));
  if (!title || !category || !endTime || !resolutionTime) {
    toast("请填写完整的必填信息", "error");
    return;
  }
  await apiFetch("/api/v1/admin/events", {
    method: "POST",
    body: JSON.stringify({ title, description, category, source_type: "custom", end_time: endTime, resolution_time: resolutionTime }),
  });
  toast("事件创建成功");
  view.showCreateEvent = false;
  render();
}

async function createMarket(form) {
  const data = new FormData(form);
  const merchantId = String(data.get("merchant_id") ?? "").trim();
  const eventId = String(data.get("event_id") ?? "").trim();
  const question = String(data.get("question") ?? "").trim();
  const options = String(data.get("options") ?? "")
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean);
  const type = String(data.get("type") ?? "binary").trim() || "binary";
  const pool = String(data.get("liquidity_pool") ?? "").trim();
  if (!merchantId) {
    toast("请选择有效的商户", "error");
    return;
  }
  if (!eventId) {
    toast("请填写有效的事件 ID", "error");
    return;
  }
  if (!question) {
    toast("请填写市场问题", "error");
    return;
  }
  if (options.length < 2) {
    toast("请至少填写两个选项（逗号分隔）", "error");
    return;
  }
  const body = { event_id: eventId, merchant_id: merchantId, type, question, options };
  const category = String(data.get("category") ?? "").trim();
  if (category) body.category = category;
  const resolutionRaw = String(data.get("resolution_time") ?? "").trim();
  if (resolutionRaw) {
    const resolution = new Date(resolutionRaw);
    if (Number.isFinite(resolution.getTime())) body.resolution_time = resolution.toISOString();
  }
  const merchantFee = String(data.get("merchant_fee_rate") ?? "").trim();
  if (merchantFee !== "") {
    const number = Number(merchantFee);
    if (!Number.isFinite(number) || number < 0 || number > 1) {
      toast("商户手续费率需为 0 到 1 之间的小数", "error");
      return;
    }
    body.merchant_fee_rate = number;
  }
  const platformFee = String(data.get("platform_fee_rate") ?? "").trim();
  if (platformFee !== "") {
    const number = Number(platformFee);
    if (!Number.isFinite(number) || number < 0 || number > 1) {
      toast("平台手续费率需为 0 到 1 之间的小数", "error");
      return;
    }
    body.platform_fee_rate = number;
  }
  const translations = {};
  for (const row of form.querySelectorAll("[data-translation-row]")) {
    const locale = String(row.querySelector('input[name="translation_locale"]')?.value ?? "").trim();
    const translatedQuestion = String(row.querySelector('input[name="translation_question"]')?.value ?? "").trim();
    const translatedOptions = String(row.querySelector('input[name="translation_options"]')?.value ?? "")
      .split(",")
      .map((value) => value.trim())
      .filter(Boolean);
    if (!locale) {
      toast("多语言行缺少语言代码", "error");
      return;
    }
    if (!translatedQuestion) {
      toast(`语言 ${locale} 缺少问题`, "error");
      return;
    }
    if (translatedOptions.length !== options.length) {
      toast(`语言 ${locale} 的选项数量必须与默认一致`, "error");
      return;
    }
    translations[locale] = { question: translatedQuestion, options: translatedOptions };
  }
  if (Object.keys(translations).length > 0) body.translations = translations;
  if (type === "parimutuel") {
    body.liquidity_pool = 0;
  } else if (pool) {
    const number = Number(pool);
    if (Number.isFinite(number) && number > 0) body.liquidity_pool = number;
  }
  await apiFetch("/api/v1/admin/markets", { method: "POST", body: JSON.stringify(body) });
  toast("市场创建成功");
  view.showCreateMarket = false;
  render();
}

async function editMerchant(form) {
  const id = form.dataset.id;
  const data = new FormData(form);
  const body = {
    name: String(data.get("name") ?? "").trim(),
    currency: String(data.get("currency") ?? "").trim(),
    timezone: String(data.get("timezone") ?? "").trim(),
  };
  const fee = String(data.get("fee_rate") ?? "").trim();
  if (fee !== "") {
    const number = Number(fee);
    if (!Number.isFinite(number)) {
      toast("费率格式无效", "error");
      return;
    }
    body.fee_rate = number;
  }
  const walletMode = String(data.get("wallet_mode") ?? "").trim();
  if (walletMode) {
    body.wallet_mode = walletMode;
    if (walletMode === "seamless") {
      const callbackUrl = String(data.get("callback_url") ?? "").trim();
      if (!callbackUrl) {
        toast("无缝模式必须填写回调地址", "error");
        return;
      }
      body.callback_url = callbackUrl;
    }
  }
  const updated = await apiFetch(`/api/v1/admin/merchants/${id}`, { method: "PATCH", body: JSON.stringify(body) });
  if (updated.callback_secret) {
    // 切换到无缝模式时生成的回调密钥，一次性展示。
    view.reissuedSecret = { id, api_secret: updated.callback_secret, callback: true };
  }
  toast("商户信息已更新");
  render();
}

async function editEvent(form) {
  const id = form.dataset.id;
  const data = new FormData(form);
  const body = {
    title: String(data.get("title") ?? "").trim(),
    description: String(data.get("description") ?? "").trim(),
  };
  const resolutionTime = toRFC3339(String(data.get("resolution_time") ?? ""));
  if (resolutionTime) body.resolution_time = resolutionTime;
  await apiFetch(`/api/v1/admin/events/${id}`, { method: "PATCH", body: JSON.stringify(body) });
  toast("事件已更新");
  render();
}

/* ============================== 启动 ============================== */

async function bootstrap() {
  try {
    me = await apiFetch("/api/v1/admin/me");
  } catch {
    /* 401：apiFetch 已跳转登录页 */
  }
  window.addEventListener("hashchange", () => {
    Object.assign(view, {
      page: 1,
      q: "",
      merchantId: "",
      userId: "",
      marketId: "",
      status: "",
      category: "",
      sourceType: "",
      eventId: "",
      type: "",
      userTxPage: 1,
      showCreateEvent: false,
      showCreateMarket: false,
      showCreateMerchant: false,
      merchantCredentials: null,
      reissuedSecret: null,
      testToken: null,
    });
    render();
  });
  render();
}

bootstrap();
