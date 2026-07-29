package httpapi

import (
	"context"
	"net/http"
	"testing"

	"github.com/afun-game/predictmarket-saas/internal/analytics"
	"github.com/afun-game/predictmarket-saas/internal/currency"
	"github.com/afun-game/predictmarket-saas/internal/event"
	"github.com/afun-game/predictmarket-saas/internal/market"
	"github.com/afun-game/predictmarket-saas/internal/merchant"
	"github.com/afun-game/predictmarket-saas/internal/order"
	"github.com/afun-game/predictmarket-saas/internal/wallet"
)

func TestAnalyticsHTTPFlowAndTenantIsolation(t *testing.T) {
	merchantService := merchant.NewService()
	eventService := event.NewService()
	marketService := market.NewService()
	analyticsService := &stubAnalyticsService{}
	handler := NewHandler(
		merchantService,
		eventService,
		marketService,
		wallet.NewService(),
		order.NewService(),
		currency.NewService(),
		"admin-secret",
		analyticsService,
	)
	first := registerMerchant(t, handler, "Analytics One", "analytics-one@example.test")
	second := registerMerchant(t, handler, "Analytics Two", "analytics-two@example.test")
	createdEvent, err := eventService.Create(context.Background(), &event.CreateRequest{
		SourceType: "custom", SourceID: "analytics-event", Title: "Analytics Event",
		Description: "fixture", Category: "sports", EndTime: "2026-12-01T12:00:00Z",
		ResolutionTime: "2026-12-01T13:00:00Z",
	})
	if err != nil {
		t.Fatalf("create event: %v", err)
	}
	createdMarket, err := marketService.Create(context.Background(), &market.CreateRequest{
		MerchantID: first.Data.MerchantID, EventID: createdEvent.ID, Type: "binary",
		Question: "Will analytics work?", Options: []string{"Yes", "No"}, LiquidityPool: 100,
	})
	if err != nil {
		t.Fatalf("create market: %v", err)
	}

	firstAuth := "Bearer " + first.Data.APIKey
	response := performRequest(t, handler, http.MethodGet, "/api/v1/analytics/merchant?time_range=30d", nil, firstAuth)
	if response.Code != http.StatusOK || analyticsService.merchantID != first.Data.MerchantID || analyticsService.timeRange != "30d" {
		t.Errorf("merchant status = %d, merchant/time = %q/%q", response.Code, analyticsService.merchantID, analyticsService.timeRange)
	}
	response = performRequest(t, handler, http.MethodGet, "/api/v1/analytics/users/user-7", nil, firstAuth)
	if response.Code != http.StatusOK || analyticsService.userID != "user-7" {
		t.Errorf("user status = %d, user = %q", response.Code, analyticsService.userID)
	}
	response = performRequest(t, handler, http.MethodGet, "/api/v1/analytics/markets/"+createdMarket.ID, nil, "Bearer "+second.Data.APIKey)
	if response.Code != http.StatusNotFound || analyticsService.marketID != "" {
		t.Errorf("cross-tenant market status = %d, analytics market = %q", response.Code, analyticsService.marketID)
	}
	response = performRequest(t, handler, http.MethodGet, "/api/v1/analytics/markets/"+createdMarket.ID, nil, firstAuth)
	if response.Code != http.StatusOK || analyticsService.marketID != createdMarket.ID {
		t.Errorf("market status = %d, market = %q", response.Code, analyticsService.marketID)
	}
	response = performRequest(t, handler, http.MethodGet, "/api/v1/analytics/platform?time_range=all", nil, "Bearer admin-secret")
	if response.Code != http.StatusOK || analyticsService.timeRange != "all" {
		t.Errorf("platform status = %d, time = %q", response.Code, analyticsService.timeRange)
	}
}

type stubAnalyticsService struct {
	merchantID string
	userID     string
	marketID   string
	timeRange  string
}

func (s *stubAnalyticsService) GetMerchantStats(
	_ context.Context,
	merchantID string,
	timeRange string,
) (*analytics.MerchantStats, error) {
	s.merchantID, s.timeRange = merchantID, timeRange
	return &analytics.MerchantStats{
		VolumeByCurrency: map[string]float64{}, RevenueByCurrency: map[string]float64{},
	}, nil
}

func (s *stubAnalyticsService) GetMarketStats(
	_ context.Context,
	marketID string,
) (*analytics.MarketStats, error) {
	s.marketID = marketID
	return &analytics.MarketStats{
		PriceHistory: []analytics.PricePoint{}, Distribution: map[string]float64{},
	}, nil
}

func (s *stubAnalyticsService) GetUserStats(
	_ context.Context,
	merchantID string,
	userID string,
) (*analytics.UserStats, error) {
	s.merchantID, s.userID = merchantID, userID
	return &analytics.UserStats{
		VolumeByCurrency: map[string]float64{}, ProfitByCurrency: map[string]float64{},
	}, nil
}

func (s *stubAnalyticsService) GetPlatformStats(
	_ context.Context,
	timeRange string,
) (*analytics.PlatformStats, error) {
	s.timeRange = timeRange
	return &analytics.PlatformStats{VolumeByCurrency: map[string]float64{}}, nil
}
