package adminquery

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// TestListOrdersMergesBetsIntegration pins that the admin order list returns
// book orders and parimutuel bets together, bets carrying type "bet".
func TestListOrdersMergesBetsIntegration(t *testing.T) {
	if os.Getenv("INTEGRATION_TEST") != "1" {
		t.Skip("set INTEGRATION_TEST=1 to run PostgreSQL integration tests")
	}
	database, err := sql.Open("pgx", os.Getenv("DATABASE_URL"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer database.Close()
	ctx := context.Background()

	merchantID := integrationUUID(t)
	userID := "adminquery-user"
	marketID := integrationUUID(t)
	eventID := integrationUUID(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := database.ExecContext(ctx, `
INSERT INTO merchants (id, name, email, api_key, api_key_prefix, api_secret, status, currency, timezone, wallet_mode, created_at, updated_at)
VALUES ($1, 'it-merchant', 'it@example.com', $3, $4, $5, 'active', 'USD', 'UTC', 'transfer', $2, $2)`,
		merchantID, now, genCredential(merchantID, "pk_")+"x", genCredential(merchantID, "pk_"), genCredential(merchantID, "sk_")); err != nil {
		t.Fatalf("insert merchant: %v", err)
	}
	if _, err := database.ExecContext(ctx, `
INSERT INTO events (id, source_type, source_id, title, description, category, end_time, resolution_time, status, created_at, updated_at)
VALUES ($1, 'custom', $2, 'IT event', '', 'other', $3, $3, 'active', $3, $3)`,
		eventID, "ev-"+eventID, now); err != nil {
		t.Fatalf("insert event: %v", err)
	}
	if _, err := database.ExecContext(ctx, `
INSERT INTO markets (id, merchant_id, event_id, type, question, options, status, total_volume, liquidity_pool, merchant_fee_rate, platform_fee_rate, created_at)
VALUES ($1, $2, $3, 'parimutuel', 'IT market', '["Yes","No"]', 'active', 0, 0, 0, 0, $4)`,
		marketID, merchantID, eventID, now); err != nil {
		t.Fatalf("insert market: %v", err)
	}
	orderID := integrationUUID(t)
	if _, err := database.ExecContext(ctx, `
INSERT INTO orders (id, merchant_id, user_id, market_id, type, option, amount, filled_amount, currency, price, time_in_force, status, created_at)
VALUES ($1, $2, $3, $4, 'buy', 'Yes', 5, 5, 'USD', 0.5, 'gtc', 'filled', $5)`,
		orderID, merchantID, userID, marketID, now); err != nil {
		t.Fatalf("insert order: %v", err)
	}
	betID := integrationUUID(t)
	if _, err := database.ExecContext(ctx, `
INSERT INTO parimutuel_bets (id, market_id, merchant_id, user_id, option, stake, currency, status, created_at)
VALUES ($1, $2, $3, $4, 'No', 10, 'USD', 'active', $5)`,
		betID, marketID, merchantID, userID, now.Add(time.Second)); err != nil {
		t.Fatalf("insert bet: %v", err)
	}

	service := New(database)
	items, total, err := service.ListOrders(ctx, "", userID, "", "", 1, 10)
	if err != nil {
		t.Fatalf("ListOrders() error = %v", err)
	}
	if total != 2 {
		t.Errorf("ListOrders() total = %d, want 2", total)
	}
	// The bet was created later, so it sorts first.
	if len(items) != 2 || items[0].Type != "bet" || items[1].Type != "buy" {
		t.Fatalf("ListOrders() items = %#v", items)
	}
	if items[0].Amount != 10 || items[0].Option != "No" || items[0].Status != "active" {
		t.Errorf("bet row = %#v", items[0])
	}
	// Merchant scoping must apply to bets too.
	otherID := integrationUUID(t)
	if _, err := database.ExecContext(ctx, `
INSERT INTO merchants (id, name, email, api_key, api_key_prefix, api_secret, status, currency, timezone, wallet_mode, created_at, updated_at)
VALUES ($1, 'it-other', 'o@example.com', $3, $4, $5, 'active', 'USD', 'UTC', 'transfer', $2, $2)`,
		otherID, now, genCredential(otherID, "pk_")+"x", genCredential(otherID, "pk_"), genCredential(otherID, "sk_")); err != nil {
		t.Fatalf("insert other merchant: %v", err)
	}
	otherBetID := integrationUUID(t)
	if _, err := database.ExecContext(ctx, `
INSERT INTO parimutuel_bets (id, market_id, merchant_id, user_id, option, stake, currency, status, created_at)
VALUES ($1, $2, $3, $4, 'Yes', 1, 'USD', 'active', $5)`,
		otherBetID, marketID, otherID, userID, now.Add(2*time.Second)); err != nil {
		t.Fatalf("insert other bet: %v", err)
	}
	scoped, scopedTotal, err := service.ListOrders(ctx, merchantID, userID, "", "", 1, 10)
	if err != nil {
		t.Fatalf("ListOrders(scoped) error = %v", err)
	}
	if scopedTotal != 2 || len(scoped) != 2 {
		t.Errorf("ListOrders(scoped) total/items = %d/%d, want 2/2", scopedTotal, len(scoped))
	}
	for _, item := range scoped {
		if item.MerchantID != merchantID {
			t.Errorf("scoped row leaked merchant %s", item.MerchantID)
		}
	}
}

func genCredential(seed, prefix string) string {
	// The UUID tail carries the nanosecond randomness; the fixed prefix
	// would otherwise collide for every fixture row.
	return prefix + strings.ReplaceAll(seed, "-", "")[20:]
}

func integrationUUID(t *testing.T) string {
	t.Helper()
	// 12 hex chars from the current time in nanoseconds keep parallel runs
	// unique; this is test data, not a security boundary.
	return "00000000-0000-4000-8000-" + fmt.Sprintf("%012x", time.Now().UnixNano()%0xffffffffffff)
}
