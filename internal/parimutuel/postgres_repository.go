package parimutuel

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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
	validOption := false
	for _, candidate := range options {
		if candidate == option {
			validOption = true
			break
		}
	}
	if !validOption {
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

	const insertBet = `
INSERT INTO parimutuel_bets (market_id, merchant_id, user_id, option, stake, currency, wallet_kind, status)
VALUES ($1, $2, $3, $4, $5, $6, $7, 'active')
RETURNING id, created_at`
	if err := databaseTx.QueryRowContext(
		ctx, insertBet,
		bet.MarketID, bet.MerchantID, bet.UserID, bet.Option, bet.Stake, bet.Currency, bet.WalletKind,
	).Scan(&bet.ID, &bet.CreatedAt); err != nil {
		return nil, fmt.Errorf("insert parimutuel bet: %w", err)
	}
	const updatePool = `
UPDATE parimutuel_pools SET total_stake = total_stake + $2, updated_at = NOW()
WHERE market_id = $1`
	if _, err := databaseTx.ExecContext(ctx, updatePool, bet.MarketID, bet.Stake); err != nil {
		return nil, fmt.Errorf("update parimutuel pool: %w", err)
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
SELECT id, market_id, merchant_id, user_id, option, stake, currency, status, created_at, settled_at
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
		if err := rows.Scan(
			&bet.ID, &bet.MarketID, &bet.MerchantID, &bet.UserID, &bet.Option,
			&bet.Stake, &bet.Currency, &bet.Status, &bet.CreatedAt, &settledAt,
		); err != nil {
			return nil, 0, fmt.Errorf("scan parimutuel bet: %w", err)
		}
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
SELECT market_id, currency, total_stake, total_fees
FROM parimutuel_pools WHERE market_id = $1`
	rows, err := r.database.QueryContext(ctx, query, marketID)
	if err != nil {
		return nil, fmt.Errorf("get parimutuel pools: %w", err)
	}
	defer func() { _ = rows.Close() }()
	items := []Pool{}
	for rows.Next() {
		pool := Pool{}
		if err := rows.Scan(&pool.MarketID, &pool.Currency, &pool.TotalStake, &pool.TotalFees); err != nil {
			return nil, fmt.Errorf("scan parimutuel pool: %w", err)
		}
		items = append(items, pool)
	}
	return items, rows.Err()
}

// OptionStakes sums the active stake per option for one market. The totals
// feed the per-outcome implied odds shown in the hosted UI.
func (r *PostgresRepository) OptionStakes(ctx context.Context, marketID string) ([]OptionStake, error) {
	if r == nil || r.database == nil {
		return nil, errors.New("parimutuel database is not configured")
	}
	const query = `
SELECT option, SUM(stake)
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
		if err := rows.Scan(&item.Option, &item.Stake); err != nil {
			return nil, fmt.Errorf("scan parimutuel option stake: %w", err)
		}
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
