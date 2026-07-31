package order

import (
	"context"
	"time"

	"github.com/afun-game/predictmarket-saas/internal/market"
	"github.com/afun-game/predictmarket-saas/pkg/types"
)

// Repository stores and matches orders.
type Repository interface {
	Place(ctx context.Context, order *types.Order) (float64, error)
	Get(ctx context.Context, orderID string) (*types.Order, error)
	GetByIdempotency(ctx context.Context, merchantID, key string) (*types.Order, error)
	List(ctx context.Context, filters ListFilters) ([]*types.Order, int, error)
	ListAfter(ctx context.Context, filters ListFilters, cursor *Cursor) ([]*types.Order, error)
	ListTrades(ctx context.Context, filters TradeListFilters, cursor *Cursor) ([]*types.Trade, error)
	Cancel(ctx context.Context, orderID string) (*types.Order, float64, error)
	GetOrderBook(ctx context.Context, marketID string) (*market.OrderBook, error)
}

// Cursor is the stable keyset boundary for newest-first order listings.
type Cursor struct {
	CreatedAt time.Time
	ID        string
}

// atomicPlacementRepository keeps the initial collateral lock, matching, and
// any immediate release in one database transaction.
type atomicPlacementRepository interface {
	PlaceWithLockedCollateral(ctx context.Context, order *types.Order, collateralCents int64) error
}

// atomicCancellationRepository keeps cancellation and its collateral release
// in one database transaction.
type atomicCancellationRepository interface {
	CancelWithUnlock(ctx context.Context, orderID string) error
}
