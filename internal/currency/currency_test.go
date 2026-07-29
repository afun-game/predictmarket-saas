package currency

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/nxsky/twill/runtime/resource"
)

func TestRefreshGetAndConvert(t *testing.T) {
	provider := &fixedRateProvider{snapshot: testSnapshot()}
	service := newService(newMemoryRepository(), resource.NewMemoryCache(), provider)
	service.now = func() time.Time { return testSnapshot().Timestamp.Add(10 * time.Minute) }

	if err := service.RefreshRates(context.Background()); err != nil {
		t.Fatalf("RefreshRates() error = %v", err)
	}
	rate, err := service.GetRate(context.Background(), "eur", "cny")
	if err != nil {
		t.Fatalf("GetRate() error = %v", err)
	}
	if rate != 9 {
		t.Errorf("EUR/CNY rate = %v, want 9", rate)
	}
	converted, err := service.Convert(context.Background(), 10.01, "USD", "EUR")
	if err != nil {
		t.Fatalf("Convert() error = %v", err)
	}
	if converted != 8.01 {
		t.Errorf("converted amount = %v, want 8.01", converted)
	}
	if provider.callCount() != 1 {
		t.Errorf("provider calls = %d, want 1", provider.callCount())
	}
}

func TestGetRateFallsBackToStaleStoredValue(t *testing.T) {
	repository := newMemoryRepository()
	oldTimestamp := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := repository.Save(context.Background(), []rateRecord{{
		From: "USD", To: "EUR", Value: "0.75000000", Provider: "old", Timestamp: oldTimestamp,
	}}); err != nil {
		t.Fatalf("seed repository: %v", err)
	}
	service := newService(
		repository,
		resource.NewMemoryCache(),
		&fixedRateProvider{err: errors.New("offline")},
	)
	service.now = func() time.Time { return oldTimestamp.Add(2 * time.Hour) }
	rate, err := service.GetRate(context.Background(), "USD", "EUR")
	if err != nil {
		t.Fatalf("GetRate() stale fallback error = %v", err)
	}
	if rate != 0.75 {
		t.Errorf("stale rate = %v, want 0.75", rate)
	}
}

func TestCurrencyValidation(t *testing.T) {
	service := newService(
		newMemoryRepository(),
		resource.NewMemoryCache(),
		&fixedRateProvider{snapshot: testSnapshot()},
	)
	tests := []struct {
		name string
		run  func() error
	}{
		{name: "unsupported currency", run: func() error {
			_, err := service.GetRate(context.Background(), "USD", "BTC")
			return err
		}},
		{name: "negative amount", run: func() error {
			_, err := service.Convert(context.Background(), -1, "USD", "EUR")
			return err
		}},
		{name: "fractional cent", run: func() error {
			_, err := service.Convert(context.Background(), 1.001, "USD", "EUR")
			return err
		}},
		{name: "NaN amount", run: func() error {
			_, err := service.Convert(context.Background(), math.NaN(), "USD", "EUR")
			return err
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var validationErr *ValidationError
			if err := test.run(); !errors.As(err, &validationErr) {
				t.Errorf("error = %v, want ValidationError", err)
			}
		})
	}
}

func TestConvertTime(t *testing.T) {
	service := NewService()
	timestamp := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	converted, err := service.ConvertTime(context.Background(), timestamp, "Asia/Shanghai")
	if err != nil {
		t.Fatalf("ConvertTime() error = %v", err)
	}
	if got := converted.Format(time.RFC3339); got != "2026-07-28T20:00:00+08:00" {
		t.Errorf("converted time = %s", got)
	}
	if _, err := service.ConvertTime(context.Background(), timestamp, "Mars/Olympus"); err == nil {
		t.Error("ConvertTime(invalid timezone) error = nil")
	}
}

func TestCrossRatesPreserveReciprocalPairs(t *testing.T) {
	rates, err := crossRates(testSnapshot())
	if err != nil {
		t.Fatalf("crossRates() error = %v", err)
	}
	if len(rates) != 25 {
		t.Fatalf("crossRates() len = %d, want 25", len(rates))
	}
	values := map[string]string{}
	for _, rate := range rates {
		values[rateKey(rate.From, rate.To)] = rate.Value
	}
	if values["EUR:CNY"] != "9.00000000" || values["USD:USD"] != "1.00000000" {
		t.Errorf("cross rates = %#v", values)
	}
}

type fixedRateProvider struct {
	mu       sync.Mutex
	calls    int
	snapshot rateSnapshot
	err      error
}

func (p *fixedRateProvider) Fetch(context.Context) (rateSnapshot, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	return p.snapshot, p.err
}

func (p *fixedRateProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func testSnapshot() rateSnapshot {
	return rateSnapshot{
		Base: "USD",
		Rates: map[string]string{
			"USD": "1", "EUR": "0.8", "CNY": "7.2", "JPY": "150", "GBP": "0.7",
		},
		Provider:  "test-provider",
		Timestamp: time.Date(2026, 7, 28, 10, 0, 0, 0, time.UTC),
	}
}
