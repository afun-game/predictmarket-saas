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
half-up rounding. Supported currencies are USD, EUR, CNY, JPY, and GBP.

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

## Webhooks (Future)

Coming soon: Real-time event notifications via webhooks.

## SDKs (Future)

Coming soon: Official client libraries for popular languages.
