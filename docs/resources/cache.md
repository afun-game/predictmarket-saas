---
name: cache
type: redis
description: Redis cache for sessions, exchange rates, and temporary data
---

# Redis Cache

## Provider
Redis 7+

## Lifecycle
External - managed outside the application

## Connection
Set via environment variable: `REDIS_URL`

Example:
```
REDIS_URL=redis://localhost:6379/0
```

## Usage

### Events
- Detail key pattern: `predictmarket:events:v1:detail:{event_id}`
- Detail TTL: 5 minutes
- List key pattern: `predictmarket:events:v1:list:{filter_hash}`
- List TTL: 1 minute
- Mutations rotate `predictmarket:events:v1:list-version` to invalidate all list variants
- Cache failures fall back to PostgreSQL

### Exchange Rates
- Key pattern: `rate:{from}:{to}`
- TTL: 1 hour
- Format: float64 as string

### Sessions
- Key pattern: `session:{token}`
- TTL: 24 hours

### Market Data
- Key pattern: `market:{id}:orderbook`
- TTL: 10 seconds

## Monitoring
Monitor memory usage, hit rate, and connection count.
