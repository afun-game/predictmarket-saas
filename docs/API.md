# API Documentation

## Base URL

```
Development: http://localhost:8080
Production: https://api.yourdomain.com
```

## Authentication

All API requests require authentication using Bearer token (API Key).

```http
Authorization: Bearer YOUR_API_KEY
```

## Response Format

### Success Response
```json
{
  "data": {},
  "meta": {
    "timestamp": "2024-07-28T10:00:00Z",
    "request_id": "uuid"
  }
}
```

### Error Response
```json
{
  "error": {
    "code": "INVALID_INPUT",
    "message": "Validation failed",
    "details": []
  },
  "meta": {
    "timestamp": "2024-07-28T10:00:00Z",
    "request_id": "uuid"
  }
}
```

## Endpoints

### Merchants

#### Register Merchant
```http
POST /api/v1/merchants/register
Content-Type: application/json

{
  "name": "Acme Predictions",
  "email": "admin@acme.com",
  "currency": "USD",
  "timezone": "America/New_York"
}
```

**Response 201**
```json
{
  "data": {
    "merchant_id": "550e8400-e29b-41d4-a716-446655440000",
    "api_key": "pk_live_abc123...",
    "api_secret": "sk_live_xyz789..."
  }
}
```

#### Get Merchant Config
```http
GET /api/v1/merchants/{id}/config
Authorization: Bearer YOUR_API_KEY
```

**Response 200**
```json
{
  "data": {
    "id": "uuid",
    "name": "Acme Predictions",
    "currency": "USD",
    "timezone": "America/New_York",
    "status": "active"
  }
}
```

#### Update Merchant Config
```http
PATCH /api/v1/merchants/{id}/config
Authorization: Bearer YOUR_API_KEY
Content-Type: application/json

{
  "name": "Acme International",
  "currency": "EUR",
  "timezone": "Europe/Paris"
}
```

Only the authenticated merchant can read or update its own configuration.

Merchant API keys cannot change merchant status or either fee rate. Both fee
rates are currently fixed at `0`; fee configuration and merchant status changes
will be introduced through a separate administrator capability.

#### List Merchants
```http
GET /api/v1/merchants?page=1&limit=20
Authorization: Bearer ADMIN_API_KEY
```

This endpoint requires the administrator API key configured through
`ADMIN_API_KEY`.

#### Reissue V3 HMAC Secret (Admin Only)

```http
POST /api/v1/merchants/{id}/v3-secret/reissue
Authorization: Bearer ADMIN_API_KEY
```

This is the migration path for merchants created before V3 encrypted secrets
were enabled. The response contains the replacement `api_secret` exactly once.
For seven days, V3 HMAC validation accepts both the preceding and replacement
Secrets; update the merchant integration before the window expires.

### Events

#### List Events
```http
GET /api/v1/events?category=sports&status=active&page=1&limit=20
Authorization: Bearer YOUR_API_KEY
```

**Query Parameters**
- `category` (optional): politics, sports, crypto, entertainment
- `status` (optional): pending, active, closed, resolved
- `page` (optional): Page number (default: 1)
- `limit` (optional): Items per page (default: 20, max: 100)

**Response 200**
```json
{
  "data": [
    {
      "id": "uuid",
      "title": "2024 US Presidential Election",
      "description": "Who will win?",
      "category": "politics",
      "end_time": "2024-11-05T23:59:59Z",
      "resolution_time": "2024-11-06T08:00:00Z",
      "status": "active"
    }
  ],
  "meta": {
    "pagination": {
      "page": 1,
      "limit": 20,
      "total": 150,
      "pages": 8
    }
  }
}
```

#### Get Event Details
```http
GET /api/v1/events/{id}
Authorization: Bearer YOUR_API_KEY
```

#### Create Event (Admin Only)
```http
POST /api/v1/events
Authorization: Bearer ADMIN_API_KEY
Content-Type: application/json

{
  "source_type": "custom",
  "source_id": "merchant-campaign-2026",
  "title": "Will the campaign reach its target?",
  "description": "A custom prediction event.",
  "category": "business",
  "end_time": "2026-12-01T12:00:00Z",
  "resolution_time": "2026-12-01T13:00:00Z"
}
```

#### Update Event Status (Admin Only)
```http
PATCH /api/v1/events/{id}/status
Authorization: Bearer ADMIN_API_KEY
Content-Type: application/json

{
  "status": "active"
}
```

