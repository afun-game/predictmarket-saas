package market

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

func TestMarketPostgresIntegration(t *testing.T) {
	if os.Getenv("INTEGRATION_TEST") != "1" {
		t.Skip("set INTEGRATION_TEST=1 to run PostgreSQL integration tests")
	}

	fixture := newMarketIntegrationFixture(t)
	ctx := context.Background()
	created, err := fixture.service.Create(ctx, &CreateRequest{
		MerchantID:    fixture.merchantID,
		EventID:       fixture.eventID,
		Type:          "binary",
		Question:      "Will the PostgreSQL integration test pass?",
		Options:       []string{"Yes", "No"},
		LiquidityPool: 250.50,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	fixture.marketID = created.ID

	stored, err := fixture.service.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	validStored := len(stored.Options) == 2 && stored.Options[0] == "Yes"
	if !validStored || stored.LiquidityPool != 250.50 {
		t.Fatalf("stored market = %#v", stored)
	}
	if stored.MerchantFeeRate != 0 || stored.PlatformFeeRate != 0 {
		t.Errorf(
			"stored fee rates = (%v, %v), want (0, 0)",
			stored.MerchantFeeRate,
			stored.PlatformFeeRate,
		)
	}
	if stored.Category != "integration" {
		t.Errorf("stored category = %q, want inherited event category integration", stored.Category)
	}
	// The fixture event resolves at now+2h; a market without an explicit
	// resolution time inherits it.
	if stored.ResolutionTime == nil || !stored.ResolutionTime.Truncate(time.Second).Equal(fixture.eventResolution().Truncate(time.Second)) {
		t.Errorf("stored resolution_time = %v, want inherited event resolution %v", stored.ResolutionTime, fixture.eventResolution())
	}
	values, total, err := fixture.service.List(ctx, &ListFilters{
		MerchantID: fixture.merchantID,
		EventID:    fixture.eventID,
		Status:     "active",
	})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if total != 1 || len(values) != 1 || values[0].ID != created.ID {
		t.Fatalf("List() values = %#v, total = %d", values, total)
	}

	newer, err := fixture.service.Create(ctx, &CreateRequest{
		MerchantID:    fixture.merchantID,
		EventID:       fixture.eventID,
		Type:          "binary",
		Question:      "Will popular sorting use volume?",
		Options:       []string{"Yes", "No"},
		LiquidityPool: 10,
	})
	if err != nil {
		t.Fatalf("Create(second market) error = %v", err)
	}
	t.Cleanup(func() {
		_, _ = fixture.database.ExecContext(context.Background(), "DELETE FROM markets WHERE id = $1", newer.ID)
	})
	if _, err := fixture.database.ExecContext(
		ctx,
		"UPDATE markets SET total_volume = CASE id WHEN $1 THEN 100 ELSE 10 END WHERE id IN ($1, $2)",
		created.ID,
		newer.ID,
	); err != nil {
		t.Fatalf("set market volumes: %v", err)
	}
	popular, _, err := fixture.service.List(ctx, &ListFilters{
		MerchantID: fixture.merchantID,
		Sort:       "popular",
	})
	if err != nil {
		t.Fatalf("List(popular) error = %v", err)
	}
	if len(popular) != 2 || popular[0].ID != created.ID {
		t.Fatalf("List(popular) values = %#v", popular)
	}

	const additions = 10
	var waitGroup sync.WaitGroup
	for range additions {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			if err := fixture.service.AddLiquidity(ctx, created.ID, 1.25); err != nil {
				t.Errorf("AddLiquidity() error = %v", err)
			}
		}()
	}
	waitGroup.Wait()
	stored, err = fixture.service.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get(after liquidity) error = %v", err)
	}
	if stored.LiquidityPool != 263 {
		t.Errorf("LiquidityPool = %v, want 263", stored.LiquidityPool)
	}

	if err := fixture.service.UpdateStatus(ctx, created.ID, "suspended"); err != nil {
		t.Fatalf("UpdateStatus(suspended) error = %v", err)
	}
	if err := fixture.service.UpdateStatus(ctx, created.ID, "active"); err != nil {
		t.Fatalf("UpdateStatus(active) error = %v", err)
	}
	if err := fixture.service.UpdateStatus(ctx, created.ID, "closed"); err != nil {
		t.Fatalf("UpdateStatus(closed) error = %v", err)
	}
	closed, err := fixture.service.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get(closed) error = %v", err)
	}
	if closed.Status != "closed" || closed.SettledAt != nil {
		t.Errorf("closed market = %#v", closed)
	}

	categorized, err := fixture.service.Create(ctx, &CreateRequest{
		MerchantID: fixture.merchantID,
		EventID:    fixture.eventID,
		Type:       "binary",
		Category:   "Crypto",
		Question:   "Will category filtering work?",
		Options:    []string{"Yes", "No"},
	})
	if err != nil {
		t.Fatalf("Create(categorized) error = %v", err)
	}
	if categorized.Category != "crypto" {
		t.Errorf("explicit category = %q, want lowercased crypto", categorized.Category)
	}
	catValues, catTotal, err := fixture.service.List(ctx, &ListFilters{
		MerchantID: fixture.merchantID,
		Category:   "crypto",
	})
	if err != nil {
		t.Fatalf("List(category) error = %v", err)
	}
	if catTotal != 1 || len(catValues) != 1 || catValues[0].ID != categorized.ID {
		t.Errorf("List(category=crypto) = %#v, total = %d", catValues, catTotal)
	}

	fixture.assertInactiveReferenceRejected(t, ctx)
}

