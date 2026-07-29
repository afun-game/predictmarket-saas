package analytics

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
)

func TestAnalyticsPostgresRedisIntegration(t *testing.T) {
	if os.Getenv("INTEGRATION_TEST") != "1" {
		t.Skip("set INTEGRATION_TEST=1 to run PostgreSQL/Redis integration tests")
	}
	fixture := newAnalyticsIntegrationFixture(t)
	ctx := context.Background()

	merchantStats, err := fixture.service.GetMerchantStats(ctx, fixture.merchantID, "7d")
	if err != nil {
		t.Fatalf("GetMerchantStats() error = %v", err)
	}
	if merchantStats.TotalVolume != 10 || merchantStats.TotalOrders != 2 || merchantStats.ActiveUsers != 2 {
		t.Errorf("merchant order stats = %#v", merchantStats)
	}
	if merchantStats.ActiveMarkets != 1 || merchantStats.RevenueFromFee != 0 || len(merchantStats.RevenueByCurrency) != 0 {
		t.Errorf("merchant market/revenue stats = %#v", merchantStats)
	}
	fixture.assertCacheTTL(t, analyticsCacheKey("merchant", fixture.merchantID, "7d"))

	allStats, err := fixture.service.GetMerchantStats(ctx, fixture.merchantID, "all")
	if err != nil {
		t.Fatalf("GetMerchantStats(all) error = %v", err)
	}
	if allStats.TotalVolume != 30 || allStats.TotalOrders != 4 {
		t.Errorf("all-time merchant stats = %#v", allStats)
	}
	marketStats, err := fixture.service.GetMarketStats(ctx, fixture.marketID)
	if err != nil {
		t.Fatalf("GetMarketStats() error = %v", err)
	}
	if marketStats.TotalVolume != 30 || marketStats.TotalOrders != 4 || marketStats.UniqueTraders != 4 {
		t.Errorf("market stats = %#v", marketStats)
	}
	if len(marketStats.PriceHistory) != 4 || marketStats.Distribution["Yes"] != 0.5 || marketStats.Distribution["No"] != 0.5 {
		t.Errorf("market history/distribution = %#v", marketStats)
	}
	userStats, err := fixture.service.GetUserStats(ctx, fixture.merchantID, "winner")
	if err != nil {
		t.Fatalf("GetUserStats() error = %v", err)
	}
	if userStats.TotalOrders != 1 || userStats.TotalVolume != 10 || userStats.WinRate != 1 || userStats.ProfitByCurrency["USD"] != 10 {
		t.Errorf("user stats = %#v", userStats)
	}
	platformStats, err := fixture.service.GetPlatformStats(ctx, "7d")
	if err != nil {
		t.Fatalf("GetPlatformStats() error = %v", err)
	}
	if platformStats.TotalMerchants < 1 || platformStats.TotalMarkets < 1 || platformStats.TotalOrders < 2 {
		t.Errorf("platform stats = %#v", platformStats)
	}
}

type analyticsIntegrationFixture struct {
	database   *sql.DB
	redis      *redis.Client
	cache      *analyticsPrefixedCache
	service    *implementation
	merchantID string
	eventID    string
	marketID   string
	orderIDs   []string
}

