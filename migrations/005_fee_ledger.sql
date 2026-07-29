-- Prepare immutable market-level fee terms and an income ledger. Fees are
-- deliberately disabled until an administrator configuration capability exists.
-- +goose Up

ALTER TABLE markets
    ADD COLUMN IF NOT EXISTS merchant_fee_rate DECIMAL(10,6) NOT NULL DEFAULT 0
        CHECK (merchant_fee_rate >= 0 AND merchant_fee_rate <= 1),
    ADD COLUMN IF NOT EXISTS platform_fee_rate DECIMAL(10,6) NOT NULL DEFAULT 0
        CHECK (platform_fee_rate >= 0 AND platform_fee_rate <= 1);

CREATE TABLE IF NOT EXISTS fee_ledger (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    market_id UUID NOT NULL REFERENCES markets(id),
    merchant_id UUID NOT NULL REFERENCES merchants(id),
    currency VARCHAR(10) NOT NULL,
    recipient VARCHAR(20) NOT NULL CHECK (recipient IN ('merchant', 'platform')),
    rate DECIMAL(10,6) NOT NULL CHECK (rate >= 0 AND rate <= 1),
    amount DECIMAL(20,2) NOT NULL CHECK (amount > 0),
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    UNIQUE (market_id, currency, recipient)
);

CREATE INDEX IF NOT EXISTS idx_fee_ledger_merchant_created
    ON fee_ledger(merchant_id, created_at DESC);

-- +goose StatementBegin
DO $$
BEGIN
    UPDATE merchants SET fee_rate = 0;
    UPDATE markets
    SET merchant_fee_rate = 0,
        platform_fee_rate = 0;

    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'merchants_fee_rate_disabled') THEN
        ALTER TABLE merchants
            ADD CONSTRAINT merchants_fee_rate_disabled CHECK (fee_rate = 0);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'markets_merchant_fee_rate_disabled') THEN
        ALTER TABLE markets
            ADD CONSTRAINT markets_merchant_fee_rate_disabled CHECK (merchant_fee_rate = 0);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'markets_platform_fee_rate_disabled') THEN
        ALTER TABLE markets
            ADD CONSTRAINT markets_platform_fee_rate_disabled CHECK (platform_fee_rate = 0);
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
ALTER TABLE markets DROP CONSTRAINT IF EXISTS markets_platform_fee_rate_disabled;
ALTER TABLE markets DROP CONSTRAINT IF EXISTS markets_merchant_fee_rate_disabled;
ALTER TABLE merchants DROP CONSTRAINT IF EXISTS merchants_fee_rate_disabled;
DROP TABLE IF EXISTS fee_ledger;
ALTER TABLE markets DROP COLUMN IF EXISTS platform_fee_rate;
ALTER TABLE markets DROP COLUMN IF EXISTS merchant_fee_rate;
