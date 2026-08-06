-- Markets get their own category so list pages can render and filter by
-- market category without joining events. Empty means "inherit the event's
-- category"; creation fills it from the event when not provided.
-- +goose Up
ALTER TABLE markets
    ADD COLUMN category VARCHAR(100) NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_markets_category
    ON markets (category, status)
    WHERE category <> '';

-- +goose Down
DROP INDEX IF EXISTS idx_markets_category;

ALTER TABLE markets
    DROP COLUMN IF EXISTS category;
