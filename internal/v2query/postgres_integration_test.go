package v2query

import (
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"
	"math"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const defaultIntegrationDatabaseURL = "postgres://predictmarket:password@localhost:5432/predictmarket?sslmode=disable"

func TestPostgresQueryServiceIntegration(t *testing.T) {
	if os.Getenv("INTEGRATION_TEST") != "1" {
		t.Skip("set INTEGRATION_TEST=1 to run PostgreSQL integration tests")
	}
	fixture := newIntegrationFixture(t)
	ctx := context.Background()
	service := New(fixture.database)

	transactions, err := service.ListTransactions(ctx, TransactionFilters{
		MerchantID: fixture.merchantID,
		UserID:     fixture.userID,
		Limit:      2,
	})
	if err != nil {
		t.Fatalf("ListTransactions() error = %v", err)
	}
	if len(transactions.Transactions) != 2 || transactions.NextCursor == "" {
		t.Fatalf("transaction page = %#v", transactions)
	}
	if transactions.Transactions[0].Amount == "" || transactions.Transactions[0].UserID != fixture.userID {
		t.Errorf("transaction = %#v", transactions.Transactions[0])
	}
	secondTransactionPage, err := service.ListTransactions(ctx, TransactionFilters{
		MerchantID: fixture.merchantID,
		UserID:     fixture.userID,
		Cursor:     transactions.NextCursor,
		Limit:      2,
	})
	if err != nil {
		t.Fatalf("ListTransactions(second page) error = %v", err)
	}
	if len(secondTransactionPage.Transactions) != 2 || secondTransactionPage.NextCursor != "" {
		t.Errorf("second transaction page = %#v", secondTransactionPage)
	}

	settlements, err := service.ListSettlements(ctx, SettlementFilters{MerchantID: fixture.merchantID})
	if err != nil {
		t.Fatalf("ListSettlements() error = %v", err)
	}
	if len(settlements.Settlements) != 1 || settlements.Settlements[0].MarketID != fixture.marketID {
		t.Errorf("settlements = %#v", settlements)
	}
	payouts, err := service.ListPayouts(ctx, PayoutFilters{MerchantID: fixture.merchantID, MarketID: fixture.marketID})
	if err != nil {
		t.Fatalf("ListPayouts() error = %v", err)
	}
	if len(payouts.Payouts) != 1 || payouts.Payouts[0].Stake != "5.00" || payouts.Payouts[0].Payout != "2.00" {
		t.Errorf("payouts = %#v", payouts)
	}
	report, err := service.DailyReport(ctx, fixture.merchantID, fixture.now, "USD")
	if err != nil {
		t.Fatalf("DailyReport() error = %v", err)
	}
	if report.Bets != "5.00" || report.Payouts != "2.00" || report.GGR != "3.00" || report.Fees != "1.00" {
		t.Errorf("daily report = %#v", report)
	}
	if report.TransferDeposits != "10.00" || report.TransferWithdrawals != "3.00" {
		t.Errorf("daily transfers = %#v", report)
	}
}

type integrationFixture struct {
	database   *sql.DB
	merchantID string
	userID     string
	marketID   string
	orderID    string
	now        time.Time
	ids        []string
}

func newIntegrationFixture(t *testing.T) *integrationFixture {
	t.Helper()
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		databaseURL = defaultIntegrationDatabaseURL
	}
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("open PostgreSQL: %v", err)
	}
	if err := database.PingContext(context.Background()); err != nil {
		_ = database.Close()
		t.Fatalf("ping PostgreSQL: %v", err)
	}
	fixture := &integrationFixture{
		database:   database,
		merchantID: integrationUUID(t),
		userID:     "v2-query-user",
		marketID:   integrationUUID(t),
		orderID:    integrationUUID(t),
		now:        time.Now().UTC().Truncate(time.Microsecond),
		ids:        []string{},
	}
	t.Cleanup(fixture.cleanup)
	fixture.seed(t)
	return fixture
}

