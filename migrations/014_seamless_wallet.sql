-- V3 seamless wallet: a merchant-authoritative balance is mirrored by a
-- platform shadow wallet. Callback and webhook records are durable outboxes.
-- +goose Up

ALTER TABLE merchants
    ADD COLUMN IF NOT EXISTS callback_url TEXT,
    ADD COLUMN IF NOT EXISTS callback_secret_enc TEXT,
    ADD COLUMN IF NOT EXISTS webhook_url TEXT,
    ADD COLUMN IF NOT EXISTS webhook_events TEXT[] NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS allowed_ips TEXT[] NOT NULL DEFAULT '{}';

ALTER TABLE wallets
    ADD COLUMN IF NOT EXISTS kind VARCHAR(16) NOT NULL DEFAULT 'user';

ALTER TABLE wallets
    DROP CONSTRAINT IF EXISTS wallets_merchant_id_user_id_currency_key;

ALTER TABLE wallets
    DROP CONSTRAINT IF EXISTS wallets_kind_check;

ALTER TABLE wallets
    ADD CONSTRAINT wallets_kind_check CHECK (kind IN ('user', 'shadow')),
    ADD CONSTRAINT wallets_merchant_id_user_id_currency_kind_key
        UNIQUE (merchant_id, user_id, currency, kind);

ALTER TABLE orders
    ADD COLUMN IF NOT EXISTS wallet_kind VARCHAR(16) NOT NULL DEFAULT 'user',
    ADD COLUMN IF NOT EXISTS channel VARCHAR(16) NOT NULL DEFAULT 'api';

ALTER TABLE orders
    DROP CONSTRAINT IF EXISTS orders_wallet_kind_check;

ALTER TABLE orders
    DROP CONSTRAINT IF EXISTS orders_channel_check;

ALTER TABLE orders
    ADD CONSTRAINT orders_wallet_kind_check CHECK (wallet_kind IN ('user', 'shadow')),
    ADD CONSTRAINT orders_channel_check CHECK (channel IN ('api', 'hosted'));

CREATE INDEX IF NOT EXISTS idx_wallets_shadow_reconciliation
    ON wallets (merchant_id, user_id, currency, id)
    WHERE kind = 'shadow';

CREATE TABLE IF NOT EXISTS seamless_transactions (
    transaction_id UUID PRIMARY KEY,
    merchant_id UUID NOT NULL REFERENCES merchants(id),
    user_id VARCHAR(255) NOT NULL,
    currency VARCHAR(10) NOT NULL,
    type VARCHAR(16) NOT NULL CHECK (type IN ('debit', 'credit')),
    reason VARCHAR(64) NOT NULL,
    amount DECIMAL(20,2) NOT NULL CHECK (amount > 0),
    order_id UUID REFERENCES orders(id),
    original_transaction_id UUID,
    status VARCHAR(32) NOT NULL CHECK (status IN (
        'created', 'accepted', 'rejected', 'unknown', 'rolled_back', 'pending_delivery', 'delivered', 'dead_letter'
    )),
    callback_response JSONB,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_seamless_transactions_merchant_user_created
    ON seamless_transactions (merchant_id, user_id, created_at DESC, transaction_id DESC);

CREATE INDEX IF NOT EXISTS idx_seamless_transactions_status
    ON seamless_transactions (status, created_at, transaction_id);

DROP TRIGGER IF EXISTS update_seamless_transactions_updated_at ON seamless_transactions;
CREATE TRIGGER update_seamless_transactions_updated_at BEFORE UPDATE ON seamless_transactions
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TABLE IF NOT EXISTS callback_outbox (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id UUID NOT NULL REFERENCES merchants(id),
    transaction_id UUID NOT NULL,
    original_transaction_id UUID,
    user_id VARCHAR(255) NOT NULL,
    currency VARCHAR(10) NOT NULL,
    type VARCHAR(16) NOT NULL CHECK (type IN ('credit', 'rollback')),
    reason VARCHAR(64) NOT NULL,
    amount DECIMAL(20,2) NOT NULL CHECK (amount > 0),
    order_id UUID REFERENCES orders(id),
    market_id UUID REFERENCES markets(id),
    event_id UUID REFERENCES events(id),
    status VARCHAR(32) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'delivered', 'dead_letter')),
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at TIMESTAMP NOT NULL DEFAULT NOW(),
    last_error TEXT,
    response JSONB,
    delivered_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_callback_outbox_pending
    ON callback_outbox (next_attempt_at, created_at, id) WHERE status = 'pending';

