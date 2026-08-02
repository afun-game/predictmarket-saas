// Package parimutuel implements pari-mutuel pool markets: every bet on a
// market joins a shared pool, and at settlement the whole pool is split among
// winning bets in proportion to each bet's stake. The platform never holds
// inventory; its only income is the configured fee (collected at bet time).
package parimutuel

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const (
	// StatusActive bets join the pool and are settleable.
	StatusActive = "active"
	// StatusSettled bets have been paid (or refunded) at settlement.
	StatusSettled = "settled"
	// StatusVoided bets were refunded because the market was voided.
	StatusVoided = "voided"
)

var (
	ErrNotFound           = errors.New("parimutuel market was not found")
	ErrNotParimutuel      = errors.New("market is not a parimutuel market")
	ErrMarketInactive     = errors.New("market is not active")
	ErrEventSettled       = errors.New("event is no longer active")
	ErrInvalidOption      = errors.New("option is not offered by the market")
	ErrInvalidBet         = errors.New("invalid bet")
	ErrPoolNotInitialized = errors.New("parimutuel pool is not initialized")
)

// Bet is one stake in a parimutuel pool.
type Bet struct {
	ID         string     `json:"id"`
	MarketID   string     `json:"market_id"`
	MerchantID string     `json:"merchant_id"`
	UserID     string     `json:"user_id"`
	Option     string     `json:"option"`
	Stake      float64    `json:"stake"`
	Currency   string     `json:"currency"`
	Status     string     `json:"status"`
	CreatedAt  time.Time  `json:"created_at"`
	SettledAt  *time.Time `json:"settled_at,omitempty"`
}

// Pool is one market's stake totals per option.
type Pool struct {
	MarketID   string  `json:"market_id"`
	Currency   string  `json:"currency"`
	TotalStake float64 `json:"total_stake"`
	TotalFees  float64 `json:"total_fees"`
}

// ListFilters scopes bet history for one merchant. MerchantID is required
// by the repository: bets are tenant-scoped like orders.
type ListFilters struct {
	MerchantID string `json:"merchant_id,omitempty"`
	MarketID   string `json:"market_id,omitempty"`
	UserID     string `json:"user_id,omitempty"`
	Page       int    `json:"page,omitempty"`
	Limit      int    `json:"limit,omitempty"`
}

// Service manages parimutuel pools and bets.
type Service interface {
	// CreatePools initializes a market's pool row (idempotent).
	CreatePools(ctx context.Context, marketID, currency string) error
	// PlaceBet records a bet and adds its stake to the pool. The caller is
	// responsible for the wallet debit; PlaceBet fails atomically when the
	// market cannot accept the bet.
	PlaceBet(ctx context.Context, bet Bet) (*Bet, error)
	// ListBets returns paginated bets for one merchant.
	ListBets(ctx context.Context, filters ListFilters) ([]Bet, int, error)
	// GetPools returns the pool totals for one market.
	GetPools(ctx context.Context, marketID string) ([]Pool, error)
}

// ValidationError identifies an invalid bet field.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("invalid %s: %s", e.Field, e.Message)
}

// Repository persists pools and bets.
type Repository interface {
	CreatePools(ctx context.Context, marketID, currency string) error
	PlaceBet(ctx context.Context, bet Bet) (*Bet, error)
	ListBets(ctx context.Context, filters ListFilters) ([]Bet, int, error)
	GetPools(ctx context.Context, marketID string) ([]Pool, error)
	// LockMarketForBet returns the market's type, status, options, and event
	// status while holding the market row lock.
	LockMarketForBet(ctx context.Context, marketID string) (marketType string, status string, options []string, eventStatus string, err error)
}
