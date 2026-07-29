---
name: primary_db
type: sql
description: Primary PostgreSQL database for all application data
---

# Primary Database

## Provider
PostgreSQL 15+

## Lifecycle
External - managed outside the application

## Connection
Set via environment variable: `DATABASE_URL`

Example:
```
DATABASE_URL=postgres://user:password@localhost:5432/predictmarket?sslmode=disable
```

## Schema
Migrations are located in `migrations/` directory.

## Tables
- merchants
- events
- markets
- orders
- wallets
- transactions
- exchange_rates

## Backup
Configure automated backups for production environments.

## Monitoring
Monitor connection pool size, query latency, and slow queries.
