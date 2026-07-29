-- API keys are bearer credentials. Keep only a bcrypt hash and a short lookup
-- prefix; existing rows are converted in place using the pgcrypto extension.
-- +goose Up
CREATE EXTENSION IF NOT EXISTS pgcrypto;

ALTER TABLE merchants
    ADD COLUMN IF NOT EXISTS api_key_prefix VARCHAR(32);

UPDATE merchants
SET api_key_prefix = LEFT(api_key, 16),
    api_key = crypt(api_key, gen_salt('bf', 12))
WHERE api_key_prefix IS NULL;

ALTER TABLE merchants
    ALTER COLUMN api_key_prefix SET NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_merchants_api_key_prefix
    ON merchants(api_key_prefix);

-- +goose Down
-- Credential hashes are intentionally irreversible. Downgrading this
-- migration would make all bearer keys unusable, so use an application
-- rollback that still understands bcrypt-backed keys instead.
SELECT 1;
