package order

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/afun-game/predictmarket-saas/internal/market"
	"github.com/afun-game/predictmarket-saas/internal/wallet"
	"github.com/afun-game/predictmarket-saas/pkg/fixed"
	"github.com/afun-game/predictmarket-saas/pkg/types"
	"github.com/nxsky/twill"
)

const (
	defaultPage          = 1
	defaultLimit         = 20
	maxLimit             = 100
	maxCursorLimit       = 500
	maxPage              = 1000
	maxUserIDLen         = 255
	maxIdempotencyKeyLen = 255
)

var (
	ErrNotFound       = errors.New("order not found")
	ErrAlreadyExists  = errors.New("order already exists")
	ErrInvalidMarket  = errors.New("market is not active for this merchant")
	ErrNotCancellable = errors.New("order cannot be cancelled")
)

// Service manages orders and price-time-priority matching.
type Service interface {
	Create(ctx context.Context, req *CreateRequest) (*types.Order, error)
	Get(ctx context.Context, orderID string) (*types.Order, error)
	List(ctx context.Context, filters *ListFilters) ([]*types.Order, int, error)
	ListCursor(ctx context.Context, filters *ListFilters) (*CursorPage, error)
	ListTrades(ctx context.Context, filters *TradeListFilters) (*TradeCursorPage, error)
	Cancel(ctx context.Context, orderID string) error
	ListByUser(ctx context.Context, merchantID, userID string, page, limit int) ([]*types.Order, int, error)
	ListByMarket(ctx context.Context, marketID string, page, limit int) ([]*types.Order, int, error)
	GetOrderBook(ctx context.Context, marketID string) (*market.OrderBook, error)
}

type CreateRequest struct {
	twill.AutoMarshal

	MerchantID     string  `json:"merchant_id"`
	UserID         string  `json:"user_id"`
	MarketID       string  `json:"market_id"`
	Type           string  `json:"type"`
	Option         string  `json:"option"`
	Amount         float64 `json:"amount"`
	Currency       string  `json:"currency"`
	Price          float64 `json:"price"`
	TimeInForce    string  `json:"time_in_force,omitempty"`
	IdempotencyKey string  `json:"-"`
	WalletKind     string  `json:"-"`
	Channel        string  `json:"-"`
}

type ListFilters struct {
	twill.AutoMarshal

	MerchantID string     `json:"merchant_id,omitempty"`
	UserID     string     `json:"user_id,omitempty"`
	MarketID   string     `json:"market_id,omitempty"`
	Status     string     `json:"status,omitempty"`
	Cursor     string     `json:"cursor,omitempty"`
	From       *time.Time `json:"from,omitempty"`
	To         *time.Time `json:"to,omitempty"`
	Page       int        `json:"page,omitempty"`
	Limit      int        `json:"limit,omitempty"`
	Keyset     bool       `json:"-"`
}

// CursorPage is an offset-free page for high-cardinality order histories.
type CursorPage struct {
	twill.AutoMarshal

	Orders     []*types.Order `json:"orders"`
	NextCursor string         `json:"next_cursor,omitempty"`
}

// TradeListFilters scopes an execution history to one merchant or order.
type TradeListFilters struct {
	twill.AutoMarshal

	MerchantID string     `json:"merchant_id,omitempty"`
	OrderID    string     `json:"order_id,omitempty"`
	Cursor     string     `json:"cursor,omitempty"`
	From       *time.Time `json:"from,omitempty"`
	To         *time.Time `json:"to,omitempty"`
	Limit      int        `json:"limit,omitempty"`
}

// TradeCursorPage is a stable keyset page of executions.
type TradeCursorPage struct {
	twill.AutoMarshal

	Trades     []*types.Trade `json:"trades"`
	NextCursor string         `json:"next_cursor,omitempty"`
}

// ValidationError identifies an invalid order field.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("invalid %s: %s", e.Field, e.Message)
}

type implementation struct {
	twill.Implements[Service]

	database  twill.Database `twill:"primary-db"`
	marketRef twill.Ref[market.Service]
	walletRef twill.Ref[wallet.Service]

	repository Repository
	markets    market.Service
	wallets    wallet.Service
	random     io.Reader
	now        func() time.Time
}

