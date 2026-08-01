-- Admin console foundation: administrator accounts, session support, and
-- an immutable trail of administrator actions. Also lifts the fee-rate
-- freeze: merchant fee configuration now exists in the admin console.
-- +goose Up

ALTER TABLE merchants DROP CONSTRAINT IF EXISTS merchants_fee_rate_disabled;

CREATE TABLE IF NOT EXISTS admin_accounts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    username VARCHAR(64) UNIQUE NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    role VARCHAR(32) NOT NULL DEFAULT 'operator',
    status VARCHAR(32) NOT NULL DEFAULT 'active',
    failed_attempts INT NOT NULL DEFAULT 0,
    locked_until TIMESTAMP,
    last_login_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
    CONSTRAINT admin_accounts_role_check CHECK (role IN ('super_admin', 'operator')),
    CONSTRAINT admin_accounts_status_check CHECK (status IN ('active', 'disabled'))
);

CREATE TABLE IF NOT EXISTS admin_action_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    admin_id UUID NOT NULL REFERENCES admin_accounts(id),
    action VARCHAR(64) NOT NULL,
    resource VARCHAR(64) NOT NULL,
    resource_id VARCHAR(255) NOT NULL DEFAULT '',
    before_state JSONB,
    after_state JSONB,
    client_ip VARCHAR(64) NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_admin_action_logs_admin
    ON admin_action_logs(admin_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_admin_action_logs_resource
    ON admin_action_logs(resource, resource_id);
CREATE INDEX IF NOT EXISTS idx_admin_action_logs_created
    ON admin_action_logs(created_at DESC);

DROP TRIGGER IF EXISTS update_admin_accounts_updated_at ON admin_accounts;
CREATE TRIGGER update_admin_accounts_updated_at BEFORE UPDATE ON admin_accounts
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- +goose Down

ALTER TABLE merchants
    ADD CONSTRAINT merchants_fee_rate_disabled CHECK (fee_rate = 0);

DROP TRIGGER IF EXISTS update_admin_accounts_updated_at ON admin_accounts;
DROP TABLE IF EXISTS admin_action_logs;
DROP TABLE IF EXISTS admin_accounts;
