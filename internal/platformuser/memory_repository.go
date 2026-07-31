package platformuser

import (
	"context"
	"sync"
)

// MemoryRepository is an in-memory Repository for unit tests.
type MemoryRepository struct {
	mu    sync.Mutex
	users map[string]User
}

// NewMemoryRepository constructs an empty in-memory repository.
func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{users: map[string]User{}}
}

// Upsert stores a normalized user by its tenant-safe composite key.
func (r *MemoryRepository) Upsert(ctx context.Context, user User) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	value, err := normalize(user)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	key := value.MerchantID + "\x00" + value.ExternalUserID
	if existing, exists := r.users[key]; exists {
		value.CreatedAt = existing.CreatedAt
	}
	r.users[key] = value
	return nil
}
