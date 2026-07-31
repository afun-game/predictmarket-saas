package session

import (
	"context"
	"sync"
	"time"
)

type launchEntry struct {
	value     Launch
	expiresAt time.Time
}

type browserSessionEntry struct {
	value     BrowserSession
	expiresAt time.Time
}

// MemoryStore is a concurrency-safe Store for tests.
type MemoryStore struct {
	mu                    sync.Mutex
	launches              map[string]launchEntry
	launchTokensBySession map[string]string
	sessions              map[string]browserSessionEntry
	nonces                map[string]time.Time
	now                   func() time.Time
}

// NewMemoryStore constructs an empty test store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		launches:              map[string]launchEntry{},
		launchTokensBySession: map[string]string{},
		sessions:              map[string]browserSessionEntry{},
		nonces:                map[string]time.Time{},
		now:                   time.Now,
	}
}

func (s *MemoryStore) CreateLaunch(ctx context.Context, token string, launch Launch, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.launches[token] = launchEntry{value: launch, expiresAt: s.now().UTC().Add(ttl)}
	s.launchTokensBySession[launch.ID] = token
	return nil
}

func (s *MemoryStore) ConsumeLaunch(ctx context.Context, token string) (Launch, error) {
	if err := ctx.Err(); err != nil {
		return Launch{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, exists := s.launches[token]
	if !exists {
		return Launch{}, ErrNotFound
	}
	delete(s.launches, token)
	delete(s.launchTokensBySession, entry.value.ID)
	if !s.now().UTC().Before(entry.expiresAt) {
		return Launch{}, ErrExpired
	}
	return entry.value, nil
}

func (s *MemoryStore) CreateBrowserSession(ctx context.Context, value BrowserSession, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[value.ID] = browserSessionEntry{value: value, expiresAt: s.now().UTC().Add(ttl)}
	return nil
}

func (s *MemoryStore) GetBrowserSession(ctx context.Context, sessionID string) (BrowserSession, error) {
	if err := ctx.Err(); err != nil {
		return BrowserSession{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, exists := s.sessions[sessionID]
	if !exists {
		return BrowserSession{}, ErrNotFound
	}
	if !s.now().UTC().Before(entry.expiresAt) {
		delete(s.sessions, sessionID)
		return BrowserSession{}, ErrExpired
	}
	return entry.value, nil
}

func (s *MemoryStore) RevokeBrowserSession(ctx context.Context, sessionID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, sessionID)
	return nil
}

func (s *MemoryStore) RevokeLaunch(ctx context.Context, merchantID, sessionID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	token, exists := s.launchTokensBySession[sessionID]
	if !exists {
		return ErrNotFound
	}
	entry, exists := s.launches[token]
	if !exists || entry.value.MerchantID != merchantID {
		return ErrNotFound
	}
	if !s.now().UTC().Before(entry.expiresAt) {
		delete(s.launches, token)
		delete(s.launchTokensBySession, sessionID)
		return ErrNotFound
	}
	delete(s.launches, token)
	delete(s.launchTokensBySession, sessionID)
	return nil
}

func (s *MemoryStore) ReserveNonce(ctx context.Context, merchantID, nonce string, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UTC()
	key := merchantID + "\x00" + nonce
	if expiresAt, exists := s.nonces[key]; exists && now.Before(expiresAt) {
		return ErrReplay
	}
	s.nonces[key] = now.Add(ttl)
	return nil
}
