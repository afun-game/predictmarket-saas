package market

import (
	"context"

	"github.com/afun-game/predictmarket-saas/pkg/types"
)

// Repository stores markets independently of the service transport.
type Repository interface {
	ValidateReferences(ctx context.Context, merchantID, eventID string) error
	Create(ctx context.Context, market *types.Market) error
	GetByID(ctx context.Context, marketID string) (*types.Market, error)
	List(ctx context.Context, filters ListFilters) ([]*types.Market, int, error)
	UpdateStatus(ctx context.Context, marketID, expectedStatus, status string) error
	AddLiquidity(ctx context.Context, marketID, expectedStatus string, amount float64) error
}
