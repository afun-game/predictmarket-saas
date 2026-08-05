package parimutuel

import (
	"context"
	"sync"
	"time"
)

// MemoryRepository is an in-memory Repository for unit tests.
type MemoryRepository struct {
	mu      sync.Mutex
	pools   map[string]Pool
	bets    map[string]Bet
	markets map[string]memoryMarket
	nextID  int
}

type memoryMarket struct {
	marketType  string
	status      string
	options     []string
	eventStatus string
}

// NewMemoryRepository constructs an empty in-memory repository.
func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		pools:   map[string]Pool{},
		bets:    map[string]Bet{},
		markets: map[string]memoryMarket{},
	}
}

// SeedMarket registers a market so PlaceBet can validate against it.
func (r *MemoryRepository) SeedMarket(marketID, marketType, status, eventStatus string, options []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.markets[marketID] = memoryMarket{
		marketType:  marketType,
		status:      status,
		options:     options,
		eventStatus: eventStatus,
	}
}

func (r *MemoryRepository) CreatePools(ctx context.Context, marketID, currency string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.pools[marketID]; !exists {
		r.pools[marketID] = Pool{MarketID: marketID, Currency: currency}
	}
	return nil
}

func (r *MemoryRepository) LockMarketForBet(
	ctx context.Context,
	marketID string,
) (string, string, []string, string, error) {
	if err := ctx.Err(); err != nil {
		return "", "", nil, "", err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	market, exists := r.markets[marketID]
	if !exists {
		return "", "", nil, "", ErrNotFound
	}
	return market.marketType, market.status, market.options, market.eventStatus, nil
}

func (r *MemoryRepository) PlaceBet(ctx context.Context, bet Bet) (*Bet, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	marketType, marketStatus, options, eventStatus, err := r.LockMarketForBet(ctx, bet.MarketID)
	if err != nil {
		return nil, err
	}
	if marketType != "parimutuel" {
		return nil, ErrNotParimutuel
	}
	if marketStatus != "active" {
		return nil, ErrMarketInactive
	}
	if eventStatus != "active" && eventStatus != "pending" {
		return nil, ErrEventSettled
	}
	valid := false
	for _, option := range options {
		if option == bet.Option {
			valid = true
			break
		}
	}
	if !valid {
		return nil, ErrInvalidOption
	}
	if bet.Stake < 0.01 {
		return nil, ErrInvalidBet
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	pool, exists := r.pools[bet.MarketID]
	if !exists {
		return nil, ErrPoolNotInitialized
	}
	if pool.Currency != bet.Currency {
		return nil, &ValidationError{Field: "currency", Message: "does not match the pool currency"}
	}
	r.nextID++
	bet.ID = "bet-" + string(rune('a'+r.nextID-1))
	bet.Status = StatusActive
	bet.CreatedAt = time.Now().UTC()
	r.bets[bet.ID] = bet
	pool.TotalStake += bet.Stake
	r.pools[bet.MarketID] = pool
	copy := bet
	return &copy, nil
}

func (r *MemoryRepository) ListBets(ctx context.Context, filters ListFilters) ([]Bet, int, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	items := []Bet{}
	for _, bet := range r.bets {
		if filters.MerchantID != "" && bet.MerchantID != filters.MerchantID {
			continue
		}
		if filters.MarketID != "" && bet.MarketID != filters.MarketID {
			continue
		}
		if filters.UserID != "" && bet.UserID != filters.UserID {
			continue
		}
		items = append(items, bet)
	}
	return items, len(items), nil
}

func (r *MemoryRepository) OptionStakes(ctx context.Context, marketID string) ([]OptionStake, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	totals := map[string]float64{}
	for _, bet := range r.bets {
		if bet.MarketID != marketID || bet.Status != StatusActive {
			continue
		}
		totals[bet.Option] += bet.Stake
	}
	items := []OptionStake{}
	for option, stake := range totals {
		items = append(items, OptionStake{Option: option, Stake: stake})
	}
	return items, nil
}

func (r *MemoryRepository) GetPools(ctx context.Context, marketID string) ([]Pool, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	pool, exists := r.pools[marketID]
	if !exists {
		return []Pool{}, nil
	}
	return []Pool{pool}, nil
}