func (f *integrationFixture) seed(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	eventID := f.newID(t)
	walletID := f.newID(t)
	orderID := f.orderID
	transactions := []struct {
		id     string
		typeID string
		amount string
	}{
		{id: f.newID(t), typeID: "credit", amount: "10.00"},
		{id: f.newID(t), typeID: "bet", amount: "5.00"},
		{id: f.newID(t), typeID: "win", amount: "2.00"},
		{id: f.newID(t), typeID: "fee", amount: "1.00"},
	}
	if _, err := f.database.ExecContext(ctx, `
INSERT INTO merchants (id, name, email, api_key, api_key_prefix, api_secret, status, currency, timezone)
VALUES ($1, 'V2 query merchant', $2, $3, $4, 'secret-hash', 'active', 'USD', 'UTC')`,
		f.merchantID,
		"v2-query-"+f.merchantID+"@example.test",
		"pk_v2_query_"+f.merchantID,
		"pk_v2_query_",
	); err != nil {
		t.Fatalf("insert merchant fixture: %v", err)
	}
	if _, err := f.database.ExecContext(ctx, `
INSERT INTO events (id, source_type, source_id, title, category, end_time, resolution_time, status, outcome)
VALUES ($1, 'custom', $2, 'V2 query event', 'test', $3, $3, 'resolved', 'Yes')`,
		eventID,
		"v2-query-"+eventID,
		f.now,
	); err != nil {
		t.Fatalf("insert event fixture: %v", err)
	}
	if _, err := f.database.ExecContext(ctx, `
INSERT INTO markets (id, merchant_id, event_id, type, question, options, status)
VALUES ($1, $2, $3, 'binary', 'Will V2 query pass?', '["Yes", "No"]', 'settled')`,
		f.marketID,
		f.merchantID,
		eventID,
	); err != nil {
		t.Fatalf("insert market fixture: %v", err)
	}
	if _, err := f.database.ExecContext(ctx, `
INSERT INTO wallets (id, merchant_id, user_id, currency, balance, locked_balance, updated_at)
VALUES ($1, $2, $3, 'USD', 6, 0, $4)`, walletID, f.merchantID, f.userID, f.now); err != nil {
		t.Fatalf("insert wallet fixture: %v", err)
	}
	if _, err := f.database.ExecContext(ctx, `
INSERT INTO orders (
    id, merchant_id, user_id, market_id, type, option, amount, filled_amount,
    currency, price, time_in_force, status, created_at
) VALUES ($1, $2, $3, $4, 'buy', 'Yes', 10, 10, 'USD', 0.5, 'gtc', 'filled', $5)`,
		orderID,
		f.merchantID,
		f.userID,
		f.marketID,
		f.now,
	); err != nil {
		t.Fatalf("insert order fixture: %v", err)
	}
	for index, transaction := range transactions {
		relatedOrderID := any(nil)
		if transaction.typeID == "bet" || transaction.typeID == "win" {
			relatedOrderID = orderID
		}
		if _, err := f.database.ExecContext(ctx, `
INSERT INTO transactions (id, wallet_id, type, amount, currency, related_order_id, status, created_at)
VALUES ($1, $2, $3, $4, 'USD', $5, 'completed', $6)`,
			transaction.id,
			walletID,
			transaction.typeID,
			transaction.amount,
			relatedOrderID,
			f.now.Add(time.Duration(index)*time.Microsecond),
		); err != nil {
			t.Fatalf("insert transaction fixture: %v", err)
		}
	}
	if _, err := f.database.ExecContext(ctx, `
INSERT INTO market_settlements (market_id, event_id, winning_option, settled_at)
VALUES ($1, $2, 'Yes', $3)`, f.marketID, eventID, f.now); err != nil {
		t.Fatalf("insert settlement fixture: %v", err)
	}
	if _, err := f.database.ExecContext(ctx, `
INSERT INTO settlement_payouts (market_id, order_id, wallet_id, currency, stake, payout, created_at)
VALUES ($1, $2, $3, 'USD', 5, 2, $4)`, f.marketID, orderID, walletID, f.now); err != nil {
		t.Fatalf("insert payout fixture: %v", err)
	}
	for _, transfer := range []struct {
		id            string
		transactionID string
		direction     string
		amount        string
	}{
		{id: f.newID(t), transactionID: transactions[0].id, direction: "deposit", amount: "10.00"},
		{id: f.newID(t), transactionID: transactions[1].id, direction: "withdrawal", amount: "3.00"},
	} {
		if _, err := f.database.ExecContext(ctx, `
INSERT INTO wallet_transfers (
    id, merchant_id, merchant_txn_id, user_id, currency, amount, direction,
    status, transaction_id, created_at, updated_at
) VALUES ($1, $2, $3, $4, 'USD', $5, $6, 'completed', $7, $8, $8)`,
			transfer.id,
			f.merchantID,
			"txn-"+transfer.id,
			f.userID,
			transfer.amount,
			transfer.direction,
			transfer.transactionID,
			f.now,
		); err != nil {
			t.Fatalf("insert transfer fixture: %v", err)
		}
	}
}

