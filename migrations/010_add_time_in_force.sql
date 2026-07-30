-- Persist each order's matching lifetime policy. Existing orders predate the
-- field and therefore retain the service's default good-till-cancelled policy.
-- +goose Up

ALTER TABLE orders
    ADD COLUMN IF NOT EXISTS time_in_force VARCHAR(10) NOT NULL DEFAULT 'gtc';

-- +goose Down

ALTER TABLE orders
    DROP COLUMN IF EXISTS time_in_force;
