package settlement

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"time"
)

type postgresRepository struct {
	database *sql.DB
}

func newPostgresRepository(database *sql.DB) *postgresRepository {
	return &postgresRepository{database: database}
}

func (r *postgresRepository) SettleEvent(
	ctx context.Context,
	eventID string,
	settledAt time.Time,
) error {
	winningOption, err := resolvedEventOutcome(ctx, r.database, eventID)
	if err != nil {
		return err
	}
	marketIDs, err := unsettledMarketIDs(ctx, r.database, eventID)
	if err != nil {
		return err
	}
	var settlementErrors error
	for _, marketID := range marketIDs {
		if err := r.settleMarket(ctx, marketID, eventID, winningOption, settledAt); err != nil {
			if errors.Is(err, ErrOutcomeNotOption) || errors.Is(err, ErrOrderWalletNotFound) {
				settlementErrors = errors.Join(settlementErrors, err)
				continue
			}
			return err
		}
	}
	return settlementErrors
}

type queryRower interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func resolvedEventOutcome(ctx context.Context, database queryRower, eventID string) (string, error) {
	const query = `SELECT status, COALESCE(outcome, '') FROM events WHERE id = $1`
	var status string
	var outcome string
	err := database.QueryRowContext(ctx, query, eventID).Scan(&status, &outcome)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrEventNotFound
	}
	if err != nil {
		return "", fmt.Errorf("get settlement event: %w", err)
	}
	if status != "resolved" || outcome == "" {
		return "", ErrEventUnresolved
	}
	return outcome, nil
}

func unsettledMarketIDs(
	ctx context.Context,
	database *sql.DB,
	eventID string,
) ([]string, error) {
	const query = `
SELECT m.id
FROM markets AS m
LEFT JOIN market_settlements AS s ON s.market_id = m.id
WHERE m.event_id = $1 AND s.market_id IS NULL
ORDER BY m.id`
	rows, err := database.QueryContext(ctx, query, eventID)
	if err != nil {
		return nil, fmt.Errorf("list unsettled markets: %w", err)
	}
	defer func() { _ = rows.Close() }()
	marketIDs := []string{}
	for rows.Next() {
		var marketID string
		if err := rows.Scan(&marketID); err != nil {
			return nil, fmt.Errorf("scan unsettled market: %w", err)
		}
		marketIDs = append(marketIDs, marketID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate unsettled markets: %w", err)
	}
	return marketIDs, nil
}

func (r *postgresRepository) settleMarket(
	ctx context.Context,
	marketID string,
	eventID string,
	winningOption string,
	settledAt time.Time,
) error {
	databaseTx, err := r.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin market settlement: %w", err)
	}
	defer func() { _ = databaseTx.Rollback() }()

	settled, err := lockMarketForSettlement(ctx, databaseTx, marketID, eventID, winningOption)
	if err != nil {
		return err
	}
	if settled {
		return nil
	}
	if err := settleMarket(ctx, databaseTx, marketID, eventID, winningOption, settledAt); err != nil {
		return err
	}
	if err := databaseTx.Commit(); err != nil {
		return fmt.Errorf("commit market settlement: %w", err)
	}
	return nil
}

func lockMarketForSettlement(
	ctx context.Context,
	databaseTx *sql.Tx,
	marketID string,
	eventID string,
	winningOption string,
) (bool, error) {
	const marketQuery = `
SELECT options ? $3
FROM markets
WHERE id = $1 AND event_id = $2
FOR UPDATE`
	var validOutcome bool
	err := databaseTx.QueryRowContext(
		ctx,
		marketQuery,
		marketID,
		eventID,
		winningOption,
	).Scan(&validOutcome)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("lock settlement market: %w", err)
	}
	if !validOutcome {
		return false, fmt.Errorf("%w: market %s", ErrOutcomeNotOption, marketID)
	}

	const settlementQuery = `SELECT EXISTS (SELECT 1 FROM market_settlements WHERE market_id = $1)`
	var settled bool
	if err := databaseTx.QueryRowContext(ctx, settlementQuery, marketID).Scan(&settled); err != nil {
		return false, fmt.Errorf("check market settlement: %w", err)
	}
	return settled, nil
}

func settleMarket(
	ctx context.Context,
	databaseTx *sql.Tx,
	marketID string,
	eventID string,
	winningOption string,
	settledAt time.Time,
) error {
	orders, err := lockSettlementOrders(ctx, databaseTx, marketID)
	if err != nil {
		return err
	}
	calculatePayouts(orders, winningOption)
	if err := assertPayoutConservation(orders); err != nil {
		return err
	}
	for _, order := range orders {
		if err := applyOrderSettlement(ctx, databaseTx, marketID, order, settledAt); err != nil {
			return err
		}
	}
	const insertSettlement = `
INSERT INTO market_settlements (market_id, event_id, winning_option, settled_at)
VALUES ($1, $2, $3, $4)`
	if _, err := databaseTx.ExecContext(
		ctx, insertSettlement, marketID, eventID, winningOption, settledAt,
	); err != nil {
		return fmt.Errorf("insert market settlement: %w", err)
	}
	if _, err := databaseTx.ExecContext(
		ctx,
		"UPDATE markets SET status = 'settled', settled_at = $2 WHERE id = $1",
		marketID,
		settledAt,
	); err != nil {
		return fmt.Errorf("mark market settled: %w", err)
	}
	return nil
}