func (f *integrationFixture) newID(t *testing.T) string {
	t.Helper()
	id := integrationUUID(t)
	f.ids = append(f.ids, id)
	return id
}

func (f *integrationFixture) cleanup() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = f.database.ExecContext(ctx, "DELETE FROM wallet_transfers WHERE merchant_id = $1", f.merchantID)
	_, _ = f.database.ExecContext(ctx, "DELETE FROM settlement_payouts WHERE market_id = $1", f.marketID)
	_, _ = f.database.ExecContext(ctx, "DELETE FROM market_settlements WHERE market_id = $1", f.marketID)
	_, _ = f.database.ExecContext(ctx, "DELETE FROM transactions WHERE wallet_id IN (SELECT id FROM wallets WHERE merchant_id = $1)", f.merchantID)
	_, _ = f.database.ExecContext(ctx, "DELETE FROM trades WHERE market_id = $1", f.marketID)
	_, _ = f.database.ExecContext(ctx, "DELETE FROM orders WHERE merchant_id = $1", f.merchantID)
	_, _ = f.database.ExecContext(ctx, "DELETE FROM wallets WHERE merchant_id = $1", f.merchantID)
	_, _ = f.database.ExecContext(ctx, "DELETE FROM markets WHERE merchant_id = $1", f.merchantID)
	_, _ = f.database.ExecContext(ctx, "DELETE FROM events WHERE source_id LIKE 'v2-query-%'")
	_, _ = f.database.ExecContext(ctx, "DELETE FROM merchants WHERE id = $1", f.merchantID)
	_ = f.database.Close()
}

func integrationUUID(t *testing.T) string {
	t.Helper()
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		t.Fatalf("generate fixture UUID: %v", err)
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf(
		"%x-%x-%x-%x-%x",
		value[0:4],
		value[4:6],
		value[6:8],
		value[8:10],
		value[10:16],
	)
}

func TestTopOfBookIntegration(t *testing.T) {
	if os.Getenv("INTEGRATION_TEST") != "1" {
		t.Skip("set INTEGRATION_TEST=1 to run PostgreSQL integration tests")
	}
	fixture := newIntegrationFixture(t)
	ctx := context.Background()
	service := New(fixture.database)

	eventID := fixture.newID(t)
	bookMarketID := fixture.newID(t)
	if _, err := fixture.database.ExecContext(ctx, `
INSERT INTO events (id, source_type, source_id, title, category, end_time, resolution_time, status)
VALUES ($1, 'custom', $2, 'TopOfBook event', 'test', $3, $3, 'active')`,
		eventID, "v2-query-"+eventID, fixture.now); err != nil {
		t.Fatalf("insert top-of-book event: %v", err)
	}
	if _, err := fixture.database.ExecContext(ctx, `
INSERT INTO markets (id, merchant_id, event_id, type, question, options, status)
VALUES ($1, $2, $3, 'binary', 'Top of book market', '["Yes", "No"]', 'active')`,
		bookMarketID, fixture.merchantID, eventID); err != nil {
		t.Fatalf("insert top-of-book market: %v", err)
	}
	resting := []struct {
		side   string
		option string
		price  string
		status string
	}{
		{side: "buy", option: "Yes", price: "0.60", status: "pending"},
		{side: "buy", option: "Yes", price: "0.55", status: "pending"},
		{side: "sell", option: "Yes", price: "0.65", status: "pending"},
		{side: "sell", option: "Yes", price: "0.70", status: "pending"},
		{side: "buy", option: "No", price: "0.40", status: "partial"},
	}
	for _, order := range resting {
		if _, err := fixture.database.ExecContext(ctx, `
INSERT INTO orders (
    id, merchant_id, user_id, market_id, type, option, amount, filled_amount,
    currency, price, time_in_force, status, created_at
) VALUES ($1, $2, $3, $4, $5, $6, 10, 0, 'USD', $7, 'gtc', $8, $9)`,
			fixture.newID(t), fixture.merchantID, fixture.userID, bookMarketID,
			order.side, order.option, order.price, order.status, fixture.now,
		); err != nil {
			t.Fatalf("insert resting order: %v", err)
		}
	}
	quotes, err := service.TopOfBook(ctx, []string{bookMarketID, fixture.marketID})
	if err != nil {
		t.Fatalf("TopOfBook() error = %v", err)
	}
	byOption := map[string]BookQuote{}
	for _, quote := range quotes[bookMarketID] {
		byOption[quote.Option] = quote
	}
	yes := byOption["Yes"]
	if yes.Bid == nil || *yes.Bid != 0.60 || yes.Ask == nil || *yes.Ask != 0.65 {
		t.Errorf("Yes quote = bid %v ask %v, want 0.60/0.65", yes.Bid, yes.Ask)
	}
	no := byOption["No"]
	if no.Bid == nil || *no.Bid != 0.40 || no.Ask != nil {
		t.Errorf("No quote = bid %v ask %v, want 0.40/nil", no.Bid, no.Ask)
	}
	if len(quotes[fixture.marketID]) != 0 {
		t.Errorf("settled market quotes = %#v, want none", quotes[fixture.marketID])
	}
	empty, err := service.TopOfBook(ctx, nil)
	if err != nil || len(empty) != 0 {
		t.Fatalf("TopOfBook(nil) = %#v, %v", empty, err)
	}
}

