-- Enable fee collection by removing the CHECK constraints that force zero rates.
-- +goose Up

ALTER TABLE merchants DROP CONSTRAINT IF EXISTS merchants_fee_rate_disabled;
ALTER TABLE markets DROP CONSTRAINT IF EXISTS markets_merchant_fee_rate_disabled;
ALTER TABLE markets DROP CONSTRAINT IF EXISTS markets_platform_fee_rate_disabled;

-- +goose Down

-- Restore the constraints that disable fees (for rollback only).
ALTER TABLE merchants ADD CONSTRAINT merchants_fee_rate_disabled CHECK (fee_rate = 0);
ALTER TABLE markets ADD CONSTRAINT markets_merchant_fee_rate_disabled CHECK (merchant_fee_rate = 0);
ALTER TABLE markets ADD CONSTRAINT markets_platform_fee_rate_disabled CHECK (platform_fee_rate = 0);
