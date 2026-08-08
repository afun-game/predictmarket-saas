// Package eventsync imports Polymarket events into the local event service.
package eventsync

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/afun-game/predictmarket-saas/internal/event"
	"github.com/afun-game/predictmarket-saas/pkg/polymarket"
	"github.com/nxsky/twill"
)

const (
	defaultSchedule = "@every 5m"
	pageSize        = 50
	maxSyncPages    = 2
	syncJobName     = "polymarket-events"
)

// Service synchronizes active Polymarket events into the local event store.
type Service interface {
	Sync(ctx context.Context) (*Result, error)
}

// Result summarizes a synchronization run.
type Result struct {
	twill.AutoMarshal

	Fetched   int  `json:"fetched"`
	Synced    int  `json:"synced"`
	Resolved  int  `json:"resolved"`
	Skipped   int  `json:"skipped"`
	Pages     int  `json:"pages"`
	Truncated bool `json:"truncated"`
}

type sourceClient interface {
	ListEvents(
		ctx context.Context,
		options polymarket.ListEventsOptions,
	) ([]polymarket.Event, error)
}

type eventSink interface {
	SyncSource(ctx context.Context, request *event.SyncRequest) error
	ResolveSource(ctx context.Context, sourceID string, outcome string) error
}

type implementation struct {
	twill.Implements[Service]

	events twill.Ref[event.Service]
	cron   twill.Cron `twill:"polymarket-sync"`

	source   sourceClient
	sink     eventSink
	schedule string
}

// NewService creates a Polymarket event synchronization service.
func NewService() Service {
	return &implementation{}
}

func newService(source sourceClient, sink eventSink) *implementation {
	return &implementation{
		source: source,
		sink:   sink,
	}
}

func (s *implementation) Init(ctx context.Context) error {
	if s.source == nil {
		options := []polymarket.Option{}
		if baseURL := strings.TrimSpace(os.Getenv("POLYMARKET_API_URL")); baseURL != "" {
			options = append(options, polymarket.WithBaseURL(baseURL))
		}
		s.source = polymarket.NewClient(options...)
	}
	if s.sink == nil {
		s.sink = s.events.Get()
	}
	if s.schedule == "" {
		s.schedule = strings.TrimSpace(os.Getenv("POLYMARKET_SYNC_INTERVAL"))
	}
	if s.schedule == "" {
		s.schedule = defaultSchedule
	}

	scheduler := s.cron.Get()
	if scheduler == nil {
		return errors.New("Polymarket sync cron is not configured")
	}
	if err := scheduler.Add(ctx, syncJobName, s.schedule, func(jobCtx context.Context) {
		result, err := s.Sync(jobCtx)
		if err != nil {
			slog.ErrorContext(jobCtx, "Polymarket event sync failed", "error", err)
			return
		}
		slog.InfoContext(
			jobCtx,
			"Polymarket event sync completed",
			"fetched",
			result.Fetched,
			"synced",
			result.Synced,
			"resolved",
			result.Resolved,
			"skipped",
			result.Skipped,
			"truncated",
			result.Truncated,
		)
	}); err != nil {
		return fmt.Errorf("register Polymarket sync job: %w", err)
	}
	return nil
}

// Sync imports both open and closed Polymarket events within the bounded
// pagination window. Only a unique, fully-settled binary outcome can resolve
// a local event automatically; ambiguous source data remains closed.
func (s *implementation) Sync(ctx context.Context) (*Result, error) {
	if s.source == nil {
		return nil, errors.New("Polymarket source is not configured")
	}
	if s.sink == nil {
		return nil, errors.New("event sink is not configured")
	}

	result := &Result{}
	if err := s.syncStatus(ctx, true, false, result); err != nil {
		return result, err
	}
	if err := s.syncStatus(ctx, false, true, result); err != nil {
		return result, err
	}
	return result, nil
}

func (s *implementation) syncStatus(
	ctx context.Context,
	active bool,
	closed bool,
	result *Result,
) error {
	ascending := false
	for page := 0; page < maxSyncPages; page++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		events, err := s.source.ListEvents(ctx, polymarket.ListEventsOptions{
			Active:    &active,
			Closed:    &closed,
			Order:     "volume24hr",
			Ascending: &ascending,
			Limit:     pageSize,
			Offset:    page * pageSize,
		})
		if err != nil {
			return fmt.Errorf("fetch Polymarket %s event page %d: %w", sourceStatusName(closed), page+1, err)
		}
		result.Pages++
		result.Fetched += len(events)

		for _, sourceEvent := range events {
			request, ok := normalizeSourceEvent(sourceEvent)
			if !ok {
				result.Skipped++
				continue
			}
			if err := s.sink.SyncSource(ctx, request); err != nil {
				return fmt.Errorf("sync Polymarket event %q: %w", sourceEvent.ID, err)
			}
			result.Synced++
			if !closed {
				continue
			}
			outcome, resolved := resolvedOutcome(sourceEvent)
			if !resolved {
				continue
			}
			if err := s.sink.ResolveSource(ctx, sourceEvent.ID, outcome); err != nil {
				return fmt.Errorf("resolve Polymarket event %q: %w", sourceEvent.ID, err)
			}
			result.Resolved++
		}
		if len(events) < pageSize {
			return nil
		}
	}
	result.Truncated = true
	return nil
}

func sourceStatusName(closed bool) string {
	if closed {
		return "closed"
	}
	return "open"
}

func resolvedOutcome(source polymarket.Event) (string, bool) {
	var outcome string
	for _, market := range source.Markets {
		candidate, ok := resolvedMarketOutcome(market)
		if !ok {
			continue
		}
		if outcome == "" {
			outcome = candidate
			continue
		}
		if outcome != candidate {
			return "", false
		}
	}
	return outcome, outcome != ""
}

func resolvedMarketOutcome(source polymarket.Market) (string, bool) {
	if len(source.Outcomes) != 2 || len(source.OutcomePrices) != 2 {
		return "", false
	}
	winner := -1
	for index, price := range source.OutcomePrices {
		if price == 1 {
			if winner != -1 {
				return "", false
			}
			winner = index
			continue
		}
		if price != 0 {
			return "", false
		}
	}
	if winner == -1 {
		return "", false
	}
	outcome := strings.TrimSpace(source.Outcomes[winner])
	return outcome, outcome != ""
}

func normalizeSourceEvent(source polymarket.Event) (*event.SyncRequest, bool) {
	source.ID = strings.TrimSpace(source.ID)
	source.Title = strings.TrimSpace(source.Title)
	hasIdentity := source.ID != "" && source.Title != ""
	if !hasIdentity || source.EndDate.IsZero() {
		return nil, false
	}

	status := "pending"
	switch {
	case source.Closed:
		status = "closed"
	case source.Active:
		status = "active"
	}
	endTime := source.EndDate.UTC().Format(time.RFC3339)
	return &event.SyncRequest{
		SourceID:       source.ID,
		Title:          source.Title,
		Description:    source.Description,
		Category:       sourceCategory(source),
		EndTime:        endTime,
		ResolutionTime: endTime,
		Status:         status,
	}, true
}

func sourceCategory(source polymarket.Event) string {
	if category := strings.ToLower(strings.TrimSpace(source.Category)); category != "" {
		return event.NormalizeCategory(category)
	}
	for _, tag := range source.Tags {
		if slug := strings.ToLower(strings.TrimSpace(tag.Slug)); slug != "" {
			return event.NormalizeCategory(slug)
		}
	}
	return event.CategoryOther
}
