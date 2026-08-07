package settlement

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const defaultIntegrationDatabaseURL = "postgres://predictmarket:password@localhost:5432/predictmarket?sslmode=disable"

func TestSettlementPostgresIntegration(t *testing.T) {
	if os.Getenv("INTEGRATION_TEST") != "1" {
		t.Skip("set INTEGRATION_TEST=1 to run PostgreSQL integration tests")
	}
	fixture := newSettlementFixture(t)

	const workers = 8
	errorsFound := make(chan error, workers)
	var waitGroup sync.WaitGroup
	for range workers {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			errorsFound <- fixture.service.SettleEvent(context.Background(), fixture.eventID)
		}()
	}
	waitGroup.Wait()
	close(errorsFound)
	for err := range errorsFound {
		if err != nil {
			t.Errorf("concurrent SettleEvent() error = %v", err)
		}
	}

	fixture.assertSettlement(t)
	if err := fixture.service.SettleEvent(context.Background(), fixture.eventID); err != nil {
		t.Fatalf("retry SettleEvent() error = %v", err)
	}
	fixture.assertSettlement(t)
}

func TestSettlementPostgresIntegrationMissingWalletDoesNotBlockOtherMarkets(t *testing.T) {
	if os.Getenv("INTEGRATION_TEST") != "1" {
		t.Skip("set INTEGRATION_TEST=1 to run PostgreSQL integration tests")
	}
	fixture := newSettlementFixture(t)
	if _, err := fixture.database.ExecContext(
		context.Background(),
		"DELETE FROM wallets WHERE id = $1",
		fixture.walletIDs["buy-winner"],
	); err != nil {
		t.Fatalf("delete fixture wallet: %v", err)
	}

	err := fixture.service.SettleEvent(context.Background(), fixture.eventID)
	if !errors.Is(err, ErrOrderWalletNotFound) {
		t.Fatalf("SettleEvent() error = %v, want ErrOrderWalletNotFound", err)
	}
	fixture.assertMarketSettlementStatus(t, fixture.marketIDs[0], "active", false)
	fixture.assertMarketSettlementStatus(t, fixture.marketIDs[1], "settled", true)
}

func TestSettlementPostgresIntegrationInvalidOutcomeDoesNotBlockOtherMarkets(t *testing.T) {
	if os.Getenv("INTEGRATION_TEST") != "1" {
		t.Skip("set INTEGRATION_TEST=1 to run PostgreSQL integration tests")
	}
	fixture := newSettlementFixture(t)
	if _, err := fixture.database.ExecContext(
		context.Background(),
		"UPDATE markets SET options = '[\"No\", \"Maybe\"]' WHERE id = $1",
		fixture.marketIDs[1],
	); err != nil {
		t.Fatalf("make fixture outcome invalid: %v", err)
	}

	err := fixture.service.SettleEvent(context.Background(), fixture.eventID)
	if !errors.Is(err, ErrOutcomeNotOption) {
		t.Fatalf("SettleEvent() error = %v, want ErrOutcomeNotOption", err)
	}
	fixture.assertMarketSettlementStatus(t, fixture.marketIDs[0], "settled", true)
	fixture.assertMarketSettlementStatus(t, fixture.marketIDs[1], "active", false)
}