Allowed transitions are `pending → active → closed`. These endpoints return
`204 No Content` after a successful transition.

#### Resolve Event (Admin Only)
```http
POST /api/v1/events/{id}/resolve
Authorization: Bearer ADMIN_API_KEY
Content-Type: application/json

{
  "outcome": "Yes"
}
```

Only a closed event can be resolved. A successful resolution returns
`204 No Content`.

### Currency and Time

All currency endpoints except refresh require merchant authentication.

```http
GET /api/v1/currencies
GET /api/v1/currencies/rate?from=USD&to=EUR
```

```http
POST /api/v1/currencies/convert
Authorization: Bearer YOUR_API_KEY
Content-Type: application/json

{
  "amount": 125.50,
  "from": "USD",
  "to": "CNY"
}
```

Converted amounts are rounded to the nearest minor unit using deterministic
half-up rounding. Supported currencies are USD, EUR, CNY, JPY, GBP, and MXN.

```http
POST /api/v1/currencies/time
Authorization: Bearer YOUR_API_KEY
Content-Type: application/json

{
  "timestamp": "2026-07-28T12:00:00Z",
  "timezone": "Asia/Shanghai"
}
```

Administrators can force a provider refresh with:

```http
POST /api/v1/currencies/refresh
Authorization: Bearer ADMIN_API_KEY
```

A successful refresh returns `204 No Content`. Normal rate reads refresh
automatically when neither Redis nor a fresh PostgreSQL snapshot is available.

### Markets

#### Create Binary Market (Admin Only)

The MVP supports binary markets only. `options` must contain exactly two unique
values. A sell order represents the complementary side of its selected option;
multi-option markets require explicit position accounting and are out of scope
for this release.
```http
POST /api/v1/markets
Authorization: Bearer ADMIN_API_KEY
Content-Type: application/json

{
	"merchant_id": "660e8400-e29b-41d4-a716-446655440000",
  "event_id": "550e8400-e29b-41d4-a716-446655440000",
  "type": "binary",
  "question": "Will Trump win the 2024 election?",
  "options": ["Yes", "No"],
  "liquidity_pool": 10000.00
}
```

**Response 201**
```json
{
  "data": {
    "id": "uuid",
    "merchant_id": "uuid",
    "event_id": "uuid",
    "type": "binary",
    "question": "Will Trump win?",
    "options": ["Yes", "No"],
    "status": "active",
    "liquidity_pool": 10000.00,
    "created_at": "2024-07-28T10:00:00Z"
  }
}
```

#### List Markets
```http
GET /api/v1/markets?status=active&page=1&limit=20
Authorization: Bearer YOUR_API_KEY
```

The authenticated merchant ID is always applied to the query. Supported
filters are `event_id` and `status`; pagination metadata is returned in `meta`.

#### Get Market
```http
GET /api/v1/markets/{id}
Authorization: Bearer YOUR_API_KEY
```

Market details and order books are only visible to the owning merchant.

#### Get Market Order Book
```http
GET /api/v1/markets/{id}/orderbook
Authorization: Bearer YOUR_API_KEY
```

**Response 200**
```json
{
  "data": {
    "market_id": "uuid",
    "bids": [
      {
		"option": "Yes",
        "price": 0.65,
        "amount": 500.00,
        "orders": 3
      }
    ],
    "asks": [
      {
		"option": "Yes",
        "price": 0.68,
        "amount": 300.00,
        "orders": 2
      }
    ]
  }
}
```

The order book contains the unfilled amount of pending and partially filled
orders, grouped by option and price. Bids use descending price priority and
asks use ascending price priority within each option.

#### Update Market Status (Admin Only)
```http
PATCH /api/v1/markets/{id}/status
Authorization: Bearer ADMIN_API_KEY
Content-Type: application/json

{
  "status": "suspended"
}
```

Supported transitions are `active → suspended → active`,
`active|suspended → closed`. A successful update returns `204 No Content`.

#### Add Market Liquidity (Admin Only)
```http
POST /api/v1/markets/{id}/liquidity
Authorization: Bearer ADMIN_API_KEY
Content-Type: application/json

{
  "amount": 1000.00
}
```

Liquidity can only be added to an active market. A successful update returns
`204 No Content`.

