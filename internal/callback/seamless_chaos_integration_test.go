package callback_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/afun-game/predictmarket-saas/internal/callback"
	"github.com/afun-game/predictmarket-saas/internal/credentials"
	"github.com/afun-game/predictmarket-saas/internal/merchantsim"
	"github.com/afun-game/predictmarket-saas/internal/order"
	"github.com/afun-game/predictmarket-saas/internal/settlement"
	"github.com/afun-game/predictmarket-saas/pkg/types"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const (
	chaosEncryptionKey  = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY"
	chaosCallbackSecret = "test-callback-secret"
	chaosDefaultDBURL   = "postgres://predictmarket:password@localhost:5432/predictmarket?sslmode=disable"
)

// chaosFixture wires the platform seamless coordinator against the in-process
// merchant simulator (the same counterpart shipped in cmd/merchant-sim).
type chaosFixture struct {
	database    *sql.DB
	service     callback.Service
	coordinator *callback.SeamlessCoordinator
	settler     settlement.Service
	protector   *credentials.Protector
	merchantID  string
	eventID     string
	marketID    string
	sim         *merchantsim.Simulator
	simServer   *httptest.Server
}

func newChaosFixture(t *testing.T, simOptions merchantsim.Options) *chaosFixture {
	t.Helper()
	if os.Getenv("INTEGRATION_TEST") != "1" {
		t.Skip("set INTEGRATION_TEST=1 to run PostgreSQL integration tests")
	}
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		databaseURL = chaosDefaultDBURL
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
	protector, err := credentials.NewProtector(chaosEncryptionKey)
	if err != nil {
		t.Fatalf("NewProtector() error = %v", err)
	}
	encryptedSecret, err := protector.Encrypt(chaosCallbackSecret)
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	fixture := &chaosFixture{
		database:   database,
		protector:  protector,
		merchantID: integrationUUID(t),
		eventID:    integrationUUID(t),
		marketID:   integrationUUID(t),
	}
	simOptions.Secret = chaosCallbackSecret
	simOptions.MerchantID = fixture.merchantID
	if simOptions.InitialBalance == "" {
		simOptions.InitialBalance = "100.00"
	}
	fixture.sim, err = merchantsim.New(simOptions)
	if err != nil {
		t.Fatalf("merchantsim.New() error = %v", err)
	}
	fixture.simServer = httptest.NewServer(fixture.sim.Handler())
	t.Cleanup(fixture.simServer.Close)
	t.Cleanup(func() { fixture.cleanup(t) })

	fixture.insertMerchant(t, encryptedSecret)
	fixture.insertEvent(t)
	fixture.insertMarket(t)

	fixture.service, err = callback.NewWithDB(database, chaosEncryptionKey, true)
	if err != nil {
		t.Fatalf("callback.NewWithDB() error = %v", err)
	}
	fixture.coordinator, err = callback.NewSeamlessCoordinator(database, protector, fixture.service, true)
	if err != nil {
		t.Fatalf("NewSeamlessCoordinator() error = %v", err)
	}
	fixture.settler = settlement.NewPostgresService(database)
	return fixture
}

func (f *chaosFixture) insertMerchant(t *testing.T, encryptedSecret string) {
	t.Helper()
	now := time.Now().UTC()
	_, err := f.database.ExecContext(context.Background(), `
INSERT INTO merchants (
    id, name, email, api_key, api_key_prefix, api_secret, status, currency, timezone,
    wallet_mode, callback_url, callback_secret_enc, webhook_url, callback_verified_at
) VALUES ($1, $2, $3, $4, $5, $6, 'active', 'USD', 'UTC',
    'seamless', $7, $8, $9, $10)`,
		f.merchantID,
		"Chaos Merchant",
		"chaos-"+f.suffix()+"@example.com",
		"pk_chaos_"+f.suffix(),
		"pk_chaos_"+f.suffix()[:16],
		"secret-hash",
		f.simServer.URL+"/callback",
		encryptedSecret,
		f.simServer.URL+"/webhook",
		now,
	)
	if err != nil {
		t.Fatalf("insert chaos merchant: %v", err)
	}
}

func (f *chaosFixture) insertEvent(t *testing.T) {
	t.Helper()
	_, err := f.database.ExecContext(context.Background(), `
INSERT INTO events (
    id, source_type, source_id, title, category, end_time, resolution_time, status, outcome
) VALUES ($1, 'custom', $2, 'Chaos event', 'integration', $3, $3, 'resolved', 'Yes')`,
		f.eventID, "chaos-"+f.suffix(), time.Now().UTC().Add(-time.Hour))
	if err != nil {
		t.Fatalf("insert chaos event: %v", err)
	}
}

