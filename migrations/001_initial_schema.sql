-- 001_initial_schema.sql
-- Initial database schema for PredictMarket SaaS
-- +goose Up

-- Merchants table
CREATE TABLE IF NOT EXISTS merchants (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL,
    email VARCHAR(320) NOT NULL,
    api_key VARCHAR(255) UNIQUE NOT NULL,
    api_secret VARCHAR(255) NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'active',
    currency VARCHAR(10) NOT NULL DEFAULT 'USD',
    timezone VARCHAR(100) NOT NULL DEFAULT 'UTC',
    fee_rate DECIMAL(5,4) NOT NULL DEFAULT 0.0000,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_merchants_api_key ON merchants(api_key);
CREATE INDEX IF NOT EXISTS idx_merchants_status ON merchants(status);

-- Events table
CREATE TABLE IF NOT EXISTS events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source_type VARCHAR(50) NOT NULL,
    source_id VARCHAR(255) NOT NULL,
    title VARCHAR(500) NOT NULL,
    description TEXT,
    category VARCHAR(100) NOT NULL,
    end_time TIMESTAMP NOT NULL,
    resolution_time TIMESTAMP NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'pending',
    outcome VARCHAR(255),
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
    UNIQUE(source_type, source_id)
);

CREATE INDEX IF NOT EXISTS idx_events_category ON events(category);
CREATE INDEX IF NOT EXISTS idx_events_status ON events(status);
CREATE INDEX IF NOT EXISTS idx_events_end_time ON events(end_time);
CREATE INDEX IF NOT EXISTS idx_events_source ON events(source_type, source_id);

