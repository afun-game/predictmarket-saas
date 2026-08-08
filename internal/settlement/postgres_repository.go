package settlement

import (
	"context"
	"database/sql"
	"encoding/json"
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

func (r *postgresRepository) VoidMarket(
	ctx context.Context,
	marketID string,
	voidedAt time.Time,
) error {
	databaseTx, err := r.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin market void: %w", err)
	}
	defer func() { _ = databaseTx.Rollback() }()

	eventID, err := lockMarketForVoid(ctx, databaseTx, marketID)
	if err != nil {
		return err
	}
	marketType, merchantID, _, _, err := marketSettlementContext(ctx, databaseTx, marketID)
	if err != nil {
		return err
	}
	if marketType == "parimutuel" {
		if err := voidParimutuelMarket(ctx, databaseTx, marketID, merchantID, eventID, voidedAt); err != nil {
			return err
		}
		return databaseTx.Commit()
	}
	orders, err := lockVoidOrders(ctx, databaseTx, marketID)
	if err != nil {
		return err
	}
	calculateVoidRefunds(orders)
	if err := applyOrderVoids(ctx, databaseTx, marketID, eventID, orders, voidedAt); err != nil {
		return err
	}
	const insertVoid = `
INSERT INTO market_settlements (market_id, event_id, winning_option, settlement_type, settled_at)
VALUES ($1, $2, NULL, 'void', $3)`
	if _, err := databaseTx.ExecContext(ctx, insertVoid, marketID, eventID, voidedAt); err != nil {
		return fmt.Errorf("insert market void: %w", err)
	}
	if _, err := databaseTx.ExecContext(
		ctx,
		"UPDATE markets SET status = 'voided', settled_at = $2 WHERE id = $1",
		marketID,
		voidedAt,
	); err != nil {
		return fmt.Errorf("mark market voided: %w", err)
	}
	if err := enqueueMarketVoidedWebhook(ctx, databaseTx, marketID, eventID, voidedAt, orders); err != nil {
		return err
	}
	if err := databaseTx.Commit(); err != nil {
		return fmt.Errorf("commit market void: %w", err)
	}
	return nil
}

// lockMarketForVoid locks an unsettled market and returns its event ID.
func lockMarketForVoid(ctx context.Context, databaseTx *sql.Tx, marketID string) (string, error) {
	const query = `
SELECT m.event_id
FROM markets AS m
LEFT JOIN market_settlements AS s ON s.market_id = m.id
WHERE m.id = $1 AND s.market_id IS NULL
FOR UPDATE OF m`
	var eventID string
	err := databaseTx.QueryRowContext(ctx, query, marketID).Scan(&eventID)
	if errors.Is(err, sql.ErrNoRows) {
		// Distinguish a missing market from an already settled one.
		var exists bool
		if checkErr := databaseTx.QueryRowContext(
			ctx, "SELECT EXISTS (SELECT 1 FROM markets WHERE id = $1)", marketID,
		).Scan(&exists); checkErr != nil {
			return "", fmt.Errorf("check void market existence: %w", checkErr)
		}
		if !exists {
			return "", ErrMarketNotFound
		}
		return "", ErrMarketAlreadySettled
	}
	if err != nil {
		return "", fmt.Errorf("lock market for void: %w", err)
	}
	return eventID, nil
}

