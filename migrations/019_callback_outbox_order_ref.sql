-- callback_outbox.order_id feeds the merchant-facing callback ref. Seamless
-- rollbacks reference orders/bets that are never persisted (the debit failed
-- before the business object existed) and parimutuel credits reference bet
-- IDs from parimutuel_bets, so the column cannot be a foreign key to
-- orders(id). The transaction_id remains the authoritative idempotency key.
-- +goose Up
ALTER TABLE callback_outbox
    DROP CONSTRAINT IF EXISTS callback_outbox_order_id_fkey;

-- +goose Down
ALTER TABLE callback_outbox
    ADD CONSTRAINT callback_outbox_order_id_fkey
    FOREIGN KEY (order_id) REFERENCES orders(id);
