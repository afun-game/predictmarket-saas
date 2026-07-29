package sports

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/redis/go-redis/v9"
	"github.com/afun-game/predictmarket-saas/internal/event"
	"github.com/afun-game/predictmarket-saas/pkg/polymarket"
)

func TestSportsPostgresRedisIntegration(t *testing.T) {
	if os.Getenv("INTEGRATION_TEST") != "1" {
		t.Skip("set INTEGRATION_TEST=1 to run PostgreSQL/Redis integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	database, err := sql.Open("pgx", sportsEnvironmentOrDefault("DATABASE_URL", "postgres://predictmarket:password@localhost:5432/predictmarket?sslmode=disable"))
	if err != nil {
		t.Fatalf("open PostgreSQL: %v", err)
	}
	if err := database.PingContext(ctx); err != nil {
		_ = database.Close()
		t.Fatalf("ping PostgreSQL: %v", err)
	}
	redisOptions, err := redis.ParseURL(sportsEnvironmentOrDefault("REDIS_URL", "redis://localhost:6379/0"))
	if err != nil {
		t.Fatalf("parse Redis URL: %v", err)
	}
	redisClient := redis.NewClient(redisOptions)
	if err := redisClient.Ping(ctx).Err(); err != nil {
		t.Fatalf("ping Redis: %v", err)
	}
	prefix := "sports-integration-" + uuid.NewString()
	cache := &sportsPrefixedCache{client: redisClient, prefix: prefix + ":"}
	start := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Second)
	source := &stubSportsSource{
		catalog: []polymarket.Sport{{Sport: "wnba", SeriesID: "10105"}},
		events: []polymarket.Event{{
			ID: prefix + "-source", Title: "Connecticut Sun vs. Washington Mystics",
			StartTime: start, EndDate: start.Add(2 * time.Hour), GameID: 13002430, Active: true,
			Teams: []polymarket.Team{
				{Name: "Connecticut Sun", Abbreviation: "conn", Ordering: "away"},
				{Name: "Washington Mystics", Abbreviation: "wsh", Ordering: "home"},
			},
		}},
	}
	service := newService(newPostgresRepository(database))
	service.cacheStore, service.source = cache, source
	service.sink = &integrationEventSink{database: database, ids: map[string]string{}}
	service.leagues = configuredLeagues("wnba")
	t.Cleanup(func() {
		_, _ = database.ExecContext(context.Background(), `DELETE FROM events WHERE source_id LIKE $1`, prefix+"%")
		_ = cache.deletePrefix(context.Background())
		_ = redisClient.Close()
		_ = database.Close()
	})

	if err := service.SyncFromPolymarket(ctx); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	if err := service.SyncFromPolymarket(ctx); err != nil {
		t.Fatalf("repeated sync: %v", err)
	}
	values, total, err := service.ListEvents(ctx, &EventFilters{League: "wnba", Team: "sun", Status: "active"})
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	if total != 1 || len(values) != 1 {
		t.Fatalf("ListEvents() count = %d/%d, want 1/1", len(values), total)
	}
	if len(values[0].Teams) != 2 || values[0].Teams[0].Role != "away" {
		t.Errorf("teams = %#v", values[0].Teams)
	}
	loaded, err := service.GetEvent(ctx, values[0].Event.ID)
	if err != nil {
		t.Fatalf("GetEvent() error = %v", err)
	}
	if loaded.GameID != "13002430" || loaded.StartTime == nil || !loaded.StartTime.Equal(start) {
		t.Errorf("loaded = %#v", loaded)
	}
	ttl, err := redisClient.TTL(ctx, cache.prefix+detailCacheKey(loaded.Event.ID)).Result()
	if err != nil || ttl <= 0 || ttl > detailCacheTTL {
		t.Errorf("detail cache TTL = %v, error = %v", ttl, err)
	}
	var sportsCount, teamCount int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM sports_events WHERE event_id = $1`, loaded.Event.ID).Scan(&sportsCount); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM sports_event_teams WHERE event_id = $1`, loaded.Event.ID).Scan(&teamCount); err != nil {
		t.Fatal(err)
	}
	if sportsCount != 1 || teamCount != 2 {
		t.Errorf("persisted sports/teams = %d/%d, want 1/2", sportsCount, teamCount)
	}
}

type integrationEventSink struct {
	database *sql.DB
	ids      map[string]string
}

func (s *integrationEventSink) SyncSource(ctx context.Context, request *event.SyncRequest) error {
	id := s.ids[request.SourceID]
	if id == "" {
		id = uuid.NewString()
		s.ids[request.SourceID] = id
	}
	endTime, err := time.Parse(time.RFC3339, request.EndTime)
	if err != nil {
		return err
	}
	_, err = s.database.ExecContext(ctx, `
INSERT INTO events (id, source_type, source_id, title, description, category, end_time, resolution_time, status, created_at, updated_at)
VALUES ($1, 'polymarket', $2, $3, $4, $5, $6, $6, $7, NOW(), NOW())
ON CONFLICT (source_type, source_id) DO UPDATE
SET title = EXCLUDED.title, category = EXCLUDED.category, status = EXCLUDED.status, updated_at = NOW()`,
		id, request.SourceID, request.Title, request.Description, request.Category, endTime, request.Status)
	return err
}

type sportsPrefixedCache struct {
	client *redis.Client
	prefix string
}

func (c *sportsPrefixedCache) Get(ctx context.Context, key string) (string, error) {
	return c.client.Get(ctx, c.prefix+key).Result()
}
func (c *sportsPrefixedCache) Put(ctx context.Context, key, value string) error {
	return c.client.Set(ctx, c.prefix+key, value, 0).Err()
}
func (c *sportsPrefixedCache) PutWithTTL(ctx context.Context, key, value string, ttl time.Duration) error {
	return c.client.Set(ctx, c.prefix+key, value, ttl).Err()
}
func (c *sportsPrefixedCache) Delete(ctx context.Context, key string) error {
	return c.client.Del(ctx, c.prefix+key).Err()
}
func (c *sportsPrefixedCache) deletePrefix(ctx context.Context) error {
	iterator := c.client.Scan(ctx, 0, c.prefix+"*", 100).Iterator()
	for iterator.Next(ctx) {
		if err := c.client.Del(ctx, iterator.Val()).Err(); err != nil {
			return err
		}
	}
	return iterator.Err()
}

func sportsEnvironmentOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