func lockSettlementOrders(
	ctx context.Context,
	databaseTx *sql.Tx,
	marketID string,
) ([]*settlementOrder, error) {
	const query = `
SELECT o.id, w.id, o.type, o.option, o.currency, o.status,
       o.amount::text, o.filled_amount::text, o.price::text
FROM orders AS o
LEFT JOIN wallets AS w
  ON w.merchant_id = o.merchant_id
 AND w.user_id = o.user_id
 AND w.currency = o.currency
WHERE o.market_id = $1
ORDER BY o.id
FOR UPDATE OF o`
	rows, err := databaseTx.QueryContext(ctx, query, marketID)
	if err != nil {
		return nil, fmt.Errorf("lock settlement orders: %w", err)
	}
	defer func() { _ = rows.Close() }()
	orders := []*settlementOrder{}
	ordersByID := make(map[string]*settlementOrder)
	for rows.Next() {
		order := &settlementOrder{}
		var walletID sql.NullString
		var amount string
		var filled string
		var price string
		if err := rows.Scan(
			&order.id, &walletID, &order.side, &order.option, &order.currency,
			&order.status, &amount, &filled, &price,
		); err != nil {
			return nil, fmt.Errorf("scan settlement order: %w", err)
		}
		if !walletID.Valid {
			return nil, fmt.Errorf("%w: order %s", ErrOrderWalletNotFound, order.id)
		}
		order.walletID = walletID.String
		order.amount, err = parseShares(amount)
		if err != nil {
			return nil, err
		}
		order.filled, err = parseShares(filled)
		if err != nil {
			return nil, err
		}
		order.price, err = parseFixed(price, 6)
		if err != nil {
			return nil, err
		}
		order.stake = new(big.Int)
		orders = append(orders, order)
		ordersByID[order.id] = order
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate settlement orders: %w", err)
	}
	if err := lockSettlementWallets(ctx, databaseTx, orders); err != nil {
		return nil, err
	}
	if err := lockTradeStakes(ctx, databaseTx, marketID, ordersByID); err != nil {
		return nil, err
	}
	return orders, nil
}

func lockSettlementWallets(
	ctx context.Context,
	databaseTx *sql.Tx,
	orders []*settlementOrder,
) error {
	walletIDs := []string{}
	expectedWallets := map[string]struct{}{}
	for _, order := range orders {
		if _, exists := expectedWallets[order.walletID]; exists {
			continue
		}
		expectedWallets[order.walletID] = struct{}{}
		walletIDs = append(walletIDs, order.walletID)
	}
	if len(walletIDs) == 0 {
		return nil
	}
	sort.Strings(walletIDs)

	const query = `
SELECT id
FROM wallets
WHERE id = ANY($1)
ORDER BY id
FOR UPDATE`
	rows, err := databaseTx.QueryContext(ctx, query, walletIDs)
	if err != nil {
		return fmt.Errorf("lock settlement wallets: %w", err)
	}
	defer func() { _ = rows.Close() }()

	lockedWallets := map[string]struct{}{}
	for rows.Next() {
		var walletID string
		if err := rows.Scan(&walletID); err != nil {
			return fmt.Errorf("scan settlement wallet: %w", err)
		}
		lockedWallets[walletID] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate settlement wallets: %w", err)
	}
	if len(lockedWallets) != len(expectedWallets) {
		return ErrOrderWalletNotFound
	}
	return nil
}

func lockTradeStakes(
	ctx context.Context,
	databaseTx *sql.Tx,
	marketID string,
	ordersByID map[string]*settlementOrder,
) error {
	const query = `
SELECT maker_order_id, taker_order_id, shares::text, matched_price::text
FROM trades
WHERE market_id = $1
ORDER BY id
FOR UPDATE`
	rows, err := databaseTx.QueryContext(ctx, query, marketID)
	if err != nil {
		return fmt.Errorf("lock settlement trades: %w", err)
	}
	defer func() { _ = rows.Close() }()

	matchedShares := make(map[string]*big.Int, len(ordersByID))
	for rows.Next() {
		var makerOrderID string
		var takerOrderID string
		var sharesValue string
		var matchedPriceValue string
		if err := rows.Scan(&makerOrderID, &takerOrderID, &sharesValue, &matchedPriceValue); err != nil {
			return fmt.Errorf("scan settlement trade: %w", err)
		}
		maker := ordersByID[makerOrderID]
		taker := ordersByID[takerOrderID]
		if maker == nil || taker == nil {
			return errors.New("settlement trade references an order outside its market")
		}
		shares, err := parseShares(sharesValue)
		if err != nil {
			return err
		}
		matchedPrice, err := parseFixed(matchedPriceValue, 6)
		if err != nil {
			return err
		}
		buyer, seller, err := tradeParties(maker, taker)
		if err != nil {
			return err
		}
		buyerStake := collateralCents("buy", shares, matchedPrice)
		sellerStake := new(big.Int).Sub(payoutCents(shares), buyerStake)
		buyer.stake.Add(buyer.stake, buyerStake)
		seller.stake.Add(seller.stake, sellerStake)
		addMatchedShares(matchedShares, buyer.id, shares)
		addMatchedShares(matchedShares, seller.id, shares)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate settlement trades: %w", err)
	}
	for orderID, order := range ordersByID {
		matched := matchedShares[orderID]
		if matched == nil {
			matched = new(big.Int)
		}
		if matched.Cmp(order.filled) != 0 {
			return fmt.Errorf(
				"order %s filled shares do not match its persisted trades",
				orderID,
			)
		}
	}
	return nil
}

