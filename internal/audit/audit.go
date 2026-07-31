// Package audit records state-changing merchant-facing requests for
// operational review (V3 §7.3).
package audit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Event is one audited merchant API change request.
type Event struct {
	MerchantID     string
	Method         string
	Path           string
	RequestID      string
	IdempotencyKey string
	ClientIP       string
	StatusCode     int
}

// Store persists audit events. Implementations must never fail the original
// request; callers invoke Record with a best-effort context.
type Store interface {
	Record(ctx context.Context, event Event) error
}

// ErrStoreClosed indicates the audit store has been shut down.
var ErrStoreClosed = errors.New("audit store is closed")

// NewPostgresStore returns a Store writing to merchant_api_audits.
func NewPostgresStore(database *sql.DB) Store {
	return &postgresStore{database: database}
}

type postgresStore struct {
	database *sql.DB
}

func (s *postgresStore) Record(ctx context.Context, event Event) error {
	if s == nil || s.database == nil {
		return errors.New("audit database is not configured")
	}
	const query = `
INSERT INTO merchant_api_audits (
    merchant_id, method, path, request_id, idempotency_key, client_ip, status_code, created_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
	_, err := s.database.ExecContext(
		ctx,
		query,
		event.MerchantID,
		event.Method,
		event.Path,
		event.RequestID,
		event.IdempotencyKey,
		event.ClientIP,
		event.StatusCode,
		time.Now().UTC(),
	)
	if err != nil {
		return fmt.Errorf("record merchant API audit: %w", err)
	}
	return nil
}

// NewMemoryStore returns an in-memory Store for tests and local runs.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{}
}

// MemoryStore is a thread-safe in-memory audit sink.
type MemoryStore struct {
	mu     sync.Mutex
	events []Event
}

// Record appends an event.
func (s *MemoryStore) Record(_ context.Context, event Event) error {
	if s == nil {
		return ErrStoreClosed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	return nil
}

// Events returns a snapshot of recorded events in insertion order.
func (s *MemoryStore) Events() []Event {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Event, len(s.events))
	copy(out, s.events)
	return out
}