// NewService creates an Order Service backed by in-memory components.
func NewService() Service {
	return newService(newMemoryRepository(), market.NewService(), wallet.NewService())
}

// NewServiceWithDependencies creates an in-memory Order Service sharing the provided services.
func NewServiceWithDependencies(
	marketService market.Service,
	walletService wallet.Service,
) Service {
	return newService(newMemoryRepository(), marketService, walletService)
}

func newService(
	repository Repository,
	marketService market.Service,
	walletService wallet.Service,
) *implementation {
	return &implementation{
		repository: repository,
		markets:    marketService,
		wallets:    walletService,
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
	if s.markets == nil {
		s.markets = s.marketRef.Get()
	}
	if s.wallets == nil {
		s.wallets = s.walletRef.Get()
	}
	if s.markets == nil || s.wallets == nil {
		return errors.New("market and wallet services are not configured")
	}
	if s.random == nil {
		s.random = rand.Reader
	}
	if s.now == nil {
		s.now = time.Now
	}
	return nil
}

func (s *implementation) Create(ctx context.Context, req *CreateRequest) (*types.Order, error) {
	input, err := validateCreateRequest(req)
	if err != nil {
		return nil, err
	}
	if input.IdempotencyKey != "" {
		existing, err := s.repository.GetByIdempotency(ctx, input.MerchantID, input.IdempotencyKey)
		if err == nil {
			return existing, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return nil, fmt.Errorf("get idempotent order: %w", err)
		}
	}
	marketValue, err := s.markets.Get(ctx, input.MarketID)
	if err != nil {
		return nil, fmt.Errorf("get order market: %w", err)
	}
	validOwner := marketValue.MerchantID == input.MerchantID
	if !validOwner || marketValue.Status != "active" || !containsOption(marketValue.Options, input.Option) {
		return nil, ErrInvalidMarket
	}
	orderID, err := generateUUID(s.random)
	if err != nil {
		return nil, fmt.Errorf("generate order ID: %w", err)
	}
	value := &types.Order{
		ID:             orderID,
		MerchantID:     input.MerchantID,
		UserID:         input.UserID,
		MarketID:       input.MarketID,
		Type:           input.Type,
		Option:         input.Option,
		Amount:         input.Amount,
		FilledAmount:   0,
		Currency:       input.Currency,
		Price:          input.Price,
		TimeInForce:    input.TimeInForce,
		IdempotencyKey: input.IdempotencyKey,
		WalletKind:     input.WalletKind,
		Channel:        input.Channel,
		Status:         "pending",
		CreatedAt:      s.now().UTC(),
	}
	collateralCents := requiredCollateralCents(input.Type, input.Amount, input.Price)
	if repository, ok := s.repository.(atomicPlacementRepository); ok {
		if err := repository.PlaceWithLockedCollateral(ctx, value, collateralCents); err != nil {
			if existing, handled := s.idempotentExistingOrder(ctx, input, err); handled {
				return existing, nil
			}
			return nil, fmt.Errorf("place order: %w", err)
		}
		return cloneOrder(value), nil
	}

	collateral := fixed.CentsToFloat(collateralCents)
	if err := s.wallets.Lock(
		ctx,
		input.MerchantID,
		input.UserID,
		input.Currency,
		collateral,
	); err != nil {
		return nil, fmt.Errorf("lock order balance: %w", err)
	}
	priceImprovement, err := s.repository.Place(ctx, value)
	if err != nil {
		s.bestEffortUnlock(ctx, input, collateral)
		if existing, handled := s.idempotentExistingOrder(ctx, input, err); handled {
			return existing, nil
		}
		return nil, fmt.Errorf("place order: %w", err)
	}
	if priceImprovement > 0 {
		if err := s.wallets.Unlock(
			ctx,
			value.MerchantID,
			value.UserID,
			value.Currency,
			priceImprovement,
		); err != nil {
			return nil, fmt.Errorf("refund order price improvement: %w", err)
		}
	}
	if value.Status == "cancelled" {
		remaining := remainingShares(value.Amount, value.FilledAmount)
		if remaining != 0 {
			remainingCollateral := requiredCollateral(value.Type, remaining, value.Price)
			if err := s.wallets.Unlock(
				ctx,
				value.MerchantID,
				value.UserID,
				value.Currency,
				remainingCollateral,
			); err != nil {
				return nil, fmt.Errorf("unlock IOC remainder: %w", err)
			}
		}
	}
	return cloneOrder(value), nil
}

func (s *implementation) Get(ctx context.Context, orderID string) (*types.Order, error) {
	orderID, err := validateUUIDField("order_id", orderID)
	if err != nil {
		return nil, err
	}
	value, err := s.repository.Get(ctx, orderID)
	if err != nil {
		return nil, fmt.Errorf("get order: %w", err)
	}
	return value, nil
}

func (s *implementation) List(
	ctx context.Context,
	filters *ListFilters,
) ([]*types.Order, int, error) {
	normalized, err := normalizeFilters(filters)
	if err != nil {
		return nil, 0, err
	}
	values, total, err := s.repository.List(ctx, normalized)
	if err != nil {
		return nil, 0, fmt.Errorf("list orders: %w", err)
	}
	return values, total, nil
}

// ListCursor returns orders using the immutable (created_at, id) keyset. It
// intentionally avoids COUNT(*) and OFFSET for deep history pages.
func (s *implementation) ListCursor(ctx context.Context, filters *ListFilters) (*CursorPage, error) {
	keysetFilters := ListFilters{Keyset: true}
	if filters != nil {
		keysetFilters = *filters
		keysetFilters.Keyset = true
	}
	normalized, err := normalizeFilters(&keysetFilters)
	if err != nil {
		return nil, err
	}
	cursor, err := decodeCursor(normalized.Cursor)
	if err != nil {
		return nil, err
	}
	values, err := s.repository.ListAfter(ctx, normalized, cursor)
	if err != nil {
		return nil, fmt.Errorf("list orders by cursor: %w", err)
	}
	page := &CursorPage{Orders: values}
	if len(values) <= normalized.Limit {
		return page, nil
	}
	page.Orders = values[:normalized.Limit]
	lastOrder := page.Orders[len(page.Orders)-1]
	page.NextCursor = encodeCursor(lastOrder.CreatedAt, lastOrder.ID)
	return page, nil
}

// ListTrades returns executions ordered newest first with a stable keyset cursor.
func (s *implementation) ListTrades(ctx context.Context, filters *TradeListFilters) (*TradeCursorPage, error) {
	normalized, err := normalizeTradeFilters(filters)
	if err != nil {
		return nil, err
	}
	cursor, err := decodeCursor(normalized.Cursor)
	if err != nil {
		return nil, err
	}
	values, err := s.repository.ListTrades(ctx, normalized, cursor)
	if err != nil {
		return nil, fmt.Errorf("list trades by cursor: %w", err)
	}
	page := &TradeCursorPage{Trades: values}
	if len(values) <= normalized.Limit {
		return page, nil
	}
	page.Trades = values[:normalized.Limit]
	lastTrade := page.Trades[len(page.Trades)-1]
	page.NextCursor = encodeCursor(lastTrade.CreatedAt, lastTrade.ID)
	return page, nil
}

func (s *implementation) Cancel(ctx context.Context, orderID string) error {
	orderID, err := validateUUIDField("order_id", orderID)
	if err != nil {
		return err
	}
	if repository, ok := s.repository.(atomicCancellationRepository); ok {
		if err := repository.CancelWithUnlock(ctx, orderID); err != nil {
			return fmt.Errorf("cancel order: %w", err)
		}
		return nil
	}
	value, remaining, err := s.repository.Cancel(ctx, orderID)
	if err != nil {
		return fmt.Errorf("cancel order: %w", err)
	}
	if remaining == 0 {
		return nil
	}
	remainingCollateral := requiredCollateral(value.Type, remaining, value.Price)
	if err := s.wallets.Unlock(
		ctx,
		value.MerchantID,
		value.UserID,
		value.Currency,
		remainingCollateral,
	); err != nil {
		return fmt.Errorf("unlock cancelled order balance: %w", err)
	}
	return nil
}

func (s *implementation) ListByUser(
	ctx context.Context,
	merchantID string,
	userID string,
	page int,
	limit int,
) ([]*types.Order, int, error) {
	return s.List(ctx, &ListFilters{
		MerchantID: merchantID,
		UserID:     userID,
		Page:       page,
		Limit:      limit,
	})
}

func (s *implementation) ListByMarket(
	ctx context.Context,
	marketID string,
	page int,
	limit int,
) ([]*types.Order, int, error) {
	return s.List(ctx, &ListFilters{MarketID: marketID, Page: page, Limit: limit})
}

func (s *implementation) GetOrderBook(
	ctx context.Context,
	marketID string,
) (*market.OrderBook, error) {
	marketID, err := validateUUIDField("market_id", marketID)
	if err != nil {
		return nil, err
	}
	if _, err := s.markets.Get(ctx, marketID); err != nil {
		return nil, fmt.Errorf("get order book market: %w", err)
	}
	book, err := s.repository.GetOrderBook(ctx, marketID)
	if err != nil {
		return nil, fmt.Errorf("get order book: %w", err)
	}
	return book, nil
}

func (s *implementation) bestEffortUnlock(ctx context.Context, req *CreateRequest, amount float64) {
	_ = s.wallets.Unlock(ctx, req.MerchantID, req.UserID, req.Currency, amount)
}

func validateCreateRequest(req *CreateRequest) (*CreateRequest, error) {
	if req == nil {
		return nil, &ValidationError{Field: "request", Message: "is required"}
	}
	input := *req
	var err error
	input.MerchantID, err = validateUUIDField("merchant_id", input.MerchantID)
	if err != nil {
		return nil, err
	}
	input.MarketID, err = validateUUIDField("market_id", input.MarketID)
	if err != nil {
		return nil, err
	}
	input.UserID = strings.TrimSpace(input.UserID)
	input.Type = strings.ToLower(strings.TrimSpace(input.Type))
	input.Option = strings.TrimSpace(input.Option)
	input.Currency = strings.ToUpper(strings.TrimSpace(input.Currency))
	input.TimeInForce = strings.ToLower(strings.TrimSpace(input.TimeInForce))
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	input.WalletKind = strings.ToLower(strings.TrimSpace(input.WalletKind))
	input.Channel = strings.ToLower(strings.TrimSpace(input.Channel))
	if input.TimeInForce == "" {
		input.TimeInForce = "gtc"
	}
	if input.WalletKind == "" {
		input.WalletKind = "user"
	}
	if input.Channel == "" {
		input.Channel = "api"
	}
	if input.UserID == "" || len(input.UserID) > maxUserIDLen {
		return nil, &ValidationError{Field: "user_id", Message: "is required and must be at most 255 characters"}
	}
	if input.Type != "buy" && input.Type != "sell" {
		return nil, &ValidationError{Field: "type", Message: "must be buy or sell"}
	}
	if input.Option == "" {
		return nil, &ValidationError{Field: "option", Message: "is required"}
	}
	input.Amount, err = normalizeShares(input.Amount)
	if err != nil {
		return nil, err
	}
	if !validCurrency(input.Currency) {
		return nil, &ValidationError{Field: "currency", Message: "is not supported"}
	}
	input.Price, err = normalizePrice(input.Price)
	if err != nil {
		return nil, err
	}
	if input.TimeInForce != "gtc" && input.TimeInForce != "ioc" {
		return nil, &ValidationError{Field: "time_in_force", Message: "must be gtc or ioc"}
	}
	if input.WalletKind != "user" && input.WalletKind != "shadow" {
		return nil, &ValidationError{Field: "wallet_kind", Message: "is not supported"}
	}
	if input.Channel != "api" && input.Channel != "hosted" && input.Channel != "mm" {
		return nil, &ValidationError{Field: "channel", Message: "is not supported"}
	}
	if len(input.IdempotencyKey) > maxIdempotencyKeyLen {
		return nil, &ValidationError{Field: "idempotency_key", Message: "must be at most 255 characters"}
	}
	return &input, nil
}

func (s *implementation) idempotentExistingOrder(
	ctx context.Context,
	request *CreateRequest,
	err error,
) (*types.Order, bool) {
	if request.IdempotencyKey == "" || !errors.Is(err, ErrAlreadyExists) {
		return nil, false
	}
	existing, lookupErr := s.repository.GetByIdempotency(ctx, request.MerchantID, request.IdempotencyKey)
	if lookupErr != nil {
		return nil, false
	}
	return existing, true
}

func normalizeFilters(filters *ListFilters) (ListFilters, error) {
	value := ListFilters{}
	if filters != nil {
		value = *filters
	}
	var err error
	if strings.TrimSpace(value.MerchantID) != "" {
		value.MerchantID, err = validateUUIDField("merchant_id", value.MerchantID)
		if err != nil {
			return ListFilters{}, err
		}
	}
	value.UserID = strings.TrimSpace(value.UserID)
	if len(value.UserID) > maxUserIDLen {
		return ListFilters{}, &ValidationError{Field: "user_id", Message: "must be at most 255 characters"}
	}
	if strings.TrimSpace(value.MarketID) != "" {
		value.MarketID, err = validateUUIDField("market_id", value.MarketID)
		if err != nil {
			return ListFilters{}, err
		}
	}
	value.Status = strings.ToLower(strings.TrimSpace(value.Status))
	value.Cursor = strings.TrimSpace(value.Cursor)
	if value.Status != "" && !validStatus(value.Status) {
		return ListFilters{}, &ValidationError{Field: "status", Message: "is not supported"}
	}
	if value.From != nil && value.To != nil && value.From.After(*value.To) {
		return ListFilters{}, &ValidationError{Field: "from", Message: "must not be after to"}
	}
	if value.Keyset {
		value.Limit, err = normalizeCursorLimit(value.Limit)
		if err != nil {
			return ListFilters{}, err
		}
		return value, nil
	}
	value.Page, value.Limit, err = normalizePagination(value.Page, value.Limit)
	if err != nil {
		return ListFilters{}, err
	}
	return value, nil
}

func normalizeTradeFilters(filters *TradeListFilters) (TradeListFilters, error) {
	value := TradeListFilters{}
	if filters != nil {
		value = *filters
	}
	var err error
	if strings.TrimSpace(value.MerchantID) != "" {
		value.MerchantID, err = validateUUIDField("merchant_id", value.MerchantID)
		if err != nil {
			return TradeListFilters{}, err
		}
	}
	if strings.TrimSpace(value.OrderID) != "" {
		value.OrderID, err = validateUUIDField("order_id", value.OrderID)
		if err != nil {
			return TradeListFilters{}, err
		}
	}
	if value.MerchantID == "" && value.OrderID == "" {
		return TradeListFilters{}, &ValidationError{Field: "merchant_id", Message: "is required when order_id is omitted"}
	}
	value.Cursor = strings.TrimSpace(value.Cursor)
	value.Limit, err = normalizeCursorLimit(value.Limit)
	if err != nil {
		return TradeListFilters{}, err
	}
	if value.From != nil && value.To != nil && value.From.After(*value.To) {
		return TradeListFilters{}, &ValidationError{Field: "from", Message: "must not be after to"}
	}
	return value, nil
}

type encodedCursor struct {
	CreatedAt string `json:"created_at"`
	ID        string `json:"id"`
}

func decodeCursor(value string) (*Cursor, error) {
	if value == "" {
		return nil, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, &ValidationError{Field: "cursor", Message: "is invalid"}
	}
	var encoded encodedCursor
	if err := json.Unmarshal(decoded, &encoded); err != nil {
		return nil, &ValidationError{Field: "cursor", Message: "is invalid"}
	}
	createdAt, err := time.Parse(time.RFC3339Nano, encoded.CreatedAt)
	if err != nil {
		return nil, &ValidationError{Field: "cursor", Message: "is invalid"}
	}
	if _, err := validateUUIDField("cursor", encoded.ID); err != nil {
		return nil, &ValidationError{Field: "cursor", Message: "is invalid"}
	}
	return &Cursor{CreatedAt: createdAt.UTC(), ID: encoded.ID}, nil
}

func encodeCursor(createdAt time.Time, id string) string {
	encoded, err := json.Marshal(encodedCursor{CreatedAt: createdAt.UTC().Format(time.RFC3339Nano), ID: id})
	if err != nil {
		panic(fmt.Errorf("marshal order cursor: %w", err))
	}
	return base64.RawURLEncoding.EncodeToString(encoded)
}

func validateUUIDField(field, value string) (string, error) {
	value = strings.TrimSpace(value)
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return "", &ValidationError{Field: field, Message: "must be a UUID"}
	}
	if _, err := hex.DecodeString(strings.ReplaceAll(value, "-", "")); err != nil {
		return "", &ValidationError{Field: field, Message: "must be a UUID"}
	}
	return value, nil
}

