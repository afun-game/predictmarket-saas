package v2query

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/afun-game/predictmarket-saas/pkg/types"
)

func (s *implementation) ListTransactions(
	ctx context.Context,
	filters TransactionFilters,
) (*TransactionPage, error) {
	filters, cursor, err := normalizeTransactionFilters(filters)
	if err != nil {
		return nil, err
	}
	if err := requireMerchant(ctx, s.database, filters.MerchantID); err != nil {
		return nil, err
	}
	cursorTime, cursorID := cursorValues(cursor)
	const query = `
WITH ledger AS (
    SELECT t.id, t.wallet_id, w.merchant_id, w.user_id, t.type,
           t.amount, t.currency, t.related_order_id, t.status, t.created_at
    FROM transactions AS t
    JOIN wallets AS w ON w.id = t.wallet_id
    UNION ALL
    SELECT st.transaction_id AS id, NULL::uuid AS wallet_id,
           st.merchant_id, st.user_id, st.type, st.amount, st.currency,
           st.order_id AS related_order_id, st.status, st.created_at
    FROM seamless_transactions AS st
)
SELECT id, COALESCE(wallet_id::text, ''), user_id, type, amount::text,
       currency, related_order_id, status, created_at
FROM ledger
WHERE merchant_id = $1
  AND ($2 = '' OR user_id = $2)
  AND ($3 = '' OR type = $3)
  AND ($4::timestamp IS NULL OR created_at >= $4)
  AND ($5::timestamp IS NULL OR created_at <= $5)
  AND ($6::timestamp IS NULL OR (created_at, id) < ($6::timestamp, $7::uuid))
ORDER BY created_at DESC, id DESC
LIMIT $8`
	rows, err := s.database.QueryContext(
		ctx,
		query,
		filters.MerchantID,
		filters.UserID,
		filters.Type,
		filters.From,
		filters.To,
		cursorTime,
		cursorID,
		filters.Limit+1,
	)
	if err != nil {
		return nil, fmt.Errorf("query V2 transactions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	values := make([]Transaction, 0, filters.Limit+1)
	for rows.Next() {
		value := Transaction{}
		var relatedOrderID sql.NullString
		if err := rows.Scan(
			&value.ID,
			&value.WalletID,
			&value.UserID,
			&value.Type,
			&value.Amount,
			&value.Currency,
			&relatedOrderID,
			&value.Status,
			&value.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan V2 transaction: %w", err)
		}
		if relatedOrderID.Valid {
			value.RelatedOrderID = &relatedOrderID.String
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate V2 transactions: %w", err)
	}
	page := &TransactionPage{Transactions: values}
	if len(values) <= filters.Limit {
		return page, nil
	}
	page.Transactions = values[:filters.Limit]
	last := page.Transactions[len(page.Transactions)-1]
	page.NextCursor = encodeCursor(last.CreatedAt, last.ID)
	return page, nil
}

func (s *implementation) ListSettlements(
	ctx context.Context,
	filters SettlementFilters,
) (*SettlementPage, error) {
	filters, cursor, err := normalizeSettlementFilters(filters)
	if err != nil {
		return nil, err
	}
	if err := requireMerchant(ctx, s.database, filters.MerchantID); err != nil {
		return nil, err
	}
	cursorTime, cursorID := cursorValues(cursor)
	const query = `
SELECT settlement.market_id, settlement.event_id,
       COALESCE(settlement.winning_option, ''), COALESCE(settlement.settlement_type, 'settle'),
       settlement.settled_at
FROM market_settlements AS settlement
JOIN markets AS market ON market.id = settlement.market_id
WHERE market.merchant_id = $1
  AND ($2::timestamp IS NULL OR settlement.settled_at >= $2)
  AND ($3::timestamp IS NULL OR settlement.settled_at <= $3)
  AND ($4::timestamp IS NULL OR (settlement.settled_at, settlement.market_id) < ($4::timestamp, $5::uuid))
ORDER BY settlement.settled_at DESC, settlement.market_id DESC
LIMIT $6`
	rows, err := s.database.QueryContext(
		ctx,
		query,
		filters.MerchantID,
		filters.From,
		filters.To,
		cursorTime,
		cursorID,
		filters.Limit+1,
	)
	if err != nil {
		return nil, fmt.Errorf("query V2 settlements: %w", err)
	}
	defer func() { _ = rows.Close() }()
	values := make([]Settlement, 0, filters.Limit+1)
	for rows.Next() {
		value := Settlement{}
		if err := rows.Scan(&value.MarketID, &value.EventID, &value.WinningOption, &value.SettlementType, &value.SettledAt); err != nil {
			return nil, fmt.Errorf("scan V2 settlement: %w", err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate V2 settlements: %w", err)
	}
	page := &SettlementPage{Settlements: values}
	if len(values) <= filters.Limit {
		return page, nil
	}
	page.Settlements = values[:filters.Limit]
	last := page.Settlements[len(page.Settlements)-1]
	page.NextCursor = encodeCursor(last.SettledAt, last.MarketID)
	return page, nil
}

func (s *implementation) ListPayouts(ctx context.Context, filters PayoutFilters) (*PayoutPage, error) {
	filters, cursor, err := normalizePayoutFilters(filters)
	if err != nil {
		return nil, err
	}
	if err := requireMarket(ctx, s.database, filters.MerchantID, filters.MarketID); err != nil {
		return nil, err
	}
	cursorTime, cursorID := cursorValues(cursor)
	const query = `
SELECT payout.market_id, payout.order_id, payout.wallet_id, wallet.user_id, payout.currency,
       payout.stake::text, payout.payout::text, payout.created_at
FROM settlement_payouts AS payout
JOIN wallets AS wallet ON wallet.id = payout.wallet_id
WHERE payout.market_id = $1
  AND ($2::timestamp IS NULL OR (payout.created_at, payout.order_id) < ($2::timestamp, $3::uuid))
ORDER BY payout.created_at DESC, payout.order_id DESC
LIMIT $4`
	rows, err := s.database.QueryContext(
		ctx,
		query,
		filters.MarketID,
		cursorTime,
		cursorID,
		filters.Limit+1,
	)
	if err != nil {
		return nil, fmt.Errorf("query V2 settlement payouts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	values := make([]Payout, 0, filters.Limit+1)
	for rows.Next() {
		value := Payout{}
		if err := rows.Scan(
			&value.MarketID,
			&value.OrderID,
			&value.WalletID,
			&value.UserID,
			&value.Currency,
			&value.Stake,
			&value.Payout,
			&value.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan V2 settlement payout: %w", err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate V2 settlement payouts: %w", err)
	}
	page := &PayoutPage{Payouts: values}
	if len(values) <= filters.Limit {
		return page, nil
	}
	page.Payouts = values[:filters.Limit]
	last := page.Payouts[len(page.Payouts)-1]
	page.NextCursor = encodeCursor(last.CreatedAt, last.OrderID)
	return page, nil
}

// TopOfBook returns the best resting bid and ask per option for each market.
// Only the executable resting orders are considered, matching the full
// order-book semantics (status pending/partial, buy = bid, sell = ask).
func (s *implementation) TopOfBook(ctx context.Context, marketIDs []string) (map[string][]BookQuote, error) {
	result := make(map[string][]BookQuote, len(marketIDs))
	if len(marketIDs) == 0 {
		return result, nil
	}
	const query = `
SELECT market_id, option,
       MAX(price) FILTER (WHERE type = 'buy') AS best_bid,
       MIN(price) FILTER (WHERE type = 'sell') AS best_ask
FROM orders
WHERE market_id::text = ANY($1) AND status IN ('pending', 'partial')
GROUP BY market_id, option`
	rows, err := s.database.QueryContext(ctx, query, marketIDs)
	if err != nil {
		return nil, fmt.Errorf("query top-of-book: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var marketID string
		var bid, ask sql.NullFloat64
		quote := BookQuote{}
		if err := rows.Scan(&marketID, &quote.Option, &bid, &ask); err != nil {
			return nil, fmt.Errorf("scan top-of-book quote: %w", err)
		}
		if bid.Valid {
			quote.Bid = &bid.Float64
		}
		if ask.Valid {
			quote.Ask = &ask.Float64
		}
		result[marketID] = append(result[marketID], quote)
	}
	return result, rows.Err()
}

// MarketEventDetails returns each market's owning event context in one
// batched query: settlement time, theme title/description, and the sports
// league/game when the event is a synced game.
func (s *implementation) MarketEventDetails(ctx context.Context, marketIDs []string) (map[string]MarketEventInfo, error) {
	result := make(map[string]MarketEventInfo, len(marketIDs))
	if len(marketIDs) == 0 {
		return result, nil
	}
	const query = `
SELECT m.id, e.title, COALESCE(e.description, ''), e.resolution_time,
       COALESCE(se.league, ''), COALESCE(se.game_id, ''), se.start_time,
       COALESCE(e.translations::text, '{}')
FROM markets m
JOIN events e ON e.id = m.event_id
LEFT JOIN sports_events se ON se.event_id = e.id
WHERE m.id::text = ANY($1)`
	rows, err := s.database.QueryContext(ctx, query, marketIDs)
	if err != nil {
		return nil, fmt.Errorf("query market event details: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var marketID, translationsText string
		info := MarketEventInfo{}
		var startTime sql.NullTime
		if err := rows.Scan(
			&marketID, &info.Title, &info.Description, &info.ResolutionTime,
			&info.League, &info.GameID, &startTime, &translationsText,
		); err != nil {
			return nil, fmt.Errorf("scan market event details: %w", err)
		}
		if startTime.Valid {
			info.StartTime = &startTime.Time
		}
		info.Translations = map[string]types.EventTranslation{}
		if err := json.Unmarshal([]byte(translationsText), &info.Translations); err != nil {
			return nil, fmt.Errorf("decode market event translations: %w", err)
		}
		result[marketID] = info
	}
	return result, rows.Err()
}

// MarketHistory returns a compact price series per binary market: the last
// trade, the hourly closing prices over the previous 24 hours (oldest first,
// at most 24 points), and the change against the series start and the
// previous hour. The series uses all market trades; for binary markets the
// complementary outcome is 1 - price.
func (s *implementation) MarketHistory(ctx context.Context, marketIDs []string) (map[string]*MarketHistory, error) {
	result := make(map[string]*MarketHistory, len(marketIDs))
	if len(marketIDs) == 0 {
		return result, nil
	}
	const pointsQuery = `
WITH hourly AS (
    SELECT DISTINCT ON (market_id, date_trunc('hour', created_at))
           market_id, matched_price,
           date_trunc('hour', created_at) AS bucket
    FROM trades
    WHERE market_id::text = ANY($1) AND created_at >= NOW() - INTERVAL '24 hours'
    ORDER BY market_id, bucket, created_at DESC
)
SELECT market_id, array_agg(matched_price ORDER BY bucket)::text
FROM hourly
GROUP BY market_id`
	pointsRows, err := s.database.QueryContext(ctx, pointsQuery, marketIDs)
	if err != nil {
		return nil, fmt.Errorf("query market price points: %w", err)
	}
	for pointsRows.Next() {
		var marketID string
		var pointsText string
		if err := pointsRows.Scan(&marketID, &pointsText); err != nil {
			_ = pointsRows.Close()
			return nil, fmt.Errorf("scan market price points: %w", err)
		}
		history := &MarketHistory{}
		if err := decodeFloatArray(pointsText, &history.Points); err != nil {
			_ = pointsRows.Close()
			return nil, fmt.Errorf("decode market price points: %w", err)
		}
		result[marketID] = history
	}
	if err := pointsRows.Close(); err != nil {
		return nil, fmt.Errorf("close market price points: %w", err)
	}

	const lastQuery = `
SELECT DISTINCT ON (market_id) market_id, matched_price
FROM trades
WHERE market_id::text = ANY($1)
ORDER BY market_id, created_at DESC`
	lastRows, err := s.database.QueryContext(ctx, lastQuery, marketIDs)
	if err != nil {
		return nil, fmt.Errorf("query last trade prices: %w", err)
	}
	defer func() { _ = lastRows.Close() }()
	for lastRows.Next() {
		var marketID string
		var price float64
		if err := lastRows.Scan(&marketID, &price); err != nil {
			return nil, fmt.Errorf("scan last trade price: %w", err)
		}
		history, exists := result[marketID]
		if !exists {
			history = &MarketHistory{}
			result[marketID] = history
		}
		history.Last = &price
		points := history.Points
		if len(points) > 0 {
			first := points[0]
			history.Change24h = &[]float64{price - first}[0]
			// The last point closes the current hour, so the previous hour's
			// close is the second-to-last point.
			if len(points) > 1 {
				previous := points[len(points)-2]
				history.Change1h = &[]float64{price - previous}[0]
			}
		}
	}
	return result, lastRows.Err()
}

func decodeFloatArray(value string, target *[]float64) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || trimmed == "{}" {
		*target = []float64{}
		return nil
	}
	inner := strings.TrimSuffix(strings.TrimPrefix(trimmed, "{"), "}")
	parts := strings.Split(inner, ",")
	points := make([]float64, 0, len(parts))
	for _, part := range parts {
		parsed, err := strconv.ParseFloat(strings.TrimSpace(part), 64)
		if err != nil {
			return err
		}
		points = append(points, parsed)
	}
	*target = points
	return nil
}

// MarketTitles returns each market's question keyed by market ID, used to
// render order lists with their market title in one pass.
func (s *implementation) MarketTitles(ctx context.Context, marketIDs []string) (map[string]string, error) {
	result := make(map[string]string, len(marketIDs))
	if len(marketIDs) == 0 {
		return result, nil
	}
	const query = `
SELECT id, question
FROM markets
WHERE id::text = ANY($1)`
	rows, err := s.database.QueryContext(ctx, query, marketIDs)
	if err != nil {
		return nil, fmt.Errorf("query market titles: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var marketID, question string
		if err := rows.Scan(&marketID, &question); err != nil {
			return nil, fmt.Errorf("scan market title: %w", err)
		}
		result[marketID] = question
	}
	return result, rows.Err()
}

// MarketOptions returns each market's option set and settlement winner in
// one batched query, for order list enrichment.
func (s *implementation) MarketOptions(ctx context.Context, marketIDs []string) (map[string]MarketOptionsInfo, error) {
	result := make(map[string]MarketOptionsInfo, len(marketIDs))
	if len(marketIDs) == 0 {
		return result, nil
	}
	const query = `
SELECT m.id, m.options::text, COALESCE(ms.winning_option, '')
FROM markets m
LEFT JOIN market_settlements ms ON ms.market_id = m.id
WHERE m.id::text = ANY($1)`
	rows, err := s.database.QueryContext(ctx, query, marketIDs)
	if err != nil {
		return nil, fmt.Errorf("query market options: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var marketID, optionsText, winningOption string
		if err := rows.Scan(&marketID, &optionsText, &winningOption); err != nil {
			return nil, fmt.Errorf("scan market options: %w", err)
		}
		info := MarketOptionsInfo{WinningOption: winningOption}
		if err := json.Unmarshal([]byte(optionsText), &info.Options); err != nil {
			return nil, fmt.Errorf("decode market options: %w", err)
		}
		result[marketID] = info
	}
	return result, rows.Err()
}

// OrderSettlements returns each order/bet's settled stake and payout,
// keyed by the order (or parimutuel bet) ID.
func (s *implementation) OrderSettlements(ctx context.Context, orderIDs []string) (map[string]SettlementInfo, error) {
	result := make(map[string]SettlementInfo, len(orderIDs))
	if len(orderIDs) == 0 {
		return result, nil
	}
	const query = `
SELECT order_id, stake::text, payout::text
FROM settlement_payouts
WHERE order_id::text = ANY($1)`
	rows, err := s.database.QueryContext(ctx, query, orderIDs)
	if err != nil {
		return nil, fmt.Errorf("query order settlements: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var orderID, stake, payout string
		if err := rows.Scan(&orderID, &stake, &payout); err != nil {
			return nil, fmt.Errorf("scan order settlement: %w", err)
		}
		result[orderID] = SettlementInfo{Stake: stake, Payout: payout}
	}
	return result, rows.Err()
}

// PoolOdds returns each market's current parimutuel gross-return odds per
// option: (total_stake - total_fees) / option_stake, mirroring the pool
// snapshot payload. Options without active stake are omitted.
func (s *implementation) PoolOdds(ctx context.Context, marketIDs []string) (map[string]map[string]string, error) {
	result := make(map[string]map[string]string, len(marketIDs))
	if len(marketIDs) == 0 {
		return result, nil
	}
	const query = `
SELECT p.market_id, p.total_stake::text, p.total_fees::text,
       b.option, SUM(b.stake)::text
FROM parimutuel_pools p
LEFT JOIN parimutuel_bets b ON b.market_id = p.market_id AND b.status = 'active'
WHERE p.market_id::text = ANY($1)
GROUP BY p.market_id, p.total_stake, p.total_fees, b.option`
	rows, err := s.database.QueryContext(ctx, query, marketIDs)
	if err != nil {
		return nil, fmt.Errorf("query pool odds: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var marketID, totalStakeStr, totalFeesStr string
		var option, optionStakeStr *string
		if err := rows.Scan(&marketID, &totalStakeStr, &totalFeesStr, &option, &optionStakeStr); err != nil {
			return nil, fmt.Errorf("scan pool odds: %w", err)
		}
		// A pool row with no active bets has NULL option and stake.
		if option == nil || optionStakeStr == nil || *optionStakeStr == "0" || *optionStakeStr == "0.00" {
			continue
		}
		options := result[marketID]
		if options == nil {
			options = make(map[string]string)
			result[marketID] = options
		}
		totalStake, ok := new(big.Float).SetString(totalStakeStr)
		if !ok {
			continue
		}
		totalFees, ok := new(big.Float).SetString(totalFeesStr)
		if !ok {
			continue
		}
		optionStake, ok := new(big.Float).SetString(*optionStakeStr)
		if !ok || optionStake.Sign() <= 0 {
			continue
		}
		available := new(big.Float).Sub(totalStake, totalFees)
		if available.Sign() <= 0 {
			continue
		}
		odds := new(big.Float).Quo(available, optionStake)
		oddsValue, _ := odds.Float64()
		options[*option] = formatOdds(oddsValue)
	}
	return result, rows.Err()
}

// formatOdds renders an odds multiplier with two decimal places.
func formatOdds(value float64) string {
	if value < 0 {
		return "0.00"
	}
	return strconv.FormatFloat(math.Round(value*100)/100, 'f', 2, 64)
}

// OrderLastFill returns each order's latest matched price (binary markets).
func (s *implementation) OrderLastFill(ctx context.Context, orderIDs []string) (map[string]float64, error) {
	result := make(map[string]float64, len(orderIDs))
	if len(orderIDs) == 0 {
		return result, nil
	}
	const query = `
SELECT DISTINCT ON (order_id) order_id, matched_price
FROM (
    SELECT taker_order_id AS order_id, matched_price, created_at
    FROM trades WHERE taker_order_id::text = ANY($1)
    UNION ALL
    SELECT maker_order_id AS order_id, matched_price, created_at
    FROM trades WHERE maker_order_id::text = ANY($1)
) AS fills
ORDER BY order_id, created_at DESC`
	rows, err := s.database.QueryContext(ctx, query, orderIDs)
	if err != nil {
		return nil, fmt.Errorf("query order last fill: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var orderID string
		var price float64
		if err := rows.Scan(&orderID, &price); err != nil {
			return nil, fmt.Errorf("scan order last fill: %w", err)
		}
		result[orderID] = price
	}
	return result, rows.Err()
}

func (s *implementation) DailyReport(
	ctx context.Context,
	merchantID string,
	date time.Time,
	currency string,
) (*DailyReport, error) {
	merchantID = strings.TrimSpace(merchantID)
	if !isUUID(merchantID) {
		return nil, &ValidationError{Field: "merchant_id", Message: "must be a UUID"}
	}
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if currency == "" || len(currency) > 10 {
		return nil, &ValidationError{Field: "currency", Message: "is required and must not exceed 10 characters"}
	}
	if err := requireMerchant(ctx, s.database, merchantID); err != nil {
		return nil, err
	}
	start := time.Date(date.UTC().Year(), date.UTC().Month(), date.UTC().Day(), 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 1)
	result := &DailyReport{Date: start.Format("2006-01-02"), Currency: currency}
	const transactionTotals = `
SELECT
    COALESCE(SUM(amount) FILTER (WHERE type = 'bet' OR (type = 'debit' AND reason = 'bet')), 0)::text,
    COALESCE(SUM(amount) FILTER (WHERE type = 'win' OR (type = 'credit' AND reason = 'payout')), 0)::text,
    COALESCE(SUM(amount) FILTER (WHERE type = 'fee'), 0)::text
FROM (
    SELECT t.amount, t.type, '' AS reason, w.merchant_id, t.currency, t.created_at
    FROM transactions AS t
    JOIN wallets AS w ON w.id = t.wallet_id
    UNION ALL
    SELECT st.amount, st.type, st.reason, st.merchant_id, st.currency, st.created_at
    FROM seamless_transactions AS st
) AS ledger
WHERE merchant_id = $1 AND currency = $2
  AND created_at >= $3 AND created_at < $4`
	if err := s.database.QueryRowContext(ctx, transactionTotals, merchantID, currency, start, end).Scan(
		&result.Bets,
		&result.Payouts,
		&result.Fees,
	); err != nil {
		return nil, fmt.Errorf("query V2 daily transaction totals: %w", err)
	}
	const refundTotals = `
SELECT COALESCE(SUM(
    ROUND((o.amount - o.filled_amount) *
        CASE WHEN o.type = 'buy' THEN o.price ELSE 1 - o.price END, 2)
), 0)::text
FROM orders AS o
JOIN market_settlements AS settlement ON settlement.market_id = o.market_id
WHERE o.merchant_id = $1 AND o.currency = $2
  AND COALESCE(o.wallet_kind, 'user') = 'user'
  AND settlement.settled_at >= $3 AND settlement.settled_at < $4`
	if err := s.database.QueryRowContext(ctx, refundTotals, merchantID, currency, start, end).Scan(&result.Refunds); err != nil {
		return nil, fmt.Errorf("query V2 daily refund totals: %w", err)
	}
	const seamlessRefundTotals = `
SELECT COALESCE(SUM(amount), 0)::text
FROM seamless_transactions
WHERE merchant_id = $1 AND currency = $2 AND type = 'credit'
  AND reason LIKE 'refund%'
  AND created_at >= $3 AND created_at < $4`
	var seamlessRefunds string
	if err := s.database.QueryRowContext(ctx, seamlessRefundTotals, merchantID, currency, start, end).Scan(&seamlessRefunds); err != nil {
		return nil, fmt.Errorf("query V2 seamless refund totals: %w", err)
	}
	result.Refunds = sumDecimalStrings(result.Refunds, seamlessRefunds)
	const transferTotals = `
SELECT
    COALESCE(SUM(amount) FILTER (WHERE direction = 'deposit' AND status = 'completed'), 0)::text,
    COALESCE(SUM(amount) FILTER (WHERE direction = 'withdrawal' AND status = 'completed'), 0)::text
FROM wallet_transfers
WHERE merchant_id = $1 AND currency = $2
  AND created_at >= $3 AND created_at < $4`
	if err := s.database.QueryRowContext(ctx, transferTotals, merchantID, currency, start, end).Scan(
		&result.TransferDeposits,
		&result.TransferWithdrawals,
	); err != nil {
		return nil, fmt.Errorf("query V2 daily transfer totals: %w", err)
	}
	result.GGR = difference(result.Bets, result.Payouts)
	return result, nil
}

func cursorValues(value *cursor) (any, any) {
	if value == nil {
		return nil, nil
	}
	return value.CreatedAt, value.ID
}

func requireMerchant(ctx context.Context, database *sql.DB, merchantID string) error {
	var exists bool
	if err := database.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM merchants WHERE id = $1)", merchantID).Scan(&exists); err != nil {
		return fmt.Errorf("query V2 merchant: %w", err)
	}
	if !exists {
		return ErrNotFound
	}
	return nil
}

func requireMarket(ctx context.Context, database *sql.DB, merchantID, marketID string) error {
	var exists bool
	if err := database.QueryRowContext(
		ctx,
		"SELECT EXISTS (SELECT 1 FROM markets WHERE id = $1 AND merchant_id = $2)",
		marketID,
		merchantID,
	).Scan(&exists); err != nil {
		return fmt.Errorf("query V2 market: %w", err)
	}
	if !exists {
		return ErrNotFound
	}
	return nil
}

func difference(left, right string) string {
	leftValue, ok := new(big.Rat).SetString(left)
	if !ok {
		return "0.00"
	}
	rightValue, ok := new(big.Rat).SetString(right)
	if !ok {
		return "0.00"
	}
	return new(big.Rat).Sub(leftValue, rightValue).FloatString(2)
}

func sumDecimalStrings(left, right string) string {
	leftValue, ok := new(big.Rat).SetString(left)
	if !ok {
		return right
	}
	rightValue, ok := new(big.Rat).SetString(right)
	if !ok {
		return left
	}
	return new(big.Rat).Add(leftValue, rightValue).FloatString(2)
}
