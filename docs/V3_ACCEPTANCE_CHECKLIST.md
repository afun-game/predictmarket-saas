# V3 merchant acceptance checklist

This checklist is the production-key gate for a merchant integration. Run it
against a sandbox database and the local counterpart below before enabling a
seamless merchant.

Start the isolated PostgreSQL sandbox with `make sandbox-db-up` and apply the
schema with `make sandbox-db-migrate`. The settlement accelerator ships as `cmd/sandbox-accelerator` and the
hosted-browser page is embedded into the API at `GET /launch`; point the
acceptance environment at them.

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
`-fail-status insufficient_funds`, `-fail-http-status 503`, `-fail-count 1`
(transient 5xx), `-delay 4s`, or `-delay-count 1` (transient timeout) to
inject callback failures.

For local seamless load tests against `127.0.0.1`, start the API with
`V3_ALLOW_PRIVATE_CALLBACK_URLS=1`.

## Required cases

- [x] API request signature accepts the primary secret and rejects an altered
      body, stale timestamp, missing key, and reused state-changing nonce.
      Evidence: `internal/v2auth/authenticator_test.go`, `internal/session/session_test.go`.
- [x] Launch token is single-use, expires after 60 seconds, and cannot cross
      merchant or user tenants.
      Evidence: `internal/session/session_test.go`, `tests/e2e` `TestHostedLaunchFlow`.
- [x] Transfer deposit/withdrawal retries return the original terminal transfer
      and conflicting merchant transaction IDs do not change the balance.
      Evidence: `internal/wallet/wallet_test.go`, `internal/wallet/integration_test.go`.
- [x] Seamless debit is idempotent by `transaction_id`; insufficient funds,
      blocked users, timeout, and HTTP 5xx have the documented result.
      Evidence: `internal/callback/seamless_chaos_integration_test.go`
      (`TestSeamlessChaosDuplicateDebitIdempotent`, `...DebitInsufficientFundsRejects`,
      `...DebitTimeoutEnqueuesRollback`, `...Debit5xxTransientRollbackDelivered`).
- [x] Credit and rollback retries preserve the same transaction ID; duplicate
      delivery is acknowledged without a second balance change.
      Evidence: `...DebitTimeoutEnqueuesRollback` and `...CreditOutboxDeliveredAndDuplicateAcknowledged`
      (same transaction_id across retries, duplicate redelivery returns `duplicate`).
- [x] Rollback-before-bet is accepted and a delayed debit with that ID is a
      duplicate.
      Evidence: `internal/merchantsim/simulator_test.go` `TestRollbackBeforeBet`,
      `...Debit5xxTransientRollbackDelivered` (balance credited once, ledger
      recorded rolledBack).
- [x] Settlement webhook and credit callback are both delivered; webhook
      retries preserve `webhook_id`, and the configured event mask is honored.
      Evidence: `internal/settlement` integration (webhook outbox rows),
      `...CreditOutboxDeliveredAndDuplicateAcknowledged`, `tests/e2e` void flow.
- [x] Dead-letter replay moves the original outbox row back to pending without
      creating a replacement transaction ID.
      Evidence: `...Debit5xxPersistentGoesToDeadLetterAndReplays`.
- [x] `/api/v2/transactions`, `/api/v2/callbacks/{transaction_id}`, settlement
      pull APIs, and the daily report reconcile against the merchant ledger.
      Evidence: `internal/v2query` integration, `tests/e2e` `TestHostedLaunchFlow`
      and void flow.
- [x] A concurrent load of at least 1,000 orders leaves the shadow-wallet drift
      metric at zero and no pending callback without a corresponding outbox row.
      Evidence: `tests/e2e` `TestSeamlessConcurrentOrderLoad` (1,000 orders, drift
      metric zero, no pending callback/webhook/transaction).
- [x] Callback ownership verification rejects a URL that does not echo the
      challenge, and seamless orders are refused until verification succeeds.
      Evidence: `internal/callback/client_test.go` `TestDeliverVerification*`,
      `internal/httpapi/v3_hardening_test.go` error mapping.
