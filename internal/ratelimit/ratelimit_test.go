package ratelimit

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMemoryLimiterAllowsWithinQuota(t *testing.T) {
	limiter := NewMemoryLimiter(2, time.Minute)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if err := limiter.Allow(ctx, "merchant:order:1"); err != nil {
			t.Fatalf("Allow(%d) error = %v", i, err)
		}
	}
	if err := limiter.Allow(ctx, "merchant:order:1"); !errors.Is(err, ErrLimited) {
		t.Fatalf("Allow over quota error = %v, want ErrLimited", err)
	}
	// A different key is independent.
	if err := limiter.Allow(ctx, "merchant:order:2"); err != nil {
		t.Fatalf("Allow(other key) error = %v", err)
	}
}

func TestMemoryLimiterSeparatesBucketsByTime(t *testing.T) {
	limiter := NewMemoryLimiter(1, time.Minute)
	ctx := context.Background()
	if err := limiter.Allow(ctx, "key"); err != nil {
		t.Fatalf("Allow() error = %v", err)
	}
	if err := limiter.Allow(ctx, "key"); !errors.Is(err, ErrLimited) {
		t.Fatalf("Allow(second) error = %v, want ErrLimited", err)
	}
	// Rewind the clock past the window.
	mem := limiter.(*memoryLimiter)
	mem.now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	if err := limiter.Allow(ctx, "key"); err != nil {
		t.Fatalf("Allow(after window) error = %v", err)
	}
}

func TestDisabledLimiterAlwaysAllows(t *testing.T) {
	limiter := NewRedisLimiter(nil, 1, time.Minute)
	if err := limiter.Allow(context.Background(), "key"); err != nil {
		t.Fatalf("disabled limiter Allow error = %v", err)
	}
}
