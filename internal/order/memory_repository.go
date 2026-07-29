package order

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/afun-game/predictmarket-saas/internal/market"
	"github.com/afun-game/predictmarket-saas/pkg/fixed"
	"github.com/afun-game/predictmarket-saas/pkg/types"
)

type memoryRepository struct {
	mu   sync.RWMutex
	byID map[string]*types.Order
}

type bookLevelKey struct {
	option string
	price  float64
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{byID: map[string]*types.Order{}}
}

func (r *memoryRepository) Place(ctx context.Context, incoming *types.Order) (float64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.byID[incoming.ID]; exists {
		return 0, ErrAlreadyExists
	}
	if incoming.IdempotencyKey != "" {
		for _, value := range r.byID {
			if value.MerchantID == incoming.MerchantID && value.IdempotencyKey == incoming.IdempotencyKey {
				return 0, ErrAlreadyExists
			}
		}
	}

	stored := cloneOrder(incoming)
	r.byID[stored.ID] = stored
	candidates := r.matchingCandidates(stored)
	var priceImprovement int64
	for _, maker := range candidates {
		incomingRemaining := storedShareUnits(stored.Amount) - storedShareUnits(stored.FilledAmount)
		if incomingRemaining == 0 {
			break
		}
		makerRemaining := storedShareUnits(maker.Amount) - storedShareUnits(maker.FilledAmount)
		fillAmount := min(incomingRemaining, makerRemaining)
		priceImprovement += priceImprovementRefundCents(
			stored.Type,
			fixed.SharesToFloat(fillAmount),
			stored.Price,
			maker.Price,
		)
		maker.FilledAmount = fixed.SharesToFloat(storedShareUnits(maker.FilledAmount) + fillAmount)
		stored.FilledAmount = fixed.SharesToFloat(storedShareUnits(stored.FilledAmount) + fillAmount)
		updateOrderFillStatus(maker, stored.CreatedAt)
	}
	updateOrderFillStatus(stored, stored.CreatedAt)
	if stored.TimeInForce == "ioc" && storedShareUnits(stored.FilledAmount) < storedShareUnits(stored.Amount) {
		stored.Status = "cancelled"
	}
	*incoming = *cloneOrder(stored)
	return fixed.CentsToFloat(priceImprovement), nil
}

func (r *memoryRepository) Get(ctx context.Context, orderID string) (*types.Order, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	value, exists := r.byID[orderID]
	if !exists {
		return nil, ErrNotFound
	}
	return cloneOrder(value), nil
}

func (r *memoryRepository) GetByIdempotency(
	ctx context.Context,
	merchantID string,
	key string,
) (*types.Order, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, value := range r.byID {
		if value.MerchantID == merchantID && value.IdempotencyKey == key {
			return cloneOrder(value), nil
		}
	}
	return nil, ErrNotFound
}

func (r *memoryRepository) List(
	ctx context.Context,
	filters ListFilters,
) ([]*types.Order, int, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	values := make([]*types.Order, 0, len(r.byID))
	for _, value := range r.byID {
		matchesMerchant := filters.MerchantID == "" || value.MerchantID == filters.MerchantID
		matchesUser := filters.UserID == "" || value.UserID == filters.UserID
		matchesMarket := filters.MarketID == "" || value.MarketID == filters.MarketID
		matchesStatus := filters.Status == "" || value.Status == filters.Status
		if matchesMerchant && matchesUser && matchesMarket && matchesStatus {
			values = append(values, cloneOrder(value))
		}
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].CreatedAt.Equal(values[j].CreatedAt) {
			return values[i].ID < values[j].ID
		}
		return values[i].CreatedAt.After(values[j].CreatedAt)
	})
	total := len(values)
	offset := (filters.Page - 1) * filters.Limit
	if offset >= total {
		return []*types.Order{}, total, nil
	}
	end := min(offset+filters.Limit, total)
	return values[offset:end], total, nil
}

func (r *memoryRepository) ListAfter(
	ctx context.Context,
	filters ListFilters,
	cursor *Cursor,
) ([]*types.Order, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	values := make([]*types.Order, 0, filters.Limit+1)
	for _, value := range r.byID {
		if !matchesOrderFilters(value, filters) || !afterCursor(value, cursor) {
			continue
		}
		values = append(values, cloneOrder(value))
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].CreatedAt.Equal(values[j].CreatedAt) {
			return values[i].ID > values[j].ID
		}
		return values[i].CreatedAt.After(values[j].CreatedAt)
	})
	if len(values) > filters.Limit+1 {
		values = values[:filters.Limit+1]
	}
	return values, nil
}

