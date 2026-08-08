-- Add localized event title/description for multilingual events.
-- +goose Up

ALTER TABLE events ADD COLUMN translations JSONB NOT NULL DEFAULT '{}'::jsonb;

COMMENT ON COLUMN events.translations IS 'Localized title/description keyed by BCP-47 locale; the default locale lives in title/description';

-- +goose Down

ALTER TABLE events DROP COLUMN IF EXISTS translations;
