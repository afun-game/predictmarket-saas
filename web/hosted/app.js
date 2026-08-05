const categories = [
  { id: "all", labelKey: "category.all" },
  { id: "sports", labelKey: "category.sports" },
  { id: "crypto", labelKey: "category.crypto" },
  { id: "world", labelKey: "category.world" },
  { id: "entertainment", labelKey: "category.entertainment" },
  { id: "technology", labelKey: "category.technology" },
];

// UI strings live in a small dictionary; server-provided titles and
// questions are data and are never translated client-side.
const translations = {
  "zh-CN": {
    "brand.home": "PredictMarket 首页",
    "balance.label": "可用余额",
    "close.label": "返回商户站点",
    "nav.label": "主导航",
    "nav.home": "发现",
    "nav.categories": "分类",
    "nav.orders": "订单",
    "lang.choose": "选择语言",
    "lang.zh": "中文",
    "lang.en": "English",
    "home.eyebrow": "预测市场",
    "home.title": "用市场价格，发现集体判断。",
    "home.subtitle": "浏览正在发生的事件，选择你认可的结果。",
    "home.categories": "浏览分类",
    "home.allCategories": "全部分类",
    "home.trending": "热门事件",
    "home.viewAll": "查看全部",
    "home.trading": "正在交易",
    "home.allMarkets": "全部市场",
    "category.all": "全部",
    "category.sports": "体育",
    "category.crypto": "加密",
    "category.world": "时事",
    "category.entertainment": "娱乐",
    "category.technology": "科技",
    "category.allTitle": "全部市场",
    "category.titleSuffix": "{label}市场",
    "category.explore": "探索",
    "category.events": "事件",
    "category.markets": "市场",
    "category.count": "{count} 项",
    "category.empty": "暂时没有该分类的事件",
    "common.home": "首页",
    "common.market": "市场",
    "event.details": "事件详情",
    "event.title": "事件",
    "event.marketsCount": "{count} 个关联市场",
    "event.marketsCountOne": "{count} 个关联市场",
    "market.details": "市场详情",
    "market.chooseOutcome": "选择结果",
    "market.currentOdds": "当前概率",
    "market.noQuote": "报价—",
    "market.rules": "结算说明",
    "market.rulesNote": "市场将依据事件页面公布的结算规则结算。",
    "market.notice": "价格反映市场当前观点，不构成任何建议。下单前请确认结算规则。",
    "market.choice": "选择：{label}",
    "market.liveBook": "实时盘口",
    "market.poolStake": "奖池投注",
    "market.modeOrderbook": "订单簿模式",
    "market.modeParimutuel": "奖池模式",
    "market.pool": "奖池",
    "market.poolTotal": "累计投注 {amount}",
    "market.poolOption": "{option} 投注 {amount}",
    "market.continue": "继续",
    "market.submitting": "提交中…",
    "orders.title": "我的订单",
    "orders.account": "账户",
    "orders.empty": "还没有订单。",
    "notFound.title": "这个页面不存在或已下线。",
    "notFound.home": "返回首页",
    "loading.markets": "正在载入市场…",
    "retry": "重试",
    "error.session": "会话已过期，请从商户站点重新打开游戏页面。",
    "error.http": "请求失败（{status}）",
    "error.launch": "请从商户站点提供的 Launch URL 打开此页面。",
    "error.load": "无法载入托管页面。",
    "order.amountPrompt": "请输入份额（最多 6 位小数）",
    "order.stakePrompt": "请输入投注金额（{currency}，最多 2 位小数）",
    "order.noQuote": "该结果暂无可成交报价，请稍后再试。",
    "order.failed": "下单失败。",
    "deadline.tbd": "待定",
    "deadline.suffix": "{date} 结算",
    "status.trading": "交易中",
    "meta.events": "{category} · {count} 个市场",
    "meta.eventsOne": "{category} · {count} 个市场",
    "volume": "交易量 {volume}",
    "quote.aria": "{label}的当前报价",
  },
  "en-US": {
    "brand.home": "PredictMarket home",
    "balance.label": "Balance",
    "close.label": "Back to merchant site",
    "nav.label": "Main navigation",
    "nav.home": "Discover",
    "nav.categories": "Categories",
    "nav.orders": "Orders",
    "lang.choose": "Choose language",
    "lang.zh": "中文",
    "lang.en": "English",
    "home.eyebrow": "Prediction markets",
    "home.title": "Market prices, collective judgment.",
    "home.subtitle": "Browse live events and pick the outcome you believe in.",
    "home.categories": "Browse categories",
    "home.allCategories": "All categories",
    "home.trending": "Trending events",
    "home.viewAll": "View all",
    "home.trading": "Live markets",
    "home.allMarkets": "All markets",
    "category.all": "All",
    "category.sports": "Sports",
    "category.crypto": "Crypto",
    "category.world": "World",
    "category.entertainment": "Entertainment",
    "category.technology": "Technology",
    "category.allTitle": "All markets",
    "category.titleSuffix": "{label} markets",
    "category.explore": "Explore",
    "category.events": "Events",
    "category.markets": "Markets",
    "category.count": "{count} items",
    "category.empty": "No events in this category yet.",
    "common.home": "Home",
    "common.market": "Market",
    "event.details": "Event details",
    "event.title": "Event",
    "event.marketsCount": "{count} markets",
    "event.marketsCountOne": "{count} market",
    "market.details": "Market details",
    "market.chooseOutcome": "Choose outcome",
    "market.currentOdds": "Current odds",
    "market.noQuote": "No quote",
    "market.rules": "Settlement rules",
    "market.rulesNote": "This market settles according to the rules published on its event page.",
    "market.notice": "Prices reflect the market's current view and are not advice. Review the settlement rules before ordering.",
    "market.choice": "Choice: {label}",
    "market.liveBook": "Live book",
    "market.poolStake": "Pool bet",
    "market.modeOrderbook": "Order book",
    "market.modeParimutuel": "Pool",
    "market.pool": "Pool",
    "market.poolTotal": "Total staked {amount}",
    "market.poolOption": "{option} staked {amount}",
    "market.continue": "Continue",
    "market.submitting": "Submitting…",
    "orders.title": "My orders",
    "orders.account": "Account",
    "orders.empty": "No orders yet.",
    "notFound.title": "This page doesn't exist or has been removed.",
    "notFound.home": "Back to home",
    "loading.markets": "Loading markets…",
    "retry": "Retry",
    "error.session": "Your session expired. Please reopen the page from the merchant site.",
    "error.http": "Request failed ({status})",
    "error.launch": "Open this page from the merchant site's Launch URL.",
    "error.load": "Unable to load the hosted page.",
    "order.amountPrompt": "Enter shares (up to 6 decimals)",
    "order.stakePrompt": "Enter stake amount ({currency}, up to 2 decimals)",
    "order.noQuote": "No tradable quote for this outcome yet. Please try again later.",
    "order.failed": "Order failed.",
    "deadline.tbd": "TBD",
    "deadline.suffix": "Settles {date}",
    "status.trading": "Trading",
    "meta.events": "{category} · {count} markets",
    "meta.eventsOne": "{category} · {count} market",
    "volume": "Volume {volume}",
    "quote.aria": "Current quote for {label}",
  },
};