func TestMarketEventDetailsAndHistoryIntegration(t *testing.T) {
	if os.Getenv("INTEGRATION_TEST") != "1" {
		t.Skip("set INTEGRATION_TEST=1 to run PostgreSQL integration tests")
	}
	fixture := newIntegrationFixture(t)
	ctx := context.Background()
	service := New(fixture.database)

	details, err := service.MarketEventDetails(ctx, []string{fixture.marketID})
	if err != nil {
		t.Fatalf("MarketEventDetails() error = %v", err)
	}
	info, exists := details[fixture.marketID]
	if !exists || info.Title != "V2 query event" || info.ResolutionTime.IsZero() {
		t.Fatalf("event details = %#v", info)
	}
	// The fixture event is not a synced game.
	if info.League != "" {
		t.Errorf("league = %q, want empty before seeding sports_events", info.League)
	}

	if _, err := fixture.database.ExecContext(ctx, `
INSERT INTO sports_events (event_id, league, game_id, start_time)
VALUES ((SELECT event_id FROM markets WHERE id = $1), 'NBA', 'game-1', $2)`,
		fixture.marketID, fixture.now); err != nil {
		t.Fatalf("seed sports event: %v", err)
	}
	details, err = service.MarketEventDetails(ctx, []string{fixture.marketID})
	if err != nil {
		t.Fatalf("MarketEventDetails() after seed error = %v", err)
	}
	if info := details[fixture.marketID]; info.League != "NBA" || info.GameID != "game-1" || info.StartTime == nil {
		t.Fatalf("sports event details = %#v", info)
	}

	// Seed trades at distinct hours and verify the sparkline + changes.
	makerID := fixture.newID(t)
	takerID := fixture.newID(t)
	for _, orderID := range []string{makerID, takerID} {
		if _, err := fixture.database.ExecContext(ctx, `
INSERT INTO orders (
    id, merchant_id, user_id, market_id, type, option, amount, filled_amount,
    currency, price, time_in_force, status, created_at
) VALUES ($1, $2, $3, $4, 'buy', 'Yes', 10, 10, 'USD', 0.5, 'gtc', 'filled', $5)`,
			orderID, fixture.merchantID, fixture.userID, fixture.marketID, fixture.now); err != nil {
			t.Fatalf("insert trade order: %v", err)
		}
	}
	trades := []struct {
		hour  int
		price float64
	}{
		{hour: 3, price: 0.50},
		{hour: 2, price: 0.55},
		{hour: 1, price: 0.60},
		{hour: 0, price: 0.65},
	}
	for index, trade := range trades {
		if _, err := fixture.database.ExecContext(ctx, `
INSERT INTO trades (market_id, maker_order_id, taker_order_id, shares, matched_price, created_at)
VALUES ($1, $2, $3, 1, $4, $5)`,
			fixture.marketID, makerID, takerID, trade.price, fixture.now.Add(-time.Duration(trade.hour)*time.Hour)); err != nil {
			t.Fatalf("insert trade %d: %v", index, err)
		}
	}

	history, err := service.MarketHistory(ctx, []string{fixture.marketID})
	if err != nil {
		t.Fatalf("MarketHistory() error = %v", err)
	}
	series := history[fixture.marketID]
	if series == nil {
		t.Fatal("market history missing")
	}
	if series.Last == nil || *series.Last != 0.65 {
		t.Errorf("last = %v, want 0.65", series.Last)
	}
	if series.Change24h == nil || math.Abs(*series.Change24h-0.15) > 1e-9 {
		t.Errorf("change_24h = %v, want 0.15", *series.Change24h)
	}
	if series.Change1h == nil || math.Abs(*series.Change1h-0.05) > 1e-9 {
		t.Errorf("change_1h = %v, want 0.05", *series.Change1h)
	}
	if len(series.Points) < 3 || series.Points[len(series.Points)-1] != 0.65 || series.Points[0] != 0.50 {
		t.Errorf("points = %#v, want hourly closes ending at 0.65", series.Points)
	}
	empty, err := service.MarketEventDetails(ctx, nil)
	if err != nil || len(empty) != 0 {
		t.Fatalf("MarketEventDetails(nil) = %#v, %v", empty, err)
	}
}

