package settlementworker

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/nxsky/twill/runtime/resource"
	"github.com/afun-game/predictmarket-saas/internal/infra"
	"github.com/afun-game/predictmarket-saas/internal/messaging"
	"github.com/afun-game/predictmarket-saas/internal/settlement"
)

const defaultIntegrationDatabaseURL = "postgres://predictmarket:password@localhost:5432/predictmarket?sslmode=disable"

func TestSettlementWorkerPostgresNATSIntegration(t *testing.T) {
	if os.Getenv("INTEGRATION_TEST") != "1" {
		t.Skip("set INTEGRATION_TEST=1 to run PostgreSQL/NATS integration tests")
	}
	fixture := newWorkerIntegrationFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	count, err := fixture.worker.Dispatch(ctx)
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if count != 1 {
		t.Fatalf("Dispatch() = %d, want 1", count)
	}
	fixture.consumeOne(t, ctx)
	fixture.assertSettledOnce(t)

	if err := fixture.pubSub.Publish(ctx, fixture.topic, fixture.payload); err != nil {
		t.Fatalf("publish duplicate message: %v", err)
	}
	fixture.consumeOne(t, ctx)
	fixture.assertSettledOnce(t)
}

type workerIntegrationFixture struct {
	database   *sql.DB
	worker     *implementation
	pubSub     resource.PubSub
	merchantID string
	eventID    string
	marketID   string
	outboxID   string
	topic      string
	consumer   string
	payload    []byte
}

func newWorkerIntegrationFixture(t *testing.T) *workerIntegrationFixture {
	t.Helper()
	databaseURL := environmentOrDefault("DATABASE_URL", defaultIntegrationDatabaseURL)
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
	pubSub, err := infra.NewNATSPubSub(environmentOrDefault("NATS_URL", "nats://localhost:4222"))
	if err != nil {
		_ = database.Close()
		t.Fatalf("open NATS JetStream: %v", err)
	}
	suffix := strings.ReplaceAll(integrationUUID(t), "-", "")
	fixture := &workerIntegrationFixture{
		database:   database,
		pubSub:     pubSub,
		merchantID: integrationUUID(t),
		eventID:    integrationUUID(t),
		marketID:   integrationUUID(t),
		outboxID:   integrationUUID(t),
		topic:      "predictmarket.event_resolved.integration_" + suffix,
		consumer:   "settlement_integration_" + suffix,
	}
	message := messaging.NewEventResolved(fixture.eventID, "Yes", time.Now().UTC())
	fixture.payload, err = json.Marshal(message)
	if err != nil {
		t.Fatalf("marshal event message: %v", err)
	}
	fixture.insertRows(t)

	subscription, err := pubSub.Subscribe(ctx, fixture.topic, fixture.consumer)
	if err != nil {
		fixture.cleanup()
		t.Fatalf("subscribe to integration topic: %v", err)
	}
	fixture.worker = newService(
		newPostgresRepository(database),
		pubSub,
		settlement.NewPostgresService(database),
	)
	fixture.worker.topic = fixture.topic
	fixture.worker.consumer = fixture.consumer
	fixture.worker.subscription = subscription
	t.Cleanup(fixture.cleanup)
	return fixture
}

