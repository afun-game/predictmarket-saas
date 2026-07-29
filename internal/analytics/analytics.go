// Package analytics provides read-only business aggregations for dashboards.
package analytics

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nxsky/twill"
	"github.com/nxsky/twill/runtime/resource"
)

var ErrNotFound = errors.New("analytics subject not found")

// Service provides analytics and reporting.
type Service interface {
	GetMerchantStats(ctx context.Context, merchantID string, timeRange string) (*MerchantStats, error)
	GetMarketStats(ctx context.Context, marketID string) (*MarketStats, error)
	GetUserStats(ctx context.Context, merchantID, userID string) (*UserStats, error)
	GetPlatformStats(ctx context.Context, timeRange string) (*PlatformStats, error)
}

type MerchantStats struct {
	twill.AutoMarshal

	TotalVolume       float64            `json:"total_volume"`
	VolumeByCurrency  map[string]float64 `json:"volume_by_currency"`
	TotalOrders       int64              `json:"total_orders"`
	ActiveMarkets     int64              `json:"active_markets"`
	ActiveUsers       int64              `json:"active_users"`
	RevenueFromFee    float64            `json:"revenue_from_fee"`
	RevenueByCurrency map[string]float64 `json:"revenue_by_currency"`
}

type MarketStats struct {
	twill.AutoMarshal

	TotalVolume   float64            `json:"total_volume"`
	TotalOrders   int64              `json:"total_orders"`
	UniqueTraders int64              `json:"unique_traders"`
	PriceHistory  []PricePoint       `json:"price_history"`
	Distribution  map[string]float64 `json:"distribution"`
}

type UserStats struct {
	twill.AutoMarshal

	TotalOrders      int64              `json:"total_orders"`
	TotalVolume      float64            `json:"total_volume"`
	VolumeByCurrency map[string]float64 `json:"volume_by_currency"`
	WinRate          float64            `json:"win_rate"`
	CurrentProfit    float64            `json:"current_profit"`
	ProfitByCurrency map[string]float64 `json:"profit_by_currency"`
}

type PlatformStats struct {
	twill.AutoMarshal

	TotalMerchants   int64              `json:"total_merchants"`
	TotalMarkets     int64              `json:"total_markets"`
	TotalVolume      float64            `json:"total_volume"`
	VolumeByCurrency map[string]float64 `json:"volume_by_currency"`
	TotalOrders      int64              `json:"total_orders"`
}

type PricePoint struct {
	twill.AutoMarshal

	Timestamp int64   `json:"timestamp"`
	Price     float64 `json:"price"`
}

// ValidationError identifies an invalid analytics query.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("invalid %s: %s", e.Field, e.Message)
}

type implementation struct {
	twill.Implements[Service]

	database twill.Database `twill:"primary-db"`
	cache    twill.Cache    `twill:"analytics-cache"`

	repository Repository
	cacheStore resource.Cache
	now        func() time.Time
}

// NewService creates an Analytics Service with an empty in-memory repository.
func NewService() Service {
	return newService(newMemoryRepository(), resource.NewMemoryCache())
}

func newService(repository Repository, cacheStore resource.Cache) *implementation {
	return &implementation{repository: repository, cacheStore: cacheStore, now: time.Now}
}

func (s *implementation) Init(context.Context) error {
	if s.repository == nil {
		database := s.database.Get()
		if database == nil || database.StdDB() == nil {
			return errors.New("primary database is not configured")
		}
		s.repository = newPostgresRepository(database.StdDB())
	}
	if s.cacheStore == nil {
		s.cacheStore = s.cache.Get()
	}
	if s.now == nil {
		s.now = time.Now
	}
	return nil
}