func TestMarketTitlesIntegration(t *testing.T) {
	if os.Getenv("INTEGRATION_TEST") != "1" {
		t.Skip("set INTEGRATION_TEST=1 to run PostgreSQL integration tests")
	}
	fixture := newIntegrationFixture(t)
	ctx := context.Background()
	service := New(fixture.database)

	titles, err := service.MarketTitles(ctx, []string{fixture.marketID, integrationUUID(t)})
	if err != nil {
		t.Fatalf("MarketTitles() error = %v", err)
	}
	if titles[fixture.marketID] != "Will V2 query pass?" {
		t.Errorf("market title = %q, want the fixture question", titles[fixture.marketID])
	}
	if _, exists := titles[fixture.marketID]; !exists {
		t.Fatal("market title missing")
	}
	empty, err := service.MarketTitles(ctx, nil)
	if err != nil || len(empty) != 0 {
		t.Fatalf("MarketTitles(nil) = %#v, %v", empty, err)
	}
}

func TestOrderListEnrichmentIntegration(t *testing.T) {
	if os.Getenv("INTEGRATION_TEST") != "1" {
		t.Skip("set INTEGRATION_TEST=1 to run PostgreSQL integration tests")
	}
	fixture := newIntegrationFixture(t)
	ctx := context.Background()
	service := New(fixture.database)

	options, err := service.MarketOptions(ctx, []string{fixture.marketID})
	if err != nil {
		t.Fatalf("MarketOptions() error = %v", err)
	}
	info, exists := options[fixture.marketID]
	if !exists || len(info.Options) != 2 || info.Options[0] != "Yes" {
		t.Fatalf("market options = %#v", info)
	}
	// The fixture market is settled with winner Yes.
	if info.WinningOption != "Yes" {
		t.Errorf("winning option = %q, want Yes", info.WinningOption)
	}

	orderID := fixture.orderID
	settlements, err := service.OrderSettlements(ctx, []string{orderID})
	if err != nil {
		t.Fatalf("OrderSettlements() error = %v", err)
	}
	settlementInfo, settled := settlements[orderID]
	if !settled || settlementInfo.Payout == "" || settlementInfo.Stake == "" {
		t.Errorf("settlement missing for %s: %#v", orderID, settlementInfo)
	}
	// The fixture has no trades; seed one so the last-fill lookup has data.
	makerID := integrationUUID(t)
	if _, err := fixture.database.ExecContext(ctx, `
INSERT INTO orders (
    id, merchant_id, user_id, market_id, type, option, amount, filled_amount,
    currency, price, time_in_force, status, created_at
) VALUES ($1, $2, $3, $4, 'buy', 'Yes', 10, 10, 'USD', 0.5, 'gtc', 'filled', $5)`,
		makerID, fixture.merchantID, fixture.userID, fixture.marketID, fixture.now); err != nil {
		t.Fatalf("insert trade maker order: %v", err)
	}
	if _, err := fixture.database.ExecContext(ctx, `
INSERT INTO trades (market_id, maker_order_id, taker_order_id, shares, matched_price, created_at)
VALUES ($1, $2, $3, 1, 0.6, $4)`,
		fixture.marketID, makerID, orderID, fixture.now); err != nil {
		t.Fatalf("insert trade fixture: %v", err)
	}
	fills, err := service.OrderLastFill(ctx, []string{orderID})
	if err != nil {
		t.Fatalf("OrderLastFill() error = %v", err)
	}
	if fills[orderID] == 0 {
		t.Errorf("last fill missing for %s", orderID)
	}
}
