package market

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	"github.com/nxsky/twill"
	"github.com/afun-game/predictmarket-saas/pkg/types"
)

const (
	defaultPage            = 1
	defaultLimit           = 20
	maxLimit               = 100
	maxPage                = 1000
	defaultMerchantFeeRate = 0.0
	defaultPlatformFeeRate = 0.0
)

var (
	ErrNotFound          = errors.New("market not found")
	ErrInvalidReference  = errors.New("merchant or event is not active")
	ErrInvalidTransition = errors.New("invalid market status transition")
)

// Service manages prediction markets and their lifecycle.
type Service interface {
	Create(ctx context.Context, req *CreateRequest) (*types.Market, error)
	Get(ctx context.Context, marketID string) (*types.Market, error)
	List(ctx context.Context, filters *ListFilters) ([]*types.Market, int, error)
	GetOrderBook(ctx context.Context, marketID string) (*OrderBook, error)
	UpdateStatus(ctx context.Context, marketID string, status string) error
	AddLiquidity(ctx context.Context, marketID string, amount float64) error
}

type CreateRequest struct {
	twill.AutoMarshal

	MerchantID    string   `json:"merchant_id"`
	EventID       string   `json:"event_id"`
	Type          string   `json:"type"`
	Question      string   `json:"question"`
	Options       []string `json:"options"`
	LiquidityPool float64  `json:"liquidity_pool"`
}

type ListFilters struct {
	twill.AutoMarshal

	MerchantID string `json:"merchant_id,omitempty"`
	EventID    string `json:"event_id,omitempty"`
	Status     string `json:"status,omitempty"`
	Page       int    `json:"page,omitempty"`
	Limit      int    `json:"limit,omitempty"`
}

type OrderBook struct {
	twill.AutoMarshal

	MarketID string           `json:"market_id"`
	Bids     []OrderBookEntry `json:"bids"`
	Asks     []OrderBookEntry `json:"asks"`
}

type OrderBookEntry struct {
	twill.AutoMarshal

	Option string  `json:"option"`
	Price  float64 `json:"price"`
	Amount float64 `json:"amount"`
	Orders int     `json:"orders"`
}

// ValidationError identifies an invalid market request field.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("invalid %s: %s", e.Field, e.Message)
}

type implementation struct {
	twill.Implements[Service]

	database   twill.Database `twill:"primary-db"`
	repository Repository
	random     io.Reader
	now        func() time.Time
}

// NewService creates a Market Service backed by an in-memory repository.
func NewService() Service {
	return newService(newMemoryRepository())
}

