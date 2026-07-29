package currency

import (
	"context"
	"encoding/json"
	"time"
)

type cacheWithTTL interface {
	PutWithTTL(ctx context.Context, key, value string, ttl time.Duration) error
}

func (s *implementation) getCachedRate(
	ctx context.Context,
	from string,
	to string,
) (rateRecord, bool) {
	if s.cacheStore == nil {
		return rateRecord{}, false
	}
	encoded, err := s.cacheStore.Get(ctx, rateCacheKey(from, to))
	if err != nil {
		return rateRecord{}, false
	}
	var rate rateRecord
	if err := json.Unmarshal([]byte(encoded), &rate); err != nil {
		_ = s.cacheStore.Delete(ctx, rateCacheKey(from, to))
		return rateRecord{}, false
	}
	if _, err := parseRate(rate.Value); err != nil {
		_ = s.cacheStore.Delete(ctx, rateCacheKey(from, to))
		return rateRecord{}, false
	}
	return rate, true
}

func (s *implementation) putCachedRate(
	ctx context.Context,
	rate rateRecord,
	ttl time.Duration,
) {
	if s.cacheStore == nil {
		return
	}
	encoded, err := json.Marshal(rate)
	if err != nil {
		return
	}
	key := rateCacheKey(rate.From, rate.To)
	if ttlCache, ok := s.cacheStore.(cacheWithTTL); ok {
		_ = ttlCache.PutWithTTL(ctx, key, string(encoded), ttl)
		return
	}
	_ = s.cacheStore.Put(ctx, key, string(encoded))
}

func rateCacheKey(from, to string) string {
	return rateCachePrefix + from + ":" + to
}
