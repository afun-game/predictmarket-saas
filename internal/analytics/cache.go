package analytics

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

const (
	analyticsCachePrefix = "predictmarket:analytics:v1:"
	analyticsCacheTTL    = 5 * time.Minute
)

type cacheWithTTL interface {
	PutWithTTL(ctx context.Context, key, value string, ttl time.Duration) error
}

func (s *implementation) getCached(ctx context.Context, key string, destination any) bool {
	if s.cacheStore == nil {
		return false
	}
	encoded, err := s.cacheStore.Get(ctx, key)
	if err != nil {
		return false
	}
	if err := json.Unmarshal([]byte(encoded), destination); err != nil {
		_ = s.cacheStore.Delete(ctx, key)
		return false
	}
	return true
}

func (s *implementation) putCached(ctx context.Context, key string, value any) {
	if s.cacheStore == nil {
		return
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return
	}
	if cache, ok := s.cacheStore.(cacheWithTTL); ok {
		_ = cache.PutWithTTL(ctx, key, string(encoded), analyticsCacheTTL)
		return
	}
	_ = s.cacheStore.Put(ctx, key, string(encoded))
}

func analyticsCacheKey(parts ...string) string {
	normalized := make([]string, 0, len(parts))
	for _, part := range parts {
		normalized = append(normalized, strings.ToLower(strings.TrimSpace(part)))
	}
	return analyticsCachePrefix + strings.Join(normalized, ":")
}
