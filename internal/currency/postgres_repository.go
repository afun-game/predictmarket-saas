package currency

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

type postgresRepository struct {
	database *sql.DB
}

func newPostgresRepository(database *sql.DB) *postgresRepository {
	return &postgresRepository{database: database}
}

func (r *postgresRepository) GetLatest(
	ctx context.Context,
	from string,
	to string,
) (rateRecord, error) {
	const query = `
SELECT from_currency, to_currency, rate::text, provider, timestamp
FROM exchange_rates
WHERE from_currency = $1 AND to_currency = $2
ORDER BY timestamp DESC
LIMIT 1`
	var rate rateRecord
	err := r.database.QueryRowContext(ctx, query, from, to).Scan(
		&rate.From,
		&rate.To,
		&rate.Value,
		&rate.Provider,
		&rate.Timestamp,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return rateRecord{}, ErrRateNotFound
	}
	if err != nil {
		return rateRecord{}, fmt.Errorf("query latest exchange rate: %w", err)
	}
	return rate, nil
}

func (r *postgresRepository) Save(ctx context.Context, rates []rateRecord) error {
	databaseTx, err := r.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin exchange rate save: %w", err)
	}
	defer func() { _ = databaseTx.Rollback() }()
	const query = `
INSERT INTO exchange_rates (from_currency, to_currency, rate, provider, timestamp)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (from_currency, to_currency, timestamp) DO UPDATE
SET rate = EXCLUDED.rate, provider = EXCLUDED.provider`
	for _, rate := range rates {
		if _, err := databaseTx.ExecContext(
			ctx,
			query,
			rate.From,
			rate.To,
			rate.Value,
			rate.Provider,
			rate.Timestamp,
		); err != nil {
			return fmt.Errorf("upsert %s/%s exchange rate: %w", rate.From, rate.To, err)
		}
	}
	if err := databaseTx.Commit(); err != nil {
		return fmt.Errorf("commit exchange rate save: %w", err)
	}
	return nil
}
