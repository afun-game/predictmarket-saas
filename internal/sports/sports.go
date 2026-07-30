// Package sports synchronizes and exposes structured Polymarket sports events.
package sports

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/afun-game/predictmarket-saas/internal/event"
	"github.com/afun-game/predictmarket-saas/pkg/polymarket"
	"github.com/afun-game/predictmarket-saas/pkg/types"
	"github.com/nxsky/twill"
	"github.com/nxsky/twill/runtime/resource"
)

const (
	defaultPage       = 1
	defaultLimit      = 20
	maxLimit          = 100
	maxPage           = 1000
	pageSize          = 100
	maxPagesPerLeague = 2
	defaultSchedule   = "@every 5m"
	syncJobName       = "polymarket-sports"
)

var (
	ErrNotFound    = errors.New("sports event not found")
	defaultLeagues = []string{"nba", "nfl", "mlb", "nhl", "wnba", "epl"}
)

// Service manages normalized sports events from Polymarket.
type Service interface {
	ListEvents(ctx context.Context, filters *EventFilters) ([]*SportsEvent, int, error)
	GetEvent(ctx context.Context, eventID string) (*SportsEvent, error)
	SyncFromPolymarket(ctx context.Context) error
}

// SportsEvent combines the common event with league and team metadata.
type SportsEvent struct {
	twill.AutoMarshal

	Event     *types.Event `json:"event"`
	League    string       `json:"league"`
	GameID    string       `json:"game_id,omitempty"`
	StartTime *time.Time   `json:"start_time,omitempty"`
	Teams     []Team       `json:"teams"`
}

// Team is a normalized team participating in a sports event.
type Team struct {
	twill.AutoMarshal

	Name         string `json:"name"`
	Abbreviation string `json:"abbreviation,omitempty"`
	Role         string `json:"role,omitempty"`
}

