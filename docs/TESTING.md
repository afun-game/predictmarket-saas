# Testing Guide

## Test layers

The project uses four test layers:

1. Unit and HTTP handler tests run without external dependencies.
2. PostgreSQL, Redis, and NATS integration tests exercise each component.
3. The end-to-end test starts the real application and uses only public HTTP APIs.
4. A k6 scenario measures authenticated read-path latency and failure rate.
5. The V3 merchant counterpart and acceptance checklist exercise callback
   idempotency, rollback ordering, and webhook delivery semantics.

## Commands

```bash
go test -race ./...
go test ./cmd/merchant-sim
make test-integration
make test-e2e
```

For the V3 merchant contract, see
[`V3_ACCEPTANCE_CHECKLIST.md`](V3_ACCEPTANCE_CHECKLIST.md). The counterpart can
be started with `make merchant-sim` and supports deterministic insufficient-fund,
HTTP 5xx, and timeout injection.

Set `V3_ALLOW_PRIVATE_CALLBACK_URLS=1` only for local sandbox runs that point
the seamless callback client at a private TLS endpoint such as `127.0.0.1`.

The GitHub Actions workflow runs the equivalent dependency-aware commands
without starting Docker Compose itself:

```bash
make test-integration-ci
make test-e2e-ci
```

`make test-e2e` keeps PostgreSQL, Redis, and NATS running. It starts the API
binary only for the duration of the test and cleans all generated merchant,
event, market, order, wallet, settlement, transaction, and outbox rows.

The end-to-end flow covers:

- merchant registration and API-key authentication;
- custom event and market creation;
- USD and EUR wallet funding;
- buy/sell order matching;
- event closure and resolution;
- transactional Outbox dispatch through NATS JetStream;
- automatic, idempotent settlement;
- wallet payout and locked-balance verification;
- per-currency settlement audit and pool conservation.

## V3 hardening coverage

The V3 hardening suite is exercised by unit tests that run without external
services:

- `TestV3IPWhitelistRejectsForeignIP` / `TestV3IPWhitelistAllowsMatchingIP` —
  merchant IP allow-list enforcement on signed V2 requests.
- `TestAdminVoidMarketEndpoint` — the market-void admin route.
- `TestWriteSeamlessOrderErrorMapsHardeningErrors` — degraded/unverified HTTP
  mapping.
- `TestDegradedTrackerFlipsAfterThreshold` / `TestDegradedTrackerRecoversOnSuccess`
  — per-merchant circuit breaker.
- `TestDeliverVerification*` — callback ownership challenge echo.
- `TestMemoryLimiter*` / `TestDisabledLimiterAlwaysAllows` — layered rate limits.
- `TestConfigureIntegrationInvalidatesCallbackVerificationOnURLChange` —
  callback URL changes invalidate the ownership proof.

Integration-only tests (PostgreSQL): `TestSettlementPostgresVoidRefundsFullCollateral`,
plus the existing transfer and seamless outbox cases. The full V3 acceptance
gate lives in `docs/V3_ACCEPTANCE_CHECKLIST.md`.

## Seamless chaos suite (Phase 3 acceptance)

`internal/callback/seamless_chaos_integration_test.go` drives the platform
seamless coordinator against the in-process merchant simulator
(`internal/merchantsim`, the same counterpart shipped in `cmd/merchant-sim`)
and requires PostgreSQL (`INTEGRATION_TEST=1`):

- healthy debit places an order with shadow-wallet conservation;
- debit timeout → `ErrDebitUnknown`, rollback enqueued with the same
  transaction ID, delivered, cleanly reversed, duplicate redelivery is a
  no-op;
- transient 5xx → rollback delivered; rollback-before-bet credits once;
- persistent 5xx → dead letter → runbook replay re-delivers the original row;
- insufficient funds → rejected with no rollback row;
- duplicate debit (same idempotency key) → one callback;
- settlement credit delivered and duplicate redelivery acknowledged without a
  second balance change.

The suite caught a real bug: rollbacks for unknown debits referenced the
never-persisted order, failing the `callback_outbox.order_id` foreign key and
silently dropping the rollback. They now carry `order_id = NULL` and are
reconciled by `transaction_id`.

## Share-model regression baseline

`make test-v2-regression` verifies the share-model financial invariants:
a winning share redeems at 1.00, and an order filled below its limit receives
the corresponding price-improvement refund. These focused tests use the
`v2regression` build tag and complement the normal test suite and CI.

## k6 load test

Install k6 locally, start the application, register a test merchant, and run:

```bash
API_KEY=pk_live_example \
BASE_URL=http://localhost:8080 \
make load-test
```

Set `MARKET_ID` to include the order-book endpoint. The scenario ramps to 25
virtual users and requires an error rate below 1% and p95 latency below 250 ms.
It is read-only and does not create orders or modify balances.

### Local baseline

Baseline recorded on 2026-07-28 under WSL using k6 v2.1.0 against the local
single-process application with PostgreSQL and Redis on localhost:

| Metric | Result | Threshold |
|---|---:|---:|
| Maximum virtual users | 25 | 25 |
| HTTP requests | 2,082 | — |
| Request rate | 38.49 req/s | — |
| Failed requests | 0.00% | < 1% |
| Average latency | 5.41 ms | — |
| p90 latency | 10.14 ms | — |
| p95 latency | 12.73 ms | < 250 ms |
| Maximum latency | 117.03 ms | — |

All 2,082 response-status checks passed. This is a local development baseline,
not a production capacity claim; network latency, TLS, multi-replica routing,
and production-sized datasets are not represented.
