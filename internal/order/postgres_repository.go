package order

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/afun-game/predictmarket-saas/internal/market"
	"github.com/afun-game/predictmarket-saas/internal/wallet"
	"github.com/afun-game/predictmarket-saas/pkg/fixed"
	"github.com/afun-game/predictmarket-saas/pkg/types"
)

const orderColumns = `
    id, merchant_id, user_id, market_id, type, option, amount,
    filled_amount, currency, price, time_in_force, status, idempotency_key, created_at, filled_at`

type postgresRepository struct {
	database *sql.DB
}

func newPostgresRepository(database *sql.DB) *postgresRepository {
	return &postgresRepository{database: database}
}

func (r *postgresRepository) Place(ctx context.Context, incoming *types.Order) (float64, error) {
	return r.place(ctx, incoming, 0, false)
}

func (r *postgresRepository) PlaceWithLockedCollateral(
	ctx context.Context,
	incoming *types.Order,
	collateralCents int64,
) error {
	_, err := r.place(ctx, incoming, collateralCents, true)
	return err
}

func (r *postgresRepository) place(
	ctx context.Context,
	incoming *types.Order,
	collateralCents int64,
	lockCollateral bool,
) (float64, error) {
	databaseTx, err := r.database.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin order placement: %w", err)
	}
	defer func() {
		_ = databaseTx.Rollback() // No-op after commit; best-effort rollback on earlier returns.
	}()

	bookKey := incoming.MerchantID + "\x1f" + incoming.MarketID + "\x1f" +
		incoming.Option + "\x1f" + incoming.Currency
	if _, err := databaseTx.ExecContext(
		ctx,
		"SELECT pg_advisory_xact_lock(hashtextextended($1, 0))",
		bookKey,
	); err != nil {
		return 0, fmt.Errorf("lock order book: %w", err)
	}
	var marketActive bool
	err = databaseTx.QueryRowContext(
		ctx,
		`SELECT status = 'active' AND merchant_id = $2
FROM markets
WHERE id = $1
FOR UPDATE`,
		incoming.MarketID,
		incoming.MerchantID,
	).Scan(&marketActive)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !marketActive) {
		return 0, ErrInvalidMarket
	}
	if err != nil {
		return 0, fmt.Errorf("lock order market: %w", err)
	}
	if lockCollateral {
		if err := lockOrderWallet(ctx, databaseTx, incoming, collateralCents); err != nil {
			return 0, err
		}
	}
	if err := insertOrder(ctx, databaseTx, incoming); err != nil {
		return 0, err
	}
	candidates, err := queryMatchingOrders(ctx, databaseTx, incoming)
	if err != nil {
		return 0, err
	}
	var priceImprovement int64
	for _, maker := range candidates {
		incomingRemaining := storedShareUnits(incoming.Amount) - storedShareUnits(incoming.FilledAmount)
		if incomingRemaining == 0 {
			break
		}
		fillAmount := min(
			incomingRemaining,
			storedShareUnits(maker.Amount)-storedShareUnits(maker.FilledAmount),
		)
		if err := insertTrade(
			ctx,
			databaseTx,
			incoming.MarketID,
			maker.ID,
			incoming.ID,
			fillAmount,
			storedPriceUnits(maker.Price),
			incoming.CreatedAt,
		); err != nil {
			return 0, err
		}
		priceImprovement += priceImprovementRefundCents(
			incoming.Type,
			fixed.SharesToFloat(fillAmount),
			incoming.Price,
			maker.Price,
		)
		maker.FilledAmount = fixed.SharesToFloat(storedShareUnits(maker.FilledAmount) + fillAmount)
		incoming.FilledAmount = fixed.SharesToFloat(storedShareUnits(incoming.FilledAmount) + fillAmount)
		updateOrderFillStatus(maker, incoming.CreatedAt)
		if err := updateStoredOrder(ctx, databaseTx, maker); err != nil {
			return 0, err
		}
	}
	updateOrderFillStatus(incoming, incoming.CreatedAt)
	if incoming.TimeInForce == "ioc" && storedShareUnits(incoming.FilledAmount) < storedShareUnits(incoming.Amount) {
		incoming.Status = "cancelled"
	}
	if err := updateStoredOrder(ctx, databaseTx, incoming); err != nil {
		return 0, err
	}
	if storedShareUnits(incoming.FilledAmount) > 0 {
		if _, err := databaseTx.ExecContext(
			ctx,
			"UPDATE markets SET total_volume = total_volume + $2 WHERE id = $1",
			incoming.MarketID,
			fixed.FormatShares(storedShareUnits(incoming.FilledAmount)),
		); err != nil {
			return 0, fmt.Errorf("update matched market volume: %w", err)
		}
	}
	if lockCollateral {
		refundCents := priceImprovement
		if incoming.Status == "cancelled" {
			refundCents += requiredCollateralCents(
				incoming.Type,
				remainingShares(incoming.Amount, incoming.FilledAmount),
				incoming.Price,
			)
		}
		if refundCents > 0 {
			if err := unlockOrderWallet(ctx, databaseTx, incoming, refundCents); err != nil {
				return 0, err
			}
		}
	}
	if err := databaseTx.Commit(); err != nil {
		return 0, fmt.Errorf("commit order placement: %w", err)
	}
	return fixed.CentsToFloat(priceImprovement), nil
}

