# Operations Runbook

## Service health

```bash
kubectl -n predictmarket get pods
kubectl -n predictmarket logs deployment/predictmarket-api --since=15m
kubectl -n predictmarket port-forward service/predictmarket-api 8080:80
curl -fsS http://localhost:8080/healthz
curl -fsS http://localhost:8080/readyz
```

Logs are JSON by default and include `service`, `environment`, `request_id`,
and `trace_id` when a trace is active. Set
`LOG_LEVEL` to `debug`, `info`, `warn`, or `error`; set `LOG_FORMAT=text` only
for local troubleshooting. Never log API keys, API secrets, connection URLs,
wallet credentials, or full Authorization headers.

## Dependency failure

### PostgreSQL

Symptoms include startup failure, HTTP 500 responses, and an unhealthy probe.

1. Check database reachability, credentials, TLS, connection limits, and disk.
2. Confirm migrations completed with `kubectl logs job/predictmarket-migrate`.
3. Restore connectivity before restarting the Deployment.
4. Do not manually alter wallet, order, settlement, or Outbox rows.

### Redis

Event, Sports, Currency, and Analytics reads fall back to PostgreSQL or the
external provider where supported. Check Redis latency, memory policy, and
authentication. Cache loss is recoverable; do not restore stale cache data.

### NATS JetStream

Event resolution remains durable in `event_outbox` while NATS is unavailable.
After NATS recovers, the dispatcher republishes pending rows.

```sql
SELECT id, event_id, created_at
FROM event_outbox
WHERE published_at IS NULL
ORDER BY created_at;
```

Check the `PREDICTMARKET_EVENTS` stream and `market-settlement` durable consumer.
Never mark an Outbox row published manually. Settlement is idempotent and may
be retried safely after restoring the stream.

Terminal settlement failures are retained on
`predictmarket.event_resolved.dead_letter`. Inspect the failure reason and its
base64-encoded original payload, repair the event or market data, then replay
the original payload to `predictmarket.event_resolved`. Do not acknowledge or
delete dead-letter messages before the underlying data issue is understood.

## Settlement investigation

For a resolved event, verify the chain in order:

```sql
SELECT id, status, outcome FROM events WHERE id = '<event-id>';
SELECT published_at FROM event_outbox WHERE event_id = '<event-id>';
SELECT id, status, settled_at FROM markets WHERE event_id = '<event-id>';
SELECT * FROM market_settlements WHERE event_id = '<event-id>';
SELECT currency, SUM(stake), SUM(payout)
FROM settlement_payouts
WHERE market_id = '<market-id>'
GROUP BY currency;
```

Per-currency stake and payout totals must be equal. Escalate any imbalance and
preserve database and JSON log evidence before attempting recovery.

## Stranded collateral recovery

The API runs `wallet-lock-reconciliation` every 10 minutes by default. Set
`RECONCILIATION_INTERVAL` to a supported Cron interval when a different cadence
is needed. It only releases a positive `locked_balance` when that wallet has no
pending, partial, or filled order, and records a `reconciliation` transaction
for the recovered amount.

```sql
SELECT t.created_at, w.user_id, t.amount, t.currency
FROM transactions AS t
JOIN wallets AS w ON w.id = t.wallet_id
WHERE t.type = 'reconciliation'
ORDER BY t.created_at DESC;
```

Do not manually unlock wallets that still have an open order. Investigate those
orders and their market settlement state first.

## Backup and restore

- Take automated PostgreSQL backups and test point-in-time recovery regularly.
- Back up NATS JetStream storage according to the provider's procedure.
- Redis is a cache; persistence is helpful locally but not the source of truth.
- Restore into an isolated environment first and run `make test-e2e` against it.

## Key rotation

1. Generate a new administrator key in the secret manager.
2. Update `predictmarket-secrets` and restart the Deployment.
3. Confirm admin endpoints accept only the new key.
4. Merchant API keys are invalidated by merchant deactivation; registration
   secrets are displayed only once.

## Monitoring

Scrape `/metrics` for request volume, latency, 5xx errors, successful orders,
settlement lag, stranded collateral, `predictmarket_dead_letter_size`, and
`predictmarket_shadow_wallet_drift_count`. Alert when the scheduled settlement
audit reports overdue events, a non-empty
`predictmarket.event_resolved.dead_letter` subject, merchant callback dead
letters, or any shadow-wallet drift. Grafana is optional; it is not a runtime
dependency of the MVP.