func (s *implementation) GetMerchantStats(
	ctx context.Context,
	merchantID string,
	timeRange string,
) (*MerchantStats, error) {
	merchantID, err := requiredValue("merchant_id", merchantID)
	if err != nil {
		return nil, err
	}
	window, err := parseTimeRange(timeRange, s.now().UTC())
	if err != nil {
		return nil, err
	}
	key := analyticsCacheKey("merchant", merchantID, window.name)
	result := newMerchantStats()
	if s.getCached(ctx, key, result) {
		return result, nil
	}
	result, err = s.repository.MerchantStats(ctx, merchantID, window.cutoff)
	if err != nil {
		return nil, fmt.Errorf("get merchant analytics: %w", err)
	}
	s.putCached(ctx, key, result)
	return result, nil
}

func (s *implementation) GetMarketStats(ctx context.Context, marketID string) (*MarketStats, error) {
	marketID, err := requiredValue("market_id", marketID)
	if err != nil {
		return nil, err
	}
	key := analyticsCacheKey("market", marketID)
	result := newMarketStats()
	if s.getCached(ctx, key, result) {
		return result, nil
	}
	result, err = s.repository.MarketStats(ctx, marketID)
	if err != nil {
		return nil, fmt.Errorf("get market analytics: %w", err)
	}
	s.putCached(ctx, key, result)
	return result, nil
}

func (s *implementation) GetUserStats(
	ctx context.Context,
	merchantID string,
	userID string,
) (*UserStats, error) {
	merchantID, err := requiredValue("merchant_id", merchantID)
	if err != nil {
		return nil, err
	}
	userID, err = requiredValue("user_id", userID)
	if err != nil {
		return nil, err
	}
	key := analyticsCacheKey("user", merchantID, userID)
	result := newUserStats()
	if s.getCached(ctx, key, result) {
		return result, nil
	}
	result, err = s.repository.UserStats(ctx, merchantID, userID)
	if err != nil {
		return nil, fmt.Errorf("get user analytics: %w", err)
	}
	s.putCached(ctx, key, result)
	return result, nil
}

func (s *implementation) GetPlatformStats(
	ctx context.Context,
	timeRange string,
) (*PlatformStats, error) {
	window, err := parseTimeRange(timeRange, s.now().UTC())
	if err != nil {
		return nil, err
	}
	key := analyticsCacheKey("platform", window.name)
	result := newPlatformStats()
	if s.getCached(ctx, key, result) {
		return result, nil
	}
	result, err = s.repository.PlatformStats(ctx, window.cutoff)
	if err != nil {
		return nil, fmt.Errorf("get platform analytics: %w", err)
	}
	s.putCached(ctx, key, result)
	return result, nil
}

type timeWindow struct {
	name   string
	cutoff *time.Time
}

func parseTimeRange(value string, now time.Time) (timeWindow, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		value = "7d"
	}
	var duration time.Duration
	switch value {
	case "24h":
		duration = 24 * time.Hour
	case "7d":
		duration = 7 * 24 * time.Hour
	case "30d":
		duration = 30 * 24 * time.Hour
	case "90d":
		duration = 90 * 24 * time.Hour
	case "all":
		return timeWindow{name: value}, nil
	default:
		return timeWindow{}, &ValidationError{
			Field: "time_range", Message: "must be one of 24h, 7d, 30d, 90d, all",
		}
	}
	cutoff := now.Add(-duration)
	return timeWindow{name: value, cutoff: &cutoff}, nil
}

func requiredValue(field, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", &ValidationError{Field: field, Message: "is required"}
	}
	return value, nil
}

func newMerchantStats() *MerchantStats {
	return &MerchantStats{
		VolumeByCurrency: map[string]float64{}, RevenueByCurrency: map[string]float64{},
	}
}

func newMarketStats() *MarketStats {
	return &MarketStats{PriceHistory: []PricePoint{}, Distribution: map[string]float64{}}
}

func newUserStats() *UserStats {
	return &UserStats{
		VolumeByCurrency: map[string]float64{}, ProfitByCurrency: map[string]float64{},
	}
}

func newPlatformStats() *PlatformStats {
	return &PlatformStats{VolumeByCurrency: map[string]float64{}}
}
