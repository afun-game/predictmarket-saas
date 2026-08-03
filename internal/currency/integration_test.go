package currency

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/redis/go-redis/v9"
)

const (
	defaultIntegrationDatabaseURL = "postgres://predictmarket:password@localhost:5432/predictmarket?sslmode=disable"
	defaultIntegrationRedisURL    = "redis://localhost:6379/0"
)

func TestCurrencyPostgresRedisIntegration(t *testing.T) {
	if os.Getenv("INTEGRATION_TEST") != "1" {
		t.Skip("set INTEGRATION_TEST=1 to run PostgreSQL/Redis integration tests")
	}
	fixture := newCurrencyIntegrationFixture(t)
	ctx := context.Background()
	if err := fixture.service.RefreshRates(ctx); err != nil {
		t.Fatalf("RefreshRates() error = %v", err)
	}
	fixture.assertStoredRates(t, 25)

	rate, err := fixture.service.GetRate(ctx, "EUR", "CNY")
	if err != nil {
		t.Fatalf("GetRate() error = %v", err)
	}
	if rate != 9 {
		t.Errorf("EUR/CNY rate = %v, want 9", rate)
	}
	fixture.assertCacheTTL(t, rateCacheKey("EUR", "CNY"))

	fixture.deleteStoredRates(t)
	cachedRate, err := fixture.service.GetRate(ctx, "EUR", "CNY")
	if err != nil {
		t.Fatalf("GetRate(cached) error = %v", err)
	}
	if cachedRate != 9 {
		t.Errorf("cached EUR/CNY rate = %v, want 9", cachedRate)
	}
	converted, err := fixture.service.Convert(ctx, 12.34, "USD", "EUR")
	if err != nil {
		t.Fatalf("Convert() error = %v", err)
	}
	if converted != 9.87 {
		t.Errorf("converted amount = %v, want 9.87", converted)
	}
}

type currencyIntegrationFixture struct {
	database  *sql.DB
	redis     *redis.Client
	cache     *prefixedCache
	service   *implementation
	provider  string
	timestamp time.Time
	server    *httptest.Server
}

func newCurrencyIntegrationFixture(t *testing.T) *currencyIntegrationFixture {
	t.Helper()
	database, err := sql.Open("pgx", environmentOrDefault("DATABASE_URL", defaultIntegrationDatabaseURL))
	if err != nil {
		t.Fatalf("open PostgreSQL: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := database.PingContext(ctx); err != nil {
		_ = database.Close()
		t.Fatalf("ping PostgreSQL: %v", err)
	}
	options, err := redis.ParseURL(environmentOrDefault("REDIS_URL", defaultIntegrationRedisURL))
	if err != nil {
		_ = database.Close()
		t.Fatalf("parse Redis URL: %v", err)
	}
	redisClient := redis.NewClient(options)
	if err := redisClient.Ping(ctx).Err(); err != nil {
		_ = redisClient.Close()
		_ = database.Close()
		t.Fatalf("ping Redis: %v", err)
	}
	timestamp := time.Now().UTC().Truncate(time.Second)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `{
            "base":"USD",
            "time_last_updated":%d,
            "rates":{"EUR":0.8,"CNY":7.2,"JPY":150,"GBP":0.7,"MXN":17.5}
        }`, timestamp.Unix())
	}))
	providerName := "integration-" + fmt.Sprintf("%d", time.Now().UnixNano())
	provider := newHTTPRateProvider(server.URL)
	provider.name = providerName
	cache := &prefixedCache{
		client: redisClient,
		prefix: "predictmarket:currency:integration:" + providerName + ":",
	}
	fixture := &currencyIntegrationFixture{
		database:  database,
		redis:     redisClient,
		cache:     cache,
		service:   newService(newPostgresRepository(database), cache, provider),
		provider:  providerName,
		timestamp: timestamp,
		server:    server,
	}
	t.Cleanup(fixture.cleanup)
	return fixture
}

func (f *currencyIntegrationFixture) assertStoredRates(t *testing.T, want int) {
	t.Helper()
	var count int
	if err := f.database.QueryRowContext(context.Background(), `
SELECT COUNT(*) FROM exchange_rates WHERE provider = $1 AND timestamp = $2`,
		f.provider,
		f.timestamp,
	).Scan(&count); err != nil {
		t.Fatalf("count exchange rates: %v", err)
	}
	if count != want {
		t.Errorf("stored rates = %d, want %d", count, want)
	}
}

func (f *currencyIntegrationFixture) assertCacheTTL(t *testing.T, key string) {
	t.Helper()
	ttl, err := f.redis.TTL(context.Background(), f.cache.prefix+key).Result()
	if err != nil {
		t.Fatalf("query cache TTL: %v", err)
	}
	if ttl < 55*time.Minute || ttl > rateCacheTTL {
		t.Errorf("cache TTL = %v, want approximately one hour", ttl)
	}
}

func (f *currencyIntegrationFixture) deleteStoredRates(t *testing.T) {
	t.Helper()
	if _, err := f.database.ExecContext(
		context.Background(),
		"DELETE FROM exchange_rates WHERE provider = $1 AND timestamp = $2",
		f.provider,
		f.timestamp,
	); err != nil {
		t.Fatalf("delete stored exchange rates: %v", err)
	}
}

func (f *currencyIntegrationFixture) cleanup() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = f.database.ExecContext(
		ctx,
		"DELETE FROM exchange_rates WHERE provider = $1 AND timestamp = $2",
		f.provider,
		f.timestamp,
	)
	_ = f.cache.deletePrefix(ctx)
	f.server.Close()
	_ = f.redis.Close()
	_ = f.database.Close()
}

type prefixedCache struct {
	client *redis.Client
	prefix string
}

func (c *prefixedCache) Get(ctx context.Context, key string) (string, error) {
	return c.client.Get(ctx, c.prefix+key).Result()
}

func (c *prefixedCache) Put(ctx context.Context, key, value string) error {
	return c.client.Set(ctx, c.prefix+key, value, 0).Err()
}

func (c *prefixedCache) PutWithTTL(
	ctx context.Context,
	key string,
	value string,
	ttl time.Duration,
) error {
	return c.client.Set(ctx, c.prefix+key, value, ttl).Err()
}

func (c *prefixedCache) Delete(ctx context.Context, key string) error {
	return c.client.Del(ctx, c.prefix+key).Err()
}

func (c *prefixedCache) deletePrefix(ctx context.Context) error {
	iterator := c.client.Scan(ctx, 0, c.prefix+"*", 100).Iterator()
	for iterator.Next(ctx) {
		if err := c.client.Del(ctx, iterator.Val()).Err(); err != nil {
			return err
		}
	}
	return iterator.Err()
}

func environmentOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