Markets are settled only after their event is resolved. Resolving an event
publishes a settlement task; the settlement service atomically records the
winning option, unlocks collateral, credits payouts, and then marks each market
settled. There is deliberately no manual market-settlement endpoint.

### Orders

#### Create Order
```http
POST /api/v1/orders
Authorization: Bearer YOUR_API_KEY
Content-Type: application/json
Idempotency-Key: 6dca17ec-fddd-4c86-aad4-9d24d8045c98

{
  "market_id": "550e8400-e29b-41d4-a716-446655440000",
  "user_id": "merchant_user_123",
  "type": "buy",
  "option": "Yes",
  "amount": 100.00,
  "currency": "USD",
  "price": 0.65,
  "time_in_force": "gtc"
}
```

**Response 201**
```json
{
  "data": {
    "id": "uuid",
    "market_id": "uuid",
    "user_id": "merchant_user_123",
    "type": "buy",
    "option": "Yes",
    "amount": 100.00,
	"filled_amount": 0.00,
    "currency": "USD",
    "status": "pending",
	"price": 0.65,
	"time_in_force": "gtc",
    "created_at": "2024-07-28T10:00:00Z"
  }
}
```

`amount` is a share quantity with up to six decimal places; `price` is the
per-share probability price and must be greater than 0 and less than 1. Cash
amounts (balances, collateral, refunds, and payouts) are rounded half-up to
the nearest cent. `time_in_force` defaults to `gtc`; `ioc` immediately cancels
and unlocks any unmatched remainder. Matching uses best price followed by
earliest creation time, and self-matching is not allowed.

`Idempotency-Key` is required for every order request (1–255 characters). A
retry using the same merchant-scoped key returns the original order and does
not lock additional collateral or create another order.

#### Get Order Status
```http
GET /api/v1/orders/{id}
Authorization: Bearer YOUR_API_KEY
```

#### List Orders
```http
GET /api/v1/orders?user_id=merchant_user_123&market_id={market_id}&status=pending&page=1&limit=20
Authorization: Bearer YOUR_API_KEY
```

All results are scoped to the authenticated merchant. Supported filters are
`user_id`, `market_id`, and `status`.

For deep order history, use keyset pagination instead of `page`/`limit`:

```http
GET /api/v1/orders?user_id=merchant_user_123&limit=100&cursor=
Authorization: Bearer YOUR_API_KEY
```

Send `cursor` (including an empty value on the first request). The response
returns `meta.next_cursor`; pass it unchanged to obtain the next page. Cursor
responses intentionally omit the offset pagination `total`, avoiding a full
table count on large histories. Offset pagination is capped at page 1000.

#### Cancel Order
```http
DELETE /api/v1/orders/{id}
Authorization: Bearer YOUR_API_KEY
```

**Response 200**
```json
{
  "data": {
    "id": "uuid",
    "status": "cancelled"
  }
}
```

Only pending and partially filled orders can be cancelled. The unfilled amount
is returned to the user's available wallet balance.

### Wallets

#### Get Wallet Balance
```http
GET /api/v1/wallets/{user_id}?currency=USD
Authorization: Bearer YOUR_API_KEY
```

If `currency` is omitted, the merchant's configured currency is used. Wallets
are scoped to the merchant identified by the API key.

**Response 200**
```json
{
  "data": {
    "user_id": "merchant_user_123",
    "balances": [
      {
        "currency": "USD",
        "available": 1500.00,
        "locked": 200.00,
        "total": 1700.00
      }
    ]
  }
}
```

#### Credit Wallet
```http
POST /api/v1/wallets/{user_id}/credit
Authorization: Bearer YOUR_API_KEY
Content-Type: application/json
Idempotency-Key: 8abf6ffc-630b-4816-a3d9-e90a64d401c6

{
  "currency": "USD",
  "amount": 1000.00,
  "type": "admin_credit"
}
```

`type` may be omitted or set to `credit`/`admin_credit`; both are recorded as a
`credit` transaction. `Idempotency-Key` is required; retrying the same key for
the same wallet returns its existing balance without adding another credit or
transaction. A successful request returns the updated wallet.

**Response 200**
```json
{
  "data": {
    "id": "uuid",
    "merchant_id": "uuid",
    "user_id": "merchant_user_123",
    "currency": "USD",
    "balance": 2500.00,
    "locked_balance": 200.00,
    "updated_at": "2026-07-28T10:00:00Z"
  }
}
```

