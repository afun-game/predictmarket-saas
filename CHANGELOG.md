# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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
