// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package resource

import (
	"context"
	"fmt"
	"time"
)

// CacheProvider is an interface for external cache implementations (e.g.,
// Redis, Memcached). Users register a provider via RegisterCacheProvider to
// replace the default in-memory cache with a production-grade client.
//
// The provider is called once per resource name and the result is
// singleton-managed by the Manager.
type CacheProvider interface {
	// Open creates a Cache handle for the given resource config. The
	// provider should use cfg.DSN as the connection address and cfg.Type
	// to determine the cache implementation (e.g., "redis", "memcached").
	Open(cfg Config) (Cache, error)
}

var cacheProvider CacheProvider

// RegisterCacheProvider registers a production cache provider (e.g., Redis).
// This should be called once at program startup, before twill.Run. If no
// provider is registered, the default in-memory cache is used.
//
// Example with go-redis:
//
//	type redisCacheProvider struct{}
//	func (redisCacheProvider) Open(cfg resource.Config) (resource.Cache, error) {
//	    opt, err := redis.ParseURL(cfg.DSN)
//	    if err != nil { return nil, err }
//	    return &redisCache{client: redis.NewClient(opt)}, nil
//	}
//	func init() {
//	    resource.RegisterCacheProvider(redisCacheProvider{})
//	}
func RegisterCacheProvider(p CacheProvider) {
	cacheProvider = p
}

// RedisCache is a Cache implementation backed by a Redis-like key-value store.
// It uses the CacheClient interface which abstracts the underlying Redis
// client, so users can adapt any Redis client library (go-redis, redigo, etc.)
// without adding a direct dependency to the runtime.
type RedisCache struct {
	client CacheClient
}

// CacheClient is the minimal interface for a Redis-like key-value client.
// Users implement this to bridge their preferred Redis library.
type CacheClient interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key, value string, ttl time.Duration) error
	Del(ctx context.Context, key string) error
}

// NewRedisCache returns a Cache backed by the provided CacheClient.
func NewRedisCache(client CacheClient) *RedisCache {
	return &RedisCache{client: client}
}

// Get implements Cache.
func (c *RedisCache) Get(ctx context.Context, key string) (string, error) {
	v, err := c.client.Get(ctx, key)
	if err != nil {
		return "", fmt.Errorf("redis get %q: %w", key, err)
	}
	return v, nil
}

// Put implements Cache.
func (c *RedisCache) Put(ctx context.Context, key, value string) error {
	if err := c.client.Set(ctx, key, value, 0); err != nil {
		return fmt.Errorf("redis set %q: %w", key, err)
	}
	return nil
}

// PutWithTTL sets a key with an expiration duration.
func (c *RedisCache) PutWithTTL(ctx context.Context, key, value string, ttl time.Duration) error {
	if err := c.client.Set(ctx, key, value, ttl); err != nil {
		return fmt.Errorf("redis set %q: %w", key, err)
	}
	return nil
}

// Delete implements Cache.
func (c *RedisCache) Delete(ctx context.Context, key string) error {
	if err := c.client.Del(ctx, key); err != nil {
		return fmt.Errorf("redis del %q: %w", key, err)
	}
	return nil
}
