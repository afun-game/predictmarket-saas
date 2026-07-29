package reconciliation

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

const defaultIntegrationDatabaseURL = "postgres://predictmarket:password@localhost:5432/predictmarket?sslmode=disable"

func TestPostgresReconciliationRecoversOnlyOrphanedLocks(t *testing.T) {
	if os.Getenv("INTEGRATION_TEST") != "1" {
		t.Skip("set INTEGRATION_TEST=1 to run PostgreSQL integration tests")
	}
	database := openDatabase(t)
	fixture := newFixture(t, database)
	t.Cleanup(fixture.cleanup)

	result, err := newService(newPostgresRepository(database)).Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if result.WalletsRecovered != 1 || result.AmountRecovered != 5 {
		t.Errorf("reconciliation result = %#v, want one recovered wallet and 5.00", result)
	}
	fixture.assertWallet(t, "orphaned", 100, 0)
	fixture.assertWallet(t, "protected", 95, 5)

	var transactions int
	if err := database.QueryRowContext(context.Background(), `
SELECT COUNT(*) FROM transactions WHERE wallet_id = $1 AND type = 'reconciliation'`, fixture.walletIDs["orphaned"]).Scan(&transactions); err != nil {
		t.Fatalf("count reconciliation transactions: %v", err)
	}
	if transactions != 1 {
		t.Errorf("reconciliation transactions = %d, want 1", transactions)
	}
}

type reconciliationFixture struct {
	database   *sql.DB
	merchantID string
	eventID    string
	marketID   string
	walletIDs  map[string]string
}

func newFixture(t *testing.T, database *sql.DB) *reconciliationFixture {
	t.Helper()
	fixture := &reconciliationFixture{
		database: database, merchantID: uuid.NewString(), eventID: uuid.NewString(), marketID: uuid.NewString(),
		walletIDs: map[string]string{"orphaned": uuid.NewString(), "protected": uuid.NewString()},
	}
	ctx := context.Background()
	_, err := database.ExecContext(ctx, `
INSERT INTO merchants (id, name, email, api_key, api_key_prefix, api_secret, status, currency, timezone)
VALUES ($1, 'Reconciliation integration', $2, $3, LEFT('pk_' || gen_random_uuid()::text, 16), 'secret-hash', 'active', 'USD', 'UTC')`,
		fixture.merchantID,
		"reconciliation-"+uuid.NewString()+"@example.com",
		"pk_reconciliation_"+uuid.NewString(),
	)
	if err != nil {
		t.Fatalf("insert merchant: %v", err)
	}
	_, err = database.ExecContext(ctx, `
INSERT INTO events (id, source_type, source_id, title, category, end_time, resolution_time, status)
VALUES ($1, 'custom', $2, 'Reconciliation event', 'integration', $3, $3, 'active')`,
		fixture.eventID,
		"reconciliation-"+uuid.NewString(),
		time.Now().UTC(),
	)
	if err != nil {
		t.Fatalf("insert event: %v", err)
	}
	_, err = database.ExecContext(ctx, `
INSERT INTO markets (id, merchant_id, event_id, type, question, options, status)
VALUES ($1, $2, $3, 'binary', 'Reconcile?', '["Yes","No"]', 'active')`,
		fixture.marketID,
		fixture.merchantID,
		fixture.eventID,
	)
	if err != nil {
		t.Fatalf("insert market: %v", err)
	}
	for userID, walletID := range fixture.walletIDs {
		if _, err := database.ExecContext(ctx, `
INSERT INTO wallets (id, merchant_id, user_id, currency, balance, locked_balance)
VALUES ($1, $2, $3, 'USD', 95, 5)`, walletID, fixture.merchantID, userID); err != nil {
			t.Fatalf("insert wallet %s: %v", userID, err)
		}
	}
	_, err = database.ExecContext(ctx, `
INSERT INTO orders (
    id, merchant_id, user_id, market_id, type, option, amount, filled_amount,
    currency, price, time_in_force, status
) VALUES ($1, $2, 'protected', $3, 'buy', 'Yes', 10, 0, 'USD', 0.5, 'gtc', 'pending')`,
		uuid.NewString(), fixture.merchantID, fixture.marketID)
	if err != nil {
		t.Fatalf("insert protected order: %v", err)
	}
	return fixture
}

func (f *reconciliationFixture) assertWallet(t *testing.T, userID string, wantBalance, wantLocked float64) {
	t.Helper()
	var balance, locked float64
	if err := f.database.QueryRowContext(context.Background(), `
SELECT balance, locked_balance FROM wallets WHERE id = $1`, f.walletIDs[userID]).Scan(&balance, &locked); err != nil {
		t.Fatalf("query wallet %s: %v", userID, err)
	}
	if balance != wantBalance || locked != wantLocked {
		t.Errorf("wallet %s = (%.2f, %.2f), want (%.2f, %.2f)", userID, balance, locked, wantBalance, wantLocked)
	}
}

func (f *reconciliationFixture) cleanup() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = f.database.ExecContext(ctx, "DELETE FROM transactions WHERE wallet_id = ANY($1)", []string{f.walletIDs["orphaned"], f.walletIDs["protected"]})
	_, _ = f.database.ExecContext(ctx, "DELETE FROM orders WHERE merchant_id = $1", f.merchantID)
	_, _ = f.database.ExecContext(ctx, "DELETE FROM wallets WHERE merchant_id = $1", f.merchantID)
	_, _ = f.database.ExecContext(ctx, "DELETE FROM markets WHERE id = $1", f.marketID)
	_, _ = f.database.ExecContext(ctx, "DELETE FROM events WHERE id = $1", f.eventID)
	_, _ = f.database.ExecContext(ctx, "DELETE FROM merchants WHERE id = $1", f.merchantID)
}

func openDatabase(t *testing.T) *sql.DB {
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
	t.Cleanup(func() { _ = database.Close() })
	return database
}
