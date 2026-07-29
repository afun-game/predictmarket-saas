package event

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const (
	defaultIntegrationDatabaseURL = "postgres://predictmarket:password@localhost:5432/predictmarket?sslmode=disable"
	defaultIntegrationRedisURL    = "redis://localhost:6379/0"
)

func TestEventPostgresRedisIntegration(t *testing.T) {
	if os.Getenv("INTEGRATION_TEST") != "1" {
		t.Skip("set INTEGRATION_TEST=1 to run PostgreSQL/Redis integration tests")
	}

	fixture := newEventIntegrationFixture(t)
	ctx := context.Background()
	created, err := fixture.service.Create(ctx, &CreateRequest{
		SourceType:     "custom",
		SourceID:       fixture.customSourceID,
		Title:          "PostgreSQL and Redis integration",
		Description:    "Integration test event.",
		Category:       fixture.category,
		EndTime:        "2027-03-01T12:00:00Z",
		ResolutionTime: "2027-03-01T13:00:00Z",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	fixture.createdEventID = created.ID
	fixture.assertDetailCached(t, ctx, created.ID)

	values, total, err := fixture.service.List(ctx, &ListFilters{Category: fixture.category})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	validCount := total == 1 && len(values) == 1
	if !validCount || values[0].ID != created.ID {
		t.Fatalf("List() values = %#v, total = %d", values, total)
	}
	versionBefore := fixture.cacheValue(t, ctx, eventListVersionKey)

	if err := fixture.service.UpdateStatus(ctx, created.ID, "active"); err != nil {
		t.Fatalf("UpdateStatus(active) error = %v", err)
	}
	versionAfter := fixture.cacheValue(t, ctx, eventListVersionKey)
	if versionBefore == versionAfter {
		t.Error("event list cache version did not change after status update")
	}
	if err := fixture.service.UpdateStatus(ctx, created.ID, "closed"); err != nil {
		t.Fatalf("UpdateStatus(closed) error = %v", err)
	}
	if err := fixture.service.Resolve(ctx, created.ID, "Yes"); err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	resolved, err := fixture.service.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get(resolved) error = %v", err)
	}
	validResolution := resolved.Status == "resolved" && resolved.Outcome != nil
	if !validResolution || *resolved.Outcome != "Yes" {
		t.Errorf("resolved event = %#v", resolved)
	}
	fixture.assertResolutionOutbox(t, ctx, created.ID)

	fixture.assertSourceUpsert(t, ctx)
}

type eventIntegrationFixture struct {
	database       *sql.DB
	redis          *redis.Client
	cache          *prefixedRedisCache
	service        *implementation
	category       string
	customSourceID string
	syncSourceID   string
	createdEventID string
}

func newEventIntegrationFixture(t *testing.T) *eventIntegrationFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	databaseURL := environmentOrDefault("DATABASE_URL", defaultIntegrationDatabaseURL)
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("open PostgreSQL: %v", err)
	}
	if err := database.PingContext(ctx); err != nil {
		_ = database.Close()
		t.Fatalf("ping PostgreSQL: %v", err)
	}

	redisURL := environmentOrDefault("REDIS_URL", defaultIntegrationRedisURL)
	redisOptions, err := redis.ParseURL(redisURL)
	if err != nil {
		_ = database.Close()
		t.Fatalf("parse Redis URL: %v", err)
	}
	redisClient := redis.NewClient(redisOptions)
	if err := redisClient.Ping(ctx).Err(); err != nil {
		_ = redisClient.Close()
		_ = database.Close()
		t.Fatalf("ping Redis: %v", err)
	}

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	cache := &prefixedRedisCache{
		client: redisClient,
		prefix: "predictmarket:integration:" + suffix + ":",
	}
	fixture := &eventIntegrationFixture{
		database:       database,
		redis:          redisClient,
		cache:          cache,
		service:        newService(newPostgresRepository(database)),
		category:       "integration-" + suffix,
		customSourceID: "integration-custom-" + suffix,
		syncSourceID:   "integration-polymarket-" + suffix,
	}
	fixture.service.cacheStore = cache
	t.Cleanup(func() {
		fixture.cleanup()
	})
	return fixture
}

func (f *eventIntegrationFixture) cleanup() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = f.database.ExecContext(
		ctx,
		`DELETE FROM event_outbox
WHERE event_id IN (
    SELECT id FROM events
    WHERE (source_type = 'custom' AND source_id = $1)
       OR (source_type = 'polymarket' AND source_id = $2)
)`,
		f.customSourceID,
		f.syncSourceID,
	)
	_, _ = f.database.ExecContext(
		ctx,
		`DELETE FROM events
WHERE (source_type = 'custom' AND source_id = $1)
   OR (source_type = 'polymarket' AND source_id = $2)`,
		f.customSourceID,
		f.syncSourceID,
	)
	_ = f.cache.DeletePrefix(ctx)
	_ = f.redis.Close()
	_ = f.database.Close()
}