## Seamless wallet callbacks

Seamless merchants keep the authoritative balance on their side. The platform
keeps a shadow wallet and delivers debit/credit/rollback callbacks from
`callback_outbox`, plus settlement notifications from `webhook_outbox`.

Inspect delivery history for one platform transaction:

```bash
curl -fsS -H "Authorization: Bearer <api_key>" \
  -H "X-PM-Timestamp: <unix>" \
  -H "X-PM-Signature: <hmac>" \
  "https://api.example/api/v2/callbacks/<transaction_id>"
```

```sql
SELECT transaction_id, type, reason, amount, status, callback_response, updated_at
FROM seamless_transactions
WHERE transaction_id = '<transaction_id>';

SELECT id, type, status, attempts, last_error, next_attempt_at, delivered_at
FROM callback_outbox
WHERE transaction_id = '<transaction_id>'
ORDER BY created_at;

SELECT channel, outbox_id, merchant_id, transaction_id, attempts, last_error, created_at, replayed_at
FROM callback_dead_letters
WHERE replayed_at IS NULL
ORDER BY created_at;
```

Shadow conservation: every `kind = 'shadow'` wallet must keep `balance = 0`
after each committed operation. Free balance is reserved into a credit outbox
row in the same database transaction. A non-zero shadow balance is an incident.

### Replay a dead letter

After fixing the merchant endpoint or payload issue, replay one dead-lettered
outbox row. The admin endpoint moves the row back to `pending` and clears
`last_error`; the dispatcher will attempt delivery again with the same
`transaction_id`.

```bash
curl -fsS -X POST \
  -H "Authorization: Bearer <admin_api_key>" \
  "https://api.example/api/v1/admin/callback-dead-letters/callback/<outbox_id>/replay"

curl -fsS -X POST \
  -H "Authorization: Bearer <admin_api_key>" \
  "https://api.example/api/v1/admin/callback-dead-letters/webhook/<outbox_id>/replay"
```

```sql
-- Prefer the admin API above. Manual SQL is last resort and must keep
-- transaction_id unchanged so merchant-side idempotency still holds.
UPDATE callback_outbox
SET status = 'pending', next_attempt_at = NOW(), last_error = NULL, updated_at = NOW()
WHERE id = '<outbox_id>' AND status = 'dead_letter';

UPDATE callback_dead_letters
SET replayed_at = NOW()
WHERE channel = 'callback' AND outbox_id = '<outbox_id>' AND replayed_at IS NULL;
```

Never create a replacement credit with a new `transaction_id` for money the
merchant may already have received. Always replay the original outbox row.

### Seamless merchant degraded

The callback dispatcher marks a merchant `seamless_degraded` after five
consecutive callback/webhook delivery failures. While degraded, seamless order
placement is refused (`503 merchant_wallet_degraded`) so the platform does not
create new rollback risk. The first healthy delivery clears the flag
automatically.

```bash
# Inspect the degraded flag and reason.
curl -fsS -H "Authorization: Bearer <admin_api_key>" \
  "https://api.example/api/v1/merchants/<merchant_id>/config"

SELECT id, seamless_degraded, seamless_degraded_at, seamless_degraded_reason, callback_verified_at
FROM merchants WHERE id = '<merchant_id>';
```

Fix the merchant endpoint (or the integration), then reset the breaker:

```bash
curl -fsS -X POST \
  -H "Authorization: Bearer <admin_api_key>" \
  "https://api.example/api/v1/merchants/<merchant_id>/integration/reset-degraded"
```

After resetting, run `verify-callback` before resuming seamless traffic:

```bash
curl -fsS -X POST \
  -H "Authorization: Bearer <admin_api_key>" \
  "https://api.example/api/v1/merchants/<merchant_id>/integration/verify-callback"
```

### Callback ownership verification

A seamless merchant's callback URL must be verified before orders are
accepted. Verification posts a signed challenge to the configured URL and
requires the exact challenge echoed back; the merchant simulator exposes
`POST /verify` for this purpose. Re-run verification whenever the callback URL
is changed.

### Unknown debit / rollback-before-bet

If a synchronous debit times out, the order is rejected and a rollback outbox
row is enqueued with the same `transaction_id`. Merchants must treat
rollback-before-bet as valid: record the original ID if unseen, then reverse it
when the delayed debit arrives or when the rollback is processed.
