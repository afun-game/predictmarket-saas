package market

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/afun-game/predictmarket-saas/pkg/types"
)

type memoryRepository struct {
	mu   sync.RWMutex
	byID map[string]*types.Market
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{byID: map[string]*types.Market{}}
}

func (r *memoryRepository) ValidateReferences(ctx context.Context, _, _ string) (string, error) {
	// The in-memory repository has no events; callers set the category
	// explicitly in tests.
	return "", ctx.Err()
}

func (r *memoryRepository) Create(ctx context.Context, value *types.Market) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.byID[value.ID]; exists {
		return fmt.Errorf("market ID already exists: %s", value.ID)
	}
	r.byID[value.ID] = cloneMarket(value)
	return nil
}

func (r *memoryRepository) GetByID(ctx context.Context, marketID string) (*types.Market, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	value, exists := r.byID[marketID]
	if !exists {
		return nil, ErrNotFound
	}
	return cloneMarket(value), nil
}

func (r *memoryRepository) List(
	ctx context.Context,
	filters ListFilters,
) ([]*types.Market, int, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	values := make([]*types.Market, 0, len(r.byID))
	for _, value := range r.byID {
		matchesMerchant := filters.MerchantID == "" || value.MerchantID == filters.MerchantID
		matchesEvent := filters.EventID == "" || value.EventID == filters.EventID
		matchesCategory := filters.Category == "" || value.Category == filters.Category
		matchesStatus := filters.Status == "" || value.Status == filters.Status
		if matchesMerchant && matchesEvent && matchesCategory && matchesStatus {
			values = append(values, cloneMarket(value))
		}
	}
	sort.Slice(values, func(i, j int) bool {
		if filters.Sort == "popular" && values[i].TotalVolume != values[j].TotalVolume {
			return values[i].TotalVolume > values[j].TotalVolume
		}
		if values[i].CreatedAt.Equal(values[j].CreatedAt) {
			return values[i].ID < values[j].ID
		}
		return values[i].CreatedAt.After(values[j].CreatedAt)
	})

	total := len(values)
	offset := (filters.Page - 1) * filters.Limit
	if offset >= total {
		return []*types.Market{}, total, nil
	}
	end := min(offset+filters.Limit, total)
	return values[offset:end], total, nil
}

func (r *memoryRepository) UpdateStatus(
	ctx context.Context,
	marketID string,
	expectedStatus string,
	status string,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	value, exists := r.byID[marketID]
	if !exists {
		return ErrNotFound
	}
	if value.Status != expectedStatus {
		return ErrInvalidTransition
	}
	value.Status = status
	return nil
}

func (r *memoryRepository) AddLiquidity(
	ctx context.Context,
	marketID string,
	expectedStatus string,
	amount float64,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	value, exists := r.byID[marketID]
	if !exists {
		return ErrNotFound
	}
	if value.Status != expectedStatus {
		return ErrInvalidTransition
	}
	value.LiquidityPool += amount
	return nil
}

func cloneMarket(value *types.Market) *types.Market {
	if value == nil {
		return nil
	}
	clone := *value
	clone.Options = append([]string{}, value.Options...)
	if value.SettledAt != nil {
		settledAt := *value.SettledAt
		clone.SettledAt = &settledAt
	}
	return &clone
}