CREATE INDEX IF NOT EXISTS idx_callback_outbox_merchant_transaction
    ON callback_outbox (merchant_id, transaction_id, created_at DESC, id DESC);

DROP TRIGGER IF EXISTS update_callback_outbox_updated_at ON callback_outbox;
CREATE TRIGGER update_callback_outbox_updated_at BEFORE UPDATE ON callback_outbox
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TABLE IF NOT EXISTS webhook_outbox (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id UUID NOT NULL REFERENCES merchants(id),
    event_type VARCHAR(64) NOT NULL,
    payload JSONB NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'delivered', 'dead_letter')),
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at TIMESTAMP NOT NULL DEFAULT NOW(),
    last_error TEXT,
    response JSONB,
    delivered_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_webhook_outbox_pending
    ON webhook_outbox (next_attempt_at, created_at, id) WHERE status = 'pending';

DROP TRIGGER IF EXISTS update_webhook_outbox_updated_at ON webhook_outbox;
CREATE TRIGGER update_webhook_outbox_updated_at BEFORE UPDATE ON webhook_outbox
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TABLE IF NOT EXISTS callback_dead_letters (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    channel VARCHAR(16) NOT NULL CHECK (channel IN ('callback', 'webhook')),
    outbox_id UUID NOT NULL,
    merchant_id UUID NOT NULL REFERENCES merchants(id),
    transaction_id UUID,
    payload JSONB NOT NULL,
    attempts INTEGER NOT NULL,
    last_error TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    replayed_at TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_callback_dead_letters_channel_outbox
    ON callback_dead_letters (channel, outbox_id);

-- +goose Down

DROP TABLE IF EXISTS callback_dead_letters;

DROP TRIGGER IF EXISTS update_webhook_outbox_updated_at ON webhook_outbox;
DROP TABLE IF EXISTS webhook_outbox;

DROP TRIGGER IF EXISTS update_callback_outbox_updated_at ON callback_outbox;
DROP TABLE IF EXISTS callback_outbox;

DROP TRIGGER IF EXISTS update_seamless_transactions_updated_at ON seamless_transactions;
DROP TABLE IF EXISTS seamless_transactions;

DROP INDEX IF EXISTS idx_wallets_shadow_reconciliation;

ALTER TABLE orders
    DROP CONSTRAINT IF EXISTS orders_channel_check,
    DROP CONSTRAINT IF EXISTS orders_wallet_kind_check,
    DROP COLUMN IF EXISTS channel,
    DROP COLUMN IF EXISTS wallet_kind;

ALTER TABLE wallets
    DROP CONSTRAINT IF EXISTS wallets_merchant_id_user_id_currency_kind_key,
    DROP CONSTRAINT IF EXISTS wallets_kind_check,
    ADD CONSTRAINT wallets_merchant_id_user_id_currency_key UNIQUE (merchant_id, user_id, currency),
    DROP COLUMN IF EXISTS kind;

ALTER TABLE merchants
    DROP COLUMN IF EXISTS allowed_ips,
    DROP COLUMN IF EXISTS webhook_events,
    DROP COLUMN IF EXISTS webhook_url,
    DROP COLUMN IF EXISTS callback_secret_enc,
    DROP COLUMN IF EXISTS callback_url;
