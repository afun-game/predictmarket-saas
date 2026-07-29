package merchant

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/afun-game/predictmarket-saas/pkg/types"
)

const merchantColumns = `
    id,
    name,
    email,
    api_key,
	api_key_prefix,
    api_secret,
    status,
    currency,
    timezone,
    fee_rate,
    created_at,
    updated_at`

type postgresRepository struct {
	database *sql.DB
}

func newPostgresRepository(database *sql.DB) *postgresRepository {
	return &postgresRepository{database: database}
}

func (r *postgresRepository) Create(ctx context.Context, merchant *types.Merchant) error {
	const query = `
INSERT INTO merchants (
    id,
    name,
    email,
    api_key,
	api_key_prefix,
    api_secret,
    status,
    currency,
    timezone,
    fee_rate,
    created_at,
    updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`

	_, err := r.database.ExecContext(
		ctx,
		query,
		merchant.ID,
		merchant.Name,
		merchant.Email,
		merchant.APIKey,
		merchant.APIKeyPrefix,
		merchant.APISecret,
		merchant.Status,
		merchant.Currency,
		merchant.Timezone,
		merchant.FeeRate,
		merchant.CreatedAt,
		merchant.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert merchant: %w", err)
	}
	return nil
}

func (r *postgresRepository) GetByID(
	ctx context.Context,
	merchantID string,
) (*types.Merchant, error) {
	query := "SELECT " + merchantColumns + " FROM merchants WHERE id = $1"
	return scanMerchant(r.database.QueryRowContext(ctx, query, merchantID))
}

func (r *postgresRepository) GetByAPIKeyPrefix(
	ctx context.Context,
	prefix string,
) (*types.Merchant, error) {
	query := "SELECT " + merchantColumns + " FROM merchants WHERE api_key_prefix = $1"
	return scanMerchant(r.database.QueryRowContext(ctx, query, prefix))
}

func (r *postgresRepository) UpdateAPIKey(
	ctx context.Context,
	merchantID string,
	prefix string,
	keyHash string,
) error {
	const query = `
UPDATE merchants
SET api_key = $2, api_key_prefix = $3, updated_at = NOW()
WHERE id = $1`
	result, err := r.database.ExecContext(ctx, query, merchantID, keyHash, prefix)
	if err != nil {
		return fmt.Errorf("update API key hash: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count API key hash update: %w", err)
	}
	if rowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *postgresRepository) Update(ctx context.Context, merchant *types.Merchant) error {
	const query = `
UPDATE merchants
SET name = $2,
    email = $3,
    api_key = $4,
    api_key_prefix = $5,
    api_secret = $6,
    status = $7,
    currency = $8,
    timezone = $9,
    fee_rate = $10,
    updated_at = $11
WHERE id = $1`

	result, err := r.database.ExecContext(
		ctx,
		query,
		merchant.ID,
		merchant.Name,
		merchant.Email,
		merchant.APIKey,
		merchant.APIKeyPrefix,
		merchant.APISecret,
		merchant.Status,
		merchant.Currency,
		merchant.Timezone,
		merchant.FeeRate,
		merchant.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("update merchant row: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count updated merchant rows: %w", err)
	}
	if rowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *postgresRepository) List(
	ctx context.Context,
	offset int,
	limit int,
) ([]*types.Merchant, error) {
	query := "SELECT " + merchantColumns + `
FROM merchants
ORDER BY created_at, id
OFFSET $1 LIMIT $2`

	rows, err := r.database.QueryContext(ctx, query, offset, limit)
	if err != nil {
		return nil, fmt.Errorf("query merchants: %w", err)
	}
	defer func() {
		_ = rows.Close() // Best-effort cleanup on early returns; checked below on the happy path.
	}()

	merchants := make([]*types.Merchant, 0, limit)
	for rows.Next() {
		merchant, err := scanMerchant(rows)
		if err != nil {
			return nil, err
		}
		merchants = append(merchants, merchant)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate merchants: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close merchant rows: %w", err)
	}
	return merchants, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanMerchant(row rowScanner) (*types.Merchant, error) {
	merchant := &types.Merchant{}
	err := row.Scan(
		&merchant.ID,
		&merchant.Name,
		&merchant.Email,
		&merchant.APIKey,
		&merchant.APIKeyPrefix,
		&merchant.APISecret,
		&merchant.Status,
		&merchant.Currency,
		&merchant.Timezone,
		&merchant.FeeRate,
		&merchant.CreatedAt,
		&merchant.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan merchant: %w", err)
	}
	return merchant, nil
}
