package merchant

import (
	"context"

	"github.com/afun-game/predictmarket-saas/pkg/types"
)

// Repository stores merchants independently of the service transport.
type Repository interface {
	Create(ctx context.Context, merchant *types.Merchant) error
	GetByID(ctx context.Context, merchantID string) (*types.Merchant, error)
	GetByAPIKeyPrefix(ctx context.Context, prefix string) (*types.Merchant, error)
	UpdateAPIKey(ctx context.Context, merchantID, prefix, keyHash string) error
	Update(ctx context.Context, merchant *types.Merchant) error
	List(ctx context.Context, offset, limit int) ([]*types.Merchant, error)
}
