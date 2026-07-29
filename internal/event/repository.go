package event

import (
	"context"
	"time"

	"github.com/afun-game/predictmarket-saas/pkg/types"
)

// Repository stores events independently of the service transport.
type Repository interface {
	Create(ctx context.Context, event *types.Event) error
	UpsertSource(ctx context.Context, event *types.Event) (string, error)
	GetByID(ctx context.Context, eventID string) (*types.Event, error)
	GetBySource(ctx context.Context, sourceType, sourceID string) (*types.Event, error)
	List(ctx context.Context, filters ListFilters) ([]*types.Event, int, error)
	UpdateStatus(
		ctx context.Context,
		eventID string,
		expectedStatus string,
		status string,
		updatedAt time.Time,
	) error
	Resolve(
		ctx context.Context,
		eventID string,
		expectedStatus string,
		outcome string,
		resolutionSource string,
		updatedAt time.Time,
	) error
}
