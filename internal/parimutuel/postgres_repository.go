package parimutuel

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/afun-game/predictmarket-saas/pkg/fixed"
)

// PostgresRepository stores parimutuel pools and bets in PostgreSQL.
type PostgresRepository struct {
	database *sql.DB
}

// NewPostgresRepository constructs a PostgreSQL-backed repository.
func NewPostgresRepository(database *sql.DB) *PostgresRepository {
	return &PostgresRepository{database: database}
}

func (r *PostgresRepository) CreatePools(ctx context.Context, marketID, currency string) error {
	if r == nil || r.database == nil {
		return errors.New("parimutuel database is not configured")
	}
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if currency == "" {
		return ErrInvalidBet
	}
	const query = `
INSERT INTO parimutuel_pools (market_id, currency)
VALUES ($1, $2)
ON CONFLICT (market_id) DO NOTHING`
	if _, err := r.database.ExecContext(ctx, query, marketID, currency); err != nil {
		return fmt.Errorf("create parimutuel pools: %w", err)
	}
	return nil
}

// LockMarketForBet locks the market row and returns its settlement context.
func (r *PostgresRepository) LockMarketForBet(
	ctx context.Context,
	marketID string,
) (string, string, []string, string, error) {
	if r == nil || r.database == nil {
		return "", "", nil, "", errors.New("parimutuel database is not configured")
	}
	const query = `
SELECT m.type, m.status, m.options, COALESCE(e.status, '')
FROM markets AS m
LEFT JOIN events AS e ON e.id = m.event_id
WHERE m.id = $1
FOR UPDATE OF m`
	var marketType, status, eventStatus string
	var options []byte
	err := r.database.QueryRowContext(ctx, query, marketID).Scan(&marketType, &status, &options, &eventStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil, "", ErrNotFound
	}
	if err != nil {
		return "", "", nil, "", fmt.Errorf("lock parimutuel market: %w", err)
	}
	var optionList []string
	if err := json.Unmarshal(options, &optionList); err != nil {
		return "", "", nil, "", fmt.Errorf("decode market options: %w", err)
	}
	return marketType, status, optionList, eventStatus, nil
}

