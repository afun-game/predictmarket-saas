package analytics

import (
	"context"
	"time"
)

type memoryRepository struct{}

func newMemoryRepository() *memoryRepository { return &memoryRepository{} }

func (*memoryRepository) MerchantStats(ctx context.Context, _ string, _ *time.Time) (*MerchantStats, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return newMerchantStats(), nil
}

func (*memoryRepository) MarketStats(ctx context.Context, _ string) (*MarketStats, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return newMarketStats(), nil
}

func (*memoryRepository) UserStats(ctx context.Context, _, _ string) (*UserStats, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return newUserStats(), nil
}

func (*memoryRepository) PlatformStats(ctx context.Context, _ *time.Time) (*PlatformStats, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return newPlatformStats(), nil
}
