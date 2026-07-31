# V3 merchant acceptance checklist

This checklist is the production-key gate for a merchant integration. Run it
against a sandbox database and the local counterpart below before enabling a
seamless merchant.

Start the isolated PostgreSQL sandbox with `make sandbox-db-up` and apply the
schema with `make sandbox-db-migrate`. The production-like settlement
accelerator and hosted-browser deployment still need to be supplied by the
environment running the acceptance test.

## Local counterpart

```bash
go run ./cmd/merchant-sim \
  -addr :8090 \
  -secret "$CALLBACK_SECRET" \
  -merchant-id "$MERCHANT_ID" \
  -initial-balance 100.00 \
  -fail-http-status 503
```

Configure the merchant callback URL as `https://<public-tunnel>/callback` and
webhook URL as `https://<public-tunnel>/webhook`. The simulator also exposes
`GET /healthz` and `GET /balance?user_id=<user>` for smoke checks. Use
`-fail-status insufficient_funds`, `-fail-http-status 503`, or `-delay 4s` to
inject callback failures.

For local seamless load tests against `127.0.0.1`, start the API with
`V3_ALLOW_PRIVATE_CALLBACK_URLS=1`.

## Required cases

- [ ] API request signature accepts the primary secret and rejects an altered
      body, stale timestamp, missing key, and reused state-changing nonce.
- [ ] Launch token is single-use, expires after 60 seconds, and cannot cross
      merchant or user tenants.
- [ ] Transfer deposit/withdrawal retries return the original terminal transfer
      and conflicting merchant transaction IDs do not change the balance.
- [ ] Seamless debit is idempotent by `transaction_id`; insufficient funds,
      blocked users, timeout, and HTTP 5xx have the documented result.
- [ ] Credit and rollback retries preserve the same transaction ID; duplicate
      delivery is acknowledged without a second balance change.
- [ ] Rollback-before-bet is accepted and a delayed debit with that ID is a
      duplicate.
- [ ] Settlement webhook and credit callback are both delivered; webhook
      retries preserve `webhook_id`, and the configured event mask is honored.
- [ ] Dead-letter replay moves the original outbox row back to pending without
      creating a replacement transaction ID.
- [ ] `/api/v2/transactions`, `/api/v2/callbacks/{transaction_id}`, settlement
      pull APIs, and the daily report reconcile against the merchant ledger.
- [ ] A concurrent load of at least 1,000 orders leaves the shadow-wallet drift
      metric at zero and no pending callback without a corresponding outbox row.
- [ ] Callback ownership verification rejects a URL that does not echo the
      challenge, and seamless orders are refused until verification succeeds.
- [ ] Five consecutive callback failures mark the merchant degraded, seamless
      orders return `503 merchant_wallet_degraded`, and a healthy delivery or
      the reset-degraded admin endpoint clears the flag.
- [ ] Configuring `allowed_ips` rejects V2 requests from other source IPs and
      permits matching IPs/CIDRs; an empty list leaves the API open.
- [ ] Market void refunds every order in full, emits `order.voided` /
      `market.voided` webhooks, records `settlement_type = "void"` in
      `/api/v2/settlements`, and rejects a second void with `409`.
- [ ] State-changing V2 requests appear in `merchant_api_audits` with request
      ID, idempotency key, client IP, and status code.
- [ ] Layered rate limits: exceeding the V2 order/query pools or the
      `/api/user/*` per-session pool returns `429 rate_limited`.
- [ ] In seamless mode `GET /api/user/me` reflects the merchant's
      `type=balance` callback answer (and falls back to the callback mirror
      when the merchant times out).
- [ ] The sandbox settlement accelerator resolves due events and settlement
      webhooks / seamless credits arrive for every resolved market.

Record request IDs, callback IDs, transaction IDs, webhook IDs, and the final
reconciliation report with the merchant's release evidence.

## Local verification record (2026-07-31)

Automated evidence collected in this repository against local PostgreSQL 15,
Redis 7, and NATS (Docker Desktop):

- `go test -race -count=1 ./...` — all 29 packages pass.
- `make test-integration-ci` — wallet transfers, V2 query suite, order,
  settlement (incl. market void), reconciliation, settlement worker, currency,
  sports, analytics pass against real services.
- `make test-e2e` (with `V3_ALLOW_PRIVATE_CALLBACK_URLS=1`) —
  `TestFullTradingSettlementFlow`, `TestHostedLaunchFlow`,
  `TestVoidMarketRefundsAndWebhooks`, and the 1,000-order seamless load test
  (shadow-wallet drift zero, merchant balance exact) all pass.
- Embedded hosted UI smoke-tested at `GET /launch` (index/app.js/styles.css).
