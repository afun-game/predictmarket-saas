# Hosted frontend design

## Scope

`V3_PLAN.md` makes the hosted frontend an independent workstream. The first
deliverable is a fast, iframe-safe discovery experience for a merchant's
logged-in user, not a merchant administration console. Its no-build prototype
is at `web/hosted/` and deliberately contains no external scripts, fonts,
images, analytics, or merchant-side mock authentication.

The prototype covers four user-facing pages:

| Page | Route | Purpose |
|---|---|---|
| Discovery | `#/home` | Brand header, available balance, horizontal categories, featured events, and active markets. |
| Category | `#/category/:category` | Filtered event and market list with a compact mobile density. |
| Event detail | `#/event/:event` | Event context, settlement time, and its related markets. |
| Market detail | `#/market/:market` | Question, resolution rule, volume, outcomes, live orderbook selection, and a ticket transition. |

`#/orders` renders the V3 session-bound order history.

## UX decisions

- **Mobile first:** the content column is capped at 760 px, has safe-area
  padding, 44 px controls, an always-visible bottom navigation, and no
  hover-only affordance.
- **Fast by construction:** system fonts, SVG-free CSS shapes, no remote
  assets, no UI framework, and hash routing mean the initial view is one HTML,
  CSS, and JS request after the iframe document.
- **Clear hierarchy:** event cards answer “what is happening”; market cards
  answer “what can be traded”; the detail screen only introduces an action
  ticket after the user explicitly selects an outcome.
- **Conservative financial UI:** prices are displayed as cents and the market
  resolution rule stays beside the action. The shell only confirms an order
  after the API returns it and balance refresh succeeds.
- **Accessible baseline:** semantic buttons, labels, focus indicators, reduced
  motion support, contrast-safe text, and no color-only status encoding.
- **Language selection:** a topbar panel switches the UI between 中文 and
  English (zh-CN / en-US). The merchant session locale
  (`/api/user/me` `locale`) sets the default; a user's own choice is persisted
  per device and wins. Server-provided titles, questions, and rules are data
  and are not translated client-side; dates and status labels are localized.
- **Text containment:** list cards clamp titles to two lines and truncate
  meta rows, so long event and market titles never overflow their cards or
  the layout.

## Launch and API handoff

The browser must not receive a merchant API key. The production shell is
entered only through the one-time Launch URL:

1. Merchant server calls `POST /api/v2/sessions`.
2. Browser opens the returned `launch_url` in an iframe or top-level window.
3. Hosted UI exchanges the token with `POST /api/user/session/exchange`.
4. The session credential is kept in memory (not a query string or persistent
   browser storage), then used for `/api/user/*` requests.

Session errors are classified by authentication context: a missing exchange
`token` is `400 validation_error`; an unknown, expired, consumed, or revoked
launch token is `401 invalid_token`. Protected `/api/user/*` routes return
`401 unauthorized` when the Bearer credential is missing and `401
invalid_token` when it is invalid or expired. A `404` is reserved for an
actual resource that is absent or hidden by tenant isolation.

When the V3 routes are enabled, the API also serves this prototype directly
at `GET /launch` (the assets are embedded into the binary), so a single
deployment can host both the API and the sandbox trading page; point
`HOSTED_UI_URL` at `https://<api-host>/launch` for that topology.

The API process enables these V3 routes only when all three deployment secrets
are present: `MERCHANT_SECRET_ENCRYPTION_KEY` (base64url 32-byte AES key),
`SESSION_JWT_SECRET` (base64url 32-byte HMAC key), and `HOSTED_UI_URL` (the
absolute HTTPS `/launch` URL). A new merchant's V3 API secret is encrypted at
registration. Existing merchants can use the administrator-only
`POST /api/v1/merchants/{merchant_id}/v3-secret/reissue` endpoint before
enabling V3. The old and replacement Secrets both verify for seven days, and
the replacement is returned only in that response.

The frontend needs the following normalized view fields in addition to the
endpoints already named in V3:

| View | Endpoint | Required fields |
|---|---|---|
| Header | session exchange, then `GET /api/user/me` fallback | `user_id`, `currency`, `available_balance`, `locale`, `wallet_mode`. |
| Event discovery | `GET /api/user/events` | `id`, `title`, `description`, `category`, `end_time`, `resolution_time`, `status`, `outcome`. |
| Event detail | `GET /api/user/events/{id}` plus `GET /api/user/markets?event_id=` | event context and the current merchant's related markets. |
| Market list/detail | `GET /api/user/markets`, `GET /api/user/markets/{id}` | `id`, `event_id`, `question`, binary `options`, `total_volume`, `status`, `settled_at`. |
| Ticket and history | `POST /api/user/orders`, `GET /api/user/orders` | V3 fixed-point amount strings, order state, filled shares, timestamps, and response `meta.available_balance`. |

The event category is a stable server-side display key; the client must never
infer a category from titles. Outcome prices are sourced from the orderbook
endpoint, and the ticket flow uses the V3 hosted order API.

The initial balance travels in the signed launch request and is returned by
session exchange. Successful and insufficient-funds order responses carry the
latest balance in `meta`, so the hosted page does not issue an immediate
`/api/user/me` request after a bet. The read-only balance callback remains a
fallback: refresh on restored page visibility/focus and after 60 seconds with
no balance-bearing response.

## Iframe contract

The plan's `postMessage` names are retained:

```text
pm:ready
pm:bet_placed
pm:balance_changed
pm:session_expired
pm:navigate_home
```

Every message should have `{ source: "predictmarket", type, detail }`. The
Launch session must include an allow-listed merchant `parent_origin`; it is
used as the exact `postMessage` target origin. Do not use `"*"`, and the
hosted UI must validate the origin of any incoming parent message before
acting on it. The prototype accepts `parent_origin` only to demonstrate this
restriction; production gets it from the signed session configuration.

## Production completion criteria

Before this becomes the production hosted UI, add browser tests for token
exchange, parent-origin enforcement, locale, keyboard navigation, and the
stated `postMessage` events. Test it in a real iframe on iOS Safari and
Android Chrome, including an on-screen keyboard in the order ticket. Keep the
compressed first-view transfer below 75 KB excluding API responses and target
an LCP below 1.5 s on a mid-tier 4G device.
