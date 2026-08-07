package market

import (
	"context"
	"database/sql"
	"time"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/afun-game/predictmarket-saas/pkg/types"
	"github.com/jackc/pgx/v5/pgconn"
)

const marketColumns = `
    id,
    merchant_id,
    event_id,
    type,
    category,
    resolution_time,
    question,
    options,
    status,
    total_volume,
    liquidity_pool,
    merchant_fee_rate,
    platform_fee_rate,
    created_at,
    settled_at`

type postgresRepository struct {
	database *sql.DB
}

func newPostgresRepository(database *sql.DB) *postgresRepository {
	return &postgresRepository{database: database}
}

func (r *postgresRepository) ValidateReferences(
	ctx context.Context,
	merchantID string,
	eventID string,
) (string, time.Time, error) {
	const query = `
SELECT EXISTS (
           SELECT 1 FROM merchants WHERE id = $1 AND status = 'active'
       ),
       EXISTS (
           SELECT 1 FROM events WHERE id = $2 AND status = 'active'
       ),
       EXISTS (
           SELECT 1 FROM events WHERE id = $2 AND resolution_time > now()
       ),
       COALESCE((SELECT category FROM events WHERE id = $2), ''),
       COALESCE((SELECT resolution_time FROM events WHERE id = $2), 'epoch')`

	var merchantActive bool
	var eventActive bool
	var eventNotExpired bool
	var eventCategory string
	var eventResolutionTime time.Time
	if err := r.database.QueryRowContext(ctx, query, merchantID, eventID).Scan(
		&merchantActive,
		&eventActive,
		&eventNotExpired,
		&eventCategory,
		&eventResolutionTime,
	); err != nil {
		return "", time.Time{}, fmt.Errorf("query market references: %w", err)
	}
	if !merchantActive || !eventActive {
		return "", time.Time{}, ErrInvalidReference
	}
	if !eventNotExpired {
		return "", time.Time{}, ErrEventExpired
	}
	return eventCategory, eventResolutionTime, nil
}

func (r *postgresRepository) Create(ctx context.Context, value *types.Market) error {
	options, err := json.Marshal(value.Options)
	if err != nil {
		return fmt.Errorf("marshal market options: %w", err)
	}
	const query = `
INSERT INTO markets (
    id,
    merchant_id,
    event_id,
    type,
    category,
    resolution_time,
    question,
    options,
    status,
    total_volume,
    liquidity_pool,
    merchant_fee_rate,
    platform_fee_rate,
    created_at,
    settled_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`

	_, err = r.database.ExecContext(
		ctx,
		query,
		value.ID,
		value.MerchantID,
		value.EventID,
		value.Type,
		value.Category,
		value.ResolutionTime,
		value.Question,
		options,
		value.Status,
		value.TotalVolume,
		value.LiquidityPool,
		value.MerchantFeeRate,
		value.PlatformFeeRate,
		value.CreatedAt,
		value.SettledAt,
	)
	if isForeignKeyViolation(err) {
		return ErrInvalidReference
	}
	if err != nil {
		return fmt.Errorf("insert market: %w", err)
	}
	return nil
}

func (r *postgresRepository) GetByID(
	ctx context.Context,
	marketID string,
) (*types.Market, error) {
	query := "SELECT " + marketColumns + " FROM markets WHERE id = $1"
	return scanMarket(r.database.QueryRowContext(ctx, query, marketID))
}

func (r *postgresRepository) List(
	ctx context.Context,
	filters ListFilters,
) ([]*types.Market, int, error) {
	const countQuery = `
SELECT COUNT(*)
FROM markets
WHERE ($1 = '' OR merchant_id = NULLIF($1, '')::uuid)
  AND ($2 = '' OR event_id = NULLIF($2, '')::uuid)
  AND ($3 = '' OR category = $3)
  AND ($4 = '' OR status = $4)`

	var total int
	err := r.database.QueryRowContext(
		ctx,
		countQuery,
		filters.MerchantID,
		filters.EventID,
		filters.Category,
		filters.Status,
	).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("count markets: %w", err)
	}

	orderBy := "created_at DESC, id"
	if filters.Sort == "popular" {
		orderBy = "total_volume DESC, created_at DESC, id"
	}
	query := "SELECT " + marketColumns + `
FROM markets
WHERE ($1 = '' OR merchant_id = NULLIF($1, '')::uuid)
  AND ($2 = '' OR event_id = NULLIF($2, '')::uuid)
  AND ($3 = '' OR category = $3)
  AND ($4 = '' OR status = $4)
ORDER BY ` + orderBy + `
LIMIT $5 OFFSET $6`
	offset := (filters.Page - 1) * filters.Limit
	rows, err := r.database.QueryContext(
		ctx,
		query,
		filters.MerchantID,
		filters.EventID,
		filters.Category,
		filters.Status,
		filters.Limit,
		offset,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("query markets: %w", err)
	}
	defer func() {
		_ = rows.Close() // Best-effort cleanup on early returns; checked below on the happy path.
	}()

	values := make([]*types.Market, 0, filters.Limit)
	for rows.Next() {
		value, err := scanMarket(rows)
		if err != nil {
			return nil, 0, err
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate markets: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, 0, fmt.Errorf("close market rows: %w", err)
	}
	return values, total, nil
}

func (r *postgresRepository) UpdateStatus(
	ctx context.Context,
	marketID string,
	expectedStatus string,
	status string,
) error {
	const query = `
UPDATE markets
SET status = $3
WHERE id = $1 AND status = $2`
	result, err := r.database.ExecContext(ctx, query, marketID, expectedStatus, status)
	if err != nil {
		return fmt.Errorf("update market status row: %w", err)
	}
	return requireUpdatedMarket(result)
}

func (r *postgresRepository) AddLiquidity(
	ctx context.Context,
	marketID string,
	expectedStatus string,
	amount float64,
) error {
	const query = `
UPDATE markets
SET liquidity_pool = liquidity_pool + $3
WHERE id = $1 AND status = $2`
	result, err := r.database.ExecContext(ctx, query, marketID, expectedStatus, amount)
	if err != nil {
		return fmt.Errorf("update market liquidity row: %w", err)
	}
	return requireUpdatedMarket(result)
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanMarket(row rowScanner) (*types.Market, error) {
	value := &types.Market{}
	var options []byte
	err := row.Scan(
		&value.ID,
		&value.MerchantID,
		&value.EventID,
		&value.Type,
		&value.Category,
		&value.ResolutionTime,
		&value.Question,
		&options,
		&value.Status,
		&value.TotalVolume,
		&value.LiquidityPool,
		&value.MerchantFeeRate,
		&value.PlatformFeeRate,
		&value.CreatedAt,
		&value.SettledAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan market: %w", err)
	}
	value.Options = []string{}
	if err := json.Unmarshal(options, &value.Options); err != nil {
		return nil, fmt.Errorf("unmarshal market options: %w", err)
	}
	if value.Options == nil {
		value.Options = []string{}
	}
	return value, nil
}

func requireUpdatedMarket(result sql.Result) error {
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count updated market rows: %w", err)
	}
	if rowsAffected == 0 {
		return ErrInvalidTransition
	}
	return nil
}

func isForeignKeyViolation(err error) bool {
	var postgresErr *pgconn.PgError
	return errors.As(err, &postgresErr) && postgresErr.Code == "23503"
}
