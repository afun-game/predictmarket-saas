package sports

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/afun-game/predictmarket-saas/internal/event"
	"github.com/afun-game/predictmarket-saas/pkg/polymarket"
)

func TestNormalizeSourceEventUsesStructuredSportsFields(t *testing.T) {
	start := time.Date(2026, 7, 28, 23, 30, 0, 0, time.UTC)
	request, metadata, ok := normalizeSourceEvent(" WNBA ", polymarket.Event{
		ID: "705118", Title: "Connecticut Sun vs. Washington Mystics", StartTime: start,
		GameID: 13002430, Active: true,
		Teams: []polymarket.Team{
			{Name: "Connecticut Sun", Abbreviation: "CONN", Ordering: "away"},
			{Name: "Washington Mystics", Abbreviation: "WSH", Ordering: "home"},
		},
	})
	if !ok {
		t.Fatal("normalizeSourceEvent() rejected valid event")
	}
	if request.Category != "sports" || request.Status != "active" || request.EndTime != start.Format(time.RFC3339) {
		t.Errorf("request = %#v", request)
	}
	if metadata.League != "wnba" || metadata.GameID != "13002430" || len(metadata.Teams) != 2 {
		t.Fatalf("metadata = %#v", metadata)
	}
	if metadata.Teams[0].Role != "away" || metadata.Teams[1].Role != "home" {
		t.Errorf("team roles = %#v", metadata.Teams)
	}
}

func TestSportsSyncFiltersCatalogAndPropagatesErrors(t *testing.T) {
	source := &stubSportsSource{
		catalog: []polymarket.Sport{
			{Sport: "nba", SeriesID: "100"}, {Sport: "cricket", SeriesID: "200"},
		},
		events: []polymarket.Event{{ID: "game-1", Title: "A vs B", EndDate: time.Now().Add(time.Hour), Active: true}},
	}
	sink := &stubEventSink{}
	repository := &captureRepository{}
	service := newService(repository)
	service.source, service.sink = source, sink
	service.leagues = configuredLeagues("nba")
	if err := service.SyncFromPolymarket(context.Background()); err != nil {
		t.Fatalf("SyncFromPolymarket() error = %v", err)
	}
	if len(source.options) != 1 || source.options[0].SeriesID != "100" {
		t.Errorf("options = %#v", source.options)
	}
	if len(sink.requests) != 1 || len(repository.sources) != 1 {
		t.Errorf("sink calls = %d, repository calls = %d", len(sink.requests), len(repository.sources))
	}

	sink.err = errors.New("sink unavailable")
	if err := service.SyncFromPolymarket(context.Background()); err == nil {
		t.Fatal("SyncFromPolymarket() swallowed sink error")
	}
}

func TestNormalizeSportsFilters(t *testing.T) {
	filters, err := normalizeFilters(&EventFilters{League: " NBA ", Team: " Sun ", Status: "ACTIVE"})
	if err != nil {
		t.Fatalf("normalizeFilters() error = %v", err)
	}
	if filters.League != "nba" || filters.Team != "Sun" || filters.Page != 1 || filters.Limit != 20 {
		t.Errorf("filters = %#v", filters)
	}
	if _, err := normalizeFilters(&EventFilters{Limit: 101}); err == nil {
		t.Fatal("excessive limit accepted")
	}
	if _, err := normalizeFilters(&EventFilters{Status: "unknown"}); err == nil {
		t.Fatal("unknown status accepted")
	}
}

type stubSportsSource struct {
	catalog []polymarket.Sport
	events  []polymarket.Event
	options []polymarket.ListEventsOptions
	err     error
}

func (s *stubSportsSource) ListSports(context.Context) ([]polymarket.Sport, error) {
	return s.catalog, s.err
}
func (s *stubSportsSource) ListEvents(_ context.Context, options polymarket.ListEventsOptions) ([]polymarket.Event, error) {
	s.options = append(s.options, options)
	return s.events, s.err
}

type stubEventSink struct {
	requests []*event.SyncRequest
	err      error
}

func (s *stubEventSink) SyncSource(_ context.Context, request *event.SyncRequest) error {
	s.requests = append(s.requests, request)
	return s.err
}

type captureRepository struct {
	sources []string
	err     error
}

func (r *captureRepository) UpsertSource(_ context.Context, sourceID string, _ *SportsEvent, _ time.Time) (string, error) {
	r.sources = append(r.sources, sourceID)
	return sourceID, r.err
}
func (*captureRepository) GetByID(context.Context, string) (*SportsEvent, error) {
	return nil, ErrNotFound
}
func (*captureRepository) List(context.Context, EventFilters) ([]*SportsEvent, int, error) {
	return []*SportsEvent{}, 0, nil
}
