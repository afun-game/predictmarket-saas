package currency

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultRateProviderURL = "https://api.exchangerate-api.com/v4/latest/USD"
	rateProviderName       = "exchangerate-api"
	providerTimeout        = 5 * time.Second
	maxProviderBodyBytes   = 1 << 20
)

type rateSnapshot struct {
	Base      string
	Rates     map[string]string
	Provider  string
	Timestamp time.Time
}

type rateProvider interface {
	Fetch(ctx context.Context) (rateSnapshot, error)
}

type httpRateProvider struct {
	client  *http.Client
	baseURL string
	name    string
	now     func() time.Time
}

func newHTTPRateProvider(baseURL string) *httpRateProvider {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		baseURL = strings.TrimSpace(os.Getenv("EXCHANGE_RATE_API_URL"))
	}
	if baseURL == "" {
		baseURL = defaultRateProviderURL
	}
	return &httpRateProvider{
		client:  &http.Client{Timeout: providerTimeout},
		baseURL: baseURL,
		name:    rateProviderName,
		now:     time.Now,
	}
}

func (p *httpRateProvider) Fetch(ctx context.Context) (rateSnapshot, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL, nil)
	if err != nil {
		return rateSnapshot{}, fmt.Errorf("create exchange rate request: %w", err)
	}
	response, err := p.client.Do(request)
	if err != nil {
		return rateSnapshot{}, fmt.Errorf("request exchange rates: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
		return rateSnapshot{}, fmt.Errorf("exchange rate provider returned HTTP %d", response.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxProviderBodyBytes))
	decoder.UseNumber()
	var payload struct {
		Base            string                 `json:"base"`
		Rates           map[string]json.Number `json:"rates"`
		TimeLastUpdated int64                  `json:"time_last_updated"`
	}
	if err := decoder.Decode(&payload); err != nil {
		return rateSnapshot{}, fmt.Errorf("decode exchange rate response: %w", err)
	}
	base := strings.ToUpper(strings.TrimSpace(payload.Base))
	if _, err := validateCurrency("provider_base", base); err != nil {
		return rateSnapshot{}, err
	}
	rates := make(map[string]string, len(supportedCurrencies))
	rates[base] = "1"
	for _, currency := range supportedCurrencies {
		if currency == base {
			continue
		}
		number, ok := payload.Rates[currency]
		if !ok {
			return rateSnapshot{}, fmt.Errorf("provider omitted %s rate", currency)
		}
		value, err := strconv.ParseFloat(number.String(), 64)
		if err != nil || value <= 0 {
			return rateSnapshot{}, fmt.Errorf("provider returned invalid %s rate %q", currency, number)
		}
		rates[currency] = strconv.FormatFloat(value, 'f', 8, 64)
	}
	timestamp := p.now().UTC()
	if payload.TimeLastUpdated > 0 {
		timestamp = time.Unix(payload.TimeLastUpdated, 0).UTC()
	}
	return rateSnapshot{
		Base:      base,
		Rates:     rates,
		Provider:  p.name,
		Timestamp: timestamp,
	}, nil
}
