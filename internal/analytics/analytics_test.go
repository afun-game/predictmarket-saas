package analytics

import (
	"context"
	"testing"
	"time"

	"github.com/nxsky/twill/runtime/resource"
)

func TestAnalyticsValidatesAndNormalizesTimeRange(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	window, err := parseTimeRange("", now)
	if err != nil {
		t.Fatalf("parseTimeRange() error = %v", err)
	}
	if window.name != "7d" || window.cutoff == nil || !window.cutoff.Equal(now.Add(-7*24*time.Hour)) {
		t.Errorf("window = %#v", window)
	}
	all, err := parseTimeRange(" ALL ", now)
	if err != nil || all.cutoff != nil {
		t.Errorf("all window = %#v, error = %v", all, err)
	}
	if _, err := parseTimeRange("1y", now); err == nil {
		t.Fatal("unsupported time range accepted")
	}
}

func TestAnalyticsCachesRepositoryResults(t *testing.T) {
	repository := &countingRepository{}
	service := newService(repository, resource.NewMemoryCache())
	service.now = func() time.Time { return time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC) }

	first, err := service.GetMerchantStats(context.Background(), "merchant-1", "24h")
	if err != nil {
		t.Fatalf("GetMerchantStats() error = %v", err)
	}
	first.TotalOrders = 999
	second, err := service.GetMerchantStats(context.Background(), "merchant-1", "24h")
	if err != nil {
		t.Fatalf("GetMerchantStats(cached) error = %v", err)
	}
	if repository.merchantCalls != 1 || second.TotalOrders != 3 {
		t.Errorf("repository calls = %d, cached stats = %#v", repository.merchantCalls, second)
	}
	if _, err := service.GetMerchantStats(context.Background(), " ", "7d"); err == nil {
		t.Fatal("empty merchant ID accepted")
	}
	if _, err := service.GetUserStats(context.Background(), "merchant-1", " "); err == nil {
		t.Fatal("empty user ID accepted")
	}
}

type countingRepository struct{ merchantCalls int }

func (r *countingRepository) MerchantStats(
	context.Context,
	string,
	*time.Time,
) (*MerchantStats, error) {
	r.merchantCalls++
	result := newMerchantStats()
	result.TotalOrders = 3
	result.VolumeByCurrency["USD"] = 12.5
	return result, nil
}

func (*countingRepository) MarketStats(context.Context, string) (*MarketStats, error) {
	return newMarketStats(), nil
}

func (*countingRepository) UserStats(context.Context, string, string) (*UserStats, error) {
	return newUserStats(), nil
}

func (*countingRepository) PlatformStats(context.Context, *time.Time) (*PlatformStats, error) {
	return newPlatformStats(), nil
}
