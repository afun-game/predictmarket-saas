package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/afun-game/predictmarket-saas/internal/event"
	"github.com/afun-game/predictmarket-saas/internal/market"
	"github.com/afun-game/predictmarket-saas/internal/merchant"
	"github.com/afun-game/predictmarket-saas/internal/order"
	"github.com/afun-game/predictmarket-saas/internal/wallet"
)

func TestCurrencyHTTPFlow(t *testing.T) {
	currencies := &stubCurrencyService{}
	handler := NewHandler(
		merchant.NewService(),
		event.NewService(),
		market.NewService(),
		wallet.NewService(),
		order.NewService(),
		currencies,
		"admin-secret",
	)
	credentials := registerMerchant(t, handler, "Currency Tenant", "currency@example.test")
	authorization := "Bearer " + credentials.Data.APIKey

	response := performRequest(
		t,
		handler,
		http.MethodGet,
		"/api/v1/currencies/rate?from=USD&to=EUR",
		nil,
		authorization,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("rate status = %d, body = %s", response.Code, response.Body.String())
	}

	response = performRequest(
		t,
		handler,
		http.MethodPost,
		"/api/v1/currencies/convert",
		[]byte(`{"amount":10,"from":"USD","to":"EUR"}`),
		authorization,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("convert status = %d, body = %s", response.Code, response.Body.String())
	}
	var converted struct {
		Data struct {
			Amount float64 `json:"converted_amount"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &converted); err != nil {
		t.Fatalf("decode conversion: %v", err)
	}
	if converted.Data.Amount != 8 {
		t.Errorf("converted amount = %v, want 8", converted.Data.Amount)
	}

	response = performRequest(
		t,
		handler,
		http.MethodPost,
		"/api/v1/currencies/time",
		[]byte(`{"timestamp":"2026-07-28T12:00:00Z","timezone":"Asia/Shanghai"}`),
		authorization,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("time conversion status = %d, body = %s", response.Code, response.Body.String())
	}

	response = performRequest(
		t,
		handler,
		http.MethodPost,
		"/api/v1/currencies/refresh",
		nil,
		"Bearer admin-secret",
	)
	if response.Code != http.StatusNoContent || currencies.refreshes != 1 {
		t.Errorf("refresh status = %d, calls = %d", response.Code, currencies.refreshes)
	}
}

type stubCurrencyService struct {
	refreshes int
}

func (*stubCurrencyService) GetRate(context.Context, string, string) (float64, error) {
	return 0.8, nil
}

func (*stubCurrencyService) Convert(context.Context, float64, string, string) (float64, error) {
	return 8, nil
}

func (*stubCurrencyService) ListSupported(context.Context) ([]string, error) {
	return []string{"USD", "EUR", "CNY", "JPY", "GBP"}, nil
}

func (s *stubCurrencyService) RefreshRates(context.Context) error {
	s.refreshes++
	return nil
}

func (*stubCurrencyService) ConvertTime(
	_ context.Context,
	timestamp time.Time,
	timezone string,
) (time.Time, error) {
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return time.Time{}, err
	}
	return timestamp.In(location), nil
}