// lockVoidOrders locks every order and its wallet for a market being voided.
func lockVoidOrders(
	ctx context.Context,
	databaseTx *sql.Tx,
	marketID string,
) ([]*settlementOrder, error) {
	const query = `
SELECT o.id, w.id, o.merchant_id, o.user_id, o.type, o.option, o.currency, o.status,
       COALESCE(o.wallet_kind, 'user'), o.amount::text, o.filled_amount::text, o.price::text
FROM orders AS o
LEFT JOIN wallets AS w
  ON w.merchant_id = o.merchant_id
 AND w.user_id = o.user_id
 AND w.currency = o.currency
 AND w.kind = COALESCE(o.wallet_kind, 'user')
WHERE o.market_id = $1
ORDER BY o.id
FOR UPDATE OF o`
	rows, err := databaseTx.QueryContext(ctx, query, marketID)
	if err != nil {
		return nil, fmt.Errorf("lock void orders: %w", err)
	}
	defer func() { _ = rows.Close() }()
	orders := []*settlementOrder{}
	for rows.Next() {
		order := &settlementOrder{}
		var walletID sql.NullString
		var amount string
		var filled string
		var price string
		if err := rows.Scan(
			&order.id, &walletID, &order.merchantID, &order.userID, &order.side, &order.option, &order.currency,
			&order.status, &order.walletKind, &amount, &filled, &price,
		); err != nil {
			return nil, fmt.Errorf("scan void order: %w", err)
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
		orders = append(orders, order)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate void orders: %w", err)
	}
	if err := lockSettlementWallets(ctx, databaseTx, orders); err != nil {
		return nil, err
	}
	return orders, nil
}

// calculateVoidRefunds refunds the full collateral of every order, including
// the unfilled remainder that was locked at placement.
func calculateVoidRefunds(orders []*settlementOrder) {
	for _, order := range orders {
		order.refund = collateralCents(order.side, order.amount, order.price)
		order.lockedUse = new(big.Int).Set(order.refund)
	}
}

// applyOrderVoids credits refunds per wallet, emits shadow credits and webhooks,
// and marks every order voided.
func applyOrderVoids(
	ctx context.Context,
	databaseTx *sql.Tx,
	marketID string,
	eventID string,
	orders []*settlementOrder,
	voidedAt time.Time,
) error {
	walletRefunds := map[string]*big.Int{}
	for _, order := range orders {
		if walletRefunds[order.walletID] == nil {
			walletRefunds[order.walletID] = new(big.Int)
		}
		walletRefunds[order.walletID].Add(walletRefunds[order.walletID], order.refund)
	}
	for walletID, total := range walletRefunds {
		const updateWallet = `
UPDATE wallets
SET balance = balance + $2::numeric,
    locked_balance = locked_balance - $2::numeric,
    updated_at = $3
WHERE id = $1 AND locked_balance >= $2::numeric`
		result, err := databaseTx.ExecContext(ctx, updateWallet, walletID, formatCents(total), voidedAt)
		if err != nil {
			return fmt.Errorf("refund voided wallet %s: %w", walletID, err)
		}
		rowsAffected, err := result.RowsAffected()
		if err != nil || rowsAffected != 1 {
			return fmt.Errorf("voided wallet %s has insufficient locked balance", walletID)
		}
	}
	for _, order := range orders {
		if order.walletKind == "shadow" && order.refund.Sign() > 0 {
			if err := releaseShadowStake(ctx, databaseTx, order.walletID, order.refund, voidedAt); err != nil {
				return err
			}
			if err := enqueueSettlementShadowCredit(
				ctx, databaseTx, order.merchantID, order.userID, order.currency, order.id, order.walletID,
				marketID, eventID, order.refund, "void", voidedAt,
			); err != nil {
				return err
			}
		}
		if _, err := databaseTx.ExecContext(
			ctx, "UPDATE orders SET status = 'voided' WHERE id = $1", order.id,
		); err != nil {
			return fmt.Errorf("mark order voided: %w", err)
		}
		if err := enqueueOrderVoidedWebhook(
			ctx, databaseTx, marketID, eventID, order, voidedAt,
		); err != nil {
			return err
		}
	}
	return nil
}

func enqueueOrderVoidedWebhook(
	ctx context.Context,
	databaseTx *sql.Tx,
	marketID string,
	eventID string,
	order *settlementOrder,
	voidedAt time.Time,
) error {
	payload, err := json.Marshal(map[string]any{
		"webhook_id": "",
		"type":       "order.voided",
		"data": map[string]any{
			"market_id":      marketID,
			"event_id":       eventID,
			"winning_option": nil,
			"order_id":       order.id,
			"user_id":        order.userID,
			"stake":          formatCents(order.refund),
			"payout":         "0.00",
			"refund":         formatCents(order.refund),
			"currency":       order.currency,
			"settled_at":     voidedAt.UTC().Format(time.RFC3339),
		},
	})
	if err != nil {
		return fmt.Errorf("marshal order voided webhook: %w", err)
	}
	return insertWebhookOutbox(ctx, databaseTx, order.merchantID, "order.voided", payload, voidedAt)
}

func enqueueMarketVoidedWebhook(
	ctx context.Context,
	databaseTx *sql.Tx,
	marketID string,
	eventID string,
	voidedAt time.Time,
	orders []*settlementOrder,
) error {
	if len(orders) == 0 {
		return nil
	}
	payload, err := json.Marshal(map[string]any{
		"webhook_id": "",
		"type":       "market.voided",
		"data": map[string]any{
			"market_id":      marketID,
			"event_id":       eventID,
			"winning_option": nil,
			"settled_at":     voidedAt.UTC().Format(time.RFC3339),
		},
	})
	if err != nil {
		return fmt.Errorf("marshal market voided webhook: %w", err)
	}
	seen := map[string]struct{}{}
	for _, order := range orders {
		if _, exists := seen[order.merchantID]; exists {
			continue
		}
		seen[order.merchantID] = struct{}{}
		if err := insertWebhookOutbox(ctx, databaseTx, order.merchantID, "market.voided", payload, voidedAt); err != nil {
			return err
		}
	}
	return nil
}

// SettleMarket settles one market immediately with the given winning
// option, regardless of the owning event's status. The market must be
// unsettled and the option must be offered by the market.
func (r *postgresRepository) SettleMarket(
	ctx context.Context,
	marketID string,
	winningOption string,
	settledAt time.Time,
) error {
	var eventID string
	if err := r.database.QueryRowContext(
		ctx,
		"SELECT event_id FROM markets WHERE id = $1",
		marketID,
	).Scan(&eventID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrMarketNotFound
		}
		return fmt.Errorf("get settle market event: %w", err)
	}
	var alreadySettled bool
	if err := r.database.QueryRowContext(
		ctx,
		"SELECT EXISTS (SELECT 1 FROM market_settlements WHERE market_id = $1)",
		marketID,
	).Scan(&alreadySettled); err != nil {
		return fmt.Errorf("check single market settlement: %w", err)
	}
	if alreadySettled {
		return ErrMarketAlreadySettled
	}
	return r.settleMarket(ctx, marketID, eventID, winningOption, settledAt)
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
	marketType, merchantID, merchantFeeRate, platformFeeRate, err := marketSettlementContext(ctx, databaseTx, marketID)
	if err != nil {
		return err
	}
	if marketType == "parimutuel" {
		return settleParimutuelMarket(ctx, databaseTx, marketID, merchantID, eventID, winningOption, merchantFeeRate, platformFeeRate, settledAt)
	}
	orders, err := lockSettlementOrders(ctx, databaseTx, marketID)
	if err != nil {
		return err
	}
	calculatePayouts(orders, winningOption)
	if err := assertPayoutConservation(orders); err != nil {
		return err
	}
	for _, order := range orders {
		if err := applyOrderSettlement(ctx, databaseTx, marketID, eventID, winningOption, order, settledAt); err != nil {
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
	if err := enqueueMarketSettledWebhook(ctx, databaseTx, marketID, eventID, winningOption, settledAt, orders); err != nil {
		return err
	}
	return nil
}

func lockSettlementOrders(
	ctx context.Context,
	databaseTx *sql.Tx,
	marketID string,
) ([]*settlementOrder, error) {
	const query = `
SELECT o.id, w.id, o.merchant_id, o.user_id, o.type, o.option, o.currency, o.status,
       COALESCE(o.wallet_kind, 'user'), o.amount::text, o.filled_amount::text, o.price::text,
       m.merchant_fee_rate::text, m.platform_fee_rate::text
FROM orders AS o
LEFT JOIN wallets AS w
  ON w.merchant_id = o.merchant_id
 AND w.user_id = o.user_id
 AND w.currency = o.currency
 AND w.kind = COALESCE(o.wallet_kind, 'user')
JOIN markets AS m ON m.id = o.market_id
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
		var merchantFeeRateStr string
		var platformFeeRateStr string
		if err := rows.Scan(
			&order.id, &walletID, &order.merchantID, &order.userID, &order.side, &order.option, &order.currency,
			&order.status, &order.walletKind, &amount, &filled, &price, &merchantFeeRateStr, &platformFeeRateStr,
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
		order.merchantFeeRate, err = parseFixed(merchantFeeRateStr, 6)
		if err != nil {
			return nil, fmt.Errorf("parse merchant fee rate: %w", err)
		}
		order.platformFeeRate, err = parseFixed(platformFeeRateStr, 6)
		if err != nil {
			return nil, fmt.Errorf("parse platform fee rate: %w", err)
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
	totalMerchantFee := make(map[string]*big.Int)
	totalPlatformFee := make(map[string]*big.Int)
	for _, order := range orders {
		if totalStake[order.currency] == nil {
			totalStake[order.currency] = new(big.Int)
			totalPayout[order.currency] = new(big.Int)
			totalMerchantFee[order.currency] = new(big.Int)
			totalPlatformFee[order.currency] = new(big.Int)
		}
		totalStake[order.currency].Add(totalStake[order.currency], order.stake)
		totalPayout[order.currency].Add(totalPayout[order.currency], order.payout)
		totalMerchantFee[order.currency].Add(totalMerchantFee[order.currency], order.merchantFeeCents)
		totalPlatformFee[order.currency].Add(totalPlatformFee[order.currency], order.platformFeeCents)
	}
	for currency, stake := range totalStake {
		expected := new(big.Int).Set(totalPayout[currency])
		expected.Add(expected, totalMerchantFee[currency])
		expected.Add(expected, totalPlatformFee[currency])
		if stake.Cmp(expected) != 0 {
			return fmt.Errorf(
				"settlement payout does not conserve the %s pool: stake %s, payout+fees %s (payout %s, merchant_fee %s, platform_fee %s)",
				currency,
				stake,
				expected,
				totalPayout[currency],
				totalMerchantFee[currency],
				totalPlatformFee[currency],
			)
		}
	}
	return nil
}

func applyOrderSettlement(
	ctx context.Context,
	databaseTx *sql.Tx,
	marketID string,
	eventID string,
	winningOption string,
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
	if order.walletKind == "shadow" {
		releaseAmount := order.stake
		if releaseAmount.Sign() == 0 {
			releaseAmount = order.refund
		}
		if releaseAmount.Sign() > 0 {
			if err := releaseShadowStake(ctx, databaseTx, order.walletID, releaseAmount, settledAt); err != nil {
				return err
			}
		}
		if order.refund.Sign() > 0 {
			if err := enqueueSettlementShadowCredit(
				ctx, databaseTx, order.merchantID, order.userID, order.currency, order.id, order.walletID,
				marketID, eventID, order.refund, "refund_cancel", settledAt,
			); err != nil {
				return err
			}
		}
		if order.payout.Sign() > 0 {
			if err := enqueueSettlementShadowCredit(
				ctx, databaseTx, order.merchantID, order.userID, order.currency, order.id, order.walletID,
				marketID, eventID, order.payout, "payout", settledAt,
			); err != nil {
				return err
			}
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
		if err := enqueueOrderSettledWebhook(
			ctx, databaseTx, marketID, eventID, winningOption, order, settledAt,
		); err != nil {
			return err
		}
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
	if order.merchantFeeCents.Sign() > 0 {
		if err := recordFee(
			ctx, databaseTx, order.merchantID, marketID,
			"merchant", order.merchantFeeRate, order.merchantFeeCents, order.currency, settledAt,
		); err != nil {
			return err
		}
	}
	if order.platformFeeCents.Sign() > 0 {
		if err := recordFee(
			ctx, databaseTx, order.merchantID, marketID,
			"platform", order.platformFeeRate, order.platformFeeCents, order.currency, settledAt,
		); err != nil {
			return err
		}
	}

	if err := enqueueOrderSettledWebhook(
		ctx, databaseTx, marketID, eventID, winningOption, order, settledAt,
	); err != nil {
		return err
	}
	return nil
}

// releaseShadowStake clears a settled position's stake from the shadow
// wallet. Every participant (winner and loser) releases exactly their own
// stake: winners are paid in cash through credit callbacks, while losing
// stakes stay with the merchant pool. Checking the balance here keeps the
// shadow ledger from going negative.
func releaseShadowStake(
	ctx context.Context,
	databaseTx *sql.Tx,
	walletID string,
	amount *big.Int,
	settledAt time.Time,
) error {
	const debitQuery = `
UPDATE wallets
SET balance = balance - $2::numeric, updated_at = $3
WHERE id = $1 AND balance >= $2::numeric`
	result, err := databaseTx.ExecContext(
		ctx,
		debitQuery,
		walletID,
		formatCents(amount),
		settledAt,
	)
	if err != nil {
		return fmt.Errorf("reserve settlement shadow credit: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil || rowsAffected != 1 {
		return fmt.Errorf("settlement shadow credit exceeds available balance")
	}
	return nil
}

// enqueueSettlementShadowCredit queues a signed credit callback for the
// merchant after the caller has released the shadow stake. orderID may be
// empty for parimutuel bets, which have no order row (both columns are
// nullable).
func enqueueSettlementShadowCredit(
	ctx context.Context,
	databaseTx *sql.Tx,
	merchantID string,
	userID string,
	currency string,
	orderID string,
	walletID string,
	marketID string,
	eventID string,
	amount *big.Int,
	reason string,
	settledAt time.Time,
) error {
	const outboxQuery = `
WITH transaction AS (
    INSERT INTO seamless_transactions (
        transaction_id, merchant_id, user_id, currency, type, reason, amount,
        order_id, status, created_at, updated_at
    ) VALUES (
        gen_random_uuid(), $1, $2, $3, 'credit', $4, $5::numeric,
        NULLIF($6, '')::uuid, 'pending_delivery', $7, $7
    )
    RETURNING transaction_id
)
INSERT INTO callback_outbox (
    merchant_id, transaction_id, user_id, currency, type, reason, amount,
    order_id, market_id, event_id, status, next_attempt_at, created_at, updated_at
)
SELECT $1, transaction_id, $2, $3, 'credit', $4, $5::numeric,
       NULLIF($6, '')::uuid, $8, $9, 'pending', $7, $7, $7
FROM transaction`
	if _, err := databaseTx.ExecContext(
		ctx,
		outboxQuery,
		merchantID,
		userID,
		currency,
		reason,
		formatCents(amount),
		orderID,
		settledAt,
		marketID,
		eventID,
	); err != nil {
		return fmt.Errorf("enqueue settlement shadow credit: %w", err)
	}
	return nil
}

func enqueueOrderSettledWebhook(
	ctx context.Context,
	databaseTx *sql.Tx,
	marketID string,
	eventID string,
	winningOption string,
	order *settlementOrder,
	settledAt time.Time,
) error {
	payload, err := json.Marshal(map[string]any{
		"webhook_id": "",
		"type":       "order.settled",
		"data": map[string]any{
			"market_id":      marketID,
			"event_id":       eventID,
			"winning_option": winningOption,
			"order_id":       order.id,
			"user_id":        order.userID,
			"stake":          formatCents(order.stake),
			"payout":         formatCents(order.payout),
			"currency":       order.currency,
			"settled_at":     settledAt.UTC().Format(time.RFC3339),
		},
	})
	if err != nil {
		return fmt.Errorf("marshal order settled webhook: %w", err)
	}
	return insertWebhookOutbox(ctx, databaseTx, order.merchantID, "order.settled", payload, settledAt)
}

func enqueueMarketSettledWebhook(
	ctx context.Context,
	databaseTx *sql.Tx,
	marketID string,
	eventID string,
	winningOption string,
	settledAt time.Time,
	orders []*settlementOrder,
) error {
	if len(orders) == 0 {
		return nil
	}
	payload, err := json.Marshal(map[string]any{
		"webhook_id": "",
		"type":       "market.settled",
		"data": map[string]any{
			"market_id":      marketID,
			"event_id":       eventID,
			"winning_option": winningOption,
			"settled_at":     settledAt.UTC().Format(time.RFC3339),
		},
	})
	if err != nil {
		return fmt.Errorf("marshal market settled webhook: %w", err)
	}
	// One market.settled webhook per distinct merchant owning orders on the market.
	seen := map[string]struct{}{}
	for _, order := range orders {
		if _, exists := seen[order.merchantID]; exists {
			continue
		}
		seen[order.merchantID] = struct{}{}
		if err := insertWebhookOutbox(ctx, databaseTx, order.merchantID, "market.settled", payload, settledAt); err != nil {
			return err
		}
	}
	return nil
}

func insertWebhookOutbox(
	ctx context.Context,
	databaseTx *sql.Tx,
	merchantID string,
	eventType string,
	payload []byte,
	createdAt time.Time,
) error {
	// Only enqueue when the merchant has a delivery target configured.
	const configured = `
SELECT EXISTS (
    SELECT 1 FROM merchants
    WHERE id = $1
      AND (
        COALESCE(webhook_url, '') <> ''
        OR COALESCE(callback_url, '') <> ''
      )
      AND (
        COALESCE(array_length(webhook_events, 1), 0) = 0
        OR $2 = ANY(webhook_events)
      )
)`
	var ready bool
	if err := databaseTx.QueryRowContext(ctx, configured, merchantID, eventType).Scan(&ready); err != nil {
		return fmt.Errorf("check merchant webhook configuration: %w", err)
	}
	if !ready {
		return nil
	}
	const insert = `
WITH outbox AS (
    SELECT gen_random_uuid() AS id
)
INSERT INTO webhook_outbox (
    id, merchant_id, event_type, payload, status, next_attempt_at, created_at, updated_at
)
SELECT outbox.id, $1, $2,
       jsonb_set($3::jsonb, '{webhook_id}', to_jsonb(outbox.id::text), true),
       'pending', $4, $4, $4
FROM outbox`
	if _, err := databaseTx.ExecContext(ctx, insert, merchantID, eventType, string(payload), createdAt); err != nil {
		return fmt.Errorf("insert webhook outbox: %w", err)
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
) VALUES (gen_random_uuid(), $1, $2, $3, $4, NULLIF($5, '')::uuid, 'completed', $6)`
	if _, err := databaseTx.ExecContext(
		ctx, query, walletID, typeName, formatCents(amount), currency, orderID, createdAt,
	); err != nil {
		return fmt.Errorf("insert %s settlement transaction: %w", typeName, err)
	}
	return nil
}
