-- Make retryable order placement and wallet credits safe to repeat.
-- +goose Up

ALTER TABLE orders
    ADD COLUMN IF NOT EXISTS idempotency_key VARCHAR(255);

CREATE UNIQUE INDEX IF NOT EXISTS idx_orders_merchant_idempotency
    ON orders(merchant_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;

ALTER TABLE transactions
    ADD COLUMN IF NOT EXISTS idempotency_key VARCHAR(255);

CREATE UNIQUE INDEX IF NOT EXISTS idx_transactions_wallet_idempotency
    ON transactions(wallet_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_transactions_wallet_idempotency;
ALTER TABLE transactions DROP COLUMN IF EXISTS idempotency_key;
DROP INDEX IF EXISTS idx_orders_merchant_idempotency;
ALTER TABLE orders DROP COLUMN IF EXISTS idempotency_key;