func (r *PostgresRepository) PlaceBet(ctx context.Context, bet Bet) (*Bet, error) {
	if r == nil || r.database == nil {
		return nil, errors.New("parimutuel database is not configured")
	}
	marketType, marketStatus, options, eventStatus, err := r.LockMarketForBet(ctx, bet.MarketID)
	if err != nil {
		return nil, err
	}
	if marketType != "parimutuel" {
		return nil, ErrNotParimutuel
	}
	if marketStatus != "active" {
		return nil, ErrMarketInactive
	}
	if eventStatus != "active" && eventStatus != "pending" {
		return nil, ErrEventSettled
	}
	option := strings.TrimSpace(bet.Option)
	if !slices.Contains(options, option) {
		return nil, ErrInvalidOption
	}
	bet.Option = option
	if bet.Stake < 0.01 {
		return nil, ErrInvalidBet
	}
	bet.Currency = strings.ToUpper(strings.TrimSpace(bet.Currency))
	if bet.Currency == "" {
		return nil, ErrInvalidBet
	}

	databaseTx, err := r.database.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin parimutuel bet: %w", err)
	}
	defer func() { _ = databaseTx.Rollback() }()

	// Re-lock the market inside the transaction. The pre-flight check above
	// runs on its own connection, so its row lock is already gone; this lock
	// is the one that serializes limit checks against concurrent bets.
	var marketActive bool
	var marketMaxBet sql.NullString
	var merchantMaxBet, merchantMaxUserExposure sql.NullString
	err = databaseTx.QueryRowContext(
		ctx,
		`SELECT m.status = 'active' AND m.merchant_id = $2,
		        m.max_bet_amount::text,
		        me.max_bet_amount::text,
		        me.max_user_exposure::text
FROM markets AS m
JOIN merchants AS me ON me.id = m.merchant_id
WHERE m.id = $1
FOR UPDATE OF m`,
		bet.MarketID,
		bet.MerchantID,
	).Scan(&marketActive, &marketMaxBet, &merchantMaxBet, &merchantMaxUserExposure)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !marketActive) {
		return nil, ErrMarketInactive
	}
	if err != nil {
		return nil, fmt.Errorf("lock parimutuel market: %w", err)
	}

	// Convert stake to cents for limit checks
	stakeCents, err := fixed.CentsFromFloat(bet.Stake)
	if err != nil {
		return nil, fmt.Errorf("convert parimutuel stake: %w", err)
	}

	// Check bet amount limit (parimutuel stake is the full bet amount)
	effectiveLimit := merchantMaxBet
	if marketMaxBet.Valid {
		effectiveLimit = marketMaxBet
	}
	if effectiveLimit.Valid {
		limitCents, err := fixed.CentsFromString(effectiveLimit.String)
		if err != nil {
			return nil, fmt.Errorf("parse parimutuel bet limit: %w", err)
		}
		if stakeCents > limitCents {
			return nil, ErrBetAmountTooLarge
		}
	}

	// Check user exposure limit
	if merchantMaxUserExposure.Valid {
		limitCents, err := fixed.CentsFromString(merchantMaxUserExposure.String)
		if err != nil {
			return nil, fmt.Errorf("parse parimutuel user exposure limit: %w", err)
		}
		const userExposureQuery = `
SELECT COALESCE(SUM(stake), 0)::text
FROM parimutuel_bets
WHERE merchant_id = $1 AND user_id = $2 AND currency = $3 AND status = 'active'`
		var lockedStr string
		if err := databaseTx.QueryRowContext(ctx, userExposureQuery, bet.MerchantID, bet.UserID, bet.Currency).Scan(&lockedStr); err != nil {
			return nil, fmt.Errorf("get parimutuel user exposure: %w", err)
		}
		currentLocked, err := fixed.CentsFromString(lockedStr)
		if err != nil {
			return nil, fmt.Errorf("parse parimutuel user locked: %w", err)
		}
		if currentLocked+stakeCents > limitCents {
			return nil, ErrUserExposureTooHigh
		}
	}

	// The pool row must exist before the bet; the handler creates it at
	// market creation. Lock it so concurrent bets serialize on the sum.
	const lockPool = `
SELECT currency FROM parimutuel_pools WHERE market_id = $1 FOR UPDATE`
	var poolCurrency string
	err = databaseTx.QueryRowContext(ctx, lockPool, bet.MarketID).Scan(&poolCurrency)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrPoolNotInitialized
	}
	if err != nil {
		return nil, fmt.Errorf("lock parimutuel pool: %w", err)
	}
	if poolCurrency != bet.Currency {
		return nil, &ValidationError{Field: "currency", Message: "does not match the pool currency"}
	}

	// A preset ID is honored (the seamless coordinator generates it before
	// the merchant debit so the wallet callback ref references the bet);
	// platform-mode bets fall back to a database-generated UUID.
	const insertBet = `
INSERT INTO parimutuel_bets (id, market_id, merchant_id, user_id, option, stake, currency, wallet_kind, status)
VALUES (COALESCE(NULLIF($1, '')::uuid, gen_random_uuid()), $2, $3, $4, $5, $6, $7, $8, 'active')
RETURNING id, created_at`
	if err := databaseTx.QueryRowContext(
		ctx, insertBet,
		bet.ID, bet.MarketID, bet.MerchantID, bet.UserID, bet.Option, fixed.FormatCents(stakeCents), bet.Currency, bet.WalletKind,
	).Scan(&bet.ID, &bet.CreatedAt); err != nil {
		return nil, fmt.Errorf("insert parimutuel bet: %w", err)
	}
	const updatePool = `
UPDATE parimutuel_pools SET total_stake = total_stake + $2::numeric, updated_at = NOW()
WHERE market_id = $1`
	if _, err := databaseTx.ExecContext(ctx, updatePool, bet.MarketID, fixed.FormatCents(stakeCents)); err != nil {
		return nil, fmt.Errorf("update parimutuel pool: %w", err)
	}
	// Volume is cumulative staked amount, mirroring the order-book market
	// semantics where total_volume only grows with matched flow.
	if _, err := databaseTx.ExecContext(
		ctx,
		"UPDATE markets SET total_volume = total_volume + $2::numeric WHERE id = $1",
		bet.MarketID,
		bet.Stake,
	); err != nil {
		return nil, fmt.Errorf("update parimutuel market volume: %w", err)
	}
	if err := databaseTx.Commit(); err != nil {
		return nil, fmt.Errorf("commit parimutuel bet: %w", err)
	}
	bet.Status = StatusActive
	return &bet, nil
}