-- Normalized sports metadata augments source events without duplicating them.
CREATE TABLE IF NOT EXISTS sports_events (
    event_id UUID PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,
    league VARCHAR(100) NOT NULL,
    game_id VARCHAR(100),
    start_time TIMESTAMP,
    synced_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_sports_events_league ON sports_events(league);
CREATE INDEX IF NOT EXISTS idx_sports_events_start_time ON sports_events(start_time);

CREATE TABLE IF NOT EXISTS sports_event_teams (
    event_id UUID NOT NULL REFERENCES sports_events(event_id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    abbreviation VARCHAR(50),
    role VARCHAR(50),
    PRIMARY KEY (event_id, name)
);

CREATE INDEX IF NOT EXISTS idx_sports_event_teams_name
    ON sports_event_teams(LOWER(name));

-- Transactional outbox keeps event resolution and message publication reliable.
CREATE TABLE IF NOT EXISTS event_outbox (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id UUID NOT NULL REFERENCES events(id),
    event_type VARCHAR(100) NOT NULL,
    topic VARCHAR(255) NOT NULL,
    payload JSONB NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    published_at TIMESTAMP,
    UNIQUE(event_id, event_type)
);

CREATE INDEX IF NOT EXISTS idx_event_outbox_pending
    ON event_outbox(created_at, id) WHERE published_at IS NULL;

-- Markets table
CREATE TABLE IF NOT EXISTS markets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id UUID NOT NULL REFERENCES merchants(id),
    event_id UUID NOT NULL REFERENCES events(id),
    type VARCHAR(50) NOT NULL,
    question TEXT NOT NULL,
    options JSONB NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'active',
    total_volume DECIMAL(28,6) NOT NULL DEFAULT 0.000000,
    liquidity_pool DECIMAL(20,2) NOT NULL DEFAULT 0.00,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    settled_at TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_markets_merchant ON markets(merchant_id);
CREATE INDEX IF NOT EXISTS idx_markets_event ON markets(event_id);
CREATE INDEX IF NOT EXISTS idx_markets_status ON markets(status);

-- Orders table
CREATE TABLE IF NOT EXISTS orders (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id UUID NOT NULL REFERENCES merchants(id),
    user_id VARCHAR(255) NOT NULL,
    market_id UUID NOT NULL REFERENCES markets(id),
    type VARCHAR(50) NOT NULL,
    option VARCHAR(255) NOT NULL,
    amount DECIMAL(28,6) NOT NULL,
    filled_amount DECIMAL(28,6) NOT NULL DEFAULT 0.000000,
    currency VARCHAR(10) NOT NULL,
    price DECIMAL(10,6) NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'pending',
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    filled_at TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_orders_merchant ON orders(merchant_id);
CREATE INDEX IF NOT EXISTS idx_orders_user ON orders(merchant_id, user_id);
CREATE INDEX IF NOT EXISTS idx_orders_market ON orders(market_id);
CREATE INDEX IF NOT EXISTS idx_orders_status ON orders(status);
CREATE INDEX IF NOT EXISTS idx_orders_created ON orders(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_orders_book
    ON orders(market_id, option, currency, status, price, created_at);

-- Wallets table
CREATE TABLE IF NOT EXISTS wallets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id UUID NOT NULL REFERENCES merchants(id),
    user_id VARCHAR(255) NOT NULL,
    currency VARCHAR(10) NOT NULL,
    balance DECIMAL(20,2) NOT NULL DEFAULT 0.00,
    locked_balance DECIMAL(20,2) NOT NULL DEFAULT 0.00,
    updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
    UNIQUE(merchant_id, user_id, currency)
);

CREATE INDEX IF NOT EXISTS idx_wallets_merchant ON wallets(merchant_id);
CREATE INDEX IF NOT EXISTS idx_wallets_user ON wallets(merchant_id, user_id);

-- Transactions table
CREATE TABLE IF NOT EXISTS transactions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    wallet_id UUID NOT NULL REFERENCES wallets(id),
    type VARCHAR(50) NOT NULL,
    amount DECIMAL(20,2) NOT NULL,
    currency VARCHAR(10) NOT NULL,
    related_order_id UUID REFERENCES orders(id),
    status VARCHAR(50) NOT NULL DEFAULT 'completed',
    created_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_transactions_wallet ON transactions(wallet_id);
CREATE INDEX IF NOT EXISTS idx_transactions_order ON transactions(related_order_id);
CREATE INDEX IF NOT EXISTS idx_transactions_created ON transactions(created_at DESC);

-- Idempotent market settlement audit records
CREATE TABLE IF NOT EXISTS market_settlements (
    market_id UUID PRIMARY KEY REFERENCES markets(id),
    event_id UUID NOT NULL REFERENCES events(id),
    winning_option VARCHAR(255) NOT NULL,
    settled_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_market_settlements_event ON market_settlements(event_id);

-- One record per filled order prevents duplicate payouts and preserves the audit trail.
CREATE TABLE IF NOT EXISTS settlement_payouts (
    market_id UUID NOT NULL REFERENCES markets(id),
    order_id UUID NOT NULL REFERENCES orders(id),
    wallet_id UUID NOT NULL REFERENCES wallets(id),
    currency VARCHAR(10) NOT NULL,
    stake DECIMAL(20,2) NOT NULL,
    payout DECIMAL(20,2) NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    PRIMARY KEY (market_id, order_id)
);

CREATE INDEX IF NOT EXISTS idx_settlement_payouts_wallet ON settlement_payouts(wallet_id);

-- Exchange rates table
CREATE TABLE IF NOT EXISTS exchange_rates (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    from_currency VARCHAR(10) NOT NULL,
    to_currency VARCHAR(10) NOT NULL,
    rate DECIMAL(20,8) NOT NULL,
    provider VARCHAR(100) NOT NULL,
    timestamp TIMESTAMP NOT NULL DEFAULT NOW(),
    UNIQUE(from_currency, to_currency, timestamp)
);

CREATE INDEX IF NOT EXISTS idx_exchange_rates_pair ON exchange_rates(from_currency, to_currency);
CREATE INDEX IF NOT EXISTS idx_exchange_rates_timestamp ON exchange_rates(timestamp DESC);

-- Updated_at trigger function
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ language 'plpgsql';
-- +goose StatementEnd

-- Apply updated_at triggers
DROP TRIGGER IF EXISTS update_merchants_updated_at ON merchants;
CREATE TRIGGER update_merchants_updated_at BEFORE UPDATE ON merchants
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

DROP TRIGGER IF EXISTS update_events_updated_at ON events;
CREATE TRIGGER update_events_updated_at BEFORE UPDATE ON events
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

DROP TRIGGER IF EXISTS update_wallets_updated_at ON wallets;
CREATE TRIGGER update_wallets_updated_at BEFORE UPDATE ON wallets
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- +goose Down
DROP TABLE IF EXISTS settlement_payouts;
DROP TABLE IF EXISTS market_settlements;
DROP TABLE IF EXISTS transactions;
DROP TABLE IF EXISTS wallets;
DROP TABLE IF EXISTS orders;
DROP TABLE IF EXISTS markets;
DROP TABLE IF EXISTS event_outbox;
DROP TABLE IF EXISTS sports_event_teams;
DROP TABLE IF EXISTS sports_events;
DROP TABLE IF EXISTS events;
DROP TABLE IF EXISTS exchange_rates;
DROP TABLE IF EXISTS merchants;
DROP FUNCTION IF EXISTS update_updated_at_column();
