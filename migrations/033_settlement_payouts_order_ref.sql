-- settlement_payouts.order_id records the business reference exposed through
-- GET /api/v2/settlements/{marketID}/payouts. Parimutuel settlement writes
-- one row per bet using the bet ID from parimutuel_bets (never an order), so
-- the column cannot be a foreign key to orders(id). The wallet_id reference
-- is retained; order_id is an audit reference like the outbox relaxations in
-- migrations 019/020.
-- +goose Up
ALTER TABLE settlement_payouts
    DROP CONSTRAINT IF EXISTS settlement_payouts_order_id_fkey;

-- +goose Down
ALTER TABLE settlement_payouts
    ADD CONSTRAINT settlement_payouts_order_id_fkey
    FOREIGN KEY (order_id) REFERENCES orders(id);
