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

// Get returns one tenant-scoped user.
func (r *MemoryRepository) Get(ctx context.Context, merchantID, externalUserID string) (User, error) {
	if err := ctx.Err(); err != nil {
		return User{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	key := merchantID + "\x00" + externalUserID
	value, exists := r.users[key]
	if !exists {
		return User{}, ErrUserNotFound
	}
	return value, nil
}

// UpdateStatus changes the user's status.
func (r *MemoryRepository) UpdateStatus(ctx context.Context, merchantID, externalUserID, status string) error {
	if status != "active" && status != "blocked" {
		return ErrInvalidUser
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	key := merchantID + "\x00" + externalUserID
	value, exists := r.users[key]
	if !exists {
		return ErrUserNotFound
	}
	value.Status = status
	r.users[key] = value
	return nil
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
