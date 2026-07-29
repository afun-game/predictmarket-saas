// Package currency manages supported currencies, exchange rates, and conversions.
package currency

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nxsky/twill"
	"github.com/nxsky/twill/runtime/resource"
)

const (
	rateCachePrefix = "predictmarket:currency:v1:rate:"
	rateCacheTTL    = time.Hour
	staleCacheTTL   = 5 * time.Minute
	maxAmount       = 999_999_999_999_999.99
)

var (
	ErrRateNotFound        = errors.New("exchange rate not found")
	ErrProviderUnavailable = errors.New("exchange rate provider unavailable")
	supportedCurrencies    = []string{"USD", "EUR", "CNY", "JPY", "GBP"}
)

// Service manages exchange rates and display conversions.
type Service interface {
	GetRate(ctx context.Context, from, to string) (float64, error)
	Convert(ctx context.Context, amount float64, from, to string) (float64, error)
	ListSupported(ctx context.Context) ([]string, error)
	RefreshRates(ctx context.Context) error
	ConvertTime(ctx context.Context, timestamp time.Time, timezone string) (time.Time, error)
}

// ValidationError identifies an invalid currency operation field.
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
	cache    twill.Cache    `twill:"currency-cache"`

	repository rateRepository
	cacheStore resource.Cache
	provider   rateProvider
	now        func() time.Time
	refreshMu  sync.Mutex
}

// NewService creates a Currency Service with in-memory persistence and cache.
func NewService() Service {
	return newService(newMemoryRepository(), resource.NewMemoryCache(), newHTTPRateProvider(""))
}

func newService(
	repository rateRepository,
	cacheStore resource.Cache,
	provider rateProvider,
) *implementation {
	return &implementation{
		repository: repository,
		cacheStore: cacheStore,
		provider:   provider,
		now:        time.Now,
	}
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
	if s.provider == nil {
		s.provider = newHTTPRateProvider("")
	}
	if s.now == nil {
		s.now = time.Now
	}
	return nil
}

func (s *implementation) GetRate(ctx context.Context, from, to string) (float64, error) {
	from, err := validateCurrency("from", from)
	if err != nil {
		return 0, err
	}
	to, err = validateCurrency("to", to)
	if err != nil {
		return 0, err
	}
	rate, err := s.getRateRecord(ctx, from, to)
	if err != nil {
		return 0, err
	}
	return parseRate(rate.Value)
}

func (s *implementation) Convert(
	ctx context.Context,
	amount float64,
	from string,
	to string,
) (float64, error) {
	amountCents, err := validateAmount(amount)
	if err != nil {
		return 0, err
	}
	from, err = validateCurrency("from", from)
	if err != nil {
		return 0, err
	}
	to, err = validateCurrency("to", to)
	if err != nil {
		return 0, err
	}
	rate, err := s.getRateRecord(ctx, from, to)
	if err != nil {
		return 0, err
	}
	convertedCents, err := multiplyCents(amountCents, rate.Value)
	if err != nil {
		return 0, fmt.Errorf("convert currency amount: %w", err)
	}
	return float64(convertedCents) / 100, nil
}

func (s *implementation) ListSupported(context.Context) ([]string, error) {
	return append([]string{}, supportedCurrencies...), nil
}

func (s *implementation) RefreshRates(ctx context.Context) error {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()

	snapshot, err := s.provider.Fetch(ctx)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrProviderUnavailable, err)
	}
	rates, err := crossRates(snapshot)
	if err != nil {
		return fmt.Errorf("normalize provider rates: %w", err)
	}
	if err := s.repository.Save(ctx, rates); err != nil {
		return fmt.Errorf("save exchange rates: %w", err)
	}
	for _, rate := range rates {
		s.putCachedRate(ctx, rate, rateCacheTTL)
	}
	return nil
}

func (s *implementation) ConvertTime(
	_ context.Context,
	timestamp time.Time,
	timezone string,
) (time.Time, error) {
	timezone = strings.TrimSpace(timezone)
	if timestamp.IsZero() {
		return time.Time{}, &ValidationError{Field: "timestamp", Message: "is required"}
	}
	location, err := time.LoadLocation(timezone)
	if timezone == "" || err != nil {
		return time.Time{}, &ValidationError{Field: "timezone", Message: "must be an IANA timezone"}
	}
	return timestamp.In(location), nil
}

func (s *implementation) getRateRecord(
	ctx context.Context,
	from string,
	to string,
) (rateRecord, error) {
	if from == to {
		return rateRecord{From: from, To: to, Value: "1.00000000"}, nil
	}
	if cached, ok := s.getCachedRate(ctx, from, to); ok {
		return cached, nil
	}
	stored, storedErr := s.repository.GetLatest(ctx, from, to)
	if storedErr == nil && s.now().UTC().Sub(stored.Timestamp) < rateCacheTTL {
		s.putCachedRate(ctx, stored, rateCacheTTL)
		return stored, nil
	}
	if storedErr != nil && !errors.Is(storedErr, ErrRateNotFound) {
		return rateRecord{}, fmt.Errorf("get stored exchange rate: %w", storedErr)
	}
	if err := s.RefreshRates(ctx); err != nil {
		if storedErr == nil {
			s.putCachedRate(ctx, stored, staleCacheTTL)
			return stored, nil
		}
		return rateRecord{}, err
	}
	stored, err := s.repository.GetLatest(ctx, from, to)
	if err != nil {
		return rateRecord{}, fmt.Errorf("get refreshed exchange rate: %w", err)
	}
	return stored, nil
}

func validateCurrency(field, value string) (string, error) {
	value = strings.ToUpper(strings.TrimSpace(value))
	for _, supported := range supportedCurrencies {
		if value == supported {
			return value, nil
		}
	}
	return "", &ValidationError{Field: field, Message: "is not supported"}
}

func validateAmount(amount float64) (int64, error) {
	invalid := math.IsNaN(amount) || math.IsInf(amount, 0)
	if invalid || amount < 0 || amount > maxAmount {
		return 0, &ValidationError{Field: "amount", Message: "must be non-negative and within the supported range"}
	}
	cents := math.Round(amount * 100)
	if math.Abs(amount*100-cents) > 1e-9 {
		return 0, &ValidationError{Field: "amount", Message: "must have at most 2 decimal places"}
	}
	return int64(cents), nil
}

func parseRate(value string) (float64, error) {
	rate, err := strconv.ParseFloat(value, 64)
	if err != nil || rate <= 0 || math.IsInf(rate, 0) || math.IsNaN(rate) {
		return 0, fmt.Errorf("invalid stored exchange rate %q", value)
	}
	return rate, nil
}