func TestSettlementPostgresIntegrationLocksSharedWalletsInStableOrder(t *testing.T) {
	if os.Getenv("INTEGRATION_TEST") != "1" {
		t.Skip("set INTEGRATION_TEST=1 to run PostgreSQL integration tests")
	}
	fixture := newSettlementFixture(t)
	secondEventID := integrationUUID(t)
	secondMarketID := integrationUUID(t)
	secondOrderIDs := []string{integrationUUID(t), integrationUUID(t)}
	ctx := context.Background()
	if _, err := fixture.database.ExecContext(ctx, `
INSERT INTO events (
    id, source_type, source_id, title, category, end_time, resolution_time, status, outcome
) VALUES ($1, 'custom', $2, 'Second settlement event', 'integration', $3, $3, 'resolved', 'Yes')`,
		secondEventID,
		"settlement-shared-wallet-"+fixture.suffix,
		time.Now().UTC(),
	); err != nil {
		t.Fatalf("insert second event: %v", err)
	}
	if _, err := fixture.database.ExecContext(ctx, `
INSERT INTO markets (id, merchant_id, event_id, type, question, options, status)
VALUES ($1, $2, $3, 'binary', 'Shared wallet settlement market', '["Yes", "No"]', 'active')`,
		secondMarketID,
		fixture.merchantID,
		secondEventID,
	); err != nil {
		t.Fatalf("insert second market: %v", err)
	}
	for _, walletID := range []string{fixture.walletIDs["buy-winner"], fixture.walletIDs["sell-loser"]} {
		if _, err := fixture.database.ExecContext(ctx, `
UPDATE wallets
SET balance = balance - 0.50, locked_balance = locked_balance + 0.50
WHERE id = $1`, walletID); err != nil {
			t.Fatalf("reserve fixture wallet %s: %v", walletID, err)
		}
	}
	orders := []struct {
		id     string
		userID string
		side   string
	}{
		{id: secondOrderIDs[0], userID: "buy-winner", side: "buy"},
		{id: secondOrderIDs[1], userID: "sell-loser", side: "sell"},
	}
	for _, order := range orders {
		if _, err := fixture.database.ExecContext(ctx, `
INSERT INTO orders (
    id, merchant_id, user_id, market_id, type, option, amount, filled_amount,
    currency, price, time_in_force, status
) VALUES ($1, $2, $3, $4, $5, 'Yes', 1, 0, 'USD', 0.5, 'gtc', 'pending')`,
			order.id,
			fixture.merchantID,
			order.userID,
			secondMarketID,
			order.side,
		); err != nil {
			t.Fatalf("insert shared-wallet order %s: %v", order.id, err)
		}
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = fixture.database.ExecContext(
			cleanupCtx,
			"DELETE FROM market_settlements WHERE market_id = $1",
			secondMarketID,
		)
		_, _ = fixture.database.ExecContext(
			cleanupCtx,
			"DELETE FROM orders WHERE id = ANY($1)",
			secondOrderIDs,
		)
		_, _ = fixture.database.ExecContext(cleanupCtx, "DELETE FROM markets WHERE id = $1", secondMarketID)
		_, _ = fixture.database.ExecContext(cleanupCtx, "DELETE FROM events WHERE id = $1", secondEventID)
	})

	settlementCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	errorsFound := make(chan error, 2)
	var waitGroup sync.WaitGroup
	for _, eventID := range []string{fixture.eventID, secondEventID} {
		waitGroup.Add(1)
		go func(eventID string) {
			defer waitGroup.Done()
			errorsFound <- fixture.service.SettleEvent(settlementCtx, eventID)
		}(eventID)
	}
	waitGroup.Wait()
	close(errorsFound)
	for err := range errorsFound {
		if err != nil {
			t.Errorf("concurrent shared-wallet SettleEvent() error = %v", err)
		}
	}
	fixture.assertMarketSettlementStatus(t, fixture.marketIDs[0], "settled", true)
	fixture.assertMarketSettlementStatus(t, fixture.marketIDs[1], "settled", true)
	fixture.assertMarketSettlementStatus(t, secondMarketID, "settled", true)
}

type settlementFixture struct {
	database      *sql.DB
	service       Service
	merchantID    string
	eventID       string
	marketIDs     []string
	walletIDs     map[string]string
	orderIDs      []string
	orderIDByUser map[string]string
	suffix        string
}

func newSettlementFixture(t *testing.T) *settlementFixture {
	t.Helper()
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		databaseURL = defaultIntegrationDatabaseURL
	}
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("open PostgreSQL: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := database.PingContext(ctx); err != nil {
		_ = database.Close()
		t.Fatalf("ping PostgreSQL: %v", err)
	}
	fixture := &settlementFixture{
		database: database, service: NewServiceWithRepository(newPostgresRepository(database)),
		merchantID: integrationUUID(t), eventID: integrationUUID(t),
		marketIDs: []string{integrationUUID(t), integrationUUID(t)},
		walletIDs: make(map[string]string), orderIDByUser: make(map[string]string),
		suffix: fmt.Sprintf("%d", time.Now().UnixNano()),
	}
	t.Cleanup(fixture.cleanup)
	fixture.insertReferences(t)
	fixture.insertOrderFixtures(t)
	return fixture
}