const DEFAULT_LOCALE = "zh-CN";
let locale = DEFAULT_LOCALE;

// Interpolation values are treated as data and HTML-escaped; dictionary
// literals are trusted.
function t(key, vars) {
  const table = translations[locale] ?? translations[DEFAULT_LOCALE];
  const template = table?.[key] ?? translations[DEFAULT_LOCALE][key] ?? key;
  if (!vars) return template;
  return template.replace(/\{(\w+)\}/g, (match, name) => {
    const value = vars[name];
    return value == null ? match : escapeHTML(value);
  });
}

function normalizeLocale(value) {
  const code = String(value ?? "").trim().toLowerCase();
  if (code.startsWith("zh")) return "zh-CN";
  if (code.startsWith("en")) return "en-US";
  return "";
}

function savedLocale() {
  try { return window.localStorage.getItem("pm_locale"); } catch { return null; }
}

function writeSavedLocale(value) {
  try { window.localStorage.setItem("pm_locale", value); } catch { /* storage can be blocked in embedded contexts */ }
}

// Preference order: the user's own choice (persisted per device), then the
// merchant session locale, then the browser language, then zh-CN.
function detectLocale() {
  const saved = savedLocale();
  if (saved && translations[saved]) return saved;
  const fromNavigator = normalizeLocale(navigator.language);
  if (translations[fromNavigator]) return fromNavigator;
  return DEFAULT_LOCALE;
}

