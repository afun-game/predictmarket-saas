-- 002_add_trades.sql
-- Preserve every execution price so collateral and settlement stay auditable.
-- +goose Up

CREATE TABLE IF NOT EXISTS trades (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    market_id UUID NOT NULL REFERENCES markets(id),
    maker_order_id UUID NOT NULL REFERENCES orders(id),
    taker_order_id UUID NOT NULL REFERENCES orders(id),
    shares DECIMAL(28,6) NOT NULL CHECK (shares > 0),
    matched_price DECIMAL(10,6) NOT NULL CHECK (matched_price > 0 AND matched_price < 1),
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    CHECK (maker_order_id <> taker_order_id)
);

CREATE INDEX IF NOT EXISTS idx_trades_market_created
    ON trades(market_id, created_at, id);

CREATE INDEX IF NOT EXISTS idx_trades_maker_order
    ON trades(maker_order_id);

CREATE INDEX IF NOT EXISTS idx_trades_taker_order
    ON trades(taker_order_id);

-- +goose Down
DROP TABLE IF EXISTS trades;