- [x] Five consecutive callback failures mark the merchant degraded, seamless
      orders return `503 merchant_wallet_degraded`, and a healthy delivery or
      the reset-degraded admin endpoint clears the flag.
      Evidence: `internal/callback/degraded_test.go`,
      `internal/httpapi/v3_hardening_test.go`.
- [x] Configuring `allowed_ips` rejects V2 requests from other source IPs and
      permits matching IPs/CIDRs; an empty list leaves the API open.
      Evidence: `internal/httpapi/v3_hardening_test.go`.
- [x] Market void refunds every order in full, emits `order.voided` /
      `market.voided` webhooks, records `settlement_type = "void"` in
      `/api/v2/settlements`, and rejects a second void with `409`.
      Evidence: `internal/settlement` `TestSettlementPostgresVoidRefundsFullCollateral`,
      `tests/e2e` `TestVoidMarketRefundsAndWebhooks`.
- [x] State-changing V2 requests appear in `merchant_api_audits` with request
      ID, idempotency key, client IP, and status code.
      Evidence: `internal/audit/audit_test.go`; audit middleware exercised by
      `tests/e2e` V2 requests (table checked in CI runs).
- [x] Layered rate limits: exceeding the V2 order/query pools or the
      `/api/user/*` per-session pool returns `429 rate_limited`.
      Evidence: `internal/ratelimit/ratelimit_test.go`; `429` behavior observed in
      `TestSeamlessConcurrentOrderLoad` before the limits were raised for runs.
- [x] In seamless mode `GET /api/user/me` reflects the merchant's
      `type=balance` callback answer (and falls back to the callback mirror
      when the merchant times out).
      Evidence: `internal/callback/client_test.go` `TestDeliverBalanceQuery*`,
      `internal/merchantsim/simulator_test.go` `TestBalanceQuery`.
- [ ] The sandbox settlement accelerator resolves due events and settlement
      webhooks / seamless credits arrive for every resolved market.

Record request IDs, callback IDs, transaction IDs, webhook IDs, and the final
reconciliation report with the merchant's release evidence.

## Local verification record (2026-07-31)

Automated evidence collected in this repository against local PostgreSQL 15,
Redis 7, and NATS (Docker Desktop):

- `go test -race -count=1 ./...` — all packages pass.
- `make test-integration-ci` — wallet transfers, V2 query suite, order,
  settlement (incl. market void), reconciliation, settlement worker, currency,
  sports, analytics, and the seamless chaos suite pass against real services.
- Chaos suite (`internal/callback/seamless_chaos_integration_test.go`,
  `INTEGRATION_TEST=1`): healthy debit + shadow conservation; debit timeout →
  unknown → rollback enqueued with the same transaction ID → delivered and
  cleanly reversed; 5xx transient → rollback-before-bet credited once; 5xx
  persistent → dead-letter → runbook replay re-delivers the original row;
  insufficient funds → rejected with no rollback; duplicate debit idempotent
  (one callback); settlement credit delivered and duplicate redelivery
  acknowledged without a second balance change.
- `make test-e2e` (with `V3_ALLOW_PRIVATE_CALLBACK_URLS=1`) —
  `TestFullTradingSettlementFlow`, `TestHostedLaunchFlow`,
  `TestVoidMarketRefundsAndWebhooks`, and the 1,000-order seamless load test
  (shadow-wallet drift zero, merchant balance exact) all pass.
- Embedded hosted UI smoke-tested at `GET /launch` (index/app.js/styles.css).
- Dev-environment real flow (`https://market.afx-game.dev`, V3 enabled): via
  `cmd/merchant-portal` and curl — register merchant → create demo event +
  market (admin) → `POST /api/v2/sessions` launch_url → exchange → `/api/user/me`
  → events/markets → fund wallet (v1 credit 100.00) → hosted order placed
  (locked 5.00, available 95.00) → my orders → orderbook. All passed.