func (r *postgresRepository) Get(ctx context.Context, orderID string) (*types.Order, error) {
	query := "SELECT " + orderColumns + " FROM orders WHERE id = $1"
	return scanOrder(r.database.QueryRowContext(ctx, query, orderID))
}

func (r *postgresRepository) GetByIdempotency(
	ctx context.Context,
	merchantID string,
	key string,
) (*types.Order, error) {
	query := "SELECT " + orderColumns + " FROM orders WHERE merchant_id = $1 AND idempotency_key = $2"
	return scanOrder(r.database.QueryRowContext(ctx, query, merchantID, key))
}

func (r *postgresRepository) List(
	ctx context.Context,
	filters ListFilters,
) ([]*types.Order, int, error) {
	const whereClause = `
FROM orders
WHERE ($1 = '' OR merchant_id = NULLIF($1, '')::uuid)
  AND ($2 = '' OR user_id = $2)
  AND ($3 = '' OR market_id = NULLIF($3, '')::uuid)
  AND ($4 = '' OR status = $4)`
	var total int
	if err := r.database.QueryRowContext(
		ctx,
		"SELECT COUNT(*) "+whereClause,
		filters.MerchantID,
		filters.UserID,
		filters.MarketID,
		filters.Status,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count orders: %w", err)
	}
	query := "SELECT " + orderColumns + whereClause + `
ORDER BY created_at DESC, id
LIMIT $5 OFFSET $6`
	rows, err := r.database.QueryContext(
		ctx,
		query,
		filters.MerchantID,
		filters.UserID,
		filters.MarketID,
		filters.Status,
		filters.Limit,
		(filters.Page-1)*filters.Limit,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("query orders: %w", err)
	}
	defer func() { _ = rows.Close() }()
	values := make([]*types.Order, 0, filters.Limit)
	for rows.Next() {
		value, err := scanOrder(rows)
		if err != nil {
			return nil, 0, err
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate orders: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, 0, fmt.Errorf("close order rows: %w", err)
	}
	return values, total, nil
}

func (r *postgresRepository) ListAfter(
	ctx context.Context,
	filters ListFilters,
	cursor *Cursor,
) ([]*types.Order, error) {
	var cursorTime any
	var cursorID any
	if cursor == nil {
		cursorTime = nil
		cursorID = nil
	} else {
		cursorTime = cursor.CreatedAt
		cursorID = cursor.ID
	}
	query := "SELECT " + orderColumns + `
FROM orders
WHERE ($1 = '' OR merchant_id = $1::uuid)
  AND ($2 = '' OR user_id = $2)
  AND ($3 = '' OR market_id = $3::uuid)
  AND ($4 = '' OR status = $4)
  AND ($5::timestamp IS NULL OR (created_at, id) < ($5::timestamp, $6::uuid))
ORDER BY created_at DESC, id DESC
LIMIT $7`
	rows, err := r.database.QueryContext(
		ctx,
		query,
		filters.MerchantID,
		filters.UserID,
		filters.MarketID,
		filters.Status,
		cursorTime,
		cursorID,
		filters.Limit+1,
	)
	if err != nil {
		return nil, fmt.Errorf("query orders by cursor: %w", err)
	}
	defer func() { _ = rows.Close() }()

	values := make([]*types.Order, 0, filters.Limit+1)
	for rows.Next() {
		value, err := scanOrder(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate cursor orders: %w", err)
	}
	return values, nil
}

func (r *postgresRepository) Cancel(
	ctx context.Context,
	orderID string,
) (*types.Order, float64, error) {
	return r.cancel(ctx, orderID, false)
}

func (r *postgresRepository) CancelWithUnlock(ctx context.Context, orderID string) error {
	_, _, err := r.cancel(ctx, orderID, true)
	return err
}

func (r *postgresRepository) cancel(
	ctx context.Context,
	orderID string,
	unlockCollateral bool,
) (*types.Order, float64, error) {
	databaseTx, err := r.database.BeginTx(ctx, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("begin order cancellation: %w", err)
	}
	defer func() { _ = databaseTx.Rollback() }()
	query := "SELECT " + orderColumns + " FROM orders WHERE id = $1 FOR UPDATE"
	value, err := scanOrder(databaseTx.QueryRowContext(ctx, query, orderID))
	if err != nil {
		return nil, 0, err
	}
	if value.Status != "pending" && value.Status != "partial" {
		return nil, 0, ErrNotCancellable
	}
	remaining := remainingShares(value.Amount, value.FilledAmount)
	value.Status = "cancelled"
	if err := updateStoredOrder(ctx, databaseTx, value); err != nil {
		return nil, 0, err
	}
	if unlockCollateral {
		collateralCents := requiredCollateralCents(value.Type, remaining, value.Price)
		if collateralCents > 0 {
			if err := unlockOrderWallet(ctx, databaseTx, value, collateralCents); err != nil {
				return nil, 0, err
			}
		}
	}
	if err := databaseTx.Commit(); err != nil {
		return nil, 0, fmt.Errorf("commit order cancellation: %w", err)
	}
	return value, remaining, nil
}

func lockOrderWallet(
	ctx context.Context,
	databaseTx *sql.Tx,
	order *types.Order,
	collateralCents int64,
) error {
	const query = `
UPDATE wallets
SET balance = balance - $4::numeric,
    locked_balance = locked_balance + $4::numeric,
    updated_at = $5
WHERE merchant_id = $1 AND user_id = $2 AND currency = $3 AND balance >= $4::numeric`
	result, err := databaseTx.ExecContext(
		ctx,
		query,
		order.MerchantID,
		order.UserID,
		order.Currency,
		fixed.FormatCents(collateralCents),
		order.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("lock order wallet: %w", err)
	}
	return requireOrderWalletUpdate(ctx, databaseTx, result, order, false)
}

func unlockOrderWallet(
	ctx context.Context,
	databaseTx *sql.Tx,
	order *types.Order,
	collateralCents int64,
) error {
	const query = `
UPDATE wallets
SET balance = balance + $4::numeric,
    locked_balance = locked_balance - $4::numeric,
    updated_at = $5
WHERE merchant_id = $1 AND user_id = $2 AND currency = $3 AND locked_balance >= $4::numeric`
	result, err := databaseTx.ExecContext(
		ctx,
		query,
		order.MerchantID,
		order.UserID,
		order.Currency,
		fixed.FormatCents(collateralCents),
		order.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("unlock order wallet: %w", err)
	}
	return requireOrderWalletUpdate(ctx, databaseTx, result, order, true)
}

func requireOrderWalletUpdate(
	ctx context.Context,
	databaseTx *sql.Tx,
	result sql.Result,
	order *types.Order,
	locked bool,
) error {
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count updated order wallet rows: %w", err)
	}
	if rowsAffected > 0 {
		return nil
	}
	const query = `
SELECT balance, locked_balance
FROM wallets
WHERE merchant_id = $1 AND user_id = $2 AND currency = $3`
	var balance float64
	var lockedBalance float64
	err = databaseTx.QueryRowContext(
		ctx,
		query,
		order.MerchantID,
		order.UserID,
		order.Currency,
	).Scan(&balance, &lockedBalance)
	if errors.Is(err, sql.ErrNoRows) {
		return wallet.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("classify order wallet update: %w", err)
	}
	if locked {
		return wallet.ErrInsufficientLocked
	}
	return wallet.ErrInsufficientBalance
}

func (r *postgresRepository) GetOrderBook(
	ctx context.Context,
	marketID string,
) (*market.OrderBook, error) {
	const query = `
SELECT type, option, price, SUM(amount - filled_amount), COUNT(*)
FROM orders
WHERE market_id = $1 AND status IN ('pending', 'partial')
GROUP BY type, option, price`
	rows, err := r.database.QueryContext(ctx, query, marketID)
	if err != nil {
		return nil, fmt.Errorf("query order book levels: %w", err)
	}
	defer func() { _ = rows.Close() }()
	bids := []market.OrderBookEntry{}
	asks := []market.OrderBookEntry{}
	for rows.Next() {
		var side string
		var entry market.OrderBookEntry
		if err := rows.Scan(&side, &entry.Option, &entry.Price, &entry.Amount, &entry.Orders); err != nil {
			return nil, fmt.Errorf("scan order book level: %w", err)
		}
		entry.Amount = fixed.SharesToFloat(storedShareUnits(entry.Amount))
		entry.Price = fixed.PriceToFloat(storedPriceUnits(entry.Price))
		if side == "buy" {
			bids = append(bids, entry)
		} else {
			asks = append(asks, entry)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate order book levels: %w", err)
	}
	sortBookEntries(bids, true)
	sortBookEntries(asks, false)
	return &market.OrderBook{MarketID: marketID, Bids: bids, Asks: asks}, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanOrder(row rowScanner) (*types.Order, error) {
	value := &types.Order{}
	var idempotencyKey sql.NullString
	err := row.Scan(
		&value.ID,
		&value.MerchantID,
		&value.UserID,
		&value.MarketID,
		&value.Type,
		&value.Option,
		&value.Amount,
		&value.FilledAmount,
		&value.Currency,
		&value.Price,
		&value.TimeInForce,
		&value.Status,
		&idempotencyKey,
		&value.CreatedAt,
		&value.FilledAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan order: %w", err)
	}
	value.IdempotencyKey = idempotencyKey.String
	value.Amount = fixed.SharesToFloat(storedShareUnits(value.Amount))
	value.FilledAmount = fixed.SharesToFloat(storedShareUnits(value.FilledAmount))
	value.Price = fixed.PriceToFloat(storedPriceUnits(value.Price))
	return value, nil
}

func insertOrder(ctx context.Context, databaseTx *sql.Tx, value *types.Order) error {
	const query = `
INSERT INTO orders (
    id, merchant_id, user_id, market_id, type, option, amount,
    filled_amount, currency, price, time_in_force, status, idempotency_key, created_at, filled_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`
	amount := fixed.FormatShares(storedShareUnits(value.Amount))
	filledAmount := fixed.FormatShares(storedShareUnits(value.FilledAmount))
	price := fixed.FormatPrice(storedPriceUnits(value.Price))
	_, err := databaseTx.ExecContext(
		ctx,
		query,
		value.ID,
		value.MerchantID,
		value.UserID,
		value.MarketID,
		value.Type,
		value.Option,
		amount,
		filledAmount,
		value.Currency,
		price,
		value.TimeInForce,
		value.Status,
		nullableIdempotencyKey(value.IdempotencyKey),
		value.CreatedAt,
		value.FilledAt,
	)
	if postgresErrorCode(err) == "23505" {
		return ErrAlreadyExists
	}
	if err != nil {
		return fmt.Errorf("insert order: %w", err)
	}
	return nil
}

func insertTrade(
	ctx context.Context,
	databaseTx *sql.Tx,
	marketID string,
	makerOrderID string,
	takerOrderID string,
	shares int64,
	matchedPrice int64,
	createdAt time.Time,
) error {
	const query = `
INSERT INTO trades (
    market_id, maker_order_id, taker_order_id, shares, matched_price, created_at
) VALUES ($1, $2, $3, $4, $5, $6)`
	if _, err := databaseTx.ExecContext(
		ctx,
		query,
		marketID,
		makerOrderID,
		takerOrderID,
		fixed.FormatShares(shares),
		fixed.FormatPrice(matchedPrice),
		createdAt,
	); err != nil {
		return fmt.Errorf("insert matched trade: %w", err)
	}
	return nil
}

func queryMatchingOrders(
	ctx context.Context,
	databaseTx *sql.Tx,
	incoming *types.Order,
) ([]*types.Order, error) {
	priceCondition := "price <= $7"
	priceOrder := "price ASC"
	if incoming.Type == "sell" {
		priceCondition = "price >= $7"
		priceOrder = "price DESC"
	}
	query := "SELECT " + orderColumns + `
FROM orders
WHERE merchant_id = $1 AND market_id = $2 AND option = $3 AND currency = $4
  AND user_id <> $5 AND type <> $6 AND status IN ('pending', 'partial')
  AND ` + priceCondition + `
ORDER BY ` + priceOrder + `, created_at, id
FOR UPDATE`
	rows, err := databaseTx.QueryContext(
		ctx,
		query,
		incoming.MerchantID,
		incoming.MarketID,
		incoming.Option,
		incoming.Currency,
		incoming.UserID,
		incoming.Type,
		fixed.FormatPrice(storedPriceUnits(incoming.Price)),
	)
	if err != nil {
		return nil, fmt.Errorf("query matching orders: %w", err)
	}
	defer func() { _ = rows.Close() }()
	values := []*types.Order{}
	for rows.Next() {
		value, err := scanOrder(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate matching orders: %w", err)
	}
	return values, nil
}

func updateStoredOrder(ctx context.Context, databaseTx *sql.Tx, value *types.Order) error {
	const query = `
UPDATE orders
SET filled_amount = $2, status = $3, filled_at = $4
WHERE id = $1`
	if _, err := databaseTx.ExecContext(
		ctx,
		query,
		value.ID,
		fixed.FormatShares(storedShareUnits(value.FilledAmount)),
		value.Status,
		value.FilledAt,
	); err != nil {
		return fmt.Errorf("update matched order: %w", err)
	}
	return nil
}

func sortBookEntries(values []market.OrderBookEntry, bids bool) {
	sort.Slice(values, func(i, j int) bool {
		if values[i].Option != values[j].Option {
			return values[i].Option < values[j].Option
		}
		if bids {
			return values[i].Price > values[j].Price
		}
		return values[i].Price < values[j].Price
	})
}

func postgresErrorCode(err error) string {
	var postgresErr *pgconn.PgError
	if errors.As(err, &postgresErr) {
		return postgresErr.Code
	}
	return ""
}

func nullableIdempotencyKey(key string) any {
	if key == "" {
		return nil
	}
	return key
}