func afterCursor(value *types.Order, cursor *Cursor) bool {
	if cursor == nil {
		return true
	}
	if value.CreatedAt.Equal(cursor.CreatedAt) {
		return value.ID < cursor.ID
	}
	return value.CreatedAt.Before(cursor.CreatedAt)
}

func matchesOrderFilters(value *types.Order, filters ListFilters) bool {
	return (filters.MerchantID == "" || value.MerchantID == filters.MerchantID) &&
		(filters.UserID == "" || value.UserID == filters.UserID) &&
		(filters.MarketID == "" || value.MarketID == filters.MarketID) &&
		(filters.Status == "" || value.Status == filters.Status)
}

func (r *memoryRepository) Cancel(
	ctx context.Context,
	orderID string,
) (*types.Order, float64, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	value, exists := r.byID[orderID]
	if !exists {
		return nil, 0, ErrNotFound
	}
	if value.Status != "pending" && value.Status != "partial" {
		return nil, 0, ErrNotCancellable
	}
	remaining := remainingShares(value.Amount, value.FilledAmount)
	value.Status = "cancelled"
	return cloneOrder(value), remaining, nil
}

func (r *memoryRepository) GetOrderBook(
	ctx context.Context,
	marketID string,
) (*market.OrderBook, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	bids := map[bookLevelKey]*market.OrderBookEntry{}
	asks := map[bookLevelKey]*market.OrderBookEntry{}
	for _, value := range r.byID {
		open := value.Status == "pending" || value.Status == "partial"
		if value.MarketID != marketID || !open {
			continue
		}
		levels := bids
		if value.Type == "sell" {
			levels = asks
		}
		key := bookLevelKey{option: value.Option, price: value.Price}
		entry := levels[key]
		if entry == nil {
			entry = &market.OrderBookEntry{Option: value.Option, Price: value.Price}
			levels[key] = entry
		}
		entry.Amount = fixed.SharesToFloat(
			storedShareUnits(entry.Amount) + storedShareUnits(value.Amount) - storedShareUnits(value.FilledAmount),
		)
		entry.Orders++
	}
	return &market.OrderBook{
		MarketID: marketID,
		Bids:     sortedLevels(bids, true),
		Asks:     sortedLevels(asks, false),
	}, nil
}

func (r *memoryRepository) matchingCandidates(incoming *types.Order) []*types.Order {
	values := make([]*types.Order, 0, len(r.byID))
	for _, value := range r.byID {
		open := value.Status == "pending" || value.Status == "partial"
		sameBook := value.MerchantID == incoming.MerchantID &&
			value.MarketID == incoming.MarketID &&
			value.Option == incoming.Option &&
			value.Currency == incoming.Currency
		oppositeUserAndSide := value.UserID != incoming.UserID && value.Type != incoming.Type
		crosses := incoming.Type == "buy" && value.Price <= incoming.Price
		if incoming.Type == "sell" {
			crosses = value.Price >= incoming.Price
		}
		if value.ID != incoming.ID && open && sameBook && oppositeUserAndSide && crosses {
			values = append(values, value)
		}
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].Price == values[j].Price {
			if values[i].CreatedAt.Equal(values[j].CreatedAt) {
				return values[i].ID < values[j].ID
			}
			return values[i].CreatedAt.Before(values[j].CreatedAt)
		}
		if incoming.Type == "buy" {
			return values[i].Price < values[j].Price
		}
		return values[i].Price > values[j].Price
	})
	return values
}

func updateOrderFillStatus(value *types.Order, filledAt time.Time) {
	switch {
	case storedShareUnits(value.FilledAmount) == 0:
		value.Status = "pending"
	case storedShareUnits(value.FilledAmount) < storedShareUnits(value.Amount):
		value.Status = "partial"
	default:
		value.FilledAmount = fixed.SharesToFloat(storedShareUnits(value.Amount))
		value.Status = "filled"
		value.FilledAt = &filledAt
	}
}

func sortedLevels(
	levels map[bookLevelKey]*market.OrderBookEntry,
	bids bool,
) []market.OrderBookEntry {
	values := make([]market.OrderBookEntry, 0, len(levels))
	for _, value := range levels {
		values = append(values, *value)
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].Option != values[j].Option {
			return values[i].Option < values[j].Option
		}
		if bids {
			return values[i].Price > values[j].Price
		}
		return values[i].Price < values[j].Price
	})
	return values
}

func cloneOrder(value *types.Order) *types.Order {
	if value == nil {
		return nil
	}
	clone := *value
	if value.FilledAt != nil {
		filledAt := *value.FilledAt
		clone.FilledAt = &filledAt
	}
	return &clone
}
