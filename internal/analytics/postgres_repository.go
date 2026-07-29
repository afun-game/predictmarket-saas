package analytics

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type postgresRepository struct{ database *sql.DB }

func newPostgresRepository(database *sql.DB) *postgresRepository {
	return &postgresRepository{database: database}
}

func (r *postgresRepository) MerchantStats(
	ctx context.Context,
	merchantID string,
	cutoff *time.Time,
) (*MerchantStats, error) {
	if err := r.requireMerchant(ctx, merchantID); err != nil {
		return nil, err
	}
	result := newMerchantStats()
	const summary = `
SELECT
    COALESCE(SUM(o.filled_amount), 0)::float8 / 2,
    COUNT(o.id),
    COUNT(DISTINCT o.user_id)
FROM orders o
WHERE o.merchant_id = $1
  AND ($2::timestamp IS NULL OR o.created_at >= $2)`
	if err := r.database.QueryRowContext(ctx, summary, merchantID, cutoff).Scan(
		&result.TotalVolume,
		&result.TotalOrders,
		&result.ActiveUsers,
	); err != nil {
		return nil, fmt.Errorf("query merchant order summary: %w", err)
	}
	if err := r.database.QueryRowContext(ctx, `
SELECT COUNT(*) FROM markets WHERE merchant_id = $1 AND status = 'active'`, merchantID).Scan(
		&result.ActiveMarkets,
	); err != nil {
		return nil, fmt.Errorf("query merchant active markets: %w", err)
	}
	volumeRows, err := r.database.QueryContext(ctx, `
SELECT currency, COALESCE(SUM(filled_amount), 0)::float8 / 2
FROM orders
WHERE merchant_id = $1 AND ($2::timestamp IS NULL OR created_at >= $2)
GROUP BY currency`, merchantID, cutoff)
	if err != nil {
		return nil, fmt.Errorf("query merchant currency volume: %w", err)
	}
	result.VolumeByCurrency, err = scanCurrencyAmounts(volumeRows)
	if err != nil {
		return nil, fmt.Errorf("scan merchant currency volume: %w", err)
	}
	feeRows, err := r.database.QueryContext(ctx, `
SELECT currency, COALESCE(SUM(amount), 0)::float8
FROM fee_ledger
WHERE merchant_id = $1
  AND recipient = 'merchant'
  AND ($2::timestamp IS NULL OR created_at >= $2)
GROUP BY currency`, merchantID, cutoff)
	if err != nil {
		return nil, fmt.Errorf("query merchant fee ledger revenue: %w", err)
	}
	result.RevenueByCurrency, err = scanCurrencyAmounts(feeRows)
	if err != nil {
		return nil, fmt.Errorf("scan merchant fee revenue: %w", err)
	}
	for _, amount := range result.RevenueByCurrency {
		result.RevenueFromFee += amount
	}
	return result, nil
}

func (r *postgresRepository) MarketStats(ctx context.Context, marketID string) (*MarketStats, error) {
	result := newMarketStats()
	const summary = `
SELECT m.total_volume::float8, COUNT(o.id), COUNT(DISTINCT o.user_id)
FROM markets m
LEFT JOIN orders o ON o.market_id = m.id
WHERE m.id = $1
GROUP BY m.id`
	err := r.database.QueryRowContext(ctx, summary, marketID).Scan(
		&result.TotalVolume,
		&result.TotalOrders,
		&result.UniqueTraders,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query market summary: %w", err)
	}
	rows, err := r.database.QueryContext(ctx, `
SELECT occurred_at, price
FROM (
    SELECT COALESCE(filled_at, created_at) AS occurred_at, price::float8
    FROM orders
    WHERE market_id = $1 AND filled_amount > 0
    ORDER BY COALESCE(filled_at, created_at) DESC, id DESC
    LIMIT 200
) recent
ORDER BY occurred_at`, marketID)
	if err != nil {
		return nil, fmt.Errorf("query market price history: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var timestamp time.Time
		var point PricePoint
		if err := rows.Scan(&timestamp, &point.Price); err != nil {
			return nil, fmt.Errorf("scan market price history: %w", err)
		}
		point.Timestamp = timestamp.UTC().Unix()
		result.PriceHistory = append(result.PriceHistory, point)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate market price history: %w", err)
	}
	distributionRows, err := r.database.QueryContext(ctx, `
SELECT option, COALESCE(SUM(filled_amount), 0)::float8
FROM orders WHERE market_id = $1 AND filled_amount > 0
GROUP BY option`, marketID)
	if err != nil {
		return nil, fmt.Errorf("query market distribution: %w", err)
	}
	defer func() { _ = distributionRows.Close() }()
	var distributionTotal float64
	for distributionRows.Next() {
		var option string
		var amount float64
		if err := distributionRows.Scan(&option, &amount); err != nil {
			return nil, fmt.Errorf("scan market distribution: %w", err)
		}
		result.Distribution[option] = amount
		distributionTotal += amount
	}
	if err := distributionRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate market distribution: %w", err)
	}
	if distributionTotal > 0 {
		for option, amount := range result.Distribution {
			result.Distribution[option] = amount / distributionTotal
		}
	}
	return result, nil
}