#### Get Transaction History
```http
GET /api/v1/wallets/{user_id}/transactions?page=1&limit=50
Authorization: Bearer YOUR_API_KEY
```

Transaction history includes all currencies for the user within the
authenticated merchant and returns pagination metadata in `meta`.

### Sports

#### List Sports Events
```http
GET /api/v1/sports/events?league=wnba&team=Sun&status=active&page=1&limit=20
Authorization: Bearer YOUR_API_KEY
```

Supported filters are `league` (normalized to lowercase), partial `team`
name or abbreviation, and event `status`. Pagination defaults to page 1 and
20 items, with a maximum limit of 100.

```json
{
  "data": [
    {
      "event": {
        "id": "uuid",
        "source_type": "polymarket",
        "source_id": "705118",
        "title": "Connecticut Sun vs. Washington Mystics",
        "category": "sports",
        "status": "active"
      },
      "league": "wnba",
      "game_id": "13002430",
      "start_time": "2026-07-28T23:30:00Z",
      "teams": [
        {"name": "Connecticut Sun", "abbreviation": "conn", "role": "away"},
        {"name": "Washington Mystics", "abbreviation": "wsh", "role": "home"}
      ]
    }
  ],
  "meta": {"pagination": {"page": 1, "limit": 20, "total": 1, "pages": 1}}
}
```

#### Get Sports Event

```http
GET /api/v1/sports/events/{event_id}
Authorization: Bearer YOUR_API_KEY
```

#### Synchronize Sports Events (Admin Only)

```http
POST /api/v1/sports/sync
Authorization: Bearer ADMIN_API_KEY
```

This triggers the same bounded, idempotent synchronization used by the
five-minute background job and returns `204 No Content`. The default league
set is NBA, NFL, MLB, NHL, WNBA, and EPL; deployments can override it with
the comma-separated `SPORTS_LEAGUES` environment variable.

### Analytics

#### Get Merchant Stats
```http
GET /api/v1/analytics/merchant?time_range=7d
Authorization: Bearer YOUR_API_KEY
```

`time_range` accepts `24h`, `7d` (default), `30d`, `90d`, or `all`. The
merchant is always taken from the authenticated API key.

**Response 200**
```json
{
  "data": {
    "total_volume": 50000.00,
    "volume_by_currency": {"USD": 40000.00, "EUR": 10000.00},
    "total_orders": 1250,
    "active_markets": 15,
    "active_users": 320,
    "revenue_from_fee": 0,
    "revenue_by_currency": {}
  }
}
```

Matched volume is counted once per matched pair. Per-currency fields should
be used for financial reporting because the legacy aggregate totals do not
apply exchange-rate conversion. Fee revenue is read only from the separate
income ledger; it is empty while both fee rates are fixed at `0`.

#### Get Market Stats
```http
GET /api/v1/analytics/markets/{id}
Authorization: Bearer YOUR_API_KEY
```

Only markets owned by the authenticated merchant are visible. The response
contains total volume, order and trader counts, up to 200 recent filled-order
price points, and an option distribution normalized to values from 0 to 1.

#### Get User Stats

```http
GET /api/v1/analytics/users/{user_id}
Authorization: Bearer YOUR_API_KEY
```

Returns order count, filled volume, per-currency volume, settled-position win
rate, and realized settlement profit. User IDs are scoped to the authenticated
merchant.

#### Get Platform Stats (Admin Only)

```http
GET /api/v1/analytics/platform?time_range=30d
Authorization: Bearer ADMIN_API_KEY
```

Returns platform-wide merchant, market, order, and matched-volume totals.
Analytics results are cached in Redis for five minutes.

## V2 Merchant Integration

V2 server-to-server endpoints require the API key plus an HMAC signature. The
signature is calculated from the exact raw request body, before JSON parsing:

```text
X-PM-Signature = hex(HMAC-SHA256(api_secret, X-PM-Timestamp + "." + raw_body))
```

See [V3 signing examples](V3_SIGNING_EXAMPLES.md) for Go, Python, and
JavaScript implementations.

`X-PM-Timestamp` is a Unix timestamp and must be within five minutes of the
platform clock. All V2 writes require `Idempotency-Key`. Use a new key for a
new business operation; retain the same `merchant_txn_id` when retrying a
transfer after an unknown network outcome.

### Transfer wallet

