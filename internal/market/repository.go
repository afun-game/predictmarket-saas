package market

import (
	"context"

	"github.com/afun-game/predictmarket-saas/pkg/types"
)

// Repository stores markets independently of the service transport.
type Repository interface {
	// ValidateReferences verifies the merchant and event references and
	// returns the event's category so new markets inherit it when they do
	// not set their own.
	ValidateReferences(ctx context.Context, merchantID, eventID string) (eventCategory string, err error)
	Create(ctx context.Context, market *types.Market) error
	GetByID(ctx context.Context, marketID string) (*types.Market, error)
	List(ctx context.Context, filters ListFilters) ([]*types.Market, int, error)
	UpdateStatus(ctx context.Context, marketID, expectedStatus, status string) error
	AddLiquidity(ctx context.Context, marketID, expectedStatus string, amount float64) error
}