func (f *chaosFixture) insertMarket(t *testing.T) {
	t.Helper()
	_, err := f.database.ExecContext(context.Background(), `
INSERT INTO markets (id, merchant_id, event_id, type, question, options, status)
VALUES ($1, $2, $3, 'binary', 'Chaos market', '["Yes","No"]', 'active')`,
		f.marketID, f.merchantID, f.eventID)
	if err != nil {
		t.Fatalf("insert chaos market: %v", err)
	}
}

func (f *chaosFixture) suffix() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

func (f *chaosFixture) cleanup(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, _ = f.database.ExecContext(ctx, "DELETE FROM callback_dead_letters WHERE merchant_id = $1", f.merchantID)
	_, _ = f.database.ExecContext(ctx, "DELETE FROM webhook_outbox WHERE merchant_id = $1", f.merchantID)
	_, _ = f.database.ExecContext(ctx, "DELETE FROM callback_outbox WHERE merchant_id = $1", f.merchantID)
	_, _ = f.database.ExecContext(ctx, "DELETE FROM seamless_transactions WHERE merchant_id = $1", f.merchantID)
	_, _ = f.database.ExecContext(ctx, "DELETE FROM settlement_payouts WHERE market_id = $1", f.marketID)
	_, _ = f.database.ExecContext(ctx, "DELETE FROM market_settlements WHERE market_id = $1", f.marketID)
	_, _ = f.database.ExecContext(ctx, "DELETE FROM orders WHERE market_id = $1", f.marketID)
	_, _ = f.database.ExecContext(ctx, "DELETE FROM wallets WHERE merchant_id = $1", f.merchantID)
	_, _ = f.database.ExecContext(ctx, "DELETE FROM markets WHERE id = $1", f.marketID)
	_, _ = f.database.ExecContext(ctx, "DELETE FROM events WHERE id = $1", f.eventID)
	_, _ = f.database.ExecContext(ctx, "DELETE FROM merchants WHERE id = $1", f.merchantID)
	_ = f.database.Close()
}

func (f *chaosFixture) place(ctx context.Context, idempotencyKey string) (*types.Order, error) {
	return f.coordinator.Place(ctx, &order.CreateRequest{
		MerchantID:     f.merchantID,
		UserID:         "chaos-user",
		MarketID:       f.marketID,
		Type:           "buy",
		Option:         "Yes",
		Amount:         10,
		Currency:       "USD",
		Price:          0.5,
		TimeInForce:    "gtc",
		IdempotencyKey: idempotencyKey,
		Channel:        "api",
	})
}

func (f *chaosFixture) rollbackOutbox(ctx context.Context, transactionID string) (string, error) {
	var id string
	err := f.database.QueryRowContext(ctx, `
SELECT id FROM callback_outbox
WHERE merchant_id = $1 AND type = 'rollback' AND transaction_id = $2
ORDER BY created_at LIMIT 1`, f.merchantID, transactionID).Scan(&id)
	return id, err
}

func (f *chaosFixture) outboxStatus(ctx context.Context, outboxID string) (string, error) {
	var status string
	err := f.database.QueryRowContext(ctx,
		"SELECT status FROM callback_outbox WHERE id = $1", outboxID).Scan(&status)
	return status, err
}

func (f *chaosFixture) debitTransactionID(ctx context.Context, idempotencyKey string) (string, string, error) {
	var id string
	var status string
	err := f.database.QueryRowContext(ctx, `
SELECT transaction_id, status FROM seamless_transactions
WHERE merchant_id = $1 AND type = 'debit'
ORDER BY created_at DESC LIMIT 1`, f.merchantID).Scan(&id, &status)
	return id, status, err
}

func integrationUUID(t *testing.T) string {
	t.Helper()
	const hexChars = "0123456789abcdef"
	value := make([]byte, 36)
	for i := range value {
		switch i {
		case 8, 13, 18, 23:
			value[i] = '-'
		case 14:
			value[i] = '4'
		default:
			value[i] = hexChars[time.Now().UnixNano()%16]
		}
	}
	value[19] = 'a'
	return string(value)
}