func (f *settlementFixture) insertReferences(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	_, err := f.database.ExecContext(ctx, `
INSERT INTO merchants (id, name, email, api_key, api_key_prefix, api_secret, status, currency, timezone)
VALUES ($1, 'Settlement integration', $2, $3, LEFT('pk_' || gen_random_uuid()::text, 16), 'secret-hash', 'active', 'USD', 'UTC')`,
		f.merchantID, "settlement-"+f.suffix+"@example.com", "pk_settlement_"+f.suffix)
	if err != nil {
		t.Fatalf("insert merchant: %v", err)
	}
	_, err = f.database.ExecContext(ctx, `
INSERT INTO events (
    id, source_type, source_id, title, category, end_time, resolution_time, status, outcome
) VALUES ($1, 'custom', $2, 'Settlement event', 'integration', $3, $3, 'resolved', 'Yes')`,
		f.eventID, "settlement-"+f.suffix, time.Now().UTC())
	if err != nil {
		t.Fatalf("insert event: %v", err)
	}
	for index, marketID := range f.marketIDs {
		_, err = f.database.ExecContext(ctx, `
INSERT INTO markets (
    id, merchant_id, event_id, type, question, options, status
) VALUES ($1, $2, $3, 'binary', $4, '["Yes","No"]', 'active')`,
			marketID, f.merchantID, f.eventID, fmt.Sprintf("Settlement market %d", index))
		if err != nil {
			t.Fatalf("insert market: %v", err)
		}
	}
}

func (f *settlementFixture) insertOrderFixtures(t *testing.T) {
	t.Helper()
	fixtures := []struct {
		userID  string
		market  int
		side    string
		option  string
		amount  string
		filled  string
		status  string
		balance string
		locked  string
	}{
		{userID: "buy-winner", side: "buy", option: "Yes", amount: "10.00", filled: "10.00", status: "filled", balance: "95.00", locked: "5.00"},
		{userID: "sell-loser", side: "sell", option: "Yes", amount: "10.00", filled: "10.00", status: "filled", balance: "95.00", locked: "5.00"},
		{userID: "sell-winner", side: "sell", option: "No", amount: "10.00", filled: "5.00", status: "partial", balance: "95.00", locked: "5.00"},
		{userID: "buy-loser", side: "buy", option: "No", amount: "5.00", filled: "5.00", status: "filled", balance: "97.50", locked: "2.50"},
		{userID: "pending", side: "buy", option: "Yes", amount: "7.00", filled: "0.00", status: "pending", balance: "96.50", locked: "3.50"},
		{userID: "buy-winner-market-two", market: 1, side: "buy", option: "Yes", amount: "3.00", filled: "3.00", status: "filled", balance: "98.50", locked: "1.50"},
		{userID: "sell-loser-market-two", market: 1, side: "sell", option: "Yes", amount: "3.00", filled: "3.00", status: "filled", balance: "98.50", locked: "1.50"},
	}
	for _, value := range fixtures {
		walletID := integrationUUID(t)
		orderID := integrationUUID(t)
		f.walletIDs[value.userID] = walletID
		f.orderIDs = append(f.orderIDs, orderID)
		f.orderIDByUser[value.userID] = orderID
		if _, err := f.database.ExecContext(context.Background(), `
INSERT INTO wallets (id, merchant_id, user_id, currency, balance, locked_balance)
VALUES ($1, $2, $3, 'USD', $4, $5)`,
			walletID, f.merchantID, value.userID, value.balance, value.locked); err != nil {
			t.Fatalf("insert wallet %s: %v", value.userID, err)
		}
		if _, err := f.database.ExecContext(context.Background(), `
INSERT INTO orders (
    id, merchant_id, user_id, market_id, type, option, amount, filled_amount,
    currency, price, time_in_force, status
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'USD', 0.5, 'gtc', $9)`,
			orderID, f.merchantID, value.userID, f.marketIDs[value.market], value.side,
			value.option, value.amount, value.filled, value.status); err != nil {
			t.Fatalf("insert order %s: %v", value.userID, err)
		}
	}
	f.insertTrade(t, 0, "buy-winner", "sell-loser", "10.00")
	f.insertTrade(t, 0, "sell-winner", "buy-loser", "5.00")
	f.insertTrade(t, 1, "buy-winner-market-two", "sell-loser-market-two", "3.00")
}