function applyLocale(next) {
  if (!translations[next]) return;
  locale = next;
  writeSavedLocale(next);
  document.documentElement.lang = locale;
  state.langOpen = false;
  render();
}

const app = document.querySelector("#app");
const state = {
  accessToken: "",
  me: null,
  events: [],
  markets: [],
  orders: [],
  selectedOutcome: null,
  marketPools: {},
  loading: true,
  error: "",
  submitting: false,
  langOpen: false,
};

// Keep the data model at the edge of the UI. The hosted shell never receives
// a merchant API key; it exchanges the one-time token embedded in launch_url
// and uses the resulting short-lived session credential for every API call.
let events = [];
let markets = [];
const BALANCE_REFRESH_INTERVAL_MS = 60_000;
let lastBalanceSyncAt = 0;
let balanceRefreshInFlight = null;

function escapeHTML(value) {
  return String(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#039;");
}

function categoryLabel(id) {
  const category = categories.find((item) => item.id === id);
  return category ? t(category.labelKey) : t("category.all");
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
        <a class="brand" href="#/home" aria-label="${t("brand.home")}">
          <span class="brand__mark">P</span><span>PredictMarket</span>
        </a>
        <div class="topbar__actions">
          <div class="balance" aria-label="${t("balance.label")}"><span>${t("balance.label")}</span><strong>${escapeHTML(balance)}</strong></div>
          <div class="lang">
            <button class="icon-button lang__toggle" type="button" data-action="toggle-lang" aria-haspopup="listbox" aria-expanded="${state.langOpen}" aria-label="${t("lang.choose")}">${locale === "zh-CN" ? "中" : "EN"}</button>
            <div class="lang__panel" role="listbox" aria-label="${t("lang.choose")}" ${state.langOpen ? "" : "hidden"}>
              <button type="button" role="option" data-lang="zh-CN" aria-selected="${locale === "zh-CN"}">${t("lang.zh")}</button>
              <button type="button" role="option" data-lang="en-US" aria-selected="${locale === "en-US"}">${t("lang.en")}</button>
            </div>
          </div>
          <button class="icon-button" type="button" data-action="close" aria-label="${t("close.label")}">×</button>
        </div>
      </header>
      ${content}
    </div>
    <nav class="nav" aria-label="${t("nav.label")}">
      ${navButton("home", "⌂", t("nav.home"), active)}
      ${navButton("categories", "▦", t("nav.categories"), active)}
      ${navButton("orders", "▤", t("nav.orders"), active)}
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
        <span class="event-card__meta">${t(event.markets.length === 1 ? "meta.eventsOne" : "meta.events", { category: categoryLabel(event.category), count: event.markets.length })}</span>
      </span>
      <span class="event-card__arrow" aria-hidden="true">›</span>
    </button>`;
}

function marketCard(market) {
  const leading = market.outcomes[0];
  const price = leading?.price == null ? t("market.noQuote") : `${leading.price}¢`;
  return `
    <button class="market-card" type="button" data-route="/market/${market.id}">
      <span class="market-card__top">
        <span class="market-card__category">${escapeHTML(marketCategoryLabel(market))}</span>
        <span class="market-card__volume">${t("volume", { volume: market.volume })}</span>
      </span>
      <h3>${escapeHTML(market.question)}</h3>
      ${market.eventTitle ? `<span class="market-card__event">${escapeHTML(market.eventTitle)}</span>` : ""}
      <span class="probability" aria-label="${t("quote.aria", { label: leading?.label ?? "" })}">
        <span class="probability__bar"><span class="probability__fill" style="width:${leading?.price ?? 0}%"></span></span>
        <strong class="probability__value">${price}</strong>
      </span>
      <span class="market-card__footer"><span>${escapeHTML(leading?.label ?? "")} ${price}</span><span class="status">${escapeHTML(marketStatusLabel(market))}</span></span>
    </button>`;
}

function homePage() {
  const featured = markets.slice(0, 3);
  return shell(`
    <section class="page">
      <div class="hero">
        <p class="eyebrow">${t("home.eyebrow")}</p>
        <h1>${t("home.title")}</h1>
        <p>${t("home.subtitle")}</p>
      </div>
      <section class="section" aria-labelledby="categories-title">
        <div class="section-heading"><h2 id="categories-title">${t("home.categories")}</h2><button class="text-button" type="button" data-route="/category/all">${t("home.allCategories")}</button></div>
        ${categoryChips("all")}
      </section>
      <section class="section" aria-labelledby="events-title">
        <div class="section-heading"><h2 id="events-title">${t("home.trending")}</h2><button class="text-button" type="button" data-route="/category/all">${t("home.viewAll")}</button></div>
        <div class="event-grid">${events.slice(0, 4).map(eventCard).join("")}</div>
      </section>
      <section class="section" aria-labelledby="markets-title">
        <div class="section-heading"><h2 id="markets-title">${t("home.trading")}</h2><button class="text-button" type="button" data-route="/category/all">${t("home.allMarkets")}</button></div>
        <div class="market-list">${featured.map(marketCard).join("")}</div>
      </section>
    </section>
  `, "home");
}

function categoryChips(activeCategory) {
  return `<div class="categories" role="tablist" aria-label="${t("nav.categories")}">${categories.map((category) => `
    <button class="chip" type="button" role="tab" data-route="/category/${category.id}" aria-current="${category.id === activeCategory}">${t(category.labelKey)}</button>`).join("")}</div>`;
}

function categoryPage(category) {
  const eventMatches = category === "all" ? events : events.filter((event) => event.category === category);
  const marketMatches = category === "all" ? markets : markets.filter((market) => events.find((event) => event.id === market.eventId)?.category === category);
  const title = category === "all" ? t("category.allTitle") : t("category.titleSuffix", { label: categoryLabel(category) });
  return shell(`
    <section class="page">
      <div class="breadcrumb"><button type="button" data-route="/home">${t("common.home")}</button><span>/</span><span>${title}</span></div>
      <section class="section">
        <p class="section-kicker">${t("category.explore")}</p>
        <h1>${title}</h1>
        ${categoryChips(category)}
      </section>
      <section class="section" aria-labelledby="category-events-title">
        <div class="section-heading"><h2 id="category-events-title">${t("category.events")}</h2><span class="label">${t("category.count", { count: eventMatches.length })}</span></div>
        <div class="event-grid">${eventMatches.length ? eventMatches.map(eventCard).join("") : `<div class="empty">${t("category.empty")}</div>`}</div>
      </section>
      <section class="section" aria-labelledby="category-markets-title">
        <div class="section-heading"><h2 id="category-markets-title">${t("category.markets")}</h2><span class="label">${t("category.count", { count: marketMatches.length })}</span></div>
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
      <div class="breadcrumb"><button type="button" data-route="/category/${event.category}">${categoryLabel(event.category)}</button><span>/</span><span>${t("event.details")}</span></div>
      <header class="event-header">
        <div class="event-heading">
          <span class="event-heading__badge" aria-hidden="true">${event.icon}</span>
          <div><p class="event-kicker">${categoryLabel(event.category)}</p><h1>${escapeHTML(event.title)}</h1><p>${deadlineLabel(event.resolutionTime)}</p></div>
        </div>
        <p class="event-note">${escapeHTML(event.description)}</p>
      </header>
      <section class="section" aria-labelledby="event-markets-title">
        <div class="section-heading"><h2 id="event-markets-title">${t(eventMarkets.length === 1 ? "event.marketsCountOne" : "event.marketsCount", { count: eventMarkets.length })}</h2><span class="status">${deadlineLabel(event.resolutionTime)}</span></div>
        <div class="market-list">${eventMarkets.map(marketCard).join("")}</div>
      </section>
    </section>
  `, "categories");
}

function marketPage(market) {
  if (!market) return notFoundPage();
  const event = events.find((item) => item.id === market.eventId);
  const selected = state.selectedOutcome?.marketId === market.id ? state.selectedOutcome : null;
  const isPool = market.type === "parimutuel";
  const pools = state.marketPools[market.id];
  const odds = poolOdds(market);
  const poolSummary = isPool && pools && !pools.error ? `
        <section class="info-card"><h2>${t("market.pool")}</h2>
          <p>${t("market.poolTotal", { amount: pools.total_stake ?? "0.00" })}${pools.currency ? ` · ${escapeHTML(pools.currency)}` : ""}</p>
          <ul>${(pools.options ?? []).map((row) => `<li>${t("market.poolOption", { option: row.option, amount: formatPoolAmount(row.stake) })}</li>`).join("")}</ul>
        </section>` : "";
  return shell(`
    <section class="page">
      <div class="breadcrumb"><button type="button" data-route="/event/${market.eventId}">${escapeHTML(event?.title ?? t("event.title"))}</button><span>/</span><span>${t("market.details")}</span></div>
      <article class="market-hero">
        <p class="event-kicker">${escapeHTML(marketCategoryLabel(market))}</p>
        <h1>${escapeHTML(market.question)}</h1>
        <p class="market-hero__meta"><span>◷ ${deadlineLabel(market.resolutionTime)}</span><span>${t("volume", { volume: market.volume })}</span><span class="badge">${isPool ? t("market.modeParimutuel") : t("market.modeOrderbook")}</span><span class="status">${escapeHTML(marketStatusLabel(market))}</span></p>
      </article>
      <section aria-labelledby="outcomes-title">
        <div class="section-heading"><h2 id="outcomes-title">${t("market.chooseOutcome")}</h2><span class="label">${t("market.currentOdds")}</span></div>
        <div class="outcome-grid">${market.outcomes.map((outcome, index) => `
          <button class="outcome" type="button" data-outcome="${market.id}:${index}" aria-pressed="${selected?.index === index}">
            <strong>${escapeHTML(outcome.label)}</strong><span>${odds[outcome.label] == null ? t("market.noQuote") : `${odds[outcome.label]}¢`}</span>
          </button>`).join("")}</div>
      </section>
      ${poolSummary}
      <section class="info-card"><h2>${t("market.rules")}</h2><p>${t("market.rulesNote")}</p></section>
      <div class="notice"><span aria-hidden="true">ⓘ</span><span>${t("market.notice")}</span></div>
      ${selected ? `
        <div class="ticket" role="status">
          <div class="ticket__summary"><div class="ticket__choice"><strong>${escapeHTML(market.question)}</strong><span>${t("market.choice", { label: market.outcomes[selected.index].label })}</span></div><span class="ticket__price">${isPool ? t("market.poolStake") : t("market.liveBook")}</span></div>
          <button class="primary-button" type="button" data-action="place-order" ${state.submitting ? "disabled" : ""}>${state.submitting ? t("market.submitting") : t("market.continue")}</button>
        </div>` : ""}
    </section>
  `, "categories");
}

function ordersPage() {
  if (state.orders.length) {
    return shell(`
      <section class="page">
        <div class="breadcrumb"><button type="button" data-route="/home">${t("common.home")}</button><span>/</span><span>${t("orders.title")}</span></div>
        <section class="section"><p class="section-kicker">${t("orders.account")}</p><h1>${t("orders.title")}</h1>
          <div class="order-list">${state.orders.map((order) => `
            <article class="info-card"><div class="section-heading"><strong>${escapeHTML(order.market_id ?? t("common.market"))}</strong><span class="status">${escapeHTML(order.status ?? "")}</span></div>
              <p>${escapeHTML(order.type ?? "")} · ${escapeHTML(order.option ?? "")} · ${escapeHTML(String(order.amount ?? ""))}</p>
            </article>`).join("")}</div>
        </section>
      </section>
    `, "orders");
  }
  return shell(`
    <section class="page">
      <div class="breadcrumb"><button type="button" data-route="/home">${t("common.home")}</button><span>/</span><span>${t("orders.title")}</span></div>
      <section class="section"><p class="section-kicker">${t("orders.account")}</p><h1>${t("orders.title")}</h1><div class="empty">${t("orders.empty")}</div></section>
    </section>
  `, "orders");
}

function notFoundPage() {
  return shell(`<section class="page"><div class="empty"><p>${t("notFound.title")}</p><button class="primary-button" type="button" data-route="/home">${t("notFound.home")}</button></div></section>`);
}

function render() {
  if (state.loading) {
    app.innerHTML = `<section class="page"><div class="empty" role="status">${t("loading.markets")}</div></section>`;
    return;
  }
  if (state.error) {
    app.innerHTML = `<section class="page"><div class="empty"><p>${escapeHTML(state.error)}</p><button class="primary-button" type="button" data-action="reload">${t("retry")}</button></div></section>`;
    return;
  }
  const [root, identifier] = parseRoute();
  if (root === "market") ensureMarketPools(identifier);
  if (root === "category") app.innerHTML = categoryPage(categories.some((category) => category.id === identifier) ? identifier : "all");
  else if (root === "event") app.innerHTML = eventPage(events.find((event) => event.id === identifier));
  else if (root === "market") app.innerHTML = marketPage(markets.find((market) => market.id === identifier));
  else if (root === "orders") app.innerHTML = ordersPage();
  else app.innerHTML = homePage();
}

document.addEventListener("click", (event) => {
  const langTarget = event.target.closest("[data-lang]");
  if (langTarget) {
    applyLocale(langTarget.dataset.lang);
    return;
  }
  const actionTarget = event.target.closest("[data-action]");
  if (actionTarget?.dataset.action === "toggle-lang") {
    state.langOpen = !state.langOpen;
    render();
    return;
  }
  const routeTarget = event.target.closest("[data-route]");
  const outcomeTarget = event.target.closest("[data-outcome]");
  if (state.langOpen && !event.target.closest(".lang")) {
    state.langOpen = false;
    render();
  }
  if (routeTarget) {
    navigate(routeTarget.dataset.route);
    return;
  }
  if (outcomeTarget) {
    const [marketId, index] = outcomeTarget.dataset.outcome.split(":");
    state.selectedOutcome = { marketId, index: Number(index) };
    render();
    return;
  }
  if (!actionTarget) return;
  if (actionTarget.dataset.action === "close") emit("pm:navigate_home");
  if (actionTarget.dataset.action === "reload") {
    bootstrap();
  }
  if (actionTarget.dataset.action === "place-order" && state.selectedOutcome) {
    placeOrder();
  }
});

document.addEventListener("keydown", (event) => {
  if (event.key === "Escape" && state.langOpen) {
    state.langOpen = false;
    render();
  }
});

window.addEventListener("hashchange", render);

async function apiFetch(path, options = {}) {
  const { envelope = false, ...requestOptions } = options;
  const headers = new Headers(requestOptions.headers ?? {});
  headers.set("Accept", "application/json");
  if (requestOptions.body !== undefined) headers.set("Content-Type", "application/json");
  if (state.accessToken) headers.set("Authorization", `Bearer ${state.accessToken}`);
  const response = await fetch(path, { ...requestOptions, headers });
  let payload = null;
  try { payload = await response.json(); } catch { /* 204/no body */ }
  if (response.status === 401) {
    state.accessToken = "";
    emit("pm:session_expired");
    throw new Error(t("error.session"));
  }
  if (!response.ok) {
    const error = new Error(payload?.error?.message ?? t("error.http", { status: response.status }));
    error.meta = payload?.meta;
    throw error;
  }
  return envelope ? payload : payload?.data ?? payload;
}

function applyBalance(meta) {
  if (!meta?.available_balance || !state.me) return;
  state.me = {
    ...state.me,
    available_balance: meta.available_balance,
    currency: meta.currency ?? state.me.currency,
  };
  lastBalanceSyncAt = Date.now();
  emit("pm:balance_changed", { balance: state.me.available_balance, currency: state.me.currency });
}

function launchToken() {
  return new URLSearchParams(window.location.search).get("token")?.trim() ?? "";
}

function formatDeadline(value) {
  if (!value) return t("deadline.tbd");
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? t("deadline.tbd") : date.toLocaleDateString(locale, { year: "numeric", month: "short", day: "numeric" });
}

function deadlineLabel(value) {
  return t("deadline.suffix", { date: formatDeadline(value) });
}

function normalizeEvent(value) {
  return {
    ...value,
    icon: value.category === "sports" ? "⚽" : value.category === "technology" ? "✦" : "◈",
    resolutionTime: value.resolution_time,
    markets: [],
  };
}

function normalizeMarket(value) {
  const event = events.find((item) => item.id === value.event_id);
  return {
    ...value,
    eventId: value.event_id,
    categoryId: event?.category,
    eventTitle: event?.title,
    resolutionTime: event?.resolution_time,
    outcomes: (value.options ?? []).map((label) => ({ label, price: null })),
    volume: value.total_volume ?? "0.000000",
    status: value.status,
  };
}

// Parimutuel markets have no order book; their per-outcome odds are implied
// by the share of active stake each option holds in the pool.
function poolOdds(market) {
  const pools = state.marketPools[market.id];
  const total = Number(pools?.total_stake ?? 0);
  if (!(total > 0) || !Array.isArray(pools?.options)) return {};
  const odds = {};
  for (const row of pools.options) {
    odds[row.option] = Math.max(1, Math.round((Number(row.stake) / total) * 100));
  }
  return odds;
}

function formatPoolAmount(value) {
  const amount = Number(value ?? 0);
  return Number.isFinite(amount) ? amount.toFixed(2) : "0.00";
}

async function ensureMarketPools(marketId) {
  if (state.marketPools[marketId] !== undefined) return;
  const market = markets.find((item) => item.id === marketId);
  if (!market || market.type !== "parimutuel") {
    state.marketPools[marketId] = { error: true };
    return;
  }
  state.marketPools[marketId] = null; // in flight
  try {
    state.marketPools[marketId] = await apiFetch(`/api/user/markets/${encodeURIComponent(marketId)}/pools`);
  } catch {
    state.marketPools[marketId] = { error: true };
  }
  render();
}

// Display helpers derive locale-dependent strings at render time so a
// language switch re-renders everything without refetching.
function marketCategoryLabel(market) {
  return categoryLabel(market.categoryId);
}

function marketStatusLabel(market) {
  return market.status === "active" ? t("status.trading") : market.status;
}

async function bootstrap() {
  state.loading = true;
  state.error = "";
  locale = detectLocale();
  document.documentElement.lang = locale;
  render();
  try {
    let sessionUser = null;
    const token = launchToken();
    if (!state.accessToken && !token) throw new Error(t("error.launch"));
    if (!state.accessToken) {
      const exchanged = await apiFetch("/api/user/session/exchange", {
        method: "POST",
        body: JSON.stringify({ token }),
      });
      state.accessToken = exchanged.access_token;
      sessionUser = exchanged.user?.available_balance ? exchanged.user : null;
      // A one-time token must not remain in browser history or referrers.
      const cleanURL = new URL(window.location.href);
      cleanURL.searchParams.delete("token");
      window.history.replaceState({}, document.title, cleanURL.toString());
    }
    const [me, eventPage, marketPage, orderPage] = await Promise.all([
      sessionUser ?? apiFetch("/api/user/me"),
      apiFetch("/api/user/events?limit=100"),
      apiFetch("/api/user/markets?limit=100&status=active"),
      apiFetch("/api/user/orders?limit=100"),
    ]);
    state.me = me;
    lastBalanceSyncAt = Date.now();
    // The merchant session locale sets the default language unless the user
    // has already picked one on this device.
    if (!savedLocale()) {
      const sessionLocale = normalizeLocale(me.locale);
      if (translations[sessionLocale]) {
        locale = sessionLocale;
        document.documentElement.lang = locale;
      }
    }
    events = (Array.isArray(eventPage) ? eventPage : []).map(normalizeEvent);
    markets = (Array.isArray(marketPage) ? marketPage : []).map(normalizeMarket);
    events.forEach((event) => { event.markets = markets.filter((market) => market.eventId === event.id).map((market) => market.id); });
    state.orders = Array.isArray(orderPage) ? orderPage : [];
  } catch (error) {
    state.error = error instanceof Error ? error.message : t("error.load");
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
  if (market.type === "parimutuel") {
    const amount = window.prompt(t("order.stakePrompt", { currency: state.me?.currency ?? "" }), "1");
    if (!amount || !/^\d+(\.\d{1,2})?$/.test(amount) || Number(amount) <= 0) return;
    state.submitting = true;
    render();
    try {
      const option = market.outcomes[selected.index];
      const result = await apiFetch("/api/user/bets", {
        method: "POST",
        headers: { "Idempotency-Key": crypto.randomUUID() },
        body: JSON.stringify({ market_id: market.id, option: option.label, amount: Number(amount) }),
        envelope: true,
      });
      const bet = result.data;
      state.orders.unshift({ ...bet, type: "bet", amount: bet.stake });
      state.selectedOutcome = null;
      applyBalance(result.meta);
      emit("pm:bet_placed", { market_id: market.id, order_id: bet.id, outcome: option.label });
    } catch (error) {
      applyBalance(error?.meta);
      state.error = error instanceof Error ? error.message : t("order.failed");
    } finally {
      state.submitting = false;
      render();
    }
    return;
  }
  const amount = window.prompt(t("order.amountPrompt"), "1");
  if (!amount || !/^\d+(\.\d{1,6})?$/.test(amount) || Number(amount) <= 0) return;
  state.submitting = true;
  render();
  try {
    const book = await apiFetch(`/api/user/markets/${encodeURIComponent(market.id)}/orderbook`);
    const option = market.outcomes[selected.index];
    const entries = [...(book?.asks ?? []), ...(book?.bids ?? [])].filter((entry) => entry.option === option.label && Number(entry.price) > 0);
    const price = entries[0]?.price;
    if (price === undefined) throw new Error(t("order.noQuote"));
    const result = await apiFetch("/api/user/orders", {
      method: "POST",
      headers: { "Idempotency-Key": crypto.randomUUID() },
      body: JSON.stringify({ market_id: market.id, type: "buy", option: option.label, amount: Number(amount), price }),
      envelope: true,
    });
    const order = result.data;
    state.orders.unshift(order);
    state.selectedOutcome = null;
    applyBalance(result.meta);
    emit("pm:bet_placed", { market_id: market.id, order_id: order.id, outcome: option.label });
  } catch (error) {
    applyBalance(error?.meta);
    state.error = error instanceof Error ? error.message : t("order.failed");
  } finally {
    state.submitting = false;
    render();
  }
}

async function refreshMe() {
  if (!state.accessToken || balanceRefreshInFlight) return balanceRefreshInFlight;
  balanceRefreshInFlight = apiFetch("/api/user/me")
    .then((me) => {
      state.me = me;
      lastBalanceSyncAt = Date.now();
      emit("pm:balance_changed", { balance: state.me.available_balance, currency: state.me.currency });
      render();
    })
    .catch(() => { /* apiFetch already emits session_expired */ })
    .finally(() => { balanceRefreshInFlight = null; });
  return balanceRefreshInFlight;
}

document.addEventListener("visibilitychange", () => {
  if (document.visibilityState === "visible") refreshMe();
});

window.addEventListener("focus", () => {
  if (Date.now() - lastBalanceSyncAt >= 1_000) refreshMe();
});

window.setInterval(() => {
  if (document.visibilityState === "visible" && Date.now() - lastBalanceSyncAt >= BALANCE_REFRESH_INTERVAL_MS) {
    refreshMe();
  }
}, BALANCE_REFRESH_INTERVAL_MS);

bootstrap();
