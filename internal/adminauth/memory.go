package adminauth

import (
	"context"
	"sync"
	"time"
)

// MemoryRepository is an in-memory Repository for tests and local runs.
type MemoryRepository struct {
	mu       sync.Mutex
	accounts map[string]*Account
	byName   map[string]string // username -> id
	nextID   int
}

// NewMemoryRepository returns an empty in-memory repository.
func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		accounts: map[string]*Account{},
		byName:   map[string]string{},
	}
}

func (r *MemoryRepository) GetByUsername(ctx context.Context, username string) (*Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	id, ok := r.byName[username]
	if !ok {
		return nil, ErrNotFound
	}
	account := *r.accounts[id]
	return &account, nil
}

func (r *MemoryRepository) GetByID(ctx context.Context, id string) (*Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	account, ok := r.accounts[id]
	if !ok {
		return nil, ErrNotFound
	}
	copy := *account
	return &copy, nil
}

func (r *MemoryRepository) Create(ctx context.Context, account Account) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if account.ID == "" {
		r.nextID++
		account.ID = "admin-" + string(rune('a'+r.nextID-1))
	}
	if _, exists := r.byName[account.Username]; exists {
		return ErrInvalidInput
	}
	account.CreatedAt = time.Now().UTC()
	r.accounts[account.ID] = &account
	r.byName[account.Username] = account.ID
	return nil
}

func (r *MemoryRepository) Count(ctx context.Context) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.accounts), nil
}

func (r *MemoryRepository) TouchLogin(ctx context.Context, id string, success bool, now time.Time) (*time.Time, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	account, ok := r.accounts[id]
	if !ok {
		return nil, ErrNotFound
	}
	var lockedUntil *time.Time
	if success {
		account.FailedAttempts = 0
		account.LockedUntil = nil
		account.LastLoginAt = &now
	} else {
		account.FailedAttempts++
		if account.FailedAttempts >= maxFailedAttempts {
			locked := now.Add(lockoutDuration)
			lockedUntil = &locked
			account.LockedUntil = &locked
		}
	}
	return lockedUntil, nil
}

// MemoryActionLog is an in-memory ActionLogStore for tests.
type MemoryActionLog struct {
	mu      sync.Mutex
	actions []Action
}

// NewMemoryActionLog returns an empty in-memory action log.
func NewMemoryActionLog() *MemoryActionLog {
	return &MemoryActionLog{}
}

func (s *MemoryActionLog) RecordAction(ctx context.Context, action Action) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.actions = append(s.actions, action)
	return nil
}

// Actions returns a snapshot of recorded actions in insertion order.
func (s *MemoryActionLog) Actions() []Action {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Action, len(s.actions))
	copy(out, s.actions)
	return out
}
