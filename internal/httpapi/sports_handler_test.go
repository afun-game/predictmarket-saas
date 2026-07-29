package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/afun-game/predictmarket-saas/internal/currency"
	"github.com/afun-game/predictmarket-saas/internal/event"
	"github.com/afun-game/predictmarket-saas/internal/market"
	"github.com/afun-game/predictmarket-saas/internal/merchant"
	"github.com/afun-game/predictmarket-saas/internal/order"
	"github.com/afun-game/predictmarket-saas/internal/sports"
	"github.com/afun-game/predictmarket-saas/internal/wallet"
	"github.com/afun-game/predictmarket-saas/pkg/types"
)

func TestSportsHTTPFlow(t *testing.T) {
	service := &stubSportsService{value: &sports.SportsEvent{
		Event:  &types.Event{ID: "event-1", Title: "Sun vs Mystics", Status: "active"},
		League: "wnba", Teams: []sports.Team{{Name: "Connecticut Sun", Role: "away"}},
	}}
	handler := NewHandler(merchant.NewService(), event.NewService(), market.NewService(), wallet.NewService(), order.NewService(), currency.NewService(), "admin-secret", service)
	credentials := registerMerchant(t, handler, "Sports Tenant", "sports@example.test")
	authorization := "Bearer " + credentials.Data.APIKey

	response := performRequest(t, handler, http.MethodGet, "/api/v1/sports/events?league=wnba&team=Sun&status=active", nil, authorization)
	if response.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", response.Code, response.Body.String())
	}
	if service.filters == nil || service.filters.League != "wnba" || service.filters.Team != "Sun" {
		t.Errorf("filters = %#v", service.filters)
	}
	var body struct {
		Data []sports.SportsEvent `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(body.Data) != 1 || body.Data[0].League != "wnba" {
		t.Errorf("body = %#v", body)
	}

	response = performRequest(t, handler, http.MethodGet, "/api/v1/sports/events/event-1", nil, authorization)
	if response.Code != http.StatusOK {
		t.Errorf("get status = %d, body = %s", response.Code, response.Body.String())
	}
	response = performRequest(t, handler, http.MethodPost, "/api/v1/sports/sync", nil, "Bearer admin-secret")
	if response.Code != http.StatusNoContent || service.syncs != 1 {
		t.Errorf("sync status = %d, calls = %d", response.Code, service.syncs)
	}
	response = performRequest(t, handler, http.MethodGet, "/api/v1/sports/events", nil, "")
	if response.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated status = %d", response.Code)
	}
}

type stubSportsService struct {
	value   *sports.SportsEvent
	filters *sports.EventFilters
	syncs   int
}

func (s *stubSportsService) ListEvents(_ context.Context, filters *sports.EventFilters) ([]*sports.SportsEvent, int, error) {
	s.filters = filters
	return []*sports.SportsEvent{s.value}, 1, nil
}
func (s *stubSportsService) GetEvent(context.Context, string) (*sports.SportsEvent, error) {
	return s.value, nil
}
func (s *stubSportsService) SyncFromPolymarket(context.Context) error { s.syncs++; return nil }
