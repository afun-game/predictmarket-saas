// Package infra connects Twill resource abstractions to local infrastructure.
package infra

import (
	"context"
	"fmt"
	"time"

	"github.com/nxsky/twill/runtime/resource"
	"github.com/redis/go-redis/v9"
)

const redisOperationTimeout = time.Second

// RegisterRedisCacheProvider configures Twill cache resources to use Redis.
func RegisterRedisCacheProvider() {
	resource.RegisterCacheProvider(redisCacheProvider{})
}

type redisCacheProvider struct{}

func (redisCacheProvider) Open(config resource.Config) (resource.Cache, error) {
	if config.Type != "redis" {
		return nil, fmt.Errorf("unsupported cache type %q", config.Type)
	}
	options, err := redis.ParseURL(config.DSN)
	if err != nil {
		return nil, fmt.Errorf("parse Redis cache URL: %w", err)
	}
	options.DialTimeout = redisOperationTimeout
	options.ReadTimeout = redisOperationTimeout
	options.WriteTimeout = redisOperationTimeout
	return resource.NewRedisCache(&redisCacheClient{
		client: redis.NewClient(options),
	}), nil
}

type redisCacheClient struct {
	client *redis.Client
}

func (c *redisCacheClient) Get(ctx context.Context, key string) (string, error) {
	return c.client.Get(ctx, key).Result()
}

func (c *redisCacheClient) Set(
	ctx context.Context,
	key string,
	value string,
	ttl time.Duration,
) error {
	return c.client.Set(ctx, key, value, ttl).Err()
}

func (c *redisCacheClient) Del(ctx context.Context, key string) error {
	return c.client.Del(ctx, key).Err()
}
