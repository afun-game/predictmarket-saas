-- V3 hardening: per-merchant seamless circuit breaker, callback ownership
-- verification, and an audit trail for merchant-facing change requests.
-- +goose Up

ALTER TABLE merchants
    ADD COLUMN IF NOT EXISTS seamless_degraded BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS seamless_degraded_at TIMESTAMP,
    ADD COLUMN IF NOT EXISTS seamless_degraded_reason TEXT,
    ADD COLUMN IF NOT EXISTS callback_verified_at TIMESTAMP;

-- Voids are recorded in the same idempotent settlement audit table with a
-- settlement type; winning_option is NULL for voided markets.
ALTER TABLE market_settlements
    ALTER COLUMN winning_option DROP NOT NULL;

ALTER TABLE market_settlements
    ADD COLUMN IF NOT EXISTS settlement_type VARCHAR(16) NOT NULL DEFAULT 'settle';

ALTER TABLE market_settlements
    DROP CONSTRAINT IF EXISTS market_settlements_settlement_type_check;

ALTER TABLE market_settlements
    ADD CONSTRAINT market_settlements_settlement_type_check
    CHECK (settlement_type IN ('settle', 'void'));

-- V3 audit trail for state-changing merchant requests (idempotent append-only).
CREATE TABLE IF NOT EXISTS merchant_api_audits (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id UUID NOT NULL REFERENCES merchants(id),
    method VARCHAR(10) NOT NULL,
    path TEXT NOT NULL,
    request_id VARCHAR(128) NOT NULL DEFAULT '',
    idempotency_key VARCHAR(255) NOT NULL DEFAULT '',
    client_ip VARCHAR(64) NOT NULL DEFAULT '',
    status_code INT NOT NULL DEFAULT 0,
    created_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_merchant_api_audits_merchant_created
    ON merchant_api_audits(merchant_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_merchant_api_audits_created
    ON merchant_api_audits(created_at DESC);

-- +goose Down

DROP TABLE IF EXISTS merchant_api_audits;

ALTER TABLE market_settlements
    DROP CONSTRAINT IF EXISTS market_settlements_settlement_type_check;

ALTER TABLE market_settlements
    DROP COLUMN IF EXISTS settlement_type;

ALTER TABLE market_settlements
    ALTER COLUMN winning_option SET NOT NULL;

ALTER TABLE merchants
    DROP COLUMN IF EXISTS callback_verified_at,
    DROP COLUMN IF EXISTS seamless_degraded_reason,
    DROP COLUMN IF EXISTS seamless_degraded_at,
    DROP COLUMN IF EXISTS seamless_degraded;
