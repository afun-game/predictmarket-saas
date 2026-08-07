// Package provider normalizes upstream sports schedules into fixtures used by
// the sports ingestion service.
package provider

import (
	"context"
	"time"
)

// State describes the lifecycle of a fixture as reported by its source.
type State string

const (
	StatePending State = "pending"
	StateActive  State = "active"
	StateClosed  State = "closed"
)

// Team is a participant in a fixture.
type Team struct {
	Name         string
	Abbreviation string
	Role         string
}

// Fixture is the provider-neutral data required to create or update a sports
// prediction event. ScheduledAt is always the UTC instant reported by the
// provider; market presentation chooses its own business timezone.
type Fixture struct {
	SourceType  string
	SourceID    string
	League      string
	ScheduledAt time.Time
	State       State
	Away        Team
	Home        Team
}

// Source retrieves provider-neutral fixtures for one source calendar day.
type Source interface {
	Fixtures(ctx context.Context, day time.Time) ([]Fixture, error)
}
