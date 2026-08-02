-- Market maker committed funds and parimutuel pool markets.
-- +goose Up

-- Allow the platform market maker channel and parimutuel settlement types.
ALTER TABLE orders
    DROP CONSTRAINT IF EXISTS orders_channel_check;

ALTER TABLE orders
    ADD CONSTRAINT orders_channel_check CHECK (channel IN ('api', 'hosted', 'mm'));

ALTER TABLE market_settlements
    DROP CONSTRAINT IF EXISTS market_settlements_settlement_type_check;

ALTER TABLE market_settlements
    ADD CONSTRAINT market_settlements_settlement_type_check
    CHECK (settlement_type IN ('settle', 'void', 'parimutuel', 'refund'));

-- The platform's committed market-making capital per binary market. The
-- worker credits the difference whenever liquidity_pool exceeds the committed
-- amount, so top-ups (admin add-liquidity) fund the maker wallet exactly once.
CREATE TABLE IF NOT EXISTS marketmaker_funds (
    market_id UUID PRIMARY KEY REFERENCES markets(id),
    committed DECIMAL(20,2) NOT NULL DEFAULT 0.00,
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

-- Parimutuel markets (markets.type = 'parimutuel') pool stakes per option.
CREATE TABLE IF NOT EXISTS parimutuel_pools (
    market_id UUID PRIMARY KEY REFERENCES markets(id),
    currency VARCHAR(10) NOT NULL,
    total_stake DECIMAL(20,2) NOT NULL DEFAULT 0.00,
    total_fees DECIMAL(20,2) NOT NULL DEFAULT 0.00,
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

-- One row per parimutuel bet. Stakes are deducted from the user wallet at
-- placement (type 'bet') and returned at settlement ('payout' for winners,
-- 'bet_refund' for voids/refunds).
CREATE TABLE IF NOT EXISTS parimutuel_bets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    market_id UUID NOT NULL REFERENCES markets(id),
    merchant_id UUID NOT NULL REFERENCES merchants(id),
    user_id VARCHAR(255) NOT NULL,
    option VARCHAR(255) NOT NULL,
    stake DECIMAL(20,2) NOT NULL,
    currency VARCHAR(10) NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'active',
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    settled_at TIMESTAMP,
    CONSTRAINT parimutuel_bets_status_check
        CHECK (status IN ('active', 'settled', 'voided'))
);

CREATE INDEX IF NOT EXISTS idx_parimutuel_bets_market
    ON parimutuel_bets(market_id, status);
CREATE INDEX IF NOT EXISTS idx_parimutuel_bets_user
    ON parimutuel_bets(merchant_id, user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_parimutuel_bets_created
    ON parimutuel_bets(created_at DESC);

-- +goose Down

ALTER TABLE orders
    DROP CONSTRAINT IF EXISTS orders_channel_check;

ALTER TABLE orders
    ADD CONSTRAINT orders_channel_check CHECK (channel IN ('api', 'hosted'));

ALTER TABLE market_settlements
    DROP CONSTRAINT IF EXISTS market_settlements_settlement_type_check;

ALTER TABLE market_settlements
    ADD CONSTRAINT market_settlements_settlement_type_check
    CHECK (settlement_type IN ('settle', 'void'));

DROP TABLE IF EXISTS parimutuel_bets;
DROP TABLE IF EXISTS parimutuel_pools;
DROP TABLE IF EXISTS marketmaker_funds;
