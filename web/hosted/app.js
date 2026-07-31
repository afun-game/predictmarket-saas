const categories = [
  { id: "all", label: "全部" },
  { id: "sports", label: "体育" },
  { id: "crypto", label: "加密" },
  { id: "world", label: "时事" },
  { id: "entertainment", label: "娱乐" },
  { id: "technology", label: "科技" },
];

const app = document.querySelector("#app");
const state = {
  accessToken: "",
  me: null,
  events: [],
  markets: [],
  orders: [],
  selectedOutcome: null,
  loading: true,
  error: "",
  submitting: false,
};

// Keep the data model at the edge of the UI. The hosted shell never receives
// a merchant API key; it exchanges the one-time token embedded in launch_url
// and uses the resulting short-lived session credential for every API call.
let events = [];
let markets = [];

function escapeHTML(value) {
  return String(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#039;");
}

function categoryLabel(id) {
  return categories.find((category) => category.id === id)?.label ?? "全部";
}

function parseRoute() {
  const route = window.location.hash.replace(/^#/, "") || "/home";
  return route.split("/").filter(Boolean);
}

function parentOrigin() {
  const origin = new URLSearchParams(window.location.search).get("parent_origin");
  try {
    const url = new URL(origin);
    return url.protocol === "https:" ? url.origin : window.location.origin;
  } catch {
    return window.location.origin;
  }
}

function emit(type, detail = {}) {
  if (window.parent !== window) {
    window.parent.postMessage({ source: "predictmarket", type, detail }, parentOrigin());
  }
}

function navigate(route) {
  window.location.hash = route;
}

function shell(content, active = "home") {
  const me = state.me;
  const balance = me?.available_balance ? `${me.currency ?? ""} ${me.available_balance}` : "—";
  return `
    <div class="shell">
      <header class="topbar">
        <a class="brand" href="#/home" aria-label="PredictMarket 首页">
          <span class="brand__mark">P</span><span>PredictMarket</span>
        </a>
        <div class="topbar__actions">
          <div class="balance" aria-label="可用余额"><span>可用余额</span><strong>${escapeHTML(balance)}</strong></div>
          <button class="icon-button" type="button" data-action="close" aria-label="返回商户站点">×</button>
        </div>
      </header>
      ${content}
    </div>
    <nav class="nav" aria-label="主导航">
      ${navButton("home", "⌂", "发现", active)}
      ${navButton("categories", "▦", "分类", active)}
      ${navButton("orders", "▤", "订单", active)}
    </nav>
  `;
}

function navButton(id, icon, label, active) {
  const route = id === "home" ? "/home" : id === "categories" ? "/category/all" : "/orders";
  return `<button type="button" data-route="${route}" aria-current="${active === id ? "page" : "false"}"><span aria-hidden="true">${icon}</span>${label}</button>`;
}

function eventCard(event) {
  return `
    <button class="event-card" type="button" data-route="/event/${event.id}">
      <span class="event-card__badge" aria-hidden="true">${event.icon}</span>
      <span class="event-card__body">
        <span class="event-card__title">${escapeHTML(event.title)}</span>
        <span class="event-card__meta">${categoryLabel(event.category)} · ${event.markets.length} 个市场</span>
      </span>
      <span class="event-card__arrow" aria-hidden="true">›</span>
    </button>`;
}

function marketCard(market) {
  const leading = market.outcomes[0];
  const price = leading?.price == null ? "—" : `${leading.price}¢`;
  return `
    <button class="market-card" type="button" data-route="/market/${market.id}">
      <span class="market-card__top">
        <span class="market-card__category">${escapeHTML(market.category)}</span>
        <span class="market-card__volume">交易量 ${market.volume}</span>
      </span>
      <h3>${escapeHTML(market.question)}</h3>
      <span class="probability" aria-label="${escapeHTML(leading?.label ?? "")}的当前报价">
        <span class="probability__bar"><span class="probability__fill" style="width:${leading?.price ?? 0}%"></span></span>
        <strong class="probability__value">${price}</strong>
      </span>
      <span class="market-card__footer"><span>${escapeHTML(leading?.label ?? "")} ${price}</span><span class="status">${escapeHTML(market.status ?? "")}</span></span>
    </button>`;
}

function homePage() {
  const featured = markets.slice(0, 3);
  return shell(`
    <section class="page">
      <div class="hero">
        <p class="eyebrow">预测市场</p>
        <h1>用市场价格，发现集体判断。</h1>
        <p>浏览正在发生的事件，选择你认可的结果。</p>
      </div>
      <section class="section" aria-labelledby="categories-title">
        <div class="section-heading"><h2 id="categories-title">浏览分类</h2><button class="text-button" type="button" data-route="/category/all">全部分类</button></div>
        ${categoryChips("all")}
      </section>
      <section class="section" aria-labelledby="events-title">
        <div class="section-heading"><h2 id="events-title">热门事件</h2><button class="text-button" type="button" data-route="/category/all">查看全部</button></div>
        <div class="event-grid">${events.slice(0, 4).map(eventCard).join("")}</div>
      </section>
      <section class="section" aria-labelledby="markets-title">
        <div class="section-heading"><h2 id="markets-title">正在交易</h2><button class="text-button" type="button" data-route="/category/all">全部市场</button></div>
        <div class="market-list">${featured.map(marketCard).join("")}</div>
      </section>
    </section>
  `, "home");
}

function categoryChips(activeCategory) {
  return `<div class="categories" role="tablist" aria-label="市场分类">${categories.map((category) => `
    <button class="chip" type="button" role="tab" data-route="/category/${category.id}" aria-current="${category.id === activeCategory}">${category.label}</button>`).join("")}</div>`;
}

function categoryPage(category) {
  const eventMatches = category === "all" ? events : events.filter((event) => event.category === category);
  const marketMatches = category === "all" ? markets : markets.filter((market) => events.find((event) => event.id === market.eventId)?.category === category);
  const title = category === "all" ? "全部市场" : `${categoryLabel(category)}市场`;
  return shell(`
    <section class="page">
      <div class="breadcrumb"><button type="button" data-route="/home">首页</button><span>/</span><span>${title}</span></div>
      <section class="section">
        <p class="section-kicker">探索</p>
        <h1>${title}</h1>
        ${categoryChips(category)}
      </section>
      <section class="section" aria-labelledby="category-events-title">
        <div class="section-heading"><h2 id="category-events-title">事件</h2><span class="label">${eventMatches.length} 项</span></div>
        <div class="event-grid">${eventMatches.length ? eventMatches.map(eventCard).join("") : "<div class=\"empty\">暂时没有该分类的事件</div>"}</div>
      </section>
      <section class="section" aria-labelledby="category-markets-title">
        <div class="section-heading"><h2 id="category-markets-title">市场</h2><span class="label">${marketMatches.length} 项</span></div>
        <div class="market-list market-list--compact">${marketMatches.map(marketCard).join("")}</div>
      </section>
    </section>
  `, "categories");
}

function eventPage(event) {
  if (!event) return notFoundPage();
  const eventMarkets = markets.filter((market) => market.eventId === event.id);
  return shell(`
    <section class="page">
      <div class="breadcrumb"><button type="button" data-route="/category/${event.category}">${categoryLabel(event.category)}</button><span>/</span><span>事件详情</span></div>
      <header class="event-header">
        <div class="event-heading">
          <span class="event-heading__badge" aria-hidden="true">${event.icon}</span>
          <div><p class="event-kicker">${categoryLabel(event.category)}</p><h1>${escapeHTML(event.title)}</h1><p>${event.deadline}</p></div>
        </div>
        <p class="event-note">${escapeHTML(event.description)}</p>
      </header>
      <section class="section" aria-labelledby="event-markets-title">
        <div class="section-heading"><h2 id="event-markets-title">${eventMarkets.length} 个关联市场</h2><span class="status">${event.deadline}</span></div>
        <div class="market-list">${eventMarkets.map(marketCard).join("")}</div>
      </section>
    </section>
  `, "categories");
}

function marketPage(market) {
  if (!market) return notFoundPage();
  const event = events.find((item) => item.id === market.eventId);
  const selected = state.selectedOutcome?.marketId === market.id ? state.selectedOutcome : null;
  return shell(`
    <section class="page">
      <div class="breadcrumb"><button type="button" data-route="/event/${market.eventId}">${escapeHTML(event?.title ?? "事件")}</button><span>/</span><span>市场详情</span></div>
      <article class="market-hero">
        <p class="event-kicker">${escapeHTML(market.category)}</p>
        <h1>${escapeHTML(market.question)}</h1>
        <p class="market-hero__meta"><span>◷ ${market.deadline} 结算</span><span>交易量 ${market.volume}</span><span class="status">${market.status}</span></p>
      </article>
      <section aria-labelledby="outcomes-title">
        <div class="section-heading"><h2 id="outcomes-title">选择结果</h2><span class="label">当前概率</span></div>
        <div class="outcome-grid">${market.outcomes.map((outcome, index) => `
          <button class="outcome" type="button" data-outcome="${market.id}:${index}" aria-pressed="${selected?.index === index}">
            <strong>${escapeHTML(outcome.label)}</strong><span>${outcome.price == null ? "报价—" : `${outcome.price}¢`}</span>
          </button>`).join("")}</div>
      </section>
      <section class="info-card"><h2>结算说明</h2><p>${escapeHTML(market.summary)}</p></section>
      <div class="notice"><span aria-hidden="true">ⓘ</span><span>价格反映市场当前观点，不构成任何建议。下单前请确认结算规则。</span></div>
      ${selected ? `
        <div class="ticket" role="status">
          <div class="ticket__summary"><div class="ticket__choice"><strong>${escapeHTML(market.question)}</strong><span>选择：${escapeHTML(market.outcomes[selected.index].label)}</span></div><span class="ticket__price">实时盘口</span></div>
          <button class="primary-button" type="button" data-action="place-order" ${state.submitting ? "disabled" : ""}>${state.submitting ? "提交中…" : "继续"}</button>
        </div>` : ""}
    </section>
  `, "categories");
}

function ordersPage() {
  if (state.orders.length) {
    return shell(`
      <section class="page">
        <div class="breadcrumb"><button type="button" data-route="/home">首页</button><span>/</span><span>我的订单</span></div>
        <section class="section"><p class="section-kicker">账户</p><h1>我的订单</h1>
          <div class="order-list">${state.orders.map((order) => `
            <article class="info-card"><div class="section-heading"><strong>${escapeHTML(order.market_id ?? "市场")}</strong><span class="status">${escapeHTML(order.status ?? "")}</span></div>
              <p>${escapeHTML(order.type ?? "")} · ${escapeHTML(order.option ?? "")} · ${escapeHTML(String(order.amount ?? ""))}</p>
            </article>`).join("")}</div>
        </section>
      </section>
    `, "orders");
  }
  return shell(`
    <section class="page">
      <div class="breadcrumb"><button type="button" data-route="/home">首页</button><span>/</span><span>我的订单</span></div>
      <section class="section"><p class="section-kicker">账户</p><h1>我的订单</h1><div class="empty">还没有订单。</div></section>
    </section>
  `, "orders");
}

function notFoundPage() {
  return shell(`<section class="page"><div class="empty"><p>这个页面不存在或已下线。</p><button class="primary-button" type="button" data-route="/home">返回首页</button></div></section>`);
}

function render() {
  if (state.loading) {
    app.innerHTML = `<section class="page"><div class="empty" role="status">正在载入市场…</div></section>`;
    return;
  }
  if (state.error) {
    app.innerHTML = `<section class="page"><div class="empty"><p>${escapeHTML(state.error)}</p><button class="primary-button" type="button" data-action="reload">重试</button></div></section>`;
    return;
  }
  const [root, identifier] = parseRoute();
  if (root === "category") app.innerHTML = categoryPage(categories.some((category) => category.id === identifier) ? identifier : "all");
  else if (root === "event") app.innerHTML = eventPage(events.find((event) => event.id === identifier));
  else if (root === "market") app.innerHTML = marketPage(markets.find((market) => market.id === identifier));
  else if (root === "orders") app.innerHTML = ordersPage();
  else app.innerHTML = homePage();
}

document.addEventListener("click", (event) => {
  const routeTarget = event.target.closest("[data-route]");
  if (routeTarget) {
    navigate(routeTarget.dataset.route);
    return;
  }
  const outcomeTarget = event.target.closest("[data-outcome]");
  if (outcomeTarget) {
    const [marketId, index] = outcomeTarget.dataset.outcome.split(":");
    state.selectedOutcome = { marketId, index: Number(index) };
    render();
    return;
  }
  const actionTarget = event.target.closest("[data-action]");
  if (!actionTarget) return;
  if (actionTarget.dataset.action === "close") emit("pm:navigate_home");
  if (actionTarget.dataset.action === "reload") {
    bootstrap();
  }
  if (actionTarget.dataset.action === "place-order" && state.selectedOutcome) {
    placeOrder();
  }
});

window.addEventListener("hashchange", render);

async function apiFetch(path, options = {}) {
  const headers = new Headers(options.headers ?? {});
  headers.set("Accept", "application/json");
  if (options.body !== undefined) headers.set("Content-Type", "application/json");
  if (state.accessToken) headers.set("Authorization", `Bearer ${state.accessToken}`);
  const response = await fetch(path, { ...options, headers });
  let payload = null;
  try { payload = await response.json(); } catch { /* 204/no body */ }
  if (response.status === 401) {
    state.accessToken = "";
    emit("pm:session_expired");
    throw new Error("会话已过期，请从商户站点重新打开游戏页面。");
  }
  if (!response.ok) {
    throw new Error(payload?.error?.message ?? `请求失败（${response.status}）`);
  }
  return payload?.data ?? payload;
}

function launchToken() {
  return new URLSearchParams(window.location.search).get("token")?.trim() ?? "";
}

function formatDeadline(value) {
  if (!value) return "待定";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "待定" : date.toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" });
}

function normalizeEvent(value) {
  return {
    ...value,
    icon: value.category === "sports" ? "⚽" : value.category === "technology" ? "✦" : "◈",
    deadline: `${formatDeadline(value.resolution_time)} 结算`,
    markets: [],
  };
}

function normalizeMarket(value) {
  const event = events.find((item) => item.id === value.event_id);
  return {
    ...value,
    eventId: value.event_id,
    category: `${categoryLabel(event?.category)} · ${event?.title ?? "预测市场"}`,
    summary: "市场将依据事件页面公布的结算规则结算。",
    outcomes: (value.options ?? []).map((label) => ({ label, price: null })),
    volume: value.total_volume ?? "0.000000",
    deadline: formatDeadline(event?.resolution_time),
    status: value.status === "active" ? "交易中" : value.status,
  };
}

async function bootstrap() {
  state.loading = true;
  state.error = "";
  render();
  try {
    const token = launchToken();
    if (!state.accessToken && !token) throw new Error("请从商户站点提供的 Launch URL 打开此页面。");
    if (!state.accessToken) {
      const exchanged = await apiFetch("/api/user/session/exchange", {
        method: "POST",
        body: JSON.stringify({ token }),
      });
      state.accessToken = exchanged.access_token;
      // A one-time token must not remain in browser history or referrers.
      const cleanURL = new URL(window.location.href);
      cleanURL.searchParams.delete("token");
      window.history.replaceState({}, document.title, cleanURL.toString());
    }
    const [me, eventPage, marketPage, orderPage] = await Promise.all([
      apiFetch("/api/user/me"),
      apiFetch("/api/user/events?limit=100"),
      apiFetch("/api/user/markets?limit=100&status=active"),
      apiFetch("/api/user/orders?limit=100"),
    ]);
    state.me = me;
    events = (Array.isArray(eventPage) ? eventPage : []).map(normalizeEvent);
    markets = (Array.isArray(marketPage) ? marketPage : []).map(normalizeMarket);
    events.forEach((event) => { event.markets = markets.filter((market) => market.eventId === event.id).map((market) => market.id); });
    state.orders = Array.isArray(orderPage) ? orderPage : [];
  } catch (error) {
    state.error = error instanceof Error ? error.message : "无法载入托管页面。";
  } finally {
    state.loading = false;
    render();
    emit("pm:ready", { version: "v3", route: window.location.hash || "#/home" });
  }
}

async function placeOrder() {
  const selected = state.selectedOutcome;
  const market = markets.find((item) => item.id === selected?.marketId);
  if (!market || state.submitting) return;
  const amount = window.prompt("请输入份额（最多 6 位小数）", "1");
  if (!amount || !/^\d+(\.\d{1,6})?$/.test(amount) || Number(amount) <= 0) return;
  state.submitting = true;
  render();
  try {
    const book = await apiFetch(`/api/user/markets/${encodeURIComponent(market.id)}/orderbook`);
    const option = market.outcomes[selected.index];
    const entries = [...(book?.asks ?? []), ...(book?.bids ?? [])].filter((entry) => entry.option === option.label && Number(entry.price) > 0);
    const price = entries[0]?.price;
    if (price === undefined) throw new Error("该结果暂无可成交报价，请稍后再试。");
    const order = await apiFetch("/api/user/orders", {
      method: "POST",
      headers: { "Idempotency-Key": crypto.randomUUID() },
      body: JSON.stringify({ market_id: market.id, type: "buy", option: option.label, amount: Number(amount), price }),
    });
    state.orders.unshift(order);
    state.selectedOutcome = null;
    await refreshMe();
    emit("pm:bet_placed", { market_id: market.id, order_id: order.id, outcome: option.label });
  } catch (error) {
    state.error = error instanceof Error ? error.message : "下单失败。";
  } finally {
    state.submitting = false;
    render();
  }
}

async function refreshMe() {
  try {
    state.me = await apiFetch("/api/user/me");
    emit("pm:balance_changed", { balance: state.me.available_balance, currency: state.me.currency });
  } catch { /* apiFetch already emits session_expired */ }
}

bootstrap();