func newService(repository Repository) *implementation {
	return &implementation{
		repository: repository,
		random:     rand.Reader,
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
	if s.random == nil {
		s.random = rand.Reader
	}
	if s.now == nil {
		s.now = time.Now
	}
	return nil
}

func (s *implementation) Create(
	ctx context.Context,
	req *CreateRequest,
) (*types.Market, error) {
	input, err := validateCreateRequest(req)
	if err != nil {
		return nil, err
	}
	if err := s.repository.ValidateReferences(ctx, input.MerchantID, input.EventID); err != nil {
		return nil, fmt.Errorf("validate market references: %w", err)
	}

	marketID, err := generateMarketID(s.random)
	if err != nil {
		return nil, fmt.Errorf("generate market ID: %w", err)
	}
	value := &types.Market{
		ID:              marketID,
		MerchantID:      input.MerchantID,
		EventID:         input.EventID,
		Type:            input.Type,
		Question:        input.Question,
		Options:         append([]string{}, input.Options...),
		Status:          "active",
		TotalVolume:     0,
		LiquidityPool:   input.LiquidityPool,
		MerchantFeeRate: defaultMerchantFeeRate,
		PlatformFeeRate: defaultPlatformFeeRate,
		CreatedAt:       s.now().UTC(),
	}
	if err := s.repository.Create(ctx, value); err != nil {
		return nil, fmt.Errorf("create market: %w", err)
	}
	return cloneMarket(value), nil
}

func (s *implementation) Get(ctx context.Context, marketID string) (*types.Market, error) {
	marketID, err := validateMarketID(marketID)
	if err != nil {
		return nil, err
	}
	value, err := s.repository.GetByID(ctx, marketID)
	if err != nil {
		return nil, fmt.Errorf("get market: %w", err)
	}
	return value, nil
}

func (s *implementation) List(
	ctx context.Context,
	filters *ListFilters,
) ([]*types.Market, int, error) {
	normalized, err := normalizeFilters(filters)
	if err != nil {
		return nil, 0, err
	}
	values, total, err := s.repository.List(ctx, normalized)
	if err != nil {
		return nil, 0, fmt.Errorf("list markets: %w", err)
	}
	return values, total, nil
}

func (s *implementation) GetOrderBook(ctx context.Context, marketID string) (*OrderBook, error) {
	market, err := s.Get(ctx, marketID)
	if err != nil {
		return nil, err
	}
	return &OrderBook{
		MarketID: market.ID,
		Bids:     []OrderBookEntry{},
		Asks:     []OrderBookEntry{},
	}, nil
}

func (s *implementation) UpdateStatus(ctx context.Context, marketID string, status string) error {
	marketID, err := validateMarketID(marketID)
	if err != nil {
		return err
	}
	status = strings.ToLower(strings.TrimSpace(status))
	if status == "settled" || !validStatus(status) {
		return &ValidationError{Field: "status", Message: "is not supported"}
	}

	value, err := s.repository.GetByID(ctx, marketID)
	if err != nil {
		return fmt.Errorf("get market for status update: %w", err)
	}
	if !canTransition(value.Status, status) {
		return fmt.Errorf("%w: %s to %s", ErrInvalidTransition, value.Status, status)
	}
	if err := s.repository.UpdateStatus(ctx, marketID, value.Status, status); err != nil {
		return fmt.Errorf("update market status: %w", err)
	}
	return nil
}

func (s *implementation) AddLiquidity(ctx context.Context, marketID string, amount float64) error {
	marketID, err := validateMarketID(marketID)
	if err != nil {
		return err
	}
	if err := validateMoney("amount", amount, false); err != nil {
		return err
	}

	value, err := s.repository.GetByID(ctx, marketID)
	if err != nil {
		return fmt.Errorf("get market for liquidity update: %w", err)
	}
	if value.Status != "active" {
		return fmt.Errorf("%w: liquidity requires active status", ErrInvalidTransition)
	}
	if err := s.repository.AddLiquidity(ctx, marketID, value.Status, amount); err != nil {
		return fmt.Errorf("add market liquidity: %w", err)
	}
	return nil
}

func validateCreateRequest(req *CreateRequest) (*CreateRequest, error) {
	if req == nil {
		return nil, &ValidationError{Field: "request", Message: "is required"}
	}

	input := *req
	input.MerchantID = strings.TrimSpace(input.MerchantID)
	input.EventID = strings.TrimSpace(input.EventID)
	input.Type = strings.ToLower(strings.TrimSpace(input.Type))
	input.Question = strings.TrimSpace(input.Question)
	input.Options = normalizeOptions(input.Options)
	if !isUUID(input.MerchantID) {
		return nil, &ValidationError{Field: "merchant_id", Message: "must be a UUID"}
	}
	if !isUUID(input.EventID) {
		return nil, &ValidationError{Field: "event_id", Message: "must be a UUID"}
	}
	if input.Type != "binary" {
		return nil, &ValidationError{Field: "type", Message: "only binary markets are supported"}
	}
	if input.Question == "" {
		return nil, &ValidationError{Field: "question", Message: "is required"}
	}
	if err := validateOptions(input.Options); err != nil {
		return nil, err
	}
	if err := validateMoney("liquidity_pool", input.LiquidityPool, true); err != nil {
		return nil, err
	}
	return &input, nil
}

func normalizeFilters(filters *ListFilters) (ListFilters, error) {
	value := ListFilters{}
	if filters != nil {
		value = *filters
	}
	value.MerchantID = strings.TrimSpace(value.MerchantID)
	value.EventID = strings.TrimSpace(value.EventID)
	value.Status = strings.ToLower(strings.TrimSpace(value.Status))
	if value.MerchantID != "" && !isUUID(value.MerchantID) {
		return ListFilters{}, &ValidationError{Field: "merchant_id", Message: "must be a UUID"}
	}
	if value.EventID != "" && !isUUID(value.EventID) {
		return ListFilters{}, &ValidationError{Field: "event_id", Message: "must be a UUID"}
	}
	if value.Status != "" && !validStatus(value.Status) {
		return ListFilters{}, &ValidationError{Field: "status", Message: "is not supported"}
	}
	if value.Page == 0 {
		value.Page = defaultPage
	}
	if value.Limit == 0 {
		value.Limit = defaultLimit
	}
	if value.Page < 1 {
		return ListFilters{}, &ValidationError{Field: "page", Message: "must be at least 1"}
	}
	if value.Page > maxPage {
		return ListFilters{}, &ValidationError{Field: "page", Message: "must not exceed 1000"}
	}
	if value.Limit < 1 || value.Limit > maxLimit {
		return ListFilters{}, &ValidationError{Field: "limit", Message: "must be between 1 and 100"}
	}
	return value, nil
}

func validateMarketID(marketID string) (string, error) {
	marketID = strings.TrimSpace(marketID)
	if !isUUID(marketID) {
		return "", &ValidationError{Field: "market_id", Message: "must be a UUID"}
	}
	return marketID, nil
}

func normalizeOptions(options []string) []string {
	values := make([]string, 0, len(options))
	for _, option := range options {
		values = append(values, strings.TrimSpace(option))
	}
	return values
}

func validateOptions(options []string) error {
	if len(options) != 2 {
		return &ValidationError{Field: "options", Message: "binary markets require exactly 2 options"}
	}
	seen := make(map[string]struct{}, len(options))
	for _, option := range options {
		if option == "" {
			return &ValidationError{Field: "options", Message: "cannot contain empty values"}
		}
		key := strings.ToLower(option)
		if _, exists := seen[key]; exists {
			return &ValidationError{Field: "options", Message: "must contain unique values"}
		}
		seen[key] = struct{}{}
	}
	return nil
}

func validateMoney(field string, value float64, allowZero bool) error {
	invalidNumber := math.IsNaN(value) || math.IsInf(value, 0)
	belowMinimum := value < 0 || (!allowZero && value == 0)
	if invalidNumber || belowMinimum {
		message := "must be greater than 0"
		if allowZero {
			message = "must be at least 0"
		}
		return &ValidationError{Field: field, Message: message}
	}
	if math.Abs(value*100-math.Round(value*100)) > 1e-9 {
		return &ValidationError{Field: field, Message: "must have at most 2 decimal places"}
	}
	return nil
}

func validStatus(status string) bool {
	switch status {
	case "active", "suspended", "closed", "settled":
		return true
	default:
		return false
	}
}

func canTransition(from, to string) bool {
	switch from {
	case "active":
		return to == "suspended" || to == "closed"
	case "suspended":
		return to == "active" || to == "closed"
	default:
		return false
	}
}

func containsOption(options []string, target string) bool {
	for _, option := range options {
		if option == target {
			return true
		}
	}
	return false
}

func isUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	compact := strings.ReplaceAll(value, "-", "")
	_, err := hex.DecodeString(compact)
	return err == nil
}

func generateMarketID(random io.Reader) (string, error) {
	buffer := make([]byte, 16)
	if _, err := io.ReadFull(random, buffer); err != nil {
		return "", err
	}
	buffer[6] = (buffer[6] & 0x0f) | 0x40
	buffer[8] = (buffer[8] & 0x3f) | 0x80
	return fmt.Sprintf(
		"%x-%x-%x-%x-%x",
		buffer[0:4],
		buffer[4:6],
		buffer[6:8],
		buffer[8:10],
		buffer[10:16],
	), nil
}
