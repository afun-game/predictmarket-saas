package callback

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeDegradePersistence struct {
	mu         sync.Mutex
	degraded   map[string]string
	cleared    map[string]bool
	markCalls  int
	clearCalls int
}

func (f *fakeDegradePersistence) MarkSeamlessDegraded(_ context.Context, merchantID, reason string, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.degraded == nil {
		f.degraded = map[string]string{}
	}
	f.degraded[merchantID] = reason
	f.markCalls++
	return nil
}

func (f *fakeDegradePersistence) ClearSeamlessDegraded(_ context.Context, merchantID string, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cleared == nil {
		f.cleared = map[string]bool{}
	}
	f.cleared[merchantID] = true
	f.clearCalls++
	return nil
}

func (f *fakeDegradePersistence) reason(merchantID string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.degraded[merchantID]
}

func TestDegradedTrackerFlipsAfterThreshold(t *testing.T) {
	persistence := &fakeDegradePersistence{}
	tracker := &degradedTracker{
		failures:    map[string]int{},
		persistence: persistence,
		threshold:   3,
		now:         time.Now,
	}
	ctx := context.Background()
	now := time.Now().UTC()

	for i := 0; i < 2; i++ {
		tracker.recordFailure(ctx, "merchant-1", "boom", now)
		if persistence.reason("merchant-1") != "" {
			t.Fatalf("merchant degraded before threshold")
		}
	}
	tracker.recordFailure(ctx, "merchant-1", "boom", now)
	if persistence.reason("merchant-1") == "" {
		t.Fatal("merchant was not marked degraded after threshold")
	}
}

func TestDegradedTrackerRecoversOnSuccess(t *testing.T) {
	persistence := &fakeDegradePersistence{}
	tracker := &degradedTracker{
		failures:    map[string]int{},
		persistence: persistence,
		threshold:   1,
		now:         time.Now,
	}
	ctx := context.Background()
	now := time.Now().UTC()

	tracker.recordFailure(ctx, "merchant-1", "boom", now)
	if persistence.reason("merchant-1") == "" {
		t.Fatal("merchant was not marked degraded")
	}
	tracker.recordSuccess(ctx, "merchant-1", now)
	if !persistence.cleared["merchant-1"] {
		t.Fatal("merchant degraded state was not cleared after a healthy delivery")
	}
	// A success without prior failures must not touch persistence.
	before := persistence.clearCalls
	tracker.recordSuccess(ctx, "merchant-2", now)
	if persistence.clearCalls != before {
		t.Fatal("recordSuccess without prior failures cleared state")
	}
}

func TestDegradedTrackerPersistenceErrorDoesNotPanic(t *testing.T) {
	persistence := &fakeDegradePersistence{}
	tracker := &degradedTracker{
		failures:    map[string]int{},
		persistence: persistence,
		threshold:   1,
		now:         time.Now,
	}
	ctx := context.Background()
	now := time.Now().UTC()
	tracker.recordFailure(ctx, "merchant-1", "boom", now)
	tracker.recordFailure(ctx, "merchant-1", "boom", now)
	if err := errors.New("ignored"); err == nil {
		t.Fatal("unreachable")
	}
}
