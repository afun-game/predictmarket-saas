-- Add bet limits and risk controls to merchants and markets.
-- +goose Up

-- Merchant-level limits (global defaults for all markets under this merchant)
ALTER TABLE merchants ADD COLUMN max_bet_amount DECIMAL(20,2);
ALTER TABLE merchants ADD COLUMN max_user_exposure DECIMAL(20,2);
ALTER TABLE merchants ADD COLUMN max_market_exposure DECIMAL(20,2);

-- Market-level limits (override merchant defaults for specific markets)
ALTER TABLE markets ADD COLUMN max_bet_amount DECIMAL(20,2);
ALTER TABLE markets ADD COLUMN max_total_exposure DECIMAL(20,2);

COMMENT ON COLUMN merchants.max_bet_amount IS 'Maximum single bet amount (NULL = no limit)';
COMMENT ON COLUMN merchants.max_user_exposure IS 'Maximum total exposure per user across all markets (NULL = no limit)';
COMMENT ON COLUMN merchants.max_market_exposure IS 'Maximum total exposure per market (NULL = no limit)';
COMMENT ON COLUMN markets.max_bet_amount IS 'Market-specific max bet (overrides merchant default, NULL = use merchant default)';
COMMENT ON COLUMN markets.max_total_exposure IS 'Market-specific total exposure cap (NULL = no limit)';

-- +goose Down

ALTER TABLE markets DROP COLUMN IF EXISTS max_total_exposure;
ALTER TABLE markets DROP COLUMN IF EXISTS max_bet_amount;
ALTER TABLE merchants DROP COLUMN IF EXISTS max_market_exposure;
ALTER TABLE merchants DROP COLUMN IF EXISTS max_user_exposure;
ALTER TABLE merchants DROP COLUMN IF EXISTS max_bet_amount;