func (r *PostgresRepository) ListBets(ctx context.Context, filters ListFilters) ([]Bet, int, error) {
	if r == nil || r.database == nil {
		return nil, 0, errors.New("parimutuel database is not configured")
	}
	page, limit := normalizePage(filters.Page, filters.Limit)
	const where = `
WHERE merchant_id::text = $1
  AND ($2 = '' OR market_id::text = $2)
  AND ($3 = '' OR user_id = $3)`
	const selectQuery = `
SELECT id, market_id, merchant_id, user_id, option, stake::text, currency, status, created_at, settled_at
FROM parimutuel_bets` + where + `
ORDER BY created_at DESC
LIMIT $4 OFFSET $5`
	rows, err := r.database.QueryContext(
		ctx, selectQuery,
		strings.TrimSpace(filters.MerchantID), strings.TrimSpace(filters.MarketID), strings.TrimSpace(filters.UserID), limit, (page-1)*limit,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("list parimutuel bets: %w", err)
	}
	defer func() { _ = rows.Close() }()
	items := []Bet{}
	for rows.Next() {
		bet := Bet{}
		var settledAt sql.NullTime
		var stakeText string
		if err := rows.Scan(
			&bet.ID, &bet.MarketID, &bet.MerchantID, &bet.UserID, &bet.Option,
			&stakeText, &bet.Currency, &bet.Status, &bet.CreatedAt, &settledAt,
		); err != nil {
			return nil, 0, fmt.Errorf("scan parimutuel bet: %w", err)
		}
		stakeCents, err := fixed.CentsFromString(stakeText)
		if err != nil {
			return nil, 0, fmt.Errorf("parse parimutuel bet stake: %w", err)
		}
		bet.Stake = fixed.CentsToFloat(stakeCents)
		if settledAt.Valid {
			bet.SettledAt = &settledAt.Time
		}
		items = append(items, bet)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	var total int
	if err := r.database.QueryRowContext(
		ctx, "SELECT COUNT(*) FROM parimutuel_bets "+where,
		strings.TrimSpace(filters.MerchantID), strings.TrimSpace(filters.MarketID), strings.TrimSpace(filters.UserID),
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count parimutuel bets: %w", err)
	}
	return items, total, nil
}

func (r *PostgresRepository) GetPools(ctx context.Context, marketID string) ([]Pool, error) {
	if r == nil || r.database == nil {
		return nil, errors.New("parimutuel database is not configured")
	}
	const query = `
SELECT market_id, currency, total_stake::text, total_fees::text
FROM parimutuel_pools WHERE market_id = $1`
	rows, err := r.database.QueryContext(ctx, query, marketID)
	if err != nil {
		return nil, fmt.Errorf("get parimutuel pools: %w", err)
	}
	defer func() { _ = rows.Close() }()
	items := []Pool{}
	for rows.Next() {
		pool := Pool{}
		var totalStakeText, totalFeesText string
		if err := rows.Scan(&pool.MarketID, &pool.Currency, &totalStakeText, &totalFeesText); err != nil {
			return nil, fmt.Errorf("scan parimutuel pool: %w", err)
		}
		stakeCents, err := parsePoolAmount(totalStakeText)
		if err != nil {
			return nil, fmt.Errorf("parse pool total stake: %w", err)
		}
		pool.TotalStake = fixed.CentsToFloat(stakeCents)
		feesCents, err := parsePoolAmount(totalFeesText)
		if err != nil {
			return nil, fmt.Errorf("parse pool total fees: %w", err)
		}
		pool.TotalFees = fixed.CentsToFloat(feesCents)
		items = append(items, pool)
	}
	return items, rows.Err()
}

// MarketPools returns pool totals plus per-option active stakes for many
// markets in two batched queries. Markets without a pool row are absent.
func (r *PostgresRepository) MarketPools(ctx context.Context, marketIDs []string) (map[string]MarketPool, error) {
	if r == nil || r.database == nil {
		return nil, errors.New("parimutuel database is not configured")
	}
	result := make(map[string]MarketPool, len(marketIDs))
	if len(marketIDs) == 0 {
		return result, nil
	}
	const poolQuery = `
SELECT market_id, currency, total_stake, total_fees
FROM parimutuel_pools
WHERE market_id::text = ANY($1)`
	rows, err := r.database.QueryContext(ctx, poolQuery, marketIDs)
	if err != nil {
		return nil, fmt.Errorf("get parimutuel pools for markets: %w", err)
	}
	for rows.Next() {
		pool := MarketPool{}
		var marketID string
		if err := rows.Scan(&marketID, &pool.Currency, &pool.TotalStake, &pool.TotalFees); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan parimutuel pool for market: %w", err)
		}
		pool.Options = []OptionStake{}
		result[marketID] = pool
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close parimutuel pools for markets: %w", err)
	}

	const stakeQuery = `
SELECT market_id, option, SUM(stake)
FROM parimutuel_bets
WHERE market_id::text = ANY($1) AND status = 'active'
GROUP BY market_id, option`
	stakeRows, err := r.database.QueryContext(ctx, stakeQuery, marketIDs)
	if err != nil {
		return nil, fmt.Errorf("get parimutuel stakes for markets: %w", err)
	}
	defer func() { _ = stakeRows.Close() }()
	for stakeRows.Next() {
		var marketID string
		stake := OptionStake{}
		if err := stakeRows.Scan(&marketID, &stake.Option, &stake.Stake); err != nil {
			return nil, fmt.Errorf("scan parimutuel stake for market: %w", err)
		}
		pool, exists := result[marketID]
		if !exists {
			pool = MarketPool{Options: []OptionStake{}}
			result[marketID] = pool
		}
		pool.Options = append(pool.Options, stake)
		result[marketID] = pool
	}
	return result, stakeRows.Err()
}