func tradeParties(
	maker *settlementOrder,
	taker *settlementOrder,
) (*settlementOrder, *settlementOrder, error) {
	if maker.side == "buy" && taker.side == "sell" {
		return maker, taker, nil
	}
	if maker.side == "sell" && taker.side == "buy" {
		return taker, maker, nil
	}
	return nil, nil, errors.New("settlement trade does not have a buy and a sell order")
}

func addMatchedShares(values map[string]*big.Int, orderID string, shares *big.Int) {
	if values[orderID] == nil {
		values[orderID] = new(big.Int)
	}
	values[orderID].Add(values[orderID], shares)
}

func assertPayoutConservation(orders []*settlementOrder) error {
	totalStake := make(map[string]*big.Int)
	totalPayout := make(map[string]*big.Int)
	for _, order := range orders {
		if totalStake[order.currency] == nil {
			totalStake[order.currency] = new(big.Int)
			totalPayout[order.currency] = new(big.Int)
		}
		totalStake[order.currency].Add(totalStake[order.currency], order.stake)
		totalPayout[order.currency].Add(totalPayout[order.currency], order.payout)
	}
	for currency, stake := range totalStake {
		if stake.Cmp(totalPayout[currency]) != 0 {
			return fmt.Errorf(
				"settlement payout does not conserve the %s pool: stake %s, payout %s",
				currency,
				stake,
				totalPayout[currency],
			)
		}
	}
	return nil
}

func applyOrderSettlement(
	ctx context.Context,
	databaseTx *sql.Tx,
	marketID string,
	order *settlementOrder,
	settledAt time.Time,
) error {
	if order.lockedUse.Sign() > 0 || order.refund.Sign() > 0 || order.payout.Sign() > 0 {
		const updateWallet = `
UPDATE wallets
SET balance = balance + $2::numeric + $3::numeric,
    locked_balance = locked_balance - $4::numeric,
    updated_at = $5
WHERE id = $1 AND locked_balance >= $4::numeric`
		result, err := databaseTx.ExecContext(
			ctx,
			updateWallet,
			order.walletID,
			formatCents(order.refund),
			formatCents(order.payout),
			formatCents(order.lockedUse),
			settledAt,
		)
		if err != nil {
			return fmt.Errorf("update settled wallet for order %s: %w", order.id, err)
		}
		rowsAffected, err := result.RowsAffected()
		if err != nil || rowsAffected != 1 {
			return fmt.Errorf("settled wallet for order %s has insufficient locked balance", order.id)
		}
	}
	if order.status == "pending" || order.status == "partial" {
		if _, err := databaseTx.ExecContext(
			ctx, "UPDATE orders SET status = 'cancelled' WHERE id = $1", order.id,
		); err != nil {
			return fmt.Errorf("cancel settled order remainder: %w", err)
		}
	}
	if order.filled.Sign() == 0 {
		return nil
	}
	const payoutQuery = `
INSERT INTO settlement_payouts (
    market_id, order_id, wallet_id, currency, stake, payout, created_at
) VALUES ($1, $2, $3, $4, $5, $6, $7)`
	if _, err := databaseTx.ExecContext(
		ctx, payoutQuery, marketID, order.id, order.walletID, order.currency,
		formatCents(order.stake), formatCents(order.payout), settledAt,
	); err != nil {
		return fmt.Errorf("insert order settlement payout: %w", err)
	}
	if err := insertSettlementTransaction(
		ctx, databaseTx, order.walletID, "bet", order.stake, order.currency, order.id, settledAt,
	); err != nil {
		return err
	}
	if order.payout.Sign() > 0 {
		if err := insertSettlementTransaction(
			ctx, databaseTx, order.walletID, "win", order.payout, order.currency, order.id, settledAt,
		); err != nil {
			return err
		}
	}
	return nil
}

func insertSettlementTransaction(
	ctx context.Context,
	databaseTx *sql.Tx,
	walletID string,
	typeName string,
	amount *big.Int,
	currency string,
	orderID string,
	createdAt time.Time,
) error {
	const query = `
INSERT INTO transactions (
    id, wallet_id, type, amount, currency, related_order_id, status, created_at
) VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, 'completed', $6)`
	if _, err := databaseTx.ExecContext(
		ctx, query, walletID, typeName, formatCents(amount), currency, orderID, createdAt,
	); err != nil {
		return fmt.Errorf("insert %s settlement transaction: %w", typeName, err)
	}
	return nil
}
