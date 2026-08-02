package marketmaker

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// PostgresRepository tracks committed market-making funds per market.
type PostgresRepository struct {
	database *sql.DB
}

// NewPostgresRepository constructs a PostgreSQL-backed repository.
func NewPostgresRepository(database *sql.DB) Repository {
	return &PostgresRepository{database: database}
}

func (r *PostgresRepository) GetCommitted(ctx context.Context, marketID string) (float64, error) {
	if r == nil || r.database == nil {
		return 0, errors.New("market maker database is not configured")
	}
	const query = `
SELECT committed FROM marketmaker_funds WHERE market_id = $1`
	var committed float64
	err := r.database.QueryRowContext(ctx, query, marketID).Scan(&committed)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("get market maker committed funds: %w", err)
	}
	return committed, nil
}

func (r *PostgresRepository) SetCommitted(ctx context.Context, marketID string, committed float64) error {
	if r == nil || r.database == nil {
		return errors.New("market maker database is not configured")
	}
	const query = `
INSERT INTO marketmaker_funds (market_id, committed, updated_at)
VALUES ($1, $2, NOW())
ON CONFLICT (market_id) DO UPDATE SET committed = EXCLUDED.committed, updated_at = NOW()`
	if _, err := r.database.ExecContext(ctx, query, marketID, committed); err != nil {
		return fmt.Errorf("record market maker committed funds: %w", err)
	}
	return nil
}
