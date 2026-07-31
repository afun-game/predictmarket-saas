-- V3 HMAC secret rotation retains the prior secret for a short migration window.
-- +goose Up

ALTER TABLE merchants
    ADD COLUMN IF NOT EXISTS api_secret_secondary_enc TEXT,
    ADD COLUMN IF NOT EXISTS api_secret_secondary_expires_at TIMESTAMP;

-- +goose Down

ALTER TABLE merchants
    DROP COLUMN IF EXISTS api_secret_secondary_expires_at,
    DROP COLUMN IF EXISTS api_secret_secondary_enc;
