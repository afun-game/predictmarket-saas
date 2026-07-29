package merchant

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/afun-game/predictmarket-saas/pkg/types"
)

type memoryRepository struct {
	mu            sync.RWMutex
	byID          map[string]*types.Merchant
	idByKeyPrefix map[string]string
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{
		byID:          map[string]*types.Merchant{},
		idByKeyPrefix: map[string]string{},
	}
}

func (r *memoryRepository) Create(ctx context.Context, merchant *types.Merchant) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.byID[merchant.ID]; exists {
		return fmt.Errorf("merchant ID already exists: %s", merchant.ID)
	}
	if _, exists := r.idByKeyPrefix[merchant.APIKeyPrefix]; exists {
		return fmt.Errorf("API key already exists")
	}
	r.byID[merchant.ID] = cloneMerchant(merchant)
	r.idByKeyPrefix[merchant.APIKeyPrefix] = merchant.ID
	return nil
}

func (r *memoryRepository) GetByID(
	ctx context.Context,
	merchantID string,
) (*types.Merchant, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	merchant, exists := r.byID[merchantID]
	if !exists {
		return nil, ErrNotFound
	}
	return cloneMerchant(merchant), nil
}

func (r *memoryRepository) GetByAPIKeyPrefix(
	ctx context.Context,
	prefix string,
) (*types.Merchant, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	merchantID, exists := r.idByKeyPrefix[prefix]
	if !exists {
		return nil, ErrNotFound
	}
	return cloneMerchant(r.byID[merchantID]), nil
}

func (r *memoryRepository) UpdateAPIKey(
	ctx context.Context,
	merchantID string,
	prefix string,
	keyHash string,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	merchant, exists := r.byID[merchantID]
	if !exists {
		return ErrNotFound
	}
	if current, exists := r.idByKeyPrefix[prefix]; exists && current != merchantID {
		return fmt.Errorf("API key prefix already exists")
	}
	delete(r.idByKeyPrefix, merchant.APIKeyPrefix)
	merchant.APIKeyPrefix = prefix
	merchant.APIKey = keyHash
	r.idByKeyPrefix[prefix] = merchantID
	return nil
}

func (r *memoryRepository) Update(ctx context.Context, merchant *types.Merchant) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	previous, exists := r.byID[merchant.ID]
	if !exists {
		return ErrNotFound
	}
	if previous.APIKeyPrefix != merchant.APIKeyPrefix {
		if _, exists := r.idByKeyPrefix[merchant.APIKeyPrefix]; exists {
			return fmt.Errorf("API key already exists")
		}
		delete(r.idByKeyPrefix, previous.APIKeyPrefix)
		r.idByKeyPrefix[merchant.APIKeyPrefix] = merchant.ID
	}
	r.byID[merchant.ID] = cloneMerchant(merchant)
	return nil
}

func (r *memoryRepository) List(
	ctx context.Context,
	offset int,
	limit int,
) ([]*types.Merchant, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	merchants := make([]*types.Merchant, 0, len(r.byID))
	for _, merchant := range r.byID {
		merchants = append(merchants, cloneMerchant(merchant))
	}
	sort.Slice(merchants, func(i, j int) bool {
		if merchants[i].CreatedAt.Equal(merchants[j].CreatedAt) {
			return merchants[i].ID < merchants[j].ID
		}
		return merchants[i].CreatedAt.Before(merchants[j].CreatedAt)
	})

	if offset >= len(merchants) {
		return []*types.Merchant{}, nil
	}
	end := min(offset+limit, len(merchants))
	return merchants[offset:end], nil
}

func cloneMerchant(merchant *types.Merchant) *types.Merchant {
	if merchant == nil {
		return nil
	}
	clone := *merchant
	return &clone
}