func (f *settlementFixture) insertTrade(t *testing.T, market int, makerUserID, takerUserID, shares string) {
	t.Helper()
	_, err := f.database.ExecContext(context.Background(), `
INSERT INTO trades (market_id, maker_order_id, taker_order_id, shares, matched_price)
VALUES ($1, $2, $3, $4, 0.5)`,
		f.marketIDs[market],
		f.orderIDByUser[makerUserID],
		f.orderIDByUser[takerUserID],
		shares,
	)
	if err != nil {
		t.Fatalf("insert trade: %v", err)
	}
}

func (f *settlementFixture) assertSettlement(t *testing.T) {
	t.Helper()
	wantBalances := map[string]string{
		"buy-winner": "105.00", "sell-loser": "95.00", "sell-winner": "102.50",
		"buy-loser": "97.50", "pending": "100.00", "buy-winner-market-two": "101.50",
		"sell-loser-market-two": "98.50",
	}
	for userID, want := range wantBalances {
		var balance string
		var locked string
		if err := f.database.QueryRowContext(context.Background(), `
SELECT balance::text, locked_balance::text FROM wallets WHERE id = $1`,
			f.walletIDs[userID]).Scan(&balance, &locked); err != nil {
			t.Fatalf("query wallet %s: %v", userID, err)
		}
		if balance != want || locked != "0.00" {
			t.Errorf("wallet %s = (%s, %s), want (%s, 0.00)", userID, balance, locked, want)
		}
	}
	for _, marketID := range f.marketIDs {
		var status string
		var settled bool
		if err := f.database.QueryRowContext(context.Background(), `
SELECT m.status, m.settled_at IS NOT NULL AND s.market_id IS NOT NULL
FROM markets AS m LEFT JOIN market_settlements AS s ON s.market_id = m.id
WHERE m.id = $1`, marketID).Scan(&status, &settled); err != nil {
			t.Fatalf("query market settlement: %v", err)
		}
		if status != "settled" || !settled {
			t.Errorf("market %s = (%s, %v), want settled", marketID, status, settled)
		}
	}
	var cancelled int
	if err := f.database.QueryRowContext(context.Background(), `
SELECT COUNT(*) FROM orders WHERE id = ANY($1) AND status = 'cancelled'`,
		[]string{f.orderIDs[2], f.orderIDs[4]}).Scan(&cancelled); err != nil {
		t.Fatalf("count cancelled orders: %v", err)
	}
	if cancelled != 2 {
		t.Errorf("cancelled open orders = %d, want 2", cancelled)
	}
	f.assertCount(t, "market_settlements", "event_id", f.eventID, 2)
	f.assertCount(t, "settlement_payouts", "market_id", f.marketIDs[0], 4)
	f.assertCount(t, "settlement_payouts", "market_id", f.marketIDs[1], 2)
	var feeLedgerEntries int
	if err := f.database.QueryRowContext(context.Background(), `
SELECT COUNT(*) FROM fee_ledger WHERE market_id = ANY($1)`, f.marketIDs).Scan(&feeLedgerEntries); err != nil {
		t.Fatalf("count fee ledger entries: %v", err)
	}
	if feeLedgerEntries != 0 {
		t.Errorf("fee ledger entries = %d, want 0 while all fee rates are disabled", feeLedgerEntries)
	}
	var transactionCount int
	if err := f.database.QueryRowContext(context.Background(), `
SELECT COUNT(*) FROM transactions WHERE related_order_id = ANY($1)`, f.orderIDs).Scan(&transactionCount); err != nil {
		t.Fatalf("count settlement transactions: %v", err)
	}
	if transactionCount != 9 {
		t.Errorf("settlement transactions = %d, want 9", transactionCount)
	}
}