The transfer-wallet API keeps the balance on the platform. Transfer amounts are
decimal strings with at most two places, never JSON floating-point numbers.

```http
POST /api/v2/users/site-user-8801/deposits
Authorization: Bearer YOUR_API_KEY
X-PM-Timestamp: 1769836800
X-PM-Signature: <hmac>
Idempotency-Key: 124c4b89-…
Content-Type: application/json

{"merchant_txn_id":"site-deposit-9021","currency":"USD","amount":"25.00"}
```

The response contains a platform transaction ID and an amount string. Retrying
the same merchant transaction with the same details returns that original
transfer. Reusing it with a different user, currency, amount, or direction
returns `409 transfer_conflict` and makes no balance change.

```http
POST /api/v2/users/{user_id}/withdrawals
GET  /api/v2/transfers/{merchant_txn_id}
GET  /api/v2/users/{user_id}/balance?currency=USD
```

Withdrawals are all-or-nothing: insufficient available balance returns `409
insufficient_balance`; no transfer record or debit is written.

### Orders, executions, and reconciliation

`POST /api/v2/orders` and `DELETE /api/v2/orders/{order_id}` provide the
server-side equivalent of the hosted trading UI. `POST` uses the v1 order body
and adds the V2 signature requirements. The following read endpoints all use
newest-first keyset pagination (`cursor`, `limit <= 500`), so clients should
persist `meta.next_cursor` and never use OFFSET for reconciliation:

```http
GET /api/v2/orders?user_id=site-user-8801&cursor=&limit=100
GET /api/v2/trades?from=2026-07-30T00:00:00Z&cursor=&limit=100
GET /api/v2/transactions?user_id=site-user-8801&type=bet&cursor=&limit=100
GET /api/v2/settlements?from=2026-07-30T00:00:00Z&cursor=&limit=100
GET /api/v2/settlements/{market_id}/payouts?cursor=&limit=100
GET /api/v2/reports/daily?date=2026-07-30&currency=USD
```

`/api/v2/transactions` includes both platform wallet ledger rows and seamless
callback transactions. Seamless rows use the platform `transaction_id` as the
stable reconciliation key and expose the callback delivery status. Transaction,
transfer, payout, and report amounts are decimal strings. The daily report is
calculated for the UTC calendar day and contains bets, refunds, payouts, GGR,
fees, and completed transfer totals.

For seamless callback delivery history, query the transaction directly:

```http
GET /api/v2/callbacks/{transaction_id}
```

### Hosted orders

After exchanging a launch token, the browser can trade only as the user bound
to its session:

```http
POST   /api/user/orders
GET    /api/user/orders?cursor=&limit=100
DELETE /api/user/orders/{order_id}
GET    /api/user/orders/{order_id}/trades?cursor=&limit=100
```

Hosted writes also require `Idempotency-Key`; the server supplies the bound
merchant, user, and currency, so those fields are not accepted from the
browser. An order belonging to another hosted user is exposed as `404`.

These write paths support both `transfer` and `seamless` wallet modes. In
seamless mode, order placement performs a synchronous merchant debit and
settlement/refund credits are delivered through the callback outbox. The
merchant callback URL and encrypted callback secret must be configured by an
administrator before seamless orders are accepted.

## Merchant integration hardening

The following administrator-only endpoints manage the production gate for
seamless merchants:

```http
PUT    /api/v1/merchants/{merchant_id}/integration
POST   /api/v1/merchants/{merchant_id}/integration/verify-callback
POST   /api/v1/merchants/{merchant_id}/integration/reset-degraded
POST   /api/v1/merchants/{merchant_id}/v3-secret/reissue
POST   /api/v1/admin/callback-dead-letters/{channel}/{outbox_id}/replay
POST   /api/v1/admin/markets/{market_id}/void
```

- **Callback ownership verification.** `verify-callback` posts a signed
  challenge to the configured `callback_url` and requires the merchant to echo
  it. Until the challenge succeeds, `callback_verified_at` is empty and
  seamless order placement is refused with `409 callback_unverified`.
- **Per-merchant circuit breaker.** After five consecutive callback or webhook
  delivery failures the merchant is marked `seamless_degraded`; seamless order
  placement is refused with `503 merchant_wallet_degraded` and no new debits
  are attempted. A healthy delivery clears the flag automatically, and an
  operator can reset it with `reset-degraded`.
