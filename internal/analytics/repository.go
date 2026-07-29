package analytics

import (
	"context"
	"time"
)

// Repository executes analytics aggregations independently of transport and caching.
type Repository interface {
	MerchantStats(ctx context.Context, merchantID string, cutoff *time.Time) (*MerchantStats, error)
	MarketStats(ctx context.Context, marketID string) (*MarketStats, error)
	UserStats(ctx context.Context, merchantID, userID string) (*UserStats, error)
	PlatformStats(ctx context.Context, cutoff *time.Time) (*PlatformStats, error)
}