func (f *settlementFixture) assertMarketSettlementStatus(
	t *testing.T,
	marketID string,
	wantStatus string,
	wantSettled bool,
) {
	t.Helper()
	var status string
	var settled bool
	if err := f.database.QueryRowContext(context.Background(), `
SELECT m.status, s.market_id IS NOT NULL
FROM markets AS m
LEFT JOIN market_settlements AS s ON s.market_id = m.id
WHERE m.id = $1`, marketID).Scan(&status, &settled); err != nil {
		t.Fatalf("query market settlement status: %v", err)
	}
	if status != wantStatus || settled != wantSettled {
		t.Errorf(
			"market %s = (%s, %v), want (%s, %v)",
			marketID,
			status,
			settled,
			wantStatus,
			wantSettled,
		)
	}
}

func (f *settlementFixture) assertCount(
	t *testing.T,
	table string,
	column string,
	value string,
	want int,
) {
	t.Helper()
	var count int
	query := fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE %s = $1", table, column)
	if err := f.database.QueryRowContext(context.Background(), query, value).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if count != want {
		t.Errorf("%s count = %d, want %d", table, count, want)
	}
}

func (f *settlementFixture) cleanup() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = f.database.ExecContext(ctx, "DELETE FROM fee_ledger WHERE market_id = ANY($1)", f.marketIDs)
	_, _ = f.database.ExecContext(ctx, "DELETE FROM settlement_payouts WHERE market_id = ANY($1)", f.marketIDs)
	_, _ = f.database.ExecContext(ctx, "DELETE FROM market_settlements WHERE event_id = $1", f.eventID)
	_, _ = f.database.ExecContext(ctx, "DELETE FROM transactions WHERE related_order_id = ANY($1)", f.orderIDs)
	_, _ = f.database.ExecContext(ctx, "DELETE FROM trades WHERE market_id = ANY($1)", f.marketIDs)
	_, _ = f.database.ExecContext(ctx, "DELETE FROM orders WHERE id = ANY($1)", f.orderIDs)
	_, _ = f.database.ExecContext(ctx, "DELETE FROM wallets WHERE merchant_id = $1", f.merchantID)
	_, _ = f.database.ExecContext(ctx, "DELETE FROM markets WHERE id = ANY($1)", f.marketIDs)
	_, _ = f.database.ExecContext(ctx, "DELETE FROM events WHERE id = $1", f.eventID)
	_, _ = f.database.ExecContext(ctx, "DELETE FROM merchants WHERE id = $1", f.merchantID)
	_ = f.database.Close()
}