- **IP allow-list (optional).** Setting `allowed_ips` (exact IPs or CIDR) on
  the merchant integration enforces the allow-list on every V2 request; any
  other source IP receives `403 ip_not_allowed`. An empty list disables the
  check.
- **Audit trail.** Every state-changing V2 request is appended to
  `merchant_api_audits` with method, path, request ID, idempotency key, client
  IP, and status code.
- **Layered rate limits.** In addition to the global limit (default 600
  requests/minute, configurable via `GLOBAL_RATE_LIMIT`), V2 writes are
  limited per merchant key (order pool), V2 reads per merchant key (query
  pool), and `/api/user/*` per browser session. The V3 pool defaults are 120
  writes, 600 queries, and 300 session calls per minute and are configurable
  via `V3_ORDER_RATE_LIMIT`, `V3_QUERY_RATE_LIMIT`, and `V3_USER_RATE_LIMIT`.
  Exceeding a layer returns `429 rate_limited`.
- **Real-time seamless balance.** In seamless mode `GET /api/user/me` queries
  the merchant's authoritative balance with a signed `type=balance` callback
  and falls back to the last debit/credit response mirror when the merchant is
  unreachable. The `merchant-sim` counterpart answers the query on `/callback`.

### Voiding a market

`POST /api/v1/admin/markets/{market_id}/void` refunds every order on an
unsettled market in full, marks the market and its orders `voided`, records the
settlement row with `settlement_type = "void"`, and enqueues
`order.voided` / `market.voided` webhooks. In seamless mode the full collateral
is delivered as a `credit` callback with reason `void`. The pull API exposes
the event as `GET /api/v2/settlements` with `settlement_type: "void"` and no
`winning_option`. Voiding an already settled or voided market returns `409
already_settled`.

## Error Codes

| Code | HTTP Status | Description |
|------|-------------|-------------|
| `INVALID_INPUT` | 400 | Request validation failed |
| `UNAUTHORIZED` | 401 | Invalid or missing API key |
| `FORBIDDEN` | 403 | Insufficient permissions |
| `NOT_FOUND` | 404 | Resource not found |
| `CONFLICT` | 409 | Resource conflict |
| `RATE_LIMITED` | 429 | Too many requests |
| `INTERNAL_ERROR` | 500 | Internal server error |
| `SERVICE_UNAVAILABLE` | 503 | Service temporarily unavailable |

## Rate Limits

- **Development**: 100 requests/minute
- **Production**: 1000 requests/minute

Rate limit headers:
```http
X-RateLimit-Limit: 1000
X-RateLimit-Remaining: 999
X-RateLimit-Reset: 1627545600
```

## Pagination

All list endpoints support pagination:

```http
GET /api/v1/events?page=2&limit=50
```

Response includes pagination metadata:
```json
{
  "meta": {
    "pagination": {
      "page": 2,
      "limit": 50,
      "total": 500,
      "pages": 10,
      "has_next": true,
      "has_prev": true
    }
  }
}
```

## Timestamps

All timestamps are in UTC ISO 8601 format:
```
2024-07-28T10:00:00Z
```

## Currency Support

Supported currencies:
- USD (US Dollar)
- EUR (Euro)
- CNY (Chinese Yuan)
- JPY (Japanese Yen)
- GBP (British Pound)
- MXN (Mexican Peso)

## Settlement webhooks

Configured merchants receive at-least-once `order.settled`,
`market.settled`, `order.voided`, and `market.voided` notifications through the
webhook outbox (filtered by the merchant's `webhook_events` mask). Delivery is
signed with the callback secret and can be retried from the administrator
dead-letter replay endpoint. The pull APIs
above remain the source of truth for reconciliation.

Use the [V3 acceptance checklist](V3_ACCEPTANCE_CHECKLIST.md) and
`go run ./cmd/merchant-sim` to exercise the callback contract before requesting
a production seamless-wallet key. The same counterpart is driven automatically
by the platform chaos suite (`internal/callback/seamless_chaos_integration_test.go`,
`INTEGRATION_TEST=1`). In the sandbox, run
`go run ./cmd/sandbox-accelerator -merchant-key <key> -admin-key <key>` to
automatically resolve due events so settlement webhooks, seamless credits, and
reconciliation can be tested end to end.

## SDKs (Future)

Coming soon: Official client libraries for popular languages.
