package sports

import (
	"context"
	"time"
)

// Repository stores sports metadata linked to common events.
type Repository interface {
	UpsertSource(ctx context.Context, sourceType, sourceID string, value *SportsEvent, syncedAt time.Time) (string, error)
	GetByID(ctx context.Context, eventID string) (*SportsEvent, error)
	List(ctx context.Context, filters EventFilters) ([]*SportsEvent, int, error)
}
