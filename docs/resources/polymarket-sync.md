---
name: polymarket-sync
type: cron
description: Periodically synchronizes Polymarket event metadata and definite outcomes
---

# Polymarket Event Sync

## Local schedule

Configure the interval with `POLYMARKET_SYNC_INTERVAL`; the default is
`@every 5m`. Production runs acquire a Redis lease before each task, so a
replica set schedules only one execution per interval.

## Idempotency

Events are upserted by `(source_type, source_id)`. Repeated runs refresh source
metadata without duplicating rows. Closed and resolved local events cannot be
reopened by a later synchronization.

Each run imports both open and closed events in two bounded pages per status.
For closed source events, it resolves the local event only when a binary source
market has exactly one outcome priced at `1` and the other at `0`. Conflicting,
non-binary, or indeterminate outcomes remain closed and are surfaced by the
settlement safety audit rather than being paid incorrectly.

## Production

The synchronization remains safe to retry, while the Redis lock avoids
duplicate upstream traffic. Manual resolution remains a terminal override and
the resolution source is retained in `event_resolution_audits`.