type EventFilters struct {
	twill.AutoMarshal

	League string `json:"league,omitempty"`
	Team   string `json:"team,omitempty"`
	Status string `json:"status,omitempty"`
	Page   int    `json:"page,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

// ValidationError identifies an invalid sports request field.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("invalid %s: %s", e.Field, e.Message)
}

type sourceClient interface {
	ListSports(ctx context.Context) ([]polymarket.Sport, error)
	ListEvents(ctx context.Context, options polymarket.ListEventsOptions) ([]polymarket.Event, error)
}

type eventSink interface {
	SyncSource(ctx context.Context, request *event.SyncRequest) error
}

type implementation struct {
	twill.Implements[Service]

	database twill.Database `twill:"primary-db"`
	cache    twill.Cache    `twill:"sports-cache"`
	events   twill.Ref[event.Service]
	cron     twill.Cron `twill:"sports-sync"`

	repository Repository
	cacheStore resource.Cache
	source     sourceClient
	sink       eventSink
	leagues    map[string]struct{}
	schedule   string
	now        func() time.Time
}

// NewService creates a Sports Service with in-memory storage for direct use in tests.
func NewService() Service {
	return newService(newMemoryRepository())
}

func newService(repository Repository) *implementation {
	return &implementation{repository: repository, now: time.Now}
}

func (s *implementation) Init(ctx context.Context) error {
	if s.repository == nil {
		database := s.database.Get()
		if database == nil || database.StdDB() == nil {
			return errors.New("primary database is not configured")
		}
		s.repository = newPostgresRepository(database.StdDB())
	}
	if s.cacheStore == nil {
		s.cacheStore = s.cache.Get()
	}
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
	if s.leagues == nil {
		s.leagues = configuredLeagues(os.Getenv("SPORTS_LEAGUES"))
	}
	if s.schedule == "" {
		s.schedule = strings.TrimSpace(os.Getenv("SPORTS_SYNC_INTERVAL"))
		if s.schedule == "" {
			s.schedule = defaultSchedule
		}
	}
	if s.now == nil {
		s.now = time.Now
	}
	scheduler := s.cron.Get()
	if scheduler == nil {
		return errors.New("sports sync cron is not configured")
	}
	if err := scheduler.Add(ctx, syncJobName, s.schedule, func(jobCtx context.Context) {
		if err := s.SyncFromPolymarket(jobCtx); err != nil {
			slog.ErrorContext(jobCtx, "Polymarket sports sync failed", "error", err)
		}
	}); err != nil {
		return fmt.Errorf("register sports sync job: %w", err)
	}
	return nil
}

func (s *implementation) ListEvents(ctx context.Context, filters *EventFilters) ([]*SportsEvent, int, error) {
	normalized, err := normalizeFilters(filters)
	if err != nil {
		return nil, 0, err
	}
	version := s.listCacheVersion(ctx)
	if values, total, ok := s.getCachedList(ctx, normalized, version); ok {
		return values, total, nil
	}
	values, total, err := s.repository.List(ctx, normalized)
	if err != nil {
		return nil, 0, fmt.Errorf("list sports events: %w", err)
	}
	s.putCachedList(ctx, normalized, version, values, total)
	return values, total, nil
}

func (s *implementation) GetEvent(ctx context.Context, eventID string) (*SportsEvent, error) {
	eventID = strings.TrimSpace(eventID)
	if eventID == "" {
		return nil, &ValidationError{Field: "event_id", Message: "is required"}
	}
	if value, ok := s.getCachedDetail(ctx, eventID); ok {
		return value, nil
	}
	value, err := s.repository.GetByID(ctx, eventID)
	if err != nil {
		return nil, fmt.Errorf("get sports event: %w", err)
	}
	s.putCachedDetail(ctx, value)
	return value, nil
}

func (s *implementation) SyncFromPolymarket(ctx context.Context) error {
	if s.source == nil || s.sink == nil || s.repository == nil {
		return errors.New("sports synchronization dependencies are not configured")
	}
	catalog, err := s.source.ListSports(ctx)
	if err != nil {
		return fmt.Errorf("fetch Polymarket sports catalog: %w", err)
	}
	leagues := selectedSports(catalog, s.leagues)
	active, closed := true, false
	for _, league := range leagues {
		for page := 0; page < maxPagesPerLeague; page++ {
			sourceEvents, err := s.source.ListEvents(ctx, polymarket.ListEventsOptions{
				SeriesID: league.SeriesID,
				Active:   &active, Closed: &closed, Limit: pageSize, Offset: page * pageSize,
			})
			if err != nil {
				return fmt.Errorf("fetch %s sports page %d: %w", league.Sport, page+1, err)
			}
			for _, sourceEvent := range sourceEvents {
				request, metadata, ok := normalizeSourceEvent(league.Sport, sourceEvent)
				if !ok {
					continue
				}
				if err := s.sink.SyncSource(ctx, request); err != nil {
					return fmt.Errorf("sync sports event %q: %w", sourceEvent.ID, err)
				}
				eventID, err := s.repository.UpsertSource(ctx, sourceEvent.ID, metadata, s.now().UTC())
				if err != nil {
					return fmt.Errorf("upsert sports metadata %q: %w", sourceEvent.ID, err)
				}
				s.deleteCachedDetail(ctx, eventID)
			}
			if len(sourceEvents) < pageSize {
				break
			}
		}
	}
	s.invalidateLists(ctx)
	return nil
}

func normalizeSourceEvent(league string, source polymarket.Event) (*event.SyncRequest, *SportsEvent, bool) {
	source.ID = strings.TrimSpace(source.ID)
	source.Title = strings.TrimSpace(source.Title)
	league = strings.ToLower(strings.TrimSpace(league))
	endTime := source.EndDate
	if endTime.IsZero() {
		endTime = source.StartTime
	}
	if source.ID == "" || source.Title == "" || league == "" || endTime.IsZero() {
		return nil, nil, false
	}
	status := "pending"
	if source.Closed {
		status = "closed"
	} else if source.Active {
		status = "active"
	}
	formattedEnd := endTime.UTC().Format(time.RFC3339)
	request := &event.SyncRequest{
		SourceID: source.ID, Title: source.Title, Description: source.Description,
		Category: "sports", EndTime: formattedEnd, ResolutionTime: formattedEnd, Status: status,
	}
	metadata := &SportsEvent{League: league, Teams: make([]Team, 0, len(source.Teams))}
	if source.GameID != 0 {
		metadata.GameID = fmt.Sprintf("%d", source.GameID)
	}
	if !source.StartTime.IsZero() {
		startTime := source.StartTime.UTC()
		metadata.StartTime = &startTime
	}
	for _, sourceTeam := range source.Teams {
		name := strings.TrimSpace(sourceTeam.Name)
		if name == "" {
			continue
		}
		metadata.Teams = append(metadata.Teams, Team{
			Name: name, Abbreviation: strings.ToLower(strings.TrimSpace(sourceTeam.Abbreviation)),
			Role: normalizeTeamRole(sourceTeam.Ordering),
		})
	}
	return request, metadata, true
}

func normalizeTeamRole(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "home":
		return "home"
	case "away":
		return "away"
	default:
		return ""
	}
}

func configuredLeagues(value string) map[string]struct{} {
	items := defaultLeagues
	if strings.TrimSpace(value) != "" {
		items = strings.Split(value, ",")
	}
	result := make(map[string]struct{}, len(items))
	for _, item := range items {
		if league := strings.ToLower(strings.TrimSpace(item)); league != "" {
			result[league] = struct{}{}
		}
	}
	return result
}

func selectedSports(catalog []polymarket.Sport, allowed map[string]struct{}) []polymarket.Sport {
	byLeague := make(map[string]polymarket.Sport)
	for _, sport := range catalog {
		league := strings.ToLower(strings.TrimSpace(sport.Sport))
		seriesID := strings.TrimSpace(sport.SeriesID)
		if _, ok := allowed[league]; !ok || seriesID == "" {
			continue
		}
		sport.Sport, sport.SeriesID = league, seriesID
		byLeague[league] = sport
	}
	result := make([]polymarket.Sport, 0, len(byLeague))
	for _, sport := range byLeague {
		result = append(result, sport)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Sport < result[j].Sport })
	return result
}

func normalizeFilters(filters *EventFilters) (EventFilters, error) {
	result := EventFilters{Page: defaultPage, Limit: defaultLimit}
	if filters != nil {
		result = *filters
		result.League = strings.ToLower(strings.TrimSpace(result.League))
		result.Team = strings.TrimSpace(result.Team)
		result.Status = strings.ToLower(strings.TrimSpace(result.Status))
		if result.Page == 0 {
			result.Page = defaultPage
		}
		if result.Limit == 0 {
			result.Limit = defaultLimit
		}
	}
	if result.Page < 1 {
		return EventFilters{}, &ValidationError{Field: "page", Message: "must be at least 1"}
	}
	if result.Page > maxPage {
		return EventFilters{}, &ValidationError{Field: "page", Message: "must not exceed 1000"}
	}
	if result.Limit < 1 || result.Limit > maxLimit {
		return EventFilters{}, &ValidationError{Field: "limit", Message: "must be between 1 and 100"}
	}
	if result.Status != "" && result.Status != "pending" && result.Status != "active" && result.Status != "closed" && result.Status != "resolved" {
		return EventFilters{}, &ValidationError{Field: "status", Message: "is not supported"}
	}
	return result, nil
}
