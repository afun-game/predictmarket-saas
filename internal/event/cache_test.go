package event

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/afun-game/predictmarket-saas/pkg/types"
)

func TestEventDetailCacheAndInvalidation(t *testing.T) {
	t.Parallel()

	cache := newFakeCache()
	service := newService(newMemoryRepository())
	service.cacheStore = cache
	created, err := service.Create(context.Background(), validCreateRequest("cached-detail", "sports"))
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	detailKey := eventDetailCacheKey(created.ID)
	if cache.ttls[detailKey] != eventDetailTTL {
		t.Errorf("detail TTL = %v, want %v", cache.ttls[detailKey], eventDetailTTL)
	}

	cache.values[detailKey] = `{"id":"` + created.ID + `","title":"From cache"}`
	cached, err := service.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("cached Get() error = %v", err)
	}
	if cached.Title != "From cache" {
		t.Errorf("cached title = %q", cached.Title)
	}

	if err := service.UpdateStatus(context.Background(), created.ID, "active"); err != nil {
		t.Fatalf("UpdateStatus() error = %v", err)
	}
	refreshed, err := service.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("refreshed Get() error = %v", err)
	}
	if refreshed.Title == "From cache" || refreshed.Status != "active" {
		t.Errorf("refreshed event = %#v", refreshed)
	}
}

func TestEventListCacheInvalidatedBySync(t *testing.T) {
	t.Parallel()

	repository := newMemoryRepository()
	cache := newFakeCache()
	service := newService(repository)
	service.cacheStore = cache
	if _, err := service.Create(
		context.Background(),
		validCreateRequest("list-first", "sports"),
	); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	_, total, err := service.List(context.Background(), &ListFilters{})
	if err != nil || total != 1 {
		t.Fatalf("first List() total = %d, error = %v", total, err)
	}
	direct := &types.Event{
		ID:             "direct-event",
		SourceType:     "custom",
		SourceID:       "list-direct",
		Title:          "Direct repository event",
		Category:       "sports",
		EndTime:        time.Date(2026, time.August, 2, 12, 0, 0, 0, time.UTC),
		ResolutionTime: time.Date(2026, time.August, 2, 13, 0, 0, 0, time.UTC),
		Status:         "pending",
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}
	if err := repository.Create(context.Background(), direct); err != nil {
		t.Fatalf("direct repository Create() error = %v", err)
	}
	_, cachedTotal, err := service.List(context.Background(), &ListFilters{})
	if err != nil || cachedTotal != 1 {
		t.Fatalf("cached List() total = %d, error = %v", cachedTotal, err)
	}

	if err := service.SyncSource(context.Background(), validSyncRequest("list-synced")); err != nil {
		t.Fatalf("SyncSource() error = %v", err)
	}
	_, refreshedTotal, err := service.List(context.Background(), &ListFilters{})
	if err != nil || refreshedTotal != 3 {
		t.Fatalf("refreshed List() total = %d, error = %v", refreshedTotal, err)
	}

	foundListTTL := false
	for key, ttl := range cache.ttls {
		isListKey := strings.HasPrefix(key, eventCachePrefix+"list:")
		if isListKey && ttl == eventListTTL {
			foundListTTL = true
			break
		}
	}
	if !foundListTTL {
		t.Errorf("no list entry used TTL %v", eventListTTL)
	}
}

func TestEventCacheFailureFallsBackToRepository(t *testing.T) {
	t.Parallel()

	service := newService(newMemoryRepository())
	service.cacheStore = errorCache{}
	created, err := service.Create(context.Background(), validCreateRequest("cache-down", "crypto"))
	if err != nil {
		t.Fatalf("Create() with failed cache error = %v", err)
	}
	if _, err := service.Get(context.Background(), created.ID); err != nil {
		t.Fatalf("Get() with failed cache error = %v", err)
	}
	values, total, err := service.List(context.Background(), &ListFilters{})
	validResult := total == 1 && len(values) == 1
	if err != nil || !validResult {
		t.Fatalf("List() with failed cache values = %d, total = %d, error = %v", len(values), total, err)
	}
}

type fakeCache struct {
	values map[string]string
	ttls   map[string]time.Duration
}

func newFakeCache() *fakeCache {
	return &fakeCache{
		values: map[string]string{},
		ttls:   map[string]time.Duration{},
	}
}

func (c *fakeCache) Get(_ context.Context, key string) (string, error) {
	value, exists := c.values[key]
	if !exists {
		return "", errors.New("cache miss")
	}
	return value, nil
}

func (c *fakeCache) Put(_ context.Context, key, value string) error {
	c.values[key] = value
	return nil
}

func (c *fakeCache) PutWithTTL(
	_ context.Context,
	key string,
	value string,
	ttl time.Duration,
) error {
	c.values[key] = value
	c.ttls[key] = ttl
	return nil
}

func (c *fakeCache) Delete(_ context.Context, key string) error {
	delete(c.values, key)
	delete(c.ttls, key)
	return nil
}

type errorCache struct{}

func (errorCache) Get(context.Context, string) (string, error) {
	return "", errors.New("cache unavailable")
}

func (errorCache) Put(context.Context, string, string) error {
	return errors.New("cache unavailable")
}

func (errorCache) Delete(context.Context, string) error {
	return errors.New("cache unavailable")
}
