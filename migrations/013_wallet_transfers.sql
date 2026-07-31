-- Merchant-authoritative transfer identifiers make deposits and withdrawals
-- safe to retry after client or network timeouts.
-- +goose Up

CREATE TABLE IF NOT EXISTS wallet_transfers (
    id UUID PRIMARY KEY,
    merchant_id UUID NOT NULL REFERENCES merchants(id),
    merchant_txn_id VARCHAR(255) NOT NULL,
    user_id VARCHAR(255) NOT NULL,
    currency VARCHAR(10) NOT NULL,
    amount DECIMAL(20,2) NOT NULL CHECK (amount > 0),
    direction VARCHAR(16) NOT NULL CHECK (direction IN ('deposit', 'withdrawal')),
    status VARCHAR(16) NOT NULL CHECK (status IN ('pending', 'completed', 'failed')),
    transaction_id UUID REFERENCES transactions(id),
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
    UNIQUE (merchant_id, merchant_txn_id)
);

CREATE INDEX IF NOT EXISTS idx_wallet_transfers_merchant_user_created
    ON wallet_transfers(merchant_id, user_id, created_at DESC, id DESC);

DROP TRIGGER IF EXISTS update_wallet_transfers_updated_at ON wallet_transfers;
CREATE TRIGGER update_wallet_transfers_updated_at BEFORE UPDATE ON wallet_transfers
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- +goose Down

DROP TRIGGER IF EXISTS update_wallet_transfers_updated_at ON wallet_transfers;
DROP TABLE IF EXISTS wallet_transfers;
