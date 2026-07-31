// Package ratelimit provides layered per-key and per-session rate limiting
// for the V3 merchant and hosted APIs (V3 §7.3).
package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// ErrLimited is returned by Limiter.Allow when the key exceeds its quota.
var ErrLimited = errors.New("rate limit exceeded")

// Limiter enforces a fixed window quota per opaque key.
type Limiter interface {
	// Allow returns nil when the request may proceed, ErrLimited when the
	// key's window quota is exhausted, or another error for infrastructure
	// failures. Infrastructure errors are logged but must not block traffic.
	Allow(ctx context.Context, key string) error
}

// NewRedisLimiter returns a Redis-backed fixed-window limiter. A nil client
// disables limiting entirely (local tests without Redis).
func NewRedisLimiter(client *redis.Client, limit int, window time.Duration) Limiter {
	if client == nil || limit <= 0 || window < time.Second {
		return disabledLimiter{}
	}
	return &redisLimiter{
		client: client,
		limit:  limit,
		window: window,
		now:    time.Now,
	}
}

type redisLimiter struct {
	client *redis.Client
	limit  int
	window time.Duration
	now    func() time.Time
}

func (l *redisLimiter) Allow(ctx context.Context, key string) error {
	bucket := l.now().UTC().Unix() / int64(l.window/time.Second)
	redisKey := fmt.Sprintf("ratelimit:%s:%d", key, bucket)
	count, err := l.client.Incr(ctx, redisKey).Result()
	if err != nil {
		return fmt.Errorf("increment rate limit key: %w", err)
	}
	if count == 1 {
		// Best-effort TTL; a missed expiration only delays a bucket reset.
		l.client.Expire(ctx, redisKey, l.window)
	}
	if count > int64(l.limit) {
		return ErrLimited
	}
	return nil
}

type disabledLimiter struct{}

func (disabledLimiter) Allow(context.Context, string) error { return nil }

// NewMemoryLimiter returns an in-memory limiter for tests.
func NewMemoryLimiter(limit int, window time.Duration) Limiter {
	return &memoryLimiter{
		limit:   limit,
		window:  window,
		now:     time.Now,
		buckets: map[string]memoryBucket{},
	}
}

type memoryBucket struct {
	count  int
	expiry time.Time
}

type memoryLimiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	now     func() time.Time
	buckets map[string]memoryBucket
}

func (l *memoryLimiter) Allow(_ context.Context, key string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	bucket, exists := l.buckets[key]
	if !exists || now.After(bucket.expiry) {
		l.buckets[key] = memoryBucket{count: 1, expiry: now.Add(l.window)}
		return nil
	}
	bucket.count++
	if bucket.count > l.limit {
		return ErrLimited
	}
	l.buckets[key] = bucket
	return nil
}

// RedisLimiter owns a Redis client and a fixed-window quota.
type RedisLimiter struct {
	client *redis.Client
	limit  int
	window time.Duration
	now    func() time.Time
}

// NewRedisLimiterFromURL builds a Redis-backed limiter from a DSN.
func NewRedisLimiterFromURL(url string, limit int, window time.Duration) (*RedisLimiter, error) {
	options, err := redis.ParseURL(strings.TrimSpace(url))
	if err != nil {
		return nil, fmt.Errorf("parse rate limit Redis URL: %w", err)
	}
	options.DialTimeout = time.Second
	options.ReadTimeout = time.Second
	options.WriteTimeout = time.Second
	return &RedisLimiter{
		client: redis.NewClient(options),
		limit:  limit,
		window: window,
		now:    time.Now,
	}, nil
}

// Allow enforces the fixed-window quota for key.
func (l *RedisLimiter) Allow(ctx context.Context, key string) error {
	if l == nil || l.client == nil {
		return nil
	}
	bucket := l.now().UTC().Unix() / int64(l.window/time.Second)
	redisKey := fmt.Sprintf("ratelimit:%s:%d", key, bucket)
	count, err := l.client.Incr(ctx, redisKey).Result()
	if err != nil {
		return fmt.Errorf("increment rate limit key: %w", err)
	}
	if count == 1 {
		l.client.Expire(ctx, redisKey, l.window)
	}
	if count > int64(l.limit) {
		return ErrLimited
	}
	return nil
}

// Close releases the Redis client.
func (l *RedisLimiter) Close() error {
	if l == nil || l.client == nil {
		return nil
	}
	return l.client.Close()
}
