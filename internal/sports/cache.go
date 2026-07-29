package sports

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nxsky/twill/runtime/resource"
)

const (
	sportsCachePrefix = "predictmarket:sports:v1:"
	listVersionKey    = sportsCachePrefix + "list-version"
	detailCacheTTL    = 5 * time.Minute
	listCacheTTL      = time.Minute
)

type ttlCache interface {
	PutWithTTL(ctx context.Context, key, value string, ttl time.Duration) error
}

type listCacheEntry struct {
	Events []*SportsEvent `json:"events"`
	Total  int            `json:"total"`
}

func (s *implementation) getCachedDetail(ctx context.Context, eventID string) (*SportsEvent, bool) {
	if s.cacheStore == nil {
		return nil, false
	}
	encoded, err := s.cacheStore.Get(ctx, detailCacheKey(eventID))
	if err != nil {
		return nil, false
	}
	value := &SportsEvent{}
	if err := json.Unmarshal([]byte(encoded), value); err != nil || value.Event == nil {
		_ = s.cacheStore.Delete(ctx, detailCacheKey(eventID))
		return nil, false
	}
	return value, true
}

func (s *implementation) putCachedDetail(ctx context.Context, value *SportsEvent) {
	if value == nil || value.Event == nil {
		return
	}
	putSportsCache(ctx, s.cacheStore, detailCacheKey(value.Event.ID), value, detailCacheTTL)
}

func (s *implementation) deleteCachedDetail(ctx context.Context, eventID string) {
	if s.cacheStore != nil && eventID != "" {
		_ = s.cacheStore.Delete(ctx, detailCacheKey(eventID))
	}
}

func (s *implementation) listCacheVersion(ctx context.Context) string {
	if s.cacheStore == nil {
		return "memory"
	}
	version, err := s.cacheStore.Get(ctx, listVersionKey)
	if err == nil && version != "" {
		return version
	}
	version = fmt.Sprintf("%d", s.now().UTC().UnixNano())
	_ = s.cacheStore.Put(ctx, listVersionKey, version)
	return version
}

func (s *implementation) invalidateLists(ctx context.Context) {
	if s.cacheStore == nil {
		return
	}
	_ = s.cacheStore.Put(ctx, listVersionKey, fmt.Sprintf("%d", s.now().UTC().UnixNano()))
}

func (s *implementation) getCachedList(ctx context.Context, filters EventFilters, version string) ([]*SportsEvent, int, bool) {
	if s.cacheStore == nil {
		return nil, 0, false
	}
	key := listCacheKey(filters, version)
	encoded, err := s.cacheStore.Get(ctx, key)
	if err != nil {
		return nil, 0, false
	}
	entry := listCacheEntry{Events: []*SportsEvent{}}
	if err := json.Unmarshal([]byte(encoded), &entry); err != nil {
		_ = s.cacheStore.Delete(ctx, key)
		return nil, 0, false
	}
	return entry.Events, entry.Total, true
}

func (s *implementation) putCachedList(ctx context.Context, filters EventFilters, version string, values []*SportsEvent, total int) {
	putSportsCache(ctx, s.cacheStore, listCacheKey(filters, version), listCacheEntry{Events: values, Total: total}, listCacheTTL)
}

func detailCacheKey(eventID string) string { return sportsCachePrefix + "detail:" + eventID }

func listCacheKey(filters EventFilters, version string) string {
	encoded, _ := json.Marshal(struct {
		Filters EventFilters `json:"filters"`
		Version string       `json:"version"`
	}{filters, version})
	digest := sha256.Sum256(encoded)
	return sportsCachePrefix + "list:" + hex.EncodeToString(digest[:])
}

func putSportsCache(ctx context.Context, cache resource.Cache, key string, value any, ttl time.Duration) {
	if cache == nil {
		return
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return
	}
	if withTTL, ok := cache.(ttlCache); ok {
		_ = withTTL.PutWithTTL(ctx, key, string(encoded), ttl)
		return
	}
	_ = cache.Put(ctx, key, string(encoded))
}
