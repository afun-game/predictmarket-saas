-- +goose Up
-- Keyset read paths and settlement monitoring need covering indexes. The old
-- migration_markers table belonged to psql replay and is now obsolete.
CREATE INDEX IF NOT EXISTS idx_orders_merchant_created_id
    ON orders(merchant_id, created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS idx_events_unresolved_resolution
    ON events(resolution_time, id)
    WHERE status <> 'resolved';

CREATE INDEX IF NOT EXISTS idx_markets_event_unsettled
    ON markets(event_id, id)
    WHERE status <> 'settled';

DROP TABLE IF EXISTS migration_markers;

-- +goose Down
CREATE TABLE IF NOT EXISTS migration_markers (
    name VARCHAR(255) PRIMARY KEY,
    applied_at TIMESTAMP NOT NULL DEFAULT NOW()
);
DROP INDEX IF EXISTS idx_markets_event_unsettled;
DROP INDEX IF EXISTS idx_events_unresolved_resolution;
DROP INDEX IF EXISTS idx_orders_merchant_created_id;