type marketIntegrationFixture struct {
	database   *sql.DB
	service    *implementation
	merchantID string
	eventID    string
	marketID   string
	suffix     string
}

func newMarketIntegrationFixture(t *testing.T) *marketIntegrationFixture {
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

	merchantID, err := generateMarketID(rand.Reader)
	if err != nil {
		_ = database.Close()
		t.Fatalf("generate merchant fixture ID: %v", err)
	}
	eventID, err := generateMarketID(rand.Reader)
	if err != nil {
		_ = database.Close()
		t.Fatalf("generate event fixture ID: %v", err)
	}
	fixture := &marketIntegrationFixture{
		database:   database,
		service:    newService(newPostgresRepository(database)),
		merchantID: merchantID,
		eventID:    eventID,
		suffix:     fmt.Sprintf("%d", time.Now().UnixNano()),
	}
	t.Cleanup(fixture.cleanup)
	fixture.insertReferences(t, ctx)
	return fixture
}

func (f *marketIntegrationFixture) insertReferences(t *testing.T, ctx context.Context) {
	t.Helper()
	_, err := f.database.ExecContext(
		ctx,
		`INSERT INTO merchants (
    id, name, email, api_key, api_key_prefix, api_secret, status, currency, timezone
) VALUES ($1, $2, $3, $4, LEFT('pk_' || gen_random_uuid()::text, 16), $5, 'active', 'USD', 'UTC')`,
		f.merchantID,
		"Market integration merchant",
		"market-integration-"+f.suffix+"@example.com",
		"pk_integration_"+f.suffix,
		"secret-hash",
	)
	if err != nil {
		t.Fatalf("insert merchant fixture: %v", err)
	}
	_, err = f.database.ExecContext(
		ctx,
		`INSERT INTO events (
    id, source_type, source_id, title, description, category,
    end_time, resolution_time, status
) VALUES ($1, 'custom', $2, $3, '', 'integration', $4, $5, 'active')`,
		f.eventID,
		"market-integration-"+f.suffix,
		"Market integration event",
		time.Now().UTC().Add(time.Hour),
		f.eventResolution(),
	)
	if err != nil {
		t.Fatalf("insert event fixture: %v", err)
	}
}

func (f *marketIntegrationFixture) eventResolution() time.Time {
	return time.Now().UTC().Add(2 * time.Hour)
}

func (f *marketIntegrationFixture) assertInactiveReferenceRejected(
	t *testing.T,
	ctx context.Context,
) {
	t.Helper()
	_, err := f.database.ExecContext(ctx, "UPDATE events SET status = 'closed' WHERE id = $1", f.eventID)
	if err != nil {
		t.Fatalf("close event fixture: %v", err)
	}
	request := &CreateRequest{
		MerchantID:    f.merchantID,
		EventID:       f.eventID,
		Type:          "binary",
		Question:      "This market must not be created",
		Options:       []string{"Yes", "No"},
		LiquidityPool: 0,
	}
	if _, err := f.service.Create(ctx, request); !errors.Is(err, ErrInvalidReference) {
		t.Errorf("Create(inactive event) error = %v, want ErrInvalidReference", err)
	}
}

func (f *marketIntegrationFixture) cleanup() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if f.marketID != "" {
		_, _ = f.database.ExecContext(ctx, "DELETE FROM markets WHERE id = $1", f.marketID)
	}
	_, _ = f.database.ExecContext(ctx, "DELETE FROM events WHERE id = $1", f.eventID)
	_, _ = f.database.ExecContext(ctx, "DELETE FROM merchants WHERE id = $1", f.merchantID)
	_ = f.database.Close()
}
