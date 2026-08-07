package sportsingest

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/afun-game/predictmarket-saas/internal/event"
	"github.com/afun-game/predictmarket-saas/internal/sports"
	"github.com/afun-game/predictmarket-saas/internal/sportsingest/provider"
)

func TestServiceSyncProjectsPendingFixtureIntoEventAndSportsMetadata(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.August, 8, 1, 30, 0, 0, time.FixedZone("America/Mexico_City", -6*60*60))
	fixture := pendingFixture(start)
	source := &fakeSource{fixturesByDay: map[string][]provider.Fixture{
		dayKey(start): {fixture},
	}}
	events := &fakeEventSink{}
	metadata := &fakeSportsSink{}

	result, err := New(source, events, metadata).Sync(context.Background(), start, 0)
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}

	if result != (Result{Fetched: 1, Synced: 1}) {
		t.Errorf("Sync() result = %#v, want %#v", result, Result{Fetched: 1, Synced: 1})
	}
	if got, want := events.calls, []eventCall{{
		sourceType: "lmb",
		request: &event.SyncRequest{
			SourceID:       "846703",
			Title:          "Will Saraperos defeat Algodoneros?",
			Description:    "Official LMB fixture. Away: Saraperos (SLW). Home: Algodoneros (LAG). LMB game ID: 846703.",
			Category:       "sports",
			EndTime:        fixture.ScheduledAt.UTC().Format(time.RFC3339),
			ResolutionTime: fixture.ScheduledAt.UTC().Format(time.RFC3339),
			Status:         "pending",
		},
	}}; !reflect.DeepEqual(got, want) {
		t.Errorf("event calls = %#v, want %#v", got, want)
	}

	if len(metadata.calls) != 1 {
		t.Fatalf("sports metadata calls = %d, want 1", len(metadata.calls))
	}
	call := metadata.calls[0]
	if call.sourceType != "lmb" || call.sourceID != "846703" {
		t.Errorf("metadata source = (%q, %q), want (lmb, 846703)", call.sourceType, call.sourceID)
	}
	if call.value.League != "lmb" || call.value.GameID != "846703" {
		t.Errorf("metadata identity = %#v, want lmb/846703", call.value)
	}
	if call.value.StartTime == nil || !call.value.StartTime.Equal(fixture.ScheduledAt.UTC()) {
		t.Errorf("metadata start time = %v, want %v", call.value.StartTime, fixture.ScheduledAt.UTC())
	}
	if got, want := call.value.Teams, []sports.Team{
		{Name: "Saraperos", Abbreviation: "SLW", Role: "away"},
		{Name: "Algodoneros", Abbreviation: "LAG", Role: "home"},
	}; !reflect.DeepEqual(got, want) {
		t.Errorf("metadata teams = %#v, want %#v", got, want)
	}
}

func TestServiceSyncQueriesConfiguredDaysInclusively(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.August, 7, 0, 0, 0, 0, time.FixedZone("America/Mexico_City", -6*60*60))
	source := &fakeSource{fixturesByDay: map[string][]provider.Fixture{}}

	result, err := New(source, &fakeEventSink{}, &fakeSportsSink{}).Sync(context.Background(), start, 2)
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if result != (Result{}) {
		t.Errorf("Sync() result = %#v, want empty result", result)
	}

	wantDays := []time.Time{start, start.AddDate(0, 0, 1), start.AddDate(0, 0, 2)}
	if !reflect.DeepEqual(source.days, wantDays) {
		t.Errorf("source days = %#v, want %#v", source.days, wantDays)
	}
}

func TestServiceSyncClosedFixtureOnlySynchronizesClosedEvent(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.August, 8, 1, 30, 0, 0, time.UTC)
	fixture := pendingFixture(start)
	fixture.State = provider.StateClosed
	source := &fakeSource{fixturesByDay: map[string][]provider.Fixture{
		dayKey(start): {fixture},
	}}
	events := &fakeEventSink{}
	metadata := &fakeSportsSink{}

	result, err := New(source, events, metadata).Sync(context.Background(), start, 0)
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if result != (Result{Fetched: 1, Synced: 1}) {
		t.Errorf("Sync() result = %#v, want %#v", result, Result{Fetched: 1, Synced: 1})
	}
	if len(events.calls) != 1 || events.calls[0].request.Status != "closed" {
		t.Errorf("event calls = %#v, want one closed event sync", events.calls)
	}
	if len(metadata.calls) != 0 {
		t.Errorf("sports metadata calls = %#v, want none for closed fixture", metadata.calls)
	}
}