func (f *workerIntegrationFixture) insertRows(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	suffix := strings.ReplaceAll(f.eventID, "-", "")
	if _, err := f.database.ExecContext(ctx, `
INSERT INTO merchants (id, name, email, api_key, api_key_prefix, api_secret, status, currency, timezone)
VALUES ($1, 'Settlement worker', $2, $3, LEFT('pk_' || gen_random_uuid()::text, 16), 'secret-hash', 'active', 'USD', 'UTC')`,
		f.merchantID,
		"settlement-worker-"+suffix+"@example.com",
		"pk_settlement_worker_"+suffix,
	); err != nil {
		t.Fatalf("insert merchant: %v", err)
	}
	if _, err := f.database.ExecContext(ctx, `
INSERT INTO events (
    id, source_type, source_id, title, category, end_time, resolution_time, status, outcome
) VALUES ($1, 'custom', $2, 'Worker event', 'integration', $3, $3, 'resolved', 'Yes')`,
		f.eventID,
		"settlement-worker-"+suffix,
		time.Now().UTC(),
	); err != nil {
		t.Fatalf("insert event: %v", err)
	}
	if _, err := f.database.ExecContext(ctx, `
INSERT INTO markets (id, merchant_id, event_id, type, question, options, status)
VALUES ($1, $2, $3, 'binary', 'Worker market', '["Yes","No"]', 'closed')`,
		f.marketID,
		f.merchantID,
		f.eventID,
	); err != nil {
		t.Fatalf("insert market: %v", err)
	}
	if _, err := f.database.ExecContext(ctx, `
INSERT INTO event_outbox (id, event_id, event_type, topic, payload)
VALUES ($1, $2, $3, $4, $5)`,
		f.outboxID,
		f.eventID,
		messaging.EventResolvedType,
		f.topic,
		f.payload,
	); err != nil {
		t.Fatalf("insert outbox: %v", err)
	}
}

func (f *workerIntegrationFixture) consumeOne(t *testing.T, ctx context.Context) {
	t.Helper()
	message, err := f.worker.subscription.Next(ctx)
	if err != nil {
		t.Fatalf("receive event message: %v", err)
	}
	if err := f.worker.processUntilAcknowledged(ctx, message); err != nil {
		t.Fatalf("process event message: %v", err)
	}
}

func (f *workerIntegrationFixture) assertSettledOnce(t *testing.T) {
	t.Helper()
	var status string
	var settlementCount int
	var published bool
	err := f.database.QueryRowContext(context.Background(), `
SELECT m.status,
       (SELECT COUNT(*) FROM market_settlements WHERE market_id = m.id),
       (SELECT published_at IS NOT NULL FROM event_outbox WHERE id = $2)
FROM markets AS m
WHERE m.id = $1`, f.marketID, f.outboxID).Scan(&status, &settlementCount, &published)
	if err != nil {
		t.Fatalf("query settlement result: %v", err)
	}
	if status != "settled" || settlementCount != 1 || !published {
		t.Errorf(
			"settlement result = (%s, %d, %v), want (settled, 1, true)",
			status,
			settlementCount,
			published,
		)
	}
}

func (f *workerIntegrationFixture) cleanup() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if f.worker != nil && f.worker.subscription != nil {
		_ = f.worker.subscription.Close()
	}
	_, _ = f.database.ExecContext(ctx, "DELETE FROM settlement_payouts WHERE market_id = $1", f.marketID)
	_, _ = f.database.ExecContext(ctx, "DELETE FROM market_settlements WHERE market_id = $1", f.marketID)
	_, _ = f.database.ExecContext(ctx, "DELETE FROM event_outbox WHERE id = $1", f.outboxID)
	_, _ = f.database.ExecContext(ctx, "DELETE FROM markets WHERE id = $1", f.marketID)
	_, _ = f.database.ExecContext(ctx, "DELETE FROM events WHERE id = $1", f.eventID)
	_, _ = f.database.ExecContext(ctx, "DELETE FROM merchants WHERE id = $1", f.merchantID)
	_ = f.database.Close()
}

func environmentOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func integrationUUID(t *testing.T) string {
	t.Helper()
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		t.Fatalf("generate UUID: %v", err)
	}
	buffer[6] = (buffer[6] & 0x0f) | 0x40
	buffer[8] = (buffer[8] & 0x3f) | 0x80
	return fmt.Sprintf(
		"%x-%x-%x-%x-%x",
		buffer[:4],
		buffer[4:6],
		buffer[6:8],
		buffer[8:10],
		buffer[10:],
	)
}