func TestSeamlessChaosHealthyDebitPlacesOrder(t *testing.T) {
	fixture := newChaosFixture(t, merchantsim.Options{})
	ctx := context.Background()
	created, err := fixture.place(ctx, "chaos-healthy-1")
	if err != nil {
		t.Fatalf("Place() error = %v", err)
	}
	if created.ID == "" {
		t.Fatal("Place() returned an order without an ID")
	}
	if balance := fixture.sim.BalanceFor("chaos-user"); balance != 9500 {
		t.Fatalf("sim balance = %d, want 9500 after 5.00 debit", balance)
	}
	if _, status, err := fixture.debitTransactionID(ctx, "chaos-healthy-1"); err != nil || status != "accepted" {
		t.Fatalf("debit transaction status = %q, err = %v", status, err)
	}
	var balance, locked string
	if err := fixture.database.QueryRowContext(ctx, `
SELECT balance::text, locked_balance::text FROM wallets
WHERE merchant_id = $1 AND kind = 'shadow'`, fixture.merchantID).Scan(&balance, &locked); err != nil {
		t.Fatalf("query shadow wallet: %v", err)
	}
	if balance != "0.00" || locked != "5.00" {
		t.Fatalf("shadow wallet = (balance %s, locked %s), want (0.00, 5.00)", balance, locked)
	}
}

func TestSeamlessChaosDebitTimeoutEnqueuesRollback(t *testing.T) {
	fixture := newChaosFixture(t, merchantsim.Options{Delay: 4 * time.Second, DelayCount: 1})
	ctx := context.Background()
	_, err := fixture.place(ctx, "chaos-timeout-1")
	if !errors.Is(err, callback.ErrDebitUnknown) {
		t.Fatalf("Place() error = %v, want ErrDebitUnknown", err)
	}
	transactionID, status, err := fixture.debitTransactionID(ctx, "chaos-timeout-1")
	if err != nil || status != "unknown" {
		t.Fatalf("debit transaction = (%s, %s), err = %v, want status unknown", transactionID, status, err)
	}
	outboxID, err := fixture.rollbackOutbox(ctx, transactionID)
	if err != nil {
		t.Fatalf("rollback outbox missing for %s: %v", transactionID, err)
	}
	var orderCount int
	if err := fixture.database.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM orders WHERE market_id = $1", fixture.marketID).Scan(&orderCount); err != nil {
		t.Fatalf("count orders: %v", err)
	}
	if orderCount != 0 {
		t.Fatalf("orders = %d, want 0 after rejected timeout", orderCount)
	}

	// Wait for the simulator to finish the delayed debit, then deliver the
	// rollback: the merchant applied the debit, so the rollback reverses it.
	time.Sleep(1200 * time.Millisecond)
	if _, err := fixture.service.Dispatch(ctx); err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	status, err = fixture.outboxStatus(ctx, outboxID)
	if err != nil || status != "delivered" {
		t.Fatalf("rollback outbox status = %q, err = %v", status, err)
	}
	if balance := fixture.sim.BalanceFor("chaos-user"); balance != 10000 {
		t.Fatalf("sim balance after rollback = %d, want 10000", balance)
	}
	snapshot := fixture.sim.Snapshot()
	if state, exists := snapshot.Transactions[transactionID]; !exists || !state.RolledBack {
		t.Fatalf("sim rollback ledger for %s = %#v, want rolledBack", transactionID, state)
	}

	// Redelivering the rollback is acknowledged as a duplicate without a
	// second balance change.
	if _, err := fixture.database.ExecContext(ctx, `
UPDATE callback_outbox SET status = 'pending', next_attempt_at = NOW() WHERE id = $1`, outboxID); err != nil {
		t.Fatalf("reset rollback outbox: %v", err)
	}
	if _, err := fixture.service.Dispatch(ctx); err != nil {
		t.Fatalf("second Dispatch() error = %v", err)
	}
	if balance := fixture.sim.BalanceFor("chaos-user"); balance != 10000 {
		t.Fatalf("sim balance after duplicate rollback = %d, want 10000", balance)
	}
}

