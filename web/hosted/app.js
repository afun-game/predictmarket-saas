const categories = [
  { id: "all", labelKey: "category.all" },
  { id: "hot", labelKey: "category.hot" },
  { id: "football", labelKey: "category.football" },
  { id: "basketball", labelKey: "category.basketball" },
  { id: "baseball", labelKey: "category.baseball" },
  { id: "boxing", labelKey: "category.boxing" },
  { id: "weather", labelKey: "category.weather" },
  { id: "bitcoin", labelKey: "category.bitcoin" },
  { id: "other", labelKey: "category.other" },
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
    "category.hot": "热门",
    "category.football": "足球",
    "category.basketball": "篮球",
    "category.baseball": "棒球",
    "category.boxing": "拳击",
    "category.weather": "天气",
    "category.bitcoin": "比特币",
    "category.other": "其它",
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
    "market.poolOptionReturn": "{option} 投注 {amount} · 回报率 {odds}",
    "market.orderbook": "盘口报价",
    "market.noQuotes": "暂无报价，做市商将在数秒内自动挂单，请稍候刷新",
    "market.livePrice": "可成交价 {price}¢",
    "orderbook.bid": "买",
    "orderbook.ask": "卖",
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
    "category.hot": "Hot",
    "category.football": "Football",
    "category.basketball": "Basketball",
    "category.baseball": "Baseball",
    "category.boxing": "Boxing",
    "category.weather": "Weather",
    "category.bitcoin": "Bitcoin",
    "category.other": "Other",
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
    "market.poolOptionReturn": "{option} staked {amount} · Return {odds}",
    "market.orderbook": "Order book",
    "market.noQuotes": "No quotes yet. The market maker will place quotes shortly; refresh later.",
    "market.livePrice": "Executable at {price}¢",
    "orderbook.bid": "Buy",
    "orderbook.ask": "Sell",
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
// The exchanged access token outlives the one-time launch token, which is
// stripped from the URL after exchange. Persist it per tab (sessionStorage) so
// a page reload resumes the browser session instead of failing; a revoked or
// expired session is cleared and the user relaunches from the merchant site.
const SESSION_STORAGE_KEY = "pm_hosted_session";

function loadStoredSession() {
  try {
    const raw = sessionStorage.getItem(SESSION_STORAGE_KEY);
    if (!raw) return null;
    const parsed = JSON.parse(raw);
    if (typeof parsed?.accessToken !== "string" || parsed.accessToken === "") return null;
    return parsed;
  } catch {
    return null;
  }
}

function storeSession(accessToken, expiresAt) {
  try {
    sessionStorage.setItem(SESSION_STORAGE_KEY, JSON.stringify({ accessToken, expiresAt: expiresAt ?? null }));
  } catch {
    // Storage unavailable (private mode): the session lives in memory only.
  }
}

function clearStoredSession() {
  try {
    sessionStorage.removeItem(SESSION_STORAGE_KEY);
  } catch {
    // Ignore storage failures; the in-memory token is cleared by callers.
  }
}

const state = {
  accessToken: "",
  me: null,
  events: [],
  markets: [],
  orders: [],
  selectedOutcome: null,
  marketPools: {},
  orderBooks: {},
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
  const quote = market.book?.quotes?.find((entry) => entry.option === leading?.label);
  const poolLeading = market.pool?.options?.[0];
  const isPool = market.type === "parimutuel";
  // Binary cards quote the best executable ask; parimutuel cards show the
  // leading option's implied probability from the embedded pool summary.
  const implied = isPool
    ? (poolLeading && Number(market.pool?.total_stake) > 0
        ? Math.round((Number(poolLeading.stake) / Number(market.pool.total_stake)) * 100)
        : null)
    : (quote?.ask == null ? null : Math.round(quote.ask * 100));
  const price = implied == null ? t("market.noQuote") : isPool ? `${implied}%` : `${implied}¢`;
  const poolLine = isPool && market.pool?.options?.length ? `
        <span class="market-card__pool">${market.pool.options.map((row) => {
          const odds = Number(row.odds) > 0 ? `${Number(row.odds).toFixed(2)}x` : t("market.noQuote");
          return `${escapeHTML(row.option)} ${formatPoolAmount(row.stake)} · ${odds}`;
        }).join(" · ")}</span>` : "";
  // Sparkline: the leading outcome's hourly closes over the last 24 hours,
  // normalized to bar heights, plus the period change badge.
  const points = market.history?.points ?? [];
  const sparkline = !isPool && points.length > 1
    ? (() => {
        const min = Math.min(...points);
        const max = Math.max(...points);
        const range = max - min || 1;
        const bars = points.map((point) => Math.max(6, Math.round(((point - min) / range) * 100)));
        const change = Number(market.history?.change_24h);
        const delta = Number.isFinite(change) && change !== 0 ? (change > 0 ? "▲" : "▼") : "";
        const deltaText = Number.isFinite(change) ? `${delta}${Math.abs(change).toFixed(2)}` : "";
        return `<span class="market-card__sparkline"><span class="sparkline__bars">${bars.map((height) => `<i style="height:${height}%"></i>`).join("")}</span>${deltaText ? `<em class="sparkline__delta${change > 0 ? " up" : change < 0 ? " down" : ""}">${deltaText} 24h</em>` : ""}</span>`;
      })()
    : "";
  const metaBits = [market.league, market.resolutionTime ? deadlineLabel(market.resolutionTime) : ""].filter(Boolean);
  return `
    <button class="market-card" type="button" data-route="/market/${market.id}">
      <span class="market-card__top">
        <span class="market-card__category">${escapeHTML(marketCategoryLabel(market))}</span>
        <span class="market-card__volume">${t("volume", { volume: market.volume })}</span>
      </span>
      <h3>${escapeHTML(market.question)}</h3>
      ${market.eventTitle ? `<span class="market-card__event">${escapeHTML(market.eventTitle)}</span>` : ""}
      ${metaBits.length ? `<span class="market-card__meta">${metaBits.map((bit) => `<span>${escapeHTML(bit)}</span>`).join('<span class="dot">·</span>')}</span>` : ""}
      ${poolLine}
      ${sparkline}
      <span class="probability" aria-label="${t("quote.aria", { label: leading?.label ?? "" })}">
        <span class="probability__bar"><span class="probability__fill" style="width:${implied ?? 0}%"></span></span>
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
  const tradingEventIds = new Set(markets.filter((market) => market.status === "active").map((market) => market.eventId));
  const eventMatches = category === "all" ? events : category === "hot"
    ? events.filter((event) => tradingEventIds.has(event.id))
    : events.filter((event) => event.category === category);
  const marketMatches = category === "all" ? markets : category === "hot"
    ? markets.filter((market) => market.status === "active")
    : markets.filter((market) => events.find((event) => event.id === market.eventId)?.category === category);
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
  const book = state.orderBooks[market.id];
  const odds = poolOdds(market);
  const quoteFor = (label) => (isPool ? odds[label] : askQuote(book, label));
  const poolSummary = isPool && pools && !pools.error ? `
        <section class="info-card"><h2>${t("market.pool")}</h2>
          <p>${t("market.poolTotal", { amount: pools.total_stake ?? "0.00" })}${pools.currency ? ` · ${escapeHTML(pools.currency)}` : ""}</p>
          <ul>${(pools.options ?? []).map((row) => {
            const odds = Number(row.odds) > 0 ? `${Number(row.odds).toFixed(2)}x` : t("market.noQuote");
            return `<li>${t("market.poolOptionReturn", { option: row.option, amount: formatPoolAmount(row.stake), odds })}</li>`;
          }).join("")}</ul>
        </section>` : "";
  const bookSummary = !isPool ? `
        <section class="info-card"><h2>${t("market.orderbook")}</h2>${orderBookSummary(market, book)}</section>` : "";
  const ticketPrice = isPool ? t("market.poolStake") : (() => {
    const price = selected ? askQuote(book, market.outcomes[selected.index].label) : undefined;
    return price == null ? t("market.liveBook") : t("market.livePrice", { price });
  })();
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
            <strong>${escapeHTML(outcome.label)}</strong><span>${quoteFor(outcome.label) == null ? t("market.noQuote") : isPool ? `${quoteFor(outcome.label)}%` : `${quoteFor(outcome.label)}¢`}</span>
          </button>`).join("")}</div>
      </section>
      ${poolSummary}
      ${bookSummary}
      <section class="info-card"><h2>${t("market.rules")}</h2><p>${t("market.rulesNote")}</p></section>
      <div class="notice"><span aria-hidden="true">ⓘ</span><span>${t("market.notice")}</span></div>
      ${selected ? `
        <div class="ticket" role="status">
          <div class="ticket__summary"><div class="ticket__choice"><strong>${escapeHTML(market.question)}</strong><span>${t("market.choice", { label: market.outcomes[selected.index].label })}</span></div><span class="ticket__price">${ticketPrice}</span></div>
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
            <article class="info-card"><div class="section-heading"><strong>${escapeHTML(order.market_title || order.market_id || t("common.market"))}</strong><span class="status">${escapeHTML(order.status ?? "")}</span></div>
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
  if (root === "market") {
    ensureMarketPools(identifier);
    ensureOrderBook(identifier);
  }
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
    clearStoredSession();
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
    icon: { hot: "🔥", football: "⚽", basketball: "🏀", baseball: "⚾", boxing: "🥊", weather: "🌦", bitcoin: "₿" }[value.category] ?? "◈",
    resolutionTime: value.resolution_time,
    markets: [],
  };
}

function normalizeMarket(value) {
  const event = events.find((item) => item.id === value.event_id);
  return {
    ...value,
    eventId: value.event_id,
    categoryId: value.category || event?.category,
    eventTitle: value.event_title || event?.title,
    resolutionTime: value.resolution_time || event?.resolution_time,
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

// Order-book markets quote a per-option price from resting orders. The best
// executable buy price is the lowest ask (the sell side of the book); the
// best bid is the highest resting buy.
function askQuote(book, optionLabel) {
  const asks = (book?.asks ?? []).filter((entry) => entry.option === optionLabel && Number(entry.price) > 0);
  return asks.length ? asks[0].price : undefined;
}

function bidQuote(book, optionLabel) {
  const bids = (book?.bids ?? []).filter((entry) => entry.option === optionLabel && Number(entry.price) > 0);
  return bids.length ? bids[0].price : undefined;
}

async function ensureOrderBook(marketId) {
  if (state.orderBooks[marketId] !== undefined) return;
  const market = markets.find((item) => item.id === marketId);
  if (!market || market.type === "parimutuel") {
    state.orderBooks[marketId] = { error: true };
    return;
  }
  state.orderBooks[marketId] = null; // in flight
  try {
    state.orderBooks[marketId] = await apiFetch(`/api/user/markets/${encodeURIComponent(marketId)}/orderbook`);
  } catch {
    state.orderBooks[marketId] = { error: true };
  }
  render();
}

// orderBookSummary renders per-option bid/ask rows for an order-book market.
function orderBookSummary(market, book) {
  if (!book || book.error) return `<p>${t("market.noQuotes")}</p>`;
  const rows = market.outcomes.map((outcome) => {
    const bid = bidQuote(book, outcome.label);
    const ask = askQuote(book, outcome.label);
    return `<li>${escapeHTML(outcome.label)} · ${t("orderbook.bid")} ${bid == null ? "—" : `${bid}¢`} · ${t("orderbook.ask")} ${ask == null ? "—" : `${ask}¢`}</li>`;
  }).join("");
  return rows ? `<ul>${rows}</ul>` : `<p>${t("market.noQuotes")}</p>`;
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
    const stored = loadStoredSession();
    if (!state.accessToken && !token && !stored) throw new Error(t("error.launch"));
    if (!state.accessToken) {
      if (token) {
        const exchanged = await apiFetch("/api/user/session/exchange", {
          method: "POST",
          body: JSON.stringify({ token }),
        });
        state.accessToken = exchanged.access_token;
        storeSession(exchanged.access_token, exchanged.expires_at);
        sessionUser = exchanged.user?.available_balance ? exchanged.user : null;
        // A one-time token must not remain in browser history or referrers.
        const cleanURL = new URL(window.location.href);
        cleanURL.searchParams.delete("token");
        window.history.replaceState({}, document.title, cleanURL.toString());
      } else {
        // Reload: resume the persisted browser session. Refresh rotates the
        // token and extends the two-hour TTL; an expired or revoked session
        // is rejected here so the page asks for a fresh Launch URL.
        state.accessToken = stored.accessToken;
        try {
          const refreshed = await apiFetch("/api/user/session/refresh", { method: "POST" });
          state.accessToken = refreshed.access_token;
          storeSession(refreshed.access_token, refreshed.expires_at);
        } catch {
          clearStoredSession();
          throw new Error(t("error.session"));
        }
      }
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
      // The response carries the post-bet pool snapshot (totals + per-option
      // return rates) so the ticket and pool card update without a refetch.
      if (result.meta?.pool) state.marketPools[market.id] = result.meta.pool;
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
    const option = market.outcomes[selected.index];
    // Refresh the book so the executable price is current, then cache it for
    // the page display.
    const book = await apiFetch(`/api/user/markets/${encodeURIComponent(market.id)}/orderbook`);
    state.orderBooks[market.id] = book;
    const price = askQuote(book, option.label);
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
