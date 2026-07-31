-- V3 Launch foundation: merchant credentials usable for HMAC authentication
-- and platform-owned identifiers for merchant users.
-- +goose Up

ALTER TABLE merchants
    ADD COLUMN IF NOT EXISTS wallet_mode VARCHAR(16) NOT NULL DEFAULT 'transfer',
    ADD COLUMN IF NOT EXISTS api_secret_enc TEXT;

ALTER TABLE merchants
    DROP CONSTRAINT IF EXISTS merchants_wallet_mode_check;

ALTER TABLE merchants
    ADD CONSTRAINT merchants_wallet_mode_check
    CHECK (wallet_mode IN ('transfer', 'seamless'));

CREATE TABLE IF NOT EXISTS platform_users (
    merchant_id UUID NOT NULL REFERENCES merchants(id) ON DELETE CASCADE,
    external_user_id VARCHAR(255) NOT NULL,
    locale VARCHAR(35) NOT NULL DEFAULT 'en-US',
    status VARCHAR(32) NOT NULL DEFAULT 'active',
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
    PRIMARY KEY (merchant_id, external_user_id),
    CONSTRAINT platform_users_status_check CHECK (status IN ('active', 'blocked'))
);

CREATE INDEX IF NOT EXISTS idx_platform_users_merchant_status
    ON platform_users(merchant_id, status);

DROP TRIGGER IF EXISTS update_platform_users_updated_at ON platform_users;
CREATE TRIGGER update_platform_users_updated_at BEFORE UPDATE ON platform_users
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- +goose Down

DROP TRIGGER IF EXISTS update_platform_users_updated_at ON platform_users;
DROP TABLE IF EXISTS platform_users;

ALTER TABLE merchants
    DROP CONSTRAINT IF EXISTS merchants_wallet_mode_check;

ALTER TABLE merchants
    DROP COLUMN IF EXISTS api_secret_enc,
    DROP COLUMN IF EXISTS wallet_mode;
