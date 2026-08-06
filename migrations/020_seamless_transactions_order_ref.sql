-- seamless_transactions.order_id records the business reference echoed to the
-- merchant in wallet callbacks. Parimutuel settlement credits reference bet
-- IDs from parimutuel_bets (never orders), so the column cannot be a foreign
-- key to orders(id). The transaction_id remains the authoritative key; the
-- callback_outbox side was relaxed in migration 019.
-- +goose Up
ALTER TABLE seamless_transactions
    DROP CONSTRAINT IF EXISTS seamless_transactions_order_id_fkey;

-- +goose Down
ALTER TABLE seamless_transactions
    ADD CONSTRAINT seamless_transactions_order_id_fkey
    FOREIGN KEY (order_id) REFERENCES orders(id);