func (f *eventIntegrationFixture) assertResolutionOutbox(
	t *testing.T,
	ctx context.Context,
	eventID string,
) {
	t.Helper()
	var eventType string
	var topic string
	var payloadEventID string
	if err := f.database.QueryRowContext(ctx, `
SELECT event_type, topic, payload->'data'->>'event_id'
FROM event_outbox
WHERE event_id = $1`, eventID).Scan(&eventType, &topic, &payloadEventID); err != nil {
		t.Fatalf("query resolution outbox: %v", err)
	}
	if eventType != "event_resolved" || topic != "predictmarket.event_resolved" || payloadEventID != eventID {
		t.Errorf("resolution outbox = (%q, %q, %q)", eventType, topic, payloadEventID)
	}
}

func (f *eventIntegrationFixture) assertDetailCached(
	t *testing.T,
	ctx context.Context,
	eventID string,
) {
	t.Helper()
	key := eventDetailCacheKey(eventID)
	if _, err := f.cache.Get(ctx, key); err != nil {
		t.Fatalf("get cached event detail: %v", err)
	}
	ttl, err := f.redis.TTL(ctx, f.cache.key(key)).Result()
	if err != nil {
		t.Fatalf("get cached event TTL: %v", err)
	}
	if ttl <= 0 || ttl > eventDetailTTL {
		t.Errorf("cached event TTL = %v, want 0 < TTL <= %v", ttl, eventDetailTTL)
	}
}

func (f *eventIntegrationFixture) cacheValue(
	t *testing.T,
	ctx context.Context,
	key string,
) string {
	t.Helper()
	value, err := f.cache.Get(ctx, key)
	if err != nil {
		t.Fatalf("get cache key %q: %v", key, err)
	}
	return value
}

func (f *eventIntegrationFixture) assertSourceUpsert(t *testing.T, ctx context.Context) {
	t.Helper()
	request := &SyncRequest{
		SourceID:       f.syncSourceID,
		Title:          "Initial source title",
		Category:       f.category,
		EndTime:        "2027-04-01T12:00:00Z",
		ResolutionTime: "2027-04-01T12:00:00Z",
		Status:         "active",
	}
	if err := f.service.SyncSource(ctx, request); err != nil {
		t.Fatalf("first SyncSource() error = %v", err)
	}
	request.Title = "Updated source title"
	if err := f.service.SyncSource(ctx, request); err != nil {
		t.Fatalf("second SyncSource() error = %v", err)
	}

	var count int
	var title string
	err := f.database.QueryRowContext(
		ctx,
		`SELECT COUNT(*), MAX(title)
FROM events
WHERE source_type = 'polymarket' AND source_id = $1`,
		f.syncSourceID,
	).Scan(&count, &title)
	if err != nil {
		t.Fatalf("query synced source: %v", err)
	}
	if count != 1 || title != request.Title {
		t.Errorf("synced source count = %d, title = %q", count, title)
	}
}

func environmentOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

type prefixedRedisCache struct {
	client *redis.Client
	prefix string
}

func (c *prefixedRedisCache) Get(ctx context.Context, key string) (string, error) {
	return c.client.Get(ctx, c.key(key)).Result()
}

func (c *prefixedRedisCache) Put(ctx context.Context, key, value string) error {
	return c.client.Set(ctx, c.key(key), value, 0).Err()
}

func (c *prefixedRedisCache) PutWithTTL(
	ctx context.Context,
	key string,
	value string,
	ttl time.Duration,
) error {
	return c.client.Set(ctx, c.key(key), value, ttl).Err()
}

func (c *prefixedRedisCache) Delete(ctx context.Context, key string) error {
	return c.client.Del(ctx, c.key(key)).Err()
}

func (c *prefixedRedisCache) DeletePrefix(ctx context.Context) error {
	var cursor uint64
	for {
		keys, nextCursor, err := c.client.Scan(ctx, cursor, c.prefix+"*", 100).Result()
		if err != nil {
			return err
		}
		if len(keys) > 0 {
			if err := c.client.Del(ctx, keys...).Err(); err != nil {
				return err
			}
		}
		cursor = nextCursor
		if cursor == 0 {
			return nil
		}
	}
}

func (c *prefixedRedisCache) key(key string) string {
	return c.prefix + key
}
