package eventsync

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/afun-game/predictmarket-saas/internal/event"
	"github.com/afun-game/predictmarket-saas/pkg/polymarket"
)

func TestSyncNormalizesAndWritesEvents(t *testing.T) {
	t.Parallel()

	endDate := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.FixedZone("offset", 8*60*60))
	source := &fakeSource{pages: [][]polymarket.Event{{
		{
			ID:          "event-1",
			Title:       "Election result",
			Description: "Who will win?",
			EndDate:     endDate,
			Active:      true,
			Tags:        []polymarket.Tag{{Slug: "Politics"}},
		},
		{
			ID:       "event-2",
			Title:    "Crypto price",
			Category: "Crypto",
			EndDate:  endDate,
			Active:   true,
		},
		{ID: "invalid", Title: "Missing date"},
	}}}
	sink := &fakeSink{requests: []*event.SyncRequest{}}
	service := newService(source, sink)

	result, err := service.Sync(context.Background())
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if result.Fetched != 3 || result.Synced != 2 || result.Skipped != 1 || result.Pages != 2 {
		t.Errorf("Sync() result = %#v", result)
	}
	if len(sink.requests) != 2 {
		t.Fatalf("synced requests = %d, want 2", len(sink.requests))
	}
	first := sink.requests[0]
	if first.Category != "other" || first.Status != "active" {
		t.Errorf("first request = %#v", first)
	}
	if first.EndTime != "2026-08-10T04:00:00Z" || first.ResolutionTime != first.EndTime {
		t.Errorf("first request timestamps = %q / %q", first.EndTime, first.ResolutionTime)
	}
	if sink.requests[1].Category != "bitcoin" {
		t.Errorf("second request category = %q", sink.requests[1].Category)
	}
	if len(source.options) != 2 {
		t.Fatalf("source calls = %d, want 2", len(source.options))
	}
	options := source.options[0]
	if options.Active == nil || !*options.Active || options.Closed == nil || *options.Closed {
		t.Errorf("source status filters = %#v", options)
	}
	if options.Order != "volume24hr" || options.Ascending == nil || *options.Ascending {
		t.Errorf("source ordering = %#v", options)
	}
}

func TestSyncPropagatesSourceAndSinkErrors(t *testing.T) {
	t.Parallel()

	sourceErr := errors.New("source unavailable")
	service := newService(&fakeSource{err: sourceErr}, &fakeSink{})
	if _, err := service.Sync(context.Background()); !errors.Is(err, sourceErr) {
		t.Errorf("Sync() source error = %v", err)
	}

	sinkErr := errors.New("database unavailable")
	source := &fakeSource{pages: [][]polymarket.Event{{validSourceEvent("event-1")}}}
	service = newService(source, &fakeSink{err: sinkErr})
	if _, err := service.Sync(context.Background()); !errors.Is(err, sinkErr) {
		t.Errorf("Sync() sink error = %v", err)
	}
}

func TestSyncValidatesDependencies(t *testing.T) {
	t.Parallel()

	if _, err := newService(nil, &fakeSink{}).Sync(context.Background()); err == nil {
		t.Fatal("Sync() without source returned no error")
	}
	if _, err := newService(&fakeSource{}, nil).Sync(context.Background()); err == nil {
		t.Fatal("Sync() without sink returned no error")
	}
}

func TestSyncUsesBoundedPagination(t *testing.T) {
	t.Parallel()

	firstPage := make([]polymarket.Event, 0, pageSize)
	secondPage := make([]polymarket.Event, 0, pageSize)
	for index := range pageSize {
		firstPage = append(firstPage, validSourceEvent("first-"+strconv.Itoa(index)))
		secondPage = append(secondPage, validSourceEvent("second-"+strconv.Itoa(index)))
	}
	source := &fakeSource{pages: [][]polymarket.Event{firstPage, secondPage}}
	sink := &fakeSink{requests: []*event.SyncRequest{}}

	result, err := newService(source, sink).Sync(context.Background())
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	usedAllPages := result.Pages == maxSyncPages+1
	syncedAllEvents := result.Synced == pageSize*maxSyncPages
	if !result.Truncated {
		t.Errorf("Sync() result was not marked truncated: %#v", result)
	}
	if !usedAllPages || !syncedAllEvents {
		t.Errorf("Sync() result = %#v", result)
	}
	if len(source.options) != maxSyncPages+1 || source.options[1].Offset != pageSize {
		t.Errorf("pagination options = %#v", source.options)
	}
}

