-- Markets carry their own settlement time, defaulting to the owning event's
-- resolution time at creation so the admin console can display and override
-- it per market. Existing markets are backfilled from their events.
-- +goose Up
ALTER TABLE markets
    ADD COLUMN resolution_time TIMESTAMP;

UPDATE markets m
SET resolution_time = e.resolution_time
FROM events e
WHERE e.id = m.event_id AND m.resolution_time IS NULL;

CREATE INDEX IF NOT EXISTS idx_markets_resolution
    ON markets (resolution_time, status)
    WHERE resolution_time IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_markets_resolution;

ALTER TABLE markets
    DROP COLUMN IF EXISTS resolution_time;