func newAnalyticsIntegrationFixture(t *testing.T) *analyticsIntegrationFixture {
	t.Helper()
	database, err := sql.Open("pgx", analyticsEnvironmentOrDefault(
		"DATABASE_URL",
		"postgres://predictmarket:password@localhost:5432/predictmarket?sslmode=disable",
	))
	if err != nil {
		t.Fatalf("open PostgreSQL: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := database.PingContext(ctx); err != nil {
		t.Fatalf("ping PostgreSQL: %v", err)
	}
	redisOptions, err := redis.ParseURL(analyticsEnvironmentOrDefault("REDIS_URL", "redis://localhost:6379/0"))
	if err != nil {
		t.Fatalf("parse Redis URL: %v", err)
	}
	redisClient := redis.NewClient(redisOptions)
	if err := redisClient.Ping(ctx).Err(); err != nil {
		t.Fatalf("ping Redis: %v", err)
	}
	prefix := "analytics-integration-" + uuid.NewString()
	cache := &analyticsPrefixedCache{client: redisClient, prefix: prefix + ":"}
	fixture := &analyticsIntegrationFixture{
		database: database, redis: redisClient, cache: cache,
		service:    newService(newPostgresRepository(database), cache),
		merchantID: uuid.NewString(), eventID: uuid.NewString(), marketID: uuid.NewString(),
		orderIDs: []string{uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()},
	}
	fixture.insertData(t, prefix)
	t.Cleanup(fixture.cleanup)
	return fixture
}

func (f *analyticsIntegrationFixture) insertData(t *testing.T, prefix string) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	old := now.Add(-100 * 24 * time.Hour)
	_, err := f.database.ExecContext(ctx, `
INSERT INTO merchants (id, name, email, api_key, api_key_prefix, api_secret, status, currency, timezone)
VALUES ($1, 'Analytics Fixture', $2, $3, LEFT('pk_' || gen_random_uuid()::text, 16), 'secret', 'active', 'USD', 'UTC')`,
		f.merchantID, prefix+"@example.test", prefix)
	if err != nil {
		t.Fatalf("insert merchant: %v", err)
	}
	_, err = f.database.ExecContext(ctx, `
INSERT INTO events (id, source_type, source_id, title, category, end_time, resolution_time, status)
VALUES ($1, 'custom', $2, 'Analytics Fixture', 'sports', NOW() + INTERVAL '1 day', NOW() + INTERVAL '1 day', 'active')`,
		f.eventID, prefix)
	if err != nil {
		t.Fatalf("insert event: %v", err)
	}
	_, err = f.database.ExecContext(ctx, `
INSERT INTO markets (id, merchant_id, event_id, type, question, options, status, total_volume, liquidity_pool, created_at)
VALUES ($1, $2, $3, 'binary', 'Analytics?', '["Yes","No"]', 'active', 30, 100, $4)`,
		f.marketID, f.merchantID, f.eventID, old)
	if err != nil {
		t.Fatalf("insert market: %v", err)
	}
	users := []string{"winner", "counter", "old-winner", "old-counter"}
	options := []string{"Yes", "No", "Yes", "No"}
	prices := []float64{0.6, 0.4, 0.55, 0.45}
	amounts := []float64{10, 10, 20, 20}
	timestamps := []time.Time{now, now, old, old}
	for index, orderID := range f.orderIDs {
		_, err := f.database.ExecContext(ctx, `
INSERT INTO orders (
    id, merchant_id, user_id, market_id, type, option, amount, filled_amount,
    currency, price, time_in_force, status, created_at, filled_at
) VALUES ($1, $2, $3, $4, 'buy', $5, $6, $6, 'USD', $7, 'gtc', 'filled', $8, $8)`,
			orderID, f.merchantID, users[index], f.marketID, options[index], amounts[index], prices[index], timestamps[index])
		if err != nil {
			t.Fatalf("insert order %d: %v", index, err)
		}
	}
	winnerWallet, counterWallet := uuid.NewString(), uuid.NewString()
	_, err = f.database.ExecContext(ctx, `
INSERT INTO wallets (id, merchant_id, user_id, currency, balance, locked_balance)
VALUES ($1, $3, 'winner', 'USD', 20, 0), ($2, $3, 'counter', 'USD', 0, 0)`,
		winnerWallet, counterWallet, f.merchantID)
	if err != nil {
		t.Fatalf("insert wallets: %v", err)
	}
	_, err = f.database.ExecContext(ctx, `
INSERT INTO settlement_payouts (market_id, order_id, wallet_id, currency, stake, payout, created_at)
VALUES ($1, $2, $3, 'USD', 10, 20, $4)`, f.marketID, f.orderIDs[0], winnerWallet, now)
	if err != nil {
		t.Fatalf("insert settlement payout: %v", err)
	}
}

func (f *analyticsIntegrationFixture) assertCacheTTL(t *testing.T, key string) {
	t.Helper()
	ttl, err := f.redis.TTL(context.Background(), f.cache.prefix+key).Result()
	if err != nil || ttl < 4*time.Minute || ttl > analyticsCacheTTL {
		t.Errorf("cache TTL = %v, error = %v", ttl, err)
	}
}

func (f *analyticsIntegrationFixture) cleanup() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = f.database.ExecContext(ctx, "DELETE FROM transactions WHERE wallet_id IN (SELECT id FROM wallets WHERE merchant_id = $1)", f.merchantID)
	_, _ = f.database.ExecContext(ctx, "DELETE FROM settlement_payouts WHERE market_id = $1", f.marketID)
	_, _ = f.database.ExecContext(ctx, "DELETE FROM orders WHERE merchant_id = $1", f.merchantID)
	_, _ = f.database.ExecContext(ctx, "DELETE FROM wallets WHERE merchant_id = $1", f.merchantID)
	_, _ = f.database.ExecContext(ctx, "DELETE FROM markets WHERE id = $1", f.marketID)
	_, _ = f.database.ExecContext(ctx, "DELETE FROM events WHERE id = $1", f.eventID)
	_, _ = f.database.ExecContext(ctx, "DELETE FROM merchants WHERE id = $1", f.merchantID)
	_ = f.cache.deletePrefix(ctx)
	_ = f.redis.Close()
	_ = f.database.Close()
}

type analyticsPrefixedCache struct {
	client *redis.Client
	prefix string
}

func (c *analyticsPrefixedCache) Get(ctx context.Context, key string) (string, error) {
	return c.client.Get(ctx, c.prefix+key).Result()
}
func (c *analyticsPrefixedCache) Put(ctx context.Context, key, value string) error {
	return c.client.Set(ctx, c.prefix+key, value, 0).Err()
}
func (c *analyticsPrefixedCache) PutWithTTL(ctx context.Context, key, value string, ttl time.Duration) error {
	return c.client.Set(ctx, c.prefix+key, value, ttl).Err()
}
func (c *analyticsPrefixedCache) Delete(ctx context.Context, key string) error {
	return c.client.Del(ctx, c.prefix+key).Err()
}
func (c *analyticsPrefixedCache) deletePrefix(ctx context.Context) error {
	iterator := c.client.Scan(ctx, 0, c.prefix+"*", 100).Iterator()
	for iterator.Next(ctx) {
		if err := c.client.Del(ctx, iterator.Val()).Err(); err != nil {
			return err
		}
	}
	return iterator.Err()
}

func analyticsEnvironmentOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
