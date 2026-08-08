-- Add localized market question/options for multilingual market creation.
-- +goose Up

ALTER TABLE markets ADD COLUMN translations JSONB NOT NULL DEFAULT '{}'::jsonb;

COMMENT ON COLUMN markets.translations IS 'Localized question/options keyed by BCP-47 locale; the default locale lives in question/options';

-- +goose Down

ALTER TABLE markets DROP COLUMN IF EXISTS translations;