func TestSyncResolvesOnlyUnambiguousClosedBinaryEvents(t *testing.T) {
	t.Parallel()

	closed := validSourceEvent("resolved-event")
	closed.Active = false
	closed.Closed = true
	closed.Markets = []polymarket.Market{{
		Outcomes:      []string{"Yes", "No"},
		OutcomePrices: []float64{1, 0},
	}}
	ambiguous := validSourceEvent("ambiguous-event")
	ambiguous.Active = false
	ambiguous.Closed = true
	ambiguous.Markets = []polymarket.Market{{
		Outcomes:      []string{"Yes", "No"},
		OutcomePrices: []float64{0.5, 0.5},
	}}
	sink := &fakeSink{requests: []*event.SyncRequest{}}
	service := newService(&fakeSource{
		pages:       [][]polymarket.Event{{}},
		closedPages: [][]polymarket.Event{{closed, ambiguous}},
	}, sink)

	result, err := service.Sync(context.Background())
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if result.Resolved != 1 || len(sink.resolutions) != 1 {
		t.Fatalf("Sync() resolutions = %#v, sink = %#v", result, sink.resolutions)
	}
	if got := sink.resolutions["resolved-event"]; got != "Yes" {
		t.Errorf("resolved outcome = %q, want Yes", got)
	}
	if _, exists := sink.resolutions["ambiguous-event"]; exists {
		t.Error("ambiguous closed event was resolved")
	}
}

func TestResolvedOutcomeRejectsConflictingMarkets(t *testing.T) {
	t.Parallel()

	_, ok := resolvedOutcome(polymarket.Event{Markets: []polymarket.Market{
		{Outcomes: []string{"Yes", "No"}, OutcomePrices: []float64{1, 0}},
		{Outcomes: []string{"Yes", "No"}, OutcomePrices: []float64{0, 1}},
	}})
	if ok {
		t.Fatal("resolvedOutcome() accepted conflicting source markets")
	}
}

type fakeSource struct {
	pages       [][]polymarket.Event
	closedPages [][]polymarket.Event
	options     []polymarket.ListEventsOptions
	err         error
}

func (f *fakeSource) ListEvents(
	_ context.Context,
	options polymarket.ListEventsOptions,
) ([]polymarket.Event, error) {
	f.options = append(f.options, options)
	if f.err != nil {
		return nil, f.err
	}
	pages := f.pages
	if options.Closed != nil && *options.Closed {
		pages = f.closedPages
	}
	page := options.Offset / pageSize
	if page >= len(pages) {
		return []polymarket.Event{}, nil
	}
	return pages[page], nil
}

type fakeSink struct {
	requests    []*event.SyncRequest
	resolutions map[string]string
	err         error
}

func (f *fakeSink) ResolveSource(_ context.Context, sourceID string, outcome string) error {
	if f.err != nil {
		return f.err
	}
	if f.resolutions == nil {
		f.resolutions = map[string]string{}
	}
	f.resolutions[sourceID] = outcome
	return nil
}

func (f *fakeSink) SyncSource(_ context.Context, request *event.SyncRequest) error {
	if f.err != nil {
		return f.err
	}
	copy := *request
	f.requests = append(f.requests, &copy)
	return nil
}

func validSourceEvent(id string) polymarket.Event {
	return polymarket.Event{
		ID:      id,
		Title:   "Valid event",
		EndDate: time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC),
		Active:  true,
	}
}
