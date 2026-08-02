package parimutuel

import (
	"context"
	"fmt"
	"strings"
)

type implementation struct {
	repository Repository
}

// NewService creates a Service backed by an in-memory repository.
func NewService() Service {
	return newService(NewMemoryRepository())
}

// NewServiceWithRepository creates a Service over the given repository.
func NewServiceWithRepository(repository Repository) Service {
	return newService(repository)
}

func newService(repository Repository) *implementation {
	return &implementation{repository: repository}
}

func (s *implementation) CreatePools(ctx context.Context, marketID, currency string) error {
	marketID = strings.TrimSpace(marketID)
	if marketID == "" {
		return ErrInvalidBet
	}
	if err := s.repository.CreatePools(ctx, marketID, currency); err != nil {
		return fmt.Errorf("create parimutuel pools: %w", err)
	}
	return nil
}

func (s *implementation) PlaceBet(ctx context.Context, bet Bet) (*Bet, error) {
	bet.MarketID = strings.TrimSpace(bet.MarketID)
	bet.MerchantID = strings.TrimSpace(bet.MerchantID)
	bet.UserID = strings.TrimSpace(bet.UserID)
	if bet.MarketID == "" || bet.MerchantID == "" || bet.UserID == "" || len(bet.UserID) > 255 {
		return nil, ErrInvalidBet
	}
	placed, err := s.repository.PlaceBet(ctx, bet)
	if err != nil {
		return nil, err
	}
	return placed, nil
}

func (s *implementation) ListBets(ctx context.Context, filters ListFilters) ([]Bet, int, error) {
	return s.repository.ListBets(ctx, filters)
}

func (s *implementation) GetPools(ctx context.Context, marketID string) ([]Pool, error) {
	return s.repository.GetPools(ctx, strings.TrimSpace(marketID))
}
