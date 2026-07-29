-- Store tradable shares at six decimal places while retaining cent-denominated
-- wallet and transaction balances.
-- +goose Up

ALTER TABLE orders
    ALTER COLUMN amount TYPE DECIMAL(28,6) USING amount::DECIMAL(28,6),
    ALTER COLUMN filled_amount TYPE DECIMAL(28,6) USING filled_amount::DECIMAL(28,6);

ALTER TABLE trades
    ALTER COLUMN shares TYPE DECIMAL(28,6) USING shares::DECIMAL(28,6);

ALTER TABLE markets
    ALTER COLUMN total_volume TYPE DECIMAL(28,6) USING total_volume::DECIMAL(28,6);

-- +goose Down
ALTER TABLE orders
    ALTER COLUMN amount TYPE DECIMAL(20,2) USING amount::DECIMAL(20,2),
    ALTER COLUMN filled_amount TYPE DECIMAL(20,2) USING filled_amount::DECIMAL(20,2);
