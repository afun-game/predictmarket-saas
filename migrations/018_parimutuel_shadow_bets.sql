-- Seamless parimutuel bets: bets placed through the seamless wallet path are
-- debited from the merchant wallet and credited back through callbacks, so
-- they never touch the platform user wallet. The wallet_kind column tells
-- settlement which payout path to use.
-- +goose Up

ALTER TABLE parimutuel_bets
    ADD COLUMN IF NOT EXISTS wallet_kind VARCHAR(16) NOT NULL DEFAULT 'platform';

ALTER TABLE parimutuel_bets
    DROP CONSTRAINT IF EXISTS parimutuel_bets_wallet_kind_check;

ALTER TABLE parimutuel_bets
    ADD CONSTRAINT parimutuel_bets_wallet_kind_check
    CHECK (wallet_kind IN ('platform', 'shadow'));

-- +goose Down

ALTER TABLE parimutuel_bets
    DROP CONSTRAINT IF EXISTS parimutuel_bets_wallet_kind_check;

ALTER TABLE parimutuel_bets
    DROP COLUMN IF EXISTS wallet_kind;