func validateAmount(amount float64) error {
	_, err := normalizeShares(amount)
	return err
}

func validatePrice(price float64) error {
	_, err := normalizePrice(price)
	return err
}

func normalizeShares(amount float64) (float64, error) {
	units, err := fixed.SharesFromFloat(amount)
	if err != nil {
		return 0, &ValidationError{Field: "amount", Message: "must be greater than 0 with at most 6 decimal places"}
	}
	return fixed.SharesToFloat(units), nil
}

func normalizePrice(price float64) (float64, error) {
	units, err := fixed.PriceFromFloat(price)
	if err != nil || units == 0 || units == fixed.PriceScale {
		return 0, &ValidationError{Field: "price", Message: "must be greater than 0 and less than 1 with at most 6 decimal places"}
	}
	return fixed.PriceToFloat(units), nil
}

func remainingShares(amount, filled float64) float64 {
	return fixed.SharesToFloat(storedShareUnits(amount) - storedShareUnits(filled))
}

func storedShareUnits(value float64) int64 {
	if value == 0 {
		return 0
	}
	units, err := fixed.SharesFromFloat(value)
	if err != nil {
		panic(err)
	}
	return units
}

func storedPriceUnits(value float64) int64 {
	units, err := fixed.PriceFromFloat(value)
	if err != nil {
		panic(err)
	}
	return units
}

