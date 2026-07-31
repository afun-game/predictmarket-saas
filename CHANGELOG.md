# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added (V3 hardening)
- Per-merchant seamless circuit breaker: five consecutive callback/webhook
  failures mark a merchant degraded, seamless orders are refused, and a
  healthy delivery or the admin `reset-degraded` endpoint clears the flag.
- Callback URL ownership verification (`verify-callback`): a signed challenge
  must be echoed before seamless orders are accepted; changing the callback
  URL invalidates the proof.
- V2 IP allow-list enforcement (`allowed_ips`, exact IPs or CIDR) on all
  signed merchant requests.
- `merchant_api_audits` table records every state-changing V2 request with
  request ID, idempotency key, client IP, and status code.
- Layered rate limits: per-merchant-key pools for V2 writes and reads plus a
  per-session pool for `/api/user/*` (Redis-backed).
- Market void: admin `POST /api/v1/admin/markets/{id}/void` refunds every
  order in full, emits `order.voided`/`market.voided` webhooks, records
  `settlement_type = "void"` in the pull API, and delivers seamless credits
  with reason `void`.
- Migration 015 (`merchants` hardening columns, `market_settlements`
  settlement type, `merchant_api_audits`).
- Real-time seamless balance callback: `/api/user/me` queries the merchant
  balance on demand and falls back to the last callback mirror.
- Sandbox fake settlement accelerator (`cmd/sandbox-accelerator`) resolves due
  events through the admin API for sandbox settlement testing.
- Embedded hosted UI served at `/launch` when the V3 routes are enabled
  (`web/hosted` is compiled into the API binary).
- Configurable rate limits (`GLOBAL_RATE_LIMIT`, `V3_ORDER_RATE_LIMIT`,
  `V3_QUERY_RATE_LIMIT`, `V3_USER_RATE_LIMIT`) for acceptance runs.
- Integration and end-to-end coverage now exercises market void and the
  1,000-order seamless load path (`make test-e2e`).
- Seamless chaos suite (`internal/callback/seamless_chaos_integration_test.go`)
  drives the coordinator through timeout / 5xx / rollback-before-bet /
  duplicate-delivery / dead-letter-replay faults against the merchant
  simulator; `internal/merchantsim` now hosts the simulator logic shared with
  `cmd/merchant-sim` (adds transient `-fail-count` and `-delay-count`
  injection).
- Fix: rollback callbacks for unknown debits no longer reference the
  never-persisted order, so the outbox insert no longer fails the
  `callback_outbox.order_id` foreign key and the rollback is always delivered.

### Added
- Initial project setup with Twill framework
- Core component interfaces:
  - Merchant service for tenant management
  - Event service for prediction event aggregation
  - Market service for market creation and management
  - Order service for order matching and execution
  - Wallet service for virtual credit management
  - Currency service for exchange rate handling
  - Sports service for sports event integration
  - Analytics service for reporting
- Database schema with PostgreSQL migrations
- Polymarket API client for event synchronization
- Docker Compose setup for local development
- Kubernetes deployment, migration Job, deployment guide, and operations runbook
- OpenAPI contract covering all 37 HTTP operations
- Structured JSON/text application logging
- Reproducible vendored Docker build for the private/local Twill dependency
- Goose-backed versioned migrations with a schema version table
- Redis-locked Cron jobs, readiness/liveness probes, graceful shutdown, and panic recovery
- Prometheus-format `/metrics`, request/trace correlation, and OTLP tracing support
- Comprehensive documentation:
  - Requirements document
  - API documentation
  - Getting started guide
  - MVP implementation plan

### Changed
- Replaced Kafka with NATS JetStream for durable business events
- Limited MVP infrastructure to PostgreSQL, Redis, and NATS JetStream
- Exposed Prometheus-format metrics without requiring Prometheus or Grafana in the MVP runtime

### Deprecated
- N/A

### Removed
- N/A

### Fixed
- N/A

### Security
- Merchant API keys are bcrypt-hashed and located by a non-secret prefix

## [0.1.0] - 2024-07-28

### Added
- Initial project scaffold
- Core architecture design
- Component interface definitions
- Database schema design
- Development environment setup

[Unreleased]: https://github.com/afun-game/predictmarket-saas/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/afun-game/predictmarket-saas/releases/tag/v0.1.0