func integrationUUID(t *testing.T) string {
	t.Helper()
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		t.Fatalf("generate UUID: %v", err)
	}
	buffer[6] = (buffer[6] & 0x0f) | 0x40
	buffer[8] = (buffer[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", buffer[:4], buffer[4:6], buffer[6:8], buffer[8:10], buffer[10:])
}

func TestSettlementPostgresVoidRefundsFullCollateral(t *testing.T) {
	if os.Getenv("INTEGRATION_TEST") != "1" {
		t.Skip("set INTEGRATION_TEST=1 to run PostgreSQL integration tests")
	}
	fixture := newSettlementFixture(t)
	ctx := context.Background()

	// Enable webhook delivery so the void path exercises the outbox.
	if _, err := fixture.database.ExecContext(ctx, `
UPDATE merchants
SET callback_url = 'https://merchant.example/callback',
    webhook_url = 'https://merchant.example/webhook',
    webhook_events = ARRAY['order.voided', 'market.voided']
WHERE id = $1`, fixture.merchantID); err != nil {
		t.Fatalf("configure fixture webhooks: %v", err)
	}

	if err := fixture.service.VoidMarket(ctx, fixture.marketIDs[0]); err != nil {
		t.Fatalf("VoidMarket() error = %v", err)
	}
	if err := fixture.service.VoidMarket(ctx, fixture.marketIDs[0]); !errors.Is(err, ErrMarketAlreadySettled) {
		t.Fatalf("second VoidMarket() error = %v, want ErrMarketAlreadySettled", err)
	}

	var marketStatus string
	if err := fixture.database.QueryRowContext(
		ctx, "SELECT status FROM markets WHERE id = $1", fixture.marketIDs[0],
	).Scan(&marketStatus); err != nil {
		t.Fatalf("query voided market status: %v", err)
	}
	if marketStatus != "voided" {
		t.Fatalf("voided market status = %q, want voided", marketStatus)
	}

	var settlementType string
	var winningOption sql.NullString
	if err := fixture.database.QueryRowContext(ctx, `
SELECT settlement_type, winning_option
FROM market_settlements
WHERE market_id = $1`, fixture.marketIDs[0]).Scan(&settlementType, &winningOption); err != nil {
		t.Fatalf("query market settlement: %v", err)
	}
	if settlementType != "void" || winningOption.Valid {
		t.Fatalf("market settlement = (%s, %v), want (void, NULL)", settlementType, winningOption)
	}

	expected := map[string][2]string{
		"buy-winner":  {"100.00", "0.00"},
		"sell-loser":  {"100.00", "0.00"},
		"sell-winner": {"100.00", "0.00"},
		"buy-loser":   {"100.00", "0.00"},
		"pending":     {"100.00", "0.00"},
	}
	for userID, want := range expected {
		var balance string
		var locked string
		if err := fixture.database.QueryRowContext(ctx, `
SELECT balance::text, locked_balance::text
FROM wallets
WHERE id = $1`, fixture.walletIDs[userID]).Scan(&balance, &locked); err != nil {
			t.Fatalf("query voided wallet %s: %v", userID, err)
		}
		if balance != want[0] || locked != want[1] {
			t.Errorf("voided wallet %s = (balance %s, locked %s), want (%s, %s)", userID, balance, locked, want[0], want[1])
		}
	}

	var voidedOrders int
	if err := fixture.database.QueryRowContext(ctx, `
SELECT COUNT(*) FROM orders WHERE market_id = $1 AND status = 'voided'`, fixture.marketIDs[0]).Scan(&voidedOrders); err != nil {
		t.Fatalf("query voided orders: %v", err)
	}
	if voidedOrders != 5 {
		t.Fatalf("voided orders = %d, want 5", voidedOrders)
	}

	var voidWebhooks int
	if err := fixture.database.QueryRowContext(ctx, `
SELECT COUNT(*) FROM webhook_outbox
WHERE merchant_id = $1
  AND event_type IN ('order.voided', 'market.voided')`, fixture.merchantID).Scan(&voidWebhooks); err != nil {
		t.Fatalf("query void webhooks: %v", err)
	}
	if voidWebhooks != 6 { // 5 order.voided + 1 market.voided
		t.Fatalf("void webhooks = %d, want 6", voidWebhooks)
	}
}

func TestSettlementPostgresSingleMarketSettle(t *testing.T) {
	if os.Getenv("INTEGRATION_TEST") != "1" {
		t.Skip("set INTEGRATION_TEST=1 to run PostgreSQL integration tests")
	}
	fixture := newSettlementFixture(t)
	ctx := context.Background()

	if err := fixture.service.SettleMarket(ctx, fixture.marketIDs[0], "Yes"); err != nil {
		t.Fatalf("SettleMarket() error = %v", err)
	}
	fixture.assertMarketSettlementStatus(t, fixture.marketIDs[0], "settled", true)
	// The owning event stays open; the second market is untouched.
	fixture.assertMarketSettlementStatus(t, fixture.marketIDs[1], "active", false)

	var payoutCount int
	if err := fixture.database.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM settlement_payouts WHERE market_id = $1", fixture.marketIDs[0]).Scan(&payoutCount); err != nil {
		t.Fatalf("count payouts: %v", err)
	}
	if payoutCount == 0 {
		t.Error("single-market settle wrote no settlement_payouts rows")
	}

	if err := fixture.service.SettleMarket(ctx, fixture.marketIDs[0], "Yes"); !errors.Is(err, ErrMarketAlreadySettled) {
		t.Errorf("second settle error = %v, want ErrMarketAlreadySettled", err)
	}
	if err := fixture.service.SettleMarket(ctx, fixture.marketIDs[1], "Maybe"); !errors.Is(err, ErrOutcomeNotOption) {
		t.Errorf("invalid option error = %v, want ErrOutcomeNotOption", err)
	}
	if err := fixture.service.SettleMarket(ctx, integrationUUID(t), "Yes"); !errors.Is(err, ErrMarketNotFound) {
		t.Errorf("unknown market error = %v, want ErrMarketNotFound", err)
	}
}
