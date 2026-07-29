package settlementmonitor

import (
	"context"
	"time"
)

// Repository reads settlement conditions that require operator intervention.
type Repository interface {
	OverdueEvents(ctx context.Context, cutoff time.Time) ([]OverdueEvent, error)
}

// OverdueEvent identifies an unresolved source event with unsettled markets.
type OverdueEvent struct {
	ID             string
	SourceID       string
	Status         string
	ResolutionTime time.Time
}