func TestSeamlessChaosDebit5xxTransientRollbackDelivered(t *testing.T) {
	fixture := newChaosFixture(t, merchantsim.Options{FailHTTPStatus: 503, FailCount: 1})
	ctx := context.Background()
	_, err := fixture.place(ctx, "chaos-fivexx-1")
	if !errors.Is(err, callback.ErrDebitUnknown) {
		t.Fatalf("Place() error = %v, want ErrDebitUnknown", err)
	}
	transactionID, _, err := fixture.debitTransactionID(ctx, "chaos-fivexx-1")
	if err != nil {
		t.Fatalf("debit transaction: %v", err)
	}
	outboxID, err := fixture.rollbackOutbox(ctx, transactionID)
	if err != nil {
		t.Fatalf("rollback outbox missing: %v", err)
	}
	if _, err := fixture.service.Dispatch(ctx); err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	status, err := fixture.outboxStatus(ctx, outboxID)
	if err != nil || status != "delivered" {
		t.Fatalf("rollback outbox status = %q, err = %v", status, err)
	}
	// The 5xx debit was never applied, so the rollback arrives before the bet:
	// the counterpart records and reverses it, crediting the amount once.
	if balance := fixture.sim.BalanceFor("chaos-user"); balance != 10500 {
		t.Fatalf("sim balance = %d, want 10500 (rollback-before-bet)", balance)
	}
}

func TestSeamlessChaosDebit5xxPersistentGoesToDeadLetterAndReplays(t *testing.T) {
	fixture := newChaosFixture(t, merchantsim.Options{FailHTTPStatus: 503})
	ctx := context.Background()
	_, err := fixture.place(ctx, "chaos-dlq-1")
	if !errors.Is(err, callback.ErrDebitUnknown) {
		t.Fatalf("Place() error = %v, want ErrDebitUnknown", err)
	}
	transactionID, _, err := fixture.debitTransactionID(ctx, "chaos-dlq-1")
	if err != nil {
		t.Fatalf("debit transaction: %v", err)
	}
	outboxID, err := fixture.rollbackOutbox(ctx, transactionID)
	if err != nil {
		t.Fatalf("rollback outbox missing: %v", err)
	}
	for i := 0; i < 6; i++ {
		if _, err := fixture.database.ExecContext(ctx, `
UPDATE callback_outbox SET next_attempt_at = NOW() WHERE id = $1`, outboxID); err != nil {
			t.Fatalf("reset outbox attempt window: %v", err)
		}
		if _, err := fixture.service.Dispatch(ctx); err != nil {
			t.Fatalf("Dispatch() #%d error = %v", i, err)
		}
		status, statusErr := fixture.outboxStatus(ctx, outboxID)
		if statusErr != nil {
			t.Fatalf("outbox status: %v", statusErr)
		}
		if status == "dead_letter" {
			break
		}
	}
	status, err := fixture.outboxStatus(ctx, outboxID)
	if err != nil || status != "dead_letter" {
		t.Fatalf("rollback outbox status = %q, err = %v, want dead_letter", status, err)
	}
	var deadLetters int
	if err := fixture.database.QueryRowContext(ctx, `
SELECT COUNT(*) FROM callback_dead_letters
WHERE channel = 'callback' AND outbox_id = $1`, outboxID).Scan(&deadLetters); err != nil {
		t.Fatalf("query dead letters: %v", err)
	}
	if deadLetters != 1 {
		t.Fatalf("dead letters = %d, want 1", deadLetters)
	}

	// Runbook replay moves the original row back to pending without creating a
	// replacement transaction.
	if err := fixture.service.ReplayDeadLetter(ctx, "callback", outboxID); err != nil {
		t.Fatalf("ReplayDeadLetter() error = %v", err)
	}
	status, err = fixture.outboxStatus(ctx, outboxID)
	if err != nil || status != "pending" {
		t.Fatalf("replayed outbox status = %q, err = %v, want pending", status, err)
	}
	var outboxCount int
	if err := fixture.database.QueryRowContext(ctx, `
SELECT COUNT(*) FROM callback_outbox
WHERE merchant_id = $1 AND transaction_id = $2`, fixture.merchantID, transactionID).Scan(&outboxCount); err != nil {
		t.Fatalf("count outbox rows: %v", err)
	}
	if outboxCount != 1 {
		t.Fatalf("outbox rows for %s = %d, want 1", transactionID, outboxCount)
	}

	fixture.sim.ClearFaultInjection()
	if _, err := fixture.service.Dispatch(ctx); err != nil {
		t.Fatalf("Dispatch() after replay error = %v", err)
	}
	status, err = fixture.outboxStatus(ctx, outboxID)
	if err != nil || status != "delivered" {
		t.Fatalf("replayed outbox status = %q, err = %v, want delivered", status, err)
	}
	if balance := fixture.sim.BalanceFor("chaos-user"); balance != 10500 {
		t.Fatalf("sim balance after replay = %d, want 10500 (rollback-before-bet)", balance)
	}
}