func (r *postgresRepository) UserStats(
	ctx context.Context,
	merchantID string,
	userID string,
) (*UserStats, error) {
	var exists bool
	if err := r.database.QueryRowContext(ctx, `
SELECT EXISTS (
    SELECT 1 FROM wallets WHERE merchant_id = $1 AND user_id = $2
    UNION ALL
    SELECT 1 FROM orders WHERE merchant_id = $1 AND user_id = $2
)`, merchantID, userID).Scan(&exists); err != nil {
		return nil, fmt.Errorf("query analytics user: %w", err)
	}
	if !exists {
		return nil, ErrNotFound
	}
	result := newUserStats()
	if err := r.database.QueryRowContext(ctx, `
SELECT COUNT(*), COALESCE(SUM(filled_amount), 0)::float8
FROM orders WHERE merchant_id = $1 AND user_id = $2`, merchantID, userID).Scan(
		&result.TotalOrders,
		&result.TotalVolume,
	); err != nil {
		return nil, fmt.Errorf("query user order summary: %w", err)
	}
	volumeRows, err := r.database.QueryContext(ctx, `
SELECT currency, COALESCE(SUM(filled_amount), 0)::float8
FROM orders WHERE merchant_id = $1 AND user_id = $2
GROUP BY currency`, merchantID, userID)
	if err != nil {
		return nil, fmt.Errorf("query user currency volume: %w", err)
	}
	result.VolumeByCurrency, err = scanCurrencyAmounts(volumeRows)
	if err != nil {
		return nil, fmt.Errorf("scan user currency volume: %w", err)
	}
	var settled, wins int64
	if err := r.database.QueryRowContext(ctx, `
SELECT COUNT(*), COUNT(*) FILTER (WHERE sp.payout > sp.stake)
FROM settlement_payouts sp
JOIN orders o ON o.id = sp.order_id
WHERE o.merchant_id = $1 AND o.user_id = $2`, merchantID, userID).Scan(&settled, &wins); err != nil {
		return nil, fmt.Errorf("query user win rate: %w", err)
	}
	if settled > 0 {
		result.WinRate = float64(wins) / float64(settled)
	}
	profitRows, err := r.database.QueryContext(ctx, `
SELECT sp.currency, COALESCE(SUM(sp.payout - sp.stake), 0)::float8
FROM settlement_payouts sp
JOIN orders o ON o.id = sp.order_id
WHERE o.merchant_id = $1 AND o.user_id = $2
GROUP BY sp.currency`, merchantID, userID)
	if err != nil {
		return nil, fmt.Errorf("query user profit: %w", err)
	}
	result.ProfitByCurrency, err = scanCurrencyAmounts(profitRows)
	if err != nil {
		return nil, fmt.Errorf("scan user profit: %w", err)
	}
	for _, amount := range result.ProfitByCurrency {
		result.CurrentProfit += amount
	}
	return result, nil
}

func (r *postgresRepository) PlatformStats(ctx context.Context, cutoff *time.Time) (*PlatformStats, error) {
	result := newPlatformStats()
	if err := r.database.QueryRowContext(ctx, `
SELECT
    (SELECT COUNT(*) FROM merchants),
    (SELECT COUNT(*) FROM markets),
    COUNT(o.id),
    COALESCE(SUM(o.filled_amount), 0)::float8 / 2
FROM orders o
WHERE ($1::timestamp IS NULL OR o.created_at >= $1)`, cutoff).Scan(
		&result.TotalMerchants,
		&result.TotalMarkets,
		&result.TotalOrders,
		&result.TotalVolume,
	); err != nil {
		return nil, fmt.Errorf("query platform summary: %w", err)
	}
	rows, err := r.database.QueryContext(ctx, `
SELECT currency, COALESCE(SUM(filled_amount), 0)::float8 / 2
FROM orders WHERE ($1::timestamp IS NULL OR created_at >= $1)
GROUP BY currency`, cutoff)
	if err != nil {
		return nil, fmt.Errorf("query platform currency volume: %w", err)
	}
	result.VolumeByCurrency, err = scanCurrencyAmounts(rows)
	if err != nil {
		return nil, fmt.Errorf("scan platform currency volume: %w", err)
	}
	return result, nil
}

func (r *postgresRepository) requireMerchant(ctx context.Context, merchantID string) error {
	var exists bool
	if err := r.database.QueryRowContext(
		ctx,
		"SELECT EXISTS (SELECT 1 FROM merchants WHERE id = $1)",
		merchantID,
	).Scan(&exists); err != nil {
		return fmt.Errorf("query analytics merchant: %w", err)
	}
	if !exists {
		return ErrNotFound
	}
	return nil
}

func scanCurrencyAmounts(rows *sql.Rows) (map[string]float64, error) {
	defer func() { _ = rows.Close() }()
	result := map[string]float64{}
	for rows.Next() {
		var currency string
		var amount float64
		if err := rows.Scan(&currency, &amount); err != nil {
			return nil, err
		}
		result[currency] = amount
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}
