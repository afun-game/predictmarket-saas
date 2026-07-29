package wallet

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const defaultIntegrationDatabaseURL = "postgres://predictmarket:password@localhost:5432/predictmarket?sslmode=disable"

func TestWalletPostgresIntegration(t *testing.T) {
	if os.Getenv("INTEGRATION_TEST") != "1" {
		t.Skip("set INTEGRATION_TEST=1 to run PostgreSQL integration tests")
	}

	fixture := newWalletIntegrationFixture(t)
	ctx := context.Background()
	created, err := fixture.service.Create(ctx, fixture.merchantID, fixture.userID, "USD")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	fixture.walletID = created.ID
	if _, err := fixture.service.Create(ctx, fixture.merchantID, fixture.userID, "USD"); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("Create(duplicate) error = %v, want ErrAlreadyExists", err)
	}
	if err := fixture.service.Credit(ctx, fixture.merchantID, fixture.userID, "USD", 20, "credit"); err != nil {
		t.Fatalf("Credit() error = %v", err)
	}
	for range 2 {
		if err := fixture.service.CreditWithIdempotency(
			ctx,
			fixture.merchantID,
			fixture.userID,
			"USD",
			5,
			"credit",
			"wallet-postgres-retry-key",
		); err != nil {
			t.Fatalf("CreditWithIdempotency() error = %v", err)
		}
	}

	const debitAttempts = 30
	var waitGroup sync.WaitGroup
	var successfulDebits atomic.Int32
	for range debitAttempts {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			err := fixture.service.Debit(ctx, fixture.merchantID, fixture.userID, "USD", 1, "debit")
			switch {
			case err == nil:
				successfulDebits.Add(1)
			case errors.Is(err, ErrInsufficientBalance):
			default:
				t.Errorf("Debit() error = %v", err)
			}
		}()
	}
	waitGroup.Wait()
	if successfulDebits.Load() != 25 {
		t.Errorf("successful debits = %d, want 25", successfulDebits.Load())
	}

	if err := fixture.service.Credit(ctx, fixture.merchantID, fixture.userID, "USD", 50, "win"); err != nil {
		t.Fatalf("Credit(win) error = %v", err)
	}
	if err := fixture.service.Lock(ctx, fixture.merchantID, fixture.userID, "USD", 30); err != nil {
		t.Fatalf("Lock() error = %v", err)
	}
	if err := fixture.service.Unlock(ctx, fixture.merchantID, fixture.userID, "USD", 10); err != nil {
		t.Fatalf("Unlock() error = %v", err)
	}
	available, locked, err := fixture.service.GetBalance(ctx, fixture.merchantID, fixture.userID, "USD")
	if err != nil {
		t.Fatalf("GetBalance() error = %v", err)
	}
	if available != 30 || locked != 20 {
		t.Errorf("balance = (%v, %v), want (30, 20)", available, locked)
	}

	transactions, total, err := fixture.service.ListTransactions(
		ctx,
		fixture.merchantID,
		fixture.userID,
		1,
		100,
	)
	if err != nil {
		t.Fatalf("ListTransactions() error = %v", err)
	}
	if total != 28 || len(transactions) != 28 {
		t.Fatalf("ListTransactions() len = %d, total = %d, want 28", len(transactions), total)
	}
	for _, transaction := range transactions {
		if transaction.WalletID != fixture.walletID || transaction.Status != "completed" {
			t.Errorf("transaction = %#v", transaction)
		}
	}

	fixture.assertAutoCreateAndMerchantValidation(t, ctx)
}

type walletIntegrationFixture struct {
	database   *sql.DB
	service    *implementation
	merchantID string
	userID     string
	walletID   string
	suffix     string
}

func newWalletIntegrationFixture(t *testing.T) *walletIntegrationFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		databaseURL = defaultIntegrationDatabaseURL
	}
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("open PostgreSQL: %v", err)
	}
	if err := database.PingContext(ctx); err != nil {
		_ = database.Close()
		t.Fatalf("ping PostgreSQL: %v", err)
	}
	merchantID, err := generateUUID(rand.Reader)
	if err != nil {
		_ = database.Close()
		t.Fatalf("generate merchant fixture ID: %v", err)
	}
	fixture := &walletIntegrationFixture{
		database:   database,
		service:    newService(newPostgresRepository(database)),
		merchantID: merchantID,
		userID:     "wallet-integration-user",
		suffix:     fmt.Sprintf("%d", time.Now().UnixNano()),
	}
	t.Cleanup(fixture.cleanup)
	_, err = database.ExecContext(
		ctx,
		`INSERT INTO merchants (
    id, name, email, api_key, api_key_prefix, api_secret, status, currency, timezone
) VALUES ($1, $2, $3, $4, LEFT('pk_' || gen_random_uuid()::text, 16), $5, 'active', 'USD', 'UTC')`,
		fixture.merchantID,
		"Wallet integration merchant",
		"wallet-integration-"+fixture.suffix+"@example.com",
		"pk_wallet_integration_"+fixture.suffix,
		"secret-hash",
	)
	if err != nil {
		t.Fatalf("insert merchant fixture: %v", err)
	}
	return fixture
}

func (f *walletIntegrationFixture) assertAutoCreateAndMerchantValidation(
	t *testing.T,
	ctx context.Context,
) {
	t.Helper()
	if err := f.service.Credit(ctx, f.merchantID, f.userID, "EUR", 5, "credit"); err != nil {
		t.Fatalf("Credit(auto-create) error = %v", err)
	}
	euroWallet, err := f.service.Get(ctx, f.merchantID, f.userID, "EUR")
	if err != nil {
		t.Fatalf("Get(auto-created) error = %v", err)
	}
	if euroWallet.Balance != 5 {
		t.Errorf("auto-created EUR balance = %v, want 5", euroWallet.Balance)
	}

	if _, err := f.database.ExecContext(
		ctx,
		"UPDATE merchants SET status = 'inactive' WHERE id = $1",
		f.merchantID,
	); err != nil {
		t.Fatalf("deactivate merchant fixture: %v", err)
	}
	err = f.service.Credit(ctx, f.merchantID, "inactive-user", "USD", 1, "credit")
	if !errors.Is(err, ErrInvalidMerchant) {
		t.Errorf("Credit(inactive merchant) error = %v, want ErrInvalidMerchant", err)
	}
}

func (f *walletIntegrationFixture) cleanup() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = f.database.ExecContext(
		ctx,
		`DELETE FROM transactions
WHERE wallet_id IN (SELECT id FROM wallets WHERE merchant_id = $1)`,
		f.merchantID,
	)
	_, _ = f.database.ExecContext(ctx, "DELETE FROM wallets WHERE merchant_id = $1", f.merchantID)
	_, _ = f.database.ExecContext(ctx, "DELETE FROM merchants WHERE id = $1", f.merchantID)
	_ = f.database.Close()
}