// OptionStakes sums the active stake per option for one market. The totals
// feed the per-outcome implied odds shown in the hosted UI.
func (r *PostgresRepository) OptionStakes(ctx context.Context, marketID string) ([]OptionStake, error) {
	if r == nil || r.database == nil {
		return nil, errors.New("parimutuel database is not configured")
	}
	const query = `
SELECT option, SUM(stake)::text
FROM parimutuel_bets
WHERE market_id = $1 AND status = 'active'
GROUP BY option`
	rows, err := r.database.QueryContext(ctx, query, marketID)
	if err != nil {
		return nil, fmt.Errorf("get parimutuel option stakes: %w", err)
	}
	defer func() { _ = rows.Close() }()
	items := []OptionStake{}
	for rows.Next() {
		item := OptionStake{}
		var stakeText string
		if err := rows.Scan(&item.Option, &stakeText); err != nil {
			return nil, fmt.Errorf("scan parimutuel option stake: %w", err)
		}
		stakeCents, err := fixed.CentsFromString(stakeText)
		if err != nil {
			return nil, fmt.Errorf("parse option stake: %w", err)
		}
		item.Stake = fixed.CentsToFloat(stakeCents)
		items = append(items, item)
	}
	return items, rows.Err()
}

func normalizePage(page, limit int) (int, int) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	return page, limit
}

// parsePoolAmount parses a pool amount that may legitimately be zero (an
// empty pool), which fixed.CentsFromString rejects.
func parsePoolAmount(value string) (int64, error) {
	if value == "0" || value == "0.00" {
		return 0, nil
	}
	return fixed.CentsFromString(value)
}
