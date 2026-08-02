package marketmaker

import (
	"context"
	"sync"
)

// MemoryRepository is an in-memory Repository for unit tests.
type MemoryRepository struct {
	mu         sync.Mutex
	committed  map[string]float64
}

// NewMemoryRepository constructs an empty in-memory repository.
func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{committed: map[string]float64{}}
}

func (r *MemoryRepository) GetCommitted(ctx context.Context, marketID string) (float64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	value, exists := r.committed[marketID]
	if !exists {
		return 0, ErrNotFound
	}
	return value, nil
}

func (r *MemoryRepository) SetCommitted(ctx context.Context, marketID string, committed float64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.committed[marketID] = committed
	return nil
}
