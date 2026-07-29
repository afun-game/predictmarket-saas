package event

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/afun-game/predictmarket-saas/pkg/types"
)

const (
	eventCachePrefix    = "predictmarket:events:v1:"
	eventListVersionKey = eventCachePrefix + "list-version"
	eventDetailTTL      = 5 * time.Minute
	eventListTTL        = time.Minute
)

type cacheStore interface {
	Get(ctx context.Context, key string) (string, error)
	Put(ctx context.Context, key, value string) error
	Delete(ctx context.Context, key string) error
}

type cacheStoreWithTTL interface {
	PutWithTTL(ctx context.Context, key, value string, ttl time.Duration) error
}

type cacheEpoch struct {
	sequence atomic.Uint64
}

func (e *cacheEpoch) Next(now time.Time) string {
	return fmt.Sprintf("%d-%d", now.UnixNano(), e.sequence.Add(1))
}

type eventListCacheEntry struct {
	Events []*types.Event `json:"events"`
	Total  int            `json:"total"`
}

func (s *implementation) getCachedEvent(ctx context.Context, eventID string) (*types.Event, bool) {
	if s.cacheStore == nil {
		return nil, false
	}
	key := eventDetailCacheKey(eventID)
	encoded, err := s.cacheStore.Get(ctx, key)
	if err != nil {
		return nil, false
	}
	value := &types.Event{}
	if err := json.Unmarshal([]byte(encoded), value); err != nil {
		_ = s.cacheStore.Delete(ctx, key)
		return nil, false
	}
	return value, true
}

func (s *implementation) putCachedEvent(ctx context.Context, value *types.Event) {
	if s.cacheStore == nil || value == nil {
		return
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return
	}
	putCacheValue(
		ctx,
		s.cacheStore,
		eventDetailCacheKey(value.ID),
		string(encoded),
		eventDetailTTL,
	)
}

func (s *implementation) deleteCachedEvent(ctx context.Context, eventID string) {
	if s.cacheStore == nil {
		return
	}
	_ = s.cacheStore.Delete(ctx, eventDetailCacheKey(eventID))
}

func (s *implementation) eventListCacheVersion(ctx context.Context) string {
	if s.cacheStore == nil {
		return ""
	}
	version, err := s.cacheStore.Get(ctx, eventListVersionKey)
	if err == nil && version != "" {
		return version
	}
	version = s.cacheEpoch.Next(s.now().UTC())
	if err := s.cacheStore.Put(ctx, eventListVersionKey, version); err != nil {
		return ""
	}
	return version
}

func (s *implementation) invalidateEventLists(ctx context.Context) {
	if s.cacheStore == nil {
		return
	}
	version := s.cacheEpoch.Next(s.now().UTC())
	_ = s.cacheStore.Put(ctx, eventListVersionKey, version)
}

func (s *implementation) getCachedEventList(
	ctx context.Context,
	filters ListFilters,
	version string,
) ([]*types.Event, int, bool) {
	if s.cacheStore == nil || version == "" {
		return nil, 0, false
	}
	key := eventListCacheKey(filters, version)
	encoded, err := s.cacheStore.Get(ctx, key)
	if err != nil {
		return nil, 0, false
	}
	entry := eventListCacheEntry{Events: []*types.Event{}}
	if err := json.Unmarshal([]byte(encoded), &entry); err != nil {
		_ = s.cacheStore.Delete(ctx, key)
		return nil, 0, false
	}
	if entry.Events == nil {
		entry.Events = []*types.Event{}
	}
	return entry.Events, entry.Total, true
}

func (s *implementation) putCachedEventList(
	ctx context.Context,
	filters ListFilters,
	version string,
	values []*types.Event,
	total int,
) {
	if s.cacheStore == nil || version == "" {
		return
	}
	entry := eventListCacheEntry{
		Events: values,
		Total:  total,
	}
	encoded, err := json.Marshal(entry)
	if err != nil {
		return
	}
	putCacheValue(
		ctx,
		s.cacheStore,
		eventListCacheKey(filters, version),
		string(encoded),
		eventListTTL,
	)
}

func eventDetailCacheKey(eventID string) string {
	return eventCachePrefix + "detail:" + eventID
}

func eventListCacheKey(filters ListFilters, version string) string {
	keyData := version + "\x00" + filters.Category + "\x00" + filters.Status + "\x00" +
		strconv.Itoa(filters.Page) + "\x00" + strconv.Itoa(filters.Limit)
	digest := sha256.Sum256([]byte(keyData))
	return eventCachePrefix + "list:" + hex.EncodeToString(digest[:])
}

func putCacheValue(
	ctx context.Context,
	cache cacheStore,
	key string,
	value string,
	ttl time.Duration,
) {
	if ttlCache, ok := cache.(cacheStoreWithTTL); ok {
		_ = ttlCache.PutWithTTL(ctx, key, value, ttl)
		return
	}
	_ = cache.Put(ctx, key, value)
}
