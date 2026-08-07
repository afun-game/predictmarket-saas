// Package sportsingest projects provider-neutral sports fixtures into
// candidate prediction events and their sports metadata.
package sportsingest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/afun-game/predictmarket-saas/internal/event"
	"github.com/afun-game/predictmarket-saas/internal/sports"
	"github.com/afun-game/predictmarket-saas/internal/sportsingest/provider"
)

// Result summarizes one sports ingestion run.
type Result struct {
	Fetched int
	Synced  int
	Skipped int
}

// eventSink stores an event under an explicit external source identity.
type eventSink interface {
	Sync(ctx context.Context, sourceType string, request *event.SyncRequest) error
}

// sportsMetadataSink stores sports metadata under an explicit source identity.
type sportsMetadataSink interface {
	UpsertSource(
		ctx context.Context,
		sourceType, sourceID string,
		value *sports.SportsEvent,
		syncedAt time.Time,
	) (string, error)
}

// Service ingests fixtures from one source into the event and sports stores.
// It deliberately has no event-resolution or market-settlement dependency.
type Service struct {
	source   provider.Source
	events   eventSink
	metadata sportsMetadataSink
	now      func() time.Time
}

// New creates a sports ingestion service.
func New(source provider.Source, events eventSink, metadata sportsMetadataSink) *Service {
	return &Service{
		source:   source,
		events:   events,
		metadata: metadata,
		now:      time.Now,
	}
}

// Sync ingests fixtures for the start day and each following calendar day up
// to lookaheadDays. The source owns the meaning and timezone of a day.
func (s *Service) Sync(ctx context.Context, startDay time.Time, lookaheadDays int) (Result, error) {
	if s == nil || s.source == nil {
		return Result{}, errors.New("sports fixture source is required")
	}
	if s.events == nil {
		return Result{}, errors.New("external event sink is required")
	}
	if s.metadata == nil {
		return Result{}, errors.New("sports metadata sink is required")
	}
	if lookaheadDays < 0 {
		return Result{}, fmt.Errorf("lookahead days must not be negative: %d", lookaheadDays)
	}

	result := Result{}
	for offset := 0; offset <= lookaheadDays; offset++ {
		if err := ctx.Err(); err != nil {
			return result, err
		}

		day := startDay.AddDate(0, 0, offset)
		fixtures, err := s.source.Fixtures(ctx, day)
		if err != nil {
			return result, fmt.Errorf("fetch fixtures for %s: %w", day.Format("2006-01-02"), err)
		}
		result.Fetched += len(fixtures)

		for _, fixture := range fixtures {
			request, metadata, ok := projectFixture(fixture)
			if !ok {
				result.Skipped++
				continue
			}
			if err := s.events.Sync(ctx, fixture.SourceType, request); err != nil {
				return result, fmt.Errorf("sync source event %q: %w", fixture.SourceID, err)
			}
			result.Synced++

			if fixture.State == provider.StateClosed {
				continue
			}
			if _, err := s.metadata.UpsertSource(
				ctx,
				fixture.SourceType,
				fixture.SourceID,
				metadata,
				s.now().UTC(),
			); err != nil {
				return result, fmt.Errorf("upsert sports metadata for source event %q: %w", fixture.SourceID, err)
			}
		}
	}
	return result, nil
}

func projectFixture(fixture provider.Fixture) (*event.SyncRequest, *sports.SportsEvent, bool) {
	sourceType := strings.ToLower(strings.TrimSpace(fixture.SourceType))
	sourceID := strings.TrimSpace(fixture.SourceID)
	league := strings.ToLower(strings.TrimSpace(fixture.League))
	awayName := strings.TrimSpace(fixture.Away.Name)
	homeName := strings.TrimSpace(fixture.Home.Name)
	if sourceType == "" || sourceID == "" || league == "" || fixture.ScheduledAt.IsZero() || awayName == "" || homeName == "" {
		return nil, nil, false
	}

	var status string
	switch fixture.State {
	case provider.StatePending:
		status = "pending"
	case provider.StateActive:
		status = "active"
	case provider.StateClosed:
		status = "closed"
	default:
		return nil, nil, false
	}

	scheduledAt := fixture.ScheduledAt.UTC()
	formattedTime := scheduledAt.Format(time.RFC3339)
	request := &event.SyncRequest{
		SourceID:       sourceID,
		Title:          fmt.Sprintf("Will %s defeat %s?", awayName, homeName),
		Description:    fmt.Sprintf("Official LMB fixture. Away: %s (%s). Home: %s (%s). LMB game ID: %s.", awayName, strings.TrimSpace(fixture.Away.Abbreviation), homeName, strings.TrimSpace(fixture.Home.Abbreviation), sourceID),
		Category:       "sports",
		EndTime:        formattedTime,
		ResolutionTime: formattedTime,
		Status:         status,
	}
	metadata := &sports.SportsEvent{
		League:    league,
		GameID:    sourceID,
		StartTime: &scheduledAt,
		Teams: []sports.Team{
			{Name: awayName, Abbreviation: strings.TrimSpace(fixture.Away.Abbreviation), Role: "away"},
			{Name: homeName, Abbreviation: strings.TrimSpace(fixture.Home.Abbreviation), Role: "home"},
		},
	}
	return request, metadata, true
}