func TestServiceSyncSkipsInvalidFixtureWithoutWriting(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.August, 8, 1, 30, 0, 0, time.UTC)
	fixture := pendingFixture(start)
	fixture.SourceID = ""
	source := &fakeSource{fixturesByDay: map[string][]provider.Fixture{
		dayKey(start): {fixture},
	}}
	events := &fakeEventSink{}
	metadata := &fakeSportsSink{}

	result, err := New(source, events, metadata).Sync(context.Background(), start, 0)
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if result != (Result{Fetched: 1, Skipped: 1}) {
		t.Errorf("Sync() result = %#v, want %#v", result, Result{Fetched: 1, Skipped: 1})
	}
	if len(events.calls) != 0 || len(metadata.calls) != 0 {
		t.Errorf("writes = events %#v, metadata %#v; want none", events.calls, metadata.calls)
	}
}

func TestServiceSyncWrapsSourceAndSinkErrors(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.August, 8, 1, 30, 0, 0, time.UTC)
	fixture := pendingFixture(start)
	boom := errors.New("unavailable")

	for name, test := range map[string]struct {
		source   *fakeSource
		events   *fakeEventSink
		metadata *fakeSportsSink
		context  string
	}{
		"source": {
			source:   &fakeSource{err: boom},
			events:   &fakeEventSink{},
			metadata: &fakeSportsSink{},
			context:  "fetch fixtures",
		},
		"event sink": {
			source: &fakeSource{fixturesByDay: map[string][]provider.Fixture{
				dayKey(start): {fixture},
			}},
			events:   &fakeEventSink{err: boom},
			metadata: &fakeSportsSink{},
			context:  "sync source event",
		},
		"metadata sink": {
			source: &fakeSource{fixturesByDay: map[string][]provider.Fixture{
				dayKey(start): {fixture},
			}},
			events:   &fakeEventSink{},
			metadata: &fakeSportsSink{err: boom},
			context:  "upsert sports metadata",
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := New(test.source, test.events, test.metadata).Sync(context.Background(), start, 0)
			if !errors.Is(err, boom) {
				t.Fatalf("Sync() error = %v, want wrapped %v", err, boom)
			}
			if !strings.Contains(err.Error(), test.context) {
				t.Errorf("Sync() error = %q, want %q context", err, test.context)
			}
		})
	}
}

func pendingFixture(start time.Time) provider.Fixture {
	return provider.Fixture{
		SourceType:  "lmb",
		SourceID:    "846703",
		League:      "lmb",
		ScheduledAt: start,
		State:       provider.StatePending,
		Away: provider.Team{
			Name:         "Saraperos",
			Abbreviation: "SLW",
			Role:         "away",
		},
		Home: provider.Team{
			Name:         "Algodoneros",
			Abbreviation: "LAG",
			Role:         "home",
		},
	}
}

func dayKey(day time.Time) string {
	return day.Format(time.RFC3339Nano)
}

type fakeSource struct {
	fixturesByDay map[string][]provider.Fixture
	err           error
	days          []time.Time
}

func (s *fakeSource) Fixtures(_ context.Context, day time.Time) ([]provider.Fixture, error) {
	s.days = append(s.days, day)
	if s.err != nil {
		return nil, s.err
	}
	return s.fixturesByDay[dayKey(day)], nil
}

type eventCall struct {
	sourceType string
	request    *event.SyncRequest
}

type fakeEventSink struct {
	calls []eventCall
	err   error
}

func (s *fakeEventSink) Sync(_ context.Context, sourceType string, request *event.SyncRequest) error {
	if s.err != nil {
		return s.err
	}
	copy := *request
	s.calls = append(s.calls, eventCall{sourceType: sourceType, request: &copy})
	return nil
}

type sportsCall struct {
	sourceType string
	sourceID   string
	value      *sports.SportsEvent
	syncedAt   time.Time
}

type fakeSportsSink struct {
	calls []sportsCall
	err   error
}

func (s *fakeSportsSink) UpsertSource(
	_ context.Context,
	sourceType, sourceID string,
	value *sports.SportsEvent,
	syncedAt time.Time,
) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	s.calls = append(s.calls, sportsCall{
		sourceType: sourceType,
		sourceID:   sourceID,
		value:      cloneSportsEvent(value),
		syncedAt:   syncedAt,
	})
	return sourceType + ":" + sourceID, nil
}

func cloneSportsEvent(value *sports.SportsEvent) *sports.SportsEvent {
	copy := *value
	if value.StartTime != nil {
		start := *value.StartTime
		copy.StartTime = &start
	}
	copy.Teams = append([]sports.Team(nil), value.Teams...)
	return &copy
}