func TestSeamlessChaosDebitInsufficientFundsRejects(t *testing.T) {
	fixture := newChaosFixture(t, merchantsim.Options{InitialBalance: "1.00"})
	ctx := context.Background()
	_, err := fixture.place(ctx, "chaos-funds-1")
	if !errors.Is(err, callback.ErrInsufficientFunds) {
		t.Fatalf("Place() error = %v, want ErrInsufficientFunds", err)
	}
	_, status, err := fixture.debitTransactionID(ctx, "chaos-funds-1")
	if err != nil || status != "rejected" {
		t.Fatalf("debit transaction status = %q, err = %v, want rejected", status, err)
	}
	var rollbacks int
	if err := fixture.database.QueryRowContext(ctx, `
SELECT COUNT(*) FROM callback_outbox
WHERE merchant_id = $1 AND type = 'rollback'`, fixture.merchantID).Scan(&rollbacks); err != nil {
		t.Fatalf("count rollbacks: %v", err)
	}
	if rollbacks != 0 {
		t.Fatalf("rollbacks = %d, want 0 for insufficient funds", rollbacks)
	}
}

func TestSeamlessChaosDuplicateDebitIdempotent(t *testing.T) {
	fixture := newChaosFixture(t, merchantsim.Options{})
	ctx := context.Background()
	first, err := fixture.place(ctx, "chaos-dup-1")
	if err != nil {
		t.Fatalf("first Place() error = %v", err)
	}
	second, err := fixture.place(ctx, "chaos-dup-1")
	if err != nil {
		t.Fatalf("second Place() error = %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("duplicate placement order IDs differ: %s != %s", first.ID, second.ID)
	}
	if requests := fixture.sim.Snapshot().Requests; requests != 1 {
		t.Fatalf("sim callback requests = %d, want 1", requests)
	}
	var orderCount int
	if err := fixture.database.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM orders WHERE market_id = $1", fixture.marketID).Scan(&orderCount); err != nil {
		t.Fatalf("count orders: %v", err)
	}
	if orderCount != 1 {
		t.Fatalf("orders = %d, want 1", orderCount)
	}
}

func TestSeamlessChaosCreditOutboxDeliveredAndDuplicateAcknowledged(t *testing.T) {
	fixture := newChaosFixture(t, merchantsim.Options{})
	ctx := context.Background()
	if _, err := fixture.place(ctx, "chaos-credit-1"); err != nil {
		t.Fatalf("Place() error = %v", err)
	}
	if err := fixture.settler.SettleEvent(ctx, fixture.eventID); err != nil {
		t.Fatalf("SettleEvent() error = %v", err)
	}
	var creditOutboxID string
	if err := fixture.database.QueryRowContext(ctx, `
SELECT id FROM callback_outbox
WHERE merchant_id = $1 AND type = 'credit'
ORDER BY created_at LIMIT 1`, fixture.merchantID).Scan(&creditOutboxID); err != nil {
		t.Fatalf("query credit outbox: %v", err)
	}
	var creditStatus string
	if err := fixture.database.QueryRowContext(ctx, `
SELECT status FROM seamless_transactions
WHERE merchant_id = $1 AND type = 'credit'
ORDER BY created_at LIMIT 1`, fixture.merchantID).Scan(&creditStatus); err != nil {
		t.Fatalf("query seamless credit transaction: %v", err)
	}
	if creditStatus != "pending_delivery" {
		t.Fatalf("seamless credit status = %q, want pending_delivery", creditStatus)
	}

	if _, err := fixture.service.Dispatch(ctx); err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if balance := fixture.sim.BalanceFor("chaos-user"); balance != 10000 {
		t.Fatalf("sim balance after credit = %d, want 10000 (95.00 - 5.00 + 5.00)", balance)
	}
	status, err := fixture.outboxStatus(ctx, creditOutboxID)
	if err != nil || status != "delivered" {
		t.Fatalf("credit outbox status = %q, err = %v", status, err)
	}

	// Duplicate redelivery of the credit is acknowledged without a second
	// balance change.
	if _, err := fixture.database.ExecContext(ctx, `
UPDATE callback_outbox SET status = 'pending', next_attempt_at = NOW() WHERE id = $1`, creditOutboxID); err != nil {
		t.Fatalf("reset credit outbox: %v", err)
	}
	if _, err := fixture.service.Dispatch(ctx); err != nil {
		t.Fatalf("second Dispatch() error = %v", err)
	}
	if balance := fixture.sim.BalanceFor("chaos-user"); balance != 10000 {
		t.Fatalf("sim balance after duplicate credit = %d, want 10000", balance)
	}
}
