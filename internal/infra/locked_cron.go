package infra

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/nxsky/twill/runtime/resource"
	"github.com/redis/go-redis/v9"
)

const cronLockPrefix = "predictmarket:cron:"

// RegisterLockedCronProvider configures every Twill cron resource to acquire a
// Redis lease before executing. It must be called before twill.Run.
func RegisterLockedCronProvider() {
	resource.RegisterCronProvider(lockedCronProvider{redisURL: redisURLFromEnvironment()})
}

type lockedCronProvider struct {
	redisURL string
}

func (p lockedCronProvider) Open(resource.Config) (resource.Cron, error) {
	options, err := redis.ParseURL(p.redisURL)
	if err != nil {
		return nil, fmt.Errorf("parse Redis cron lock URL: %w", err)
	}
	options.DialTimeout = redisOperationTimeout
	options.ReadTimeout = redisOperationTimeout
	options.WriteTimeout = redisOperationTimeout
	client := redis.NewClient(options)
	return &managedLockedCron{
		Cron: resource.NewLockedCron(
			resource.NewMemoryCron(),
			redisLockClient{client: client},
			cronLockPrefix,
			cronLockHolder(),
		),
		client: client,
	}, nil
}

func redisURLFromEnvironment() string {
	if value := strings.TrimSpace(os.Getenv("TWILL_TWILL_RESOURCES_EVENT_CACHE_DSN")); value != "" {
		return value
	}
	if value := strings.TrimSpace(os.Getenv("REDIS_URL")); value != "" {
		return value
	}
	return "redis://localhost:6379/0"
}

func cronLockHolder() string {
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = "unknown"
	}
	return fmt.Sprintf("%s:%d", hostname, os.Getpid())
}

type managedLockedCron struct {
	resource.Cron
	client *redis.Client
}

func (c *managedLockedCron) Close() error {
	if c == nil {
		return nil
	}
	if err := c.Cron.Close(); err != nil {
		return err
	}
	if c.client == nil {
		return nil
	}
	return c.client.Close()
}

type redisLockClient struct {
	client *redis.Client
}

func (c redisLockClient) SetNX(ctx context.Context, key, value string, ttl time.Duration) (bool, error) {
	return c.client.SetNX(ctx, key, value, ttl).Result()
}

func (c redisLockClient) CompareAndDelete(ctx context.Context, key, value string) (bool, error) {
	const compareAndDelete = `
if redis.call("GET", KEYS[1]) == ARGV[1] then
    return redis.call("DEL", KEYS[1])
end
return 0`
	deleted, err := c.client.Eval(ctx, compareAndDelete, []string{key}, value).Int64()
	if err != nil {
		return false, err
	}
	return deleted == 1, nil
}