func normalizePagination(page, limit int) (int, int, error) {
	if page == 0 {
		page = defaultPage
	}
	if limit == 0 {
		limit = defaultLimit
	}
	if page < 1 {
		return 0, 0, &ValidationError{Field: "page", Message: "must be at least 1"}
	}
	if page > maxPage {
		return 0, 0, &ValidationError{Field: "page", Message: "must not exceed 1000; use cursor pagination for deep history"}
	}
	if limit < 1 || limit > maxLimit {
		return 0, 0, &ValidationError{Field: "limit", Message: "must be between 1 and 100"}
	}
	return page, limit, nil
}

func normalizeCursorLimit(limit int) (int, error) {
	if limit == 0 {
		limit = defaultLimit
	}
	if limit < 1 || limit > maxCursorLimit {
		return 0, &ValidationError{Field: "limit", Message: "must be between 1 and 500"}
	}
	return limit, nil
}

func validCurrency(currency string) bool {
	switch currency {
	case "USD", "EUR", "CNY", "JPY", "GBP", "MXN":
		return true
	default:
		return false
	}
}

func validStatus(status string) bool {
	switch status {
	case "pending", "partial", "filled", "cancelled", "voided":
		return true
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

func generateUUID(random io.Reader) (string, error) {
	buffer := make([]byte, 16)
	if _, err := io.ReadFull(random, buffer); err != nil {
		return "", err
	}
	buffer[6] = (buffer[6] & 0x0f) | 0x40
	buffer[8] = (buffer[8] & 0x3f) | 0x80
	return fmt.Sprintf(
		"%x-%x-%x-%x-%x",
		buffer[0:4], buffer[4:6], buffer[6:8], buffer[8:10], buffer[10:16],
	), nil
}
