package event

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/afun-game/predictmarket-saas/pkg/types"
	"github.com/nxsky/twill"
)

const (
	defaultPage  = 1
	defaultLimit = 20
	maxLimit     = 100
	maxPage      = 1000
)

var (
	ErrNotFound          = errors.New("event not found")
	ErrAlreadyExists     = errors.New("event already exists")
	ErrInvalidTransition = errors.New("invalid event status transition")
	// ErrResolved prevents edits to events that have been resolved.
	ErrResolved = errors.New("event has been resolved")
)

// Service manages prediction events and their lifecycle.
type Service interface {
	Create(ctx context.Context, req *CreateRequest) (*types.Event, error)
	SyncSource(ctx context.Context, req *SyncRequest) error
	Get(ctx context.Context, eventID string) (*types.Event, error)
	List(ctx context.Context, filters *ListFilters) ([]*types.Event, int, error)
	Update(ctx context.Context, eventID string, req *UpdateRequest) (*types.Event, error)
	UpdateStatus(ctx context.Context, eventID string, status string) error
	Resolve(ctx context.Context, eventID string, outcome string) error
	ResolveSource(ctx context.Context, sourceID string, outcome string) error
}

type CreateRequest struct {
	twill.AutoMarshal

	SourceType     string `json:"source_type"`
	SourceID       string `json:"source_id"`
	Title          string `json:"title"`
	Description    string `json:"description"`
	Category       string `json:"category"`
	EndTime        string `json:"end_time"`
	ResolutionTime string `json:"resolution_time"`
}

// SyncRequest contains normalized event data from an authoritative external source.
type SyncRequest struct {
	twill.AutoMarshal

	SourceID       string `json:"source_id"`
	Title          string `json:"title"`
	Description    string `json:"description"`
	Category       string `json:"category"`
	EndTime        string `json:"end_time"`
	ResolutionTime string `json:"resolution_time"`
	Status         string `json:"status"`
}

type ListFilters struct {
	twill.AutoMarshal

	Category string `json:"category,omitempty"`
	Status   string `json:"status,omitempty"`
	Page     int    `json:"page,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}

// UpdateRequest carries editable event fields. Resolution is not editable;
// use UpdateStatus/Resolve for lifecycle changes.
type UpdateRequest struct {
	twill.AutoMarshal

	Title          *string `json:"title,omitempty"`
	Description    *string `json:"description,omitempty"`
	ResolutionTime *string `json:"resolution_time,omitempty"` // RFC3339
}

// ValidationError identifies an invalid event request field.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("invalid %s: %s", e.Field, e.Message)
}

type implementation struct {
	twill.Implements[Service]

	database   twill.Database `twill:"primary-db"`
	cache      twill.Cache    `twill:"event-cache"`
	repository Repository
	cacheStore cacheStore
	cacheEpoch cacheEpoch
	random     io.Reader
	now        func() time.Time
}

// NewService creates an Event Service backed by an in-memory repository.
func NewService() Service {
	return newService(newMemoryRepository())
}

func newService(repository Repository) *implementation {
	return &implementation{
		repository: repository,
		random:     rand.Reader,
		now:        time.Now,
	}
}

func (s *implementation) Init(context.Context) error {
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
	if s.random == nil {
		s.random = rand.Reader
	}
	if s.now == nil {
		s.now = time.Now
	}
	return nil
}

func (s *implementation) Create(ctx context.Context, req *CreateRequest) (*types.Event, error) {
	input, endTime, resolutionTime, err := validateCreateRequest(req)
	if err != nil {
		return nil, err
	}

	eventID, err := generateEventID(s.random)
	if err != nil {
		return nil, fmt.Errorf("generate event ID: %w", err)
	}
	now := s.now().UTC()
	// A market whose resolution time already passed can never be traded:
	// the market maker stops quoting near resolution and the hosted UI
	// cannot fill an order. Reject it at creation instead of silently
	// producing a dead market.
	if resolutionTime.Before(now) {
		return nil, &ValidationError{Field: "resolution_time", Message: "must not be in the past"}
	}
	value := &types.Event{
		ID:             eventID,
		SourceType:     input.SourceType,
		SourceID:       input.SourceID,
		Title:          input.Title,
		Description:    input.Description,
		Category:       input.Category,
		EndTime:        endTime,
		ResolutionTime: resolutionTime,
		Status:         "pending",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := s.repository.Create(ctx, value); err != nil {
		return nil, fmt.Errorf("create event: %w", err)
	}
	s.invalidateEventLists(ctx)
	s.putCachedEvent(ctx, value)
	return cloneEvent(value), nil
}

// SyncSource creates or refreshes a Polymarket event without duplicating its source ID.
func (s *implementation) SyncSource(ctx context.Context, req *SyncRequest) error {
	input, endTime, resolutionTime, err := validateSyncRequest(req)
	if err != nil {
		return err
	}

	eventID, err := generateEventID(s.random)
	if err != nil {
		return fmt.Errorf("generate synced event ID: %w", err)
	}
	now := s.now().UTC()
	value := &types.Event{
		ID:             eventID,
		SourceType:     "polymarket",
		SourceID:       input.SourceID,
		Title:          input.Title,
		Description:    input.Description,
		Category:       input.Category,
		EndTime:        endTime,
		ResolutionTime: resolutionTime,
		Status:         input.Status,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	storedID, err := s.repository.UpsertSource(ctx, value)
	if err != nil {
		return fmt.Errorf("upsert synced event: %w", err)
	}
	s.deleteCachedEvent(ctx, storedID)
	s.invalidateEventLists(ctx)
	return nil
}

func (s *implementation) Get(ctx context.Context, eventID string) (*types.Event, error) {
	eventID = strings.TrimSpace(eventID)
	if eventID == "" {
		return nil, &ValidationError{Field: "event_id", Message: "is required"}
	}
	if value, ok := s.getCachedEvent(ctx, eventID); ok {
		return value, nil
	}
	value, err := s.repository.GetByID(ctx, eventID)
	if err != nil {
		return nil, fmt.Errorf("get event: %w", err)
	}
	s.putCachedEvent(ctx, value)
	return value, nil
}

func (s *implementation) List(
	ctx context.Context,
	filters *ListFilters,
) ([]*types.Event, int, error) {
	normalized, err := normalizeFilters(filters)
	if err != nil {
		return nil, 0, err
	}
	cacheVersion := s.eventListCacheVersion(ctx)
	if values, total, ok := s.getCachedEventList(ctx, normalized, cacheVersion); ok {
		return values, total, nil
	}
	values, total, err := s.repository.List(ctx, normalized)
	if err != nil {
		return nil, 0, fmt.Errorf("list events: %w", err)
	}
	s.putCachedEventList(ctx, normalized, cacheVersion, values, total)
	return values, total, nil
}

// Update edits editable event fields. Resolved events are immutable.
func (s *implementation) Update(
	ctx context.Context,
	eventID string,
	req *UpdateRequest,
) (*types.Event, error) {
	eventID = strings.TrimSpace(eventID)
	if eventID == "" {
		return nil, &ValidationError{Field: "event_id", Message: "is required"}
	}
	if req == nil {
		return nil, &ValidationError{Field: "request", Message: "is required"}
	}
	value, err := s.repository.GetByID(ctx, eventID)
	if err != nil {
		return nil, fmt.Errorf("get event for update: %w", err)
	}
	if value.Status == "resolved" {
		return nil, ErrResolved
	}
	if req.Title != nil {
		title := strings.TrimSpace(*req.Title)
		if title == "" {
			return nil, &ValidationError{Field: "title", Message: "cannot be empty"}
		}
		value.Title = title
	}
	if req.Description != nil {
		value.Description = strings.TrimSpace(*req.Description)
	}
	if req.ResolutionTime != nil {
		resolutionTime, parseErr := time.Parse(time.RFC3339, strings.TrimSpace(*req.ResolutionTime))
		if parseErr != nil {
			return nil, &ValidationError{Field: "resolution_time", Message: "must be an RFC3339 timestamp"}
		}
		if resolutionTime.Before(s.now().UTC()) {
			return nil, &ValidationError{Field: "resolution_time", Message: "must not be in the past"}
		}
		value.ResolutionTime = resolutionTime
	}
	value.UpdatedAt = s.now().UTC()
	if err := s.repository.Update(ctx, value); err != nil {
		return nil, fmt.Errorf("update event: %w", err)
	}
	s.deleteCachedEvent(ctx, eventID)
	s.invalidateEventLists(ctx)
	return value, nil
}

func (s *implementation) UpdateStatus(ctx context.Context, eventID string, status string) error {
	eventID = strings.TrimSpace(eventID)
	status = strings.ToLower(strings.TrimSpace(status))
	if eventID == "" {
		return &ValidationError{Field: "event_id", Message: "is required"}
	}
	if status == "resolved" || !validStatus(status) {
		return &ValidationError{Field: "status", Message: "is not supported"}
	}

	value, err := s.repository.GetByID(ctx, eventID)
	if err != nil {
		return fmt.Errorf("get event for status update: %w", err)
	}
	if !canTransition(value.Status, status) {
		return fmt.Errorf("%w: %s to %s", ErrInvalidTransition, value.Status, status)
	}
	if err := s.repository.UpdateStatus(
		ctx,
		eventID,
		value.Status,
		status,
		s.now().UTC(),
	); err != nil {
		return fmt.Errorf("update event status: %w", err)
	}
	s.deleteCachedEvent(ctx, eventID)
	s.invalidateEventLists(ctx)
	return nil
}

func (s *implementation) Resolve(ctx context.Context, eventID string, outcome string) error {
	return s.resolve(ctx, eventID, outcome, "manual")
}

// ResolveSource records an outcome supplied by the authoritative Polymarket
// source. A manually resolved event remains terminal and is never overwritten.
func (s *implementation) ResolveSource(ctx context.Context, sourceID string, outcome string) error {
	sourceID = strings.TrimSpace(sourceID)
	outcome = strings.TrimSpace(outcome)
	if sourceID == "" {
		return &ValidationError{Field: "source_id", Message: "is required"}
	}
	if outcome == "" {
		return &ValidationError{Field: "outcome", Message: "is required"}
	}

	value, err := s.repository.GetBySource(ctx, "polymarket", sourceID)
	if err != nil {
		return fmt.Errorf("get event for source resolution: %w", err)
	}
	if value.Status == "resolved" {
		return nil
	}
	return s.resolve(ctx, value.ID, outcome, "polymarket")
}

func (s *implementation) resolve(ctx context.Context, eventID string, outcome string, resolutionSource string) error {
	eventID = strings.TrimSpace(eventID)
	outcome = strings.TrimSpace(outcome)
	if eventID == "" {
		return &ValidationError{Field: "event_id", Message: "is required"}
	}
	if outcome == "" {
		return &ValidationError{Field: "outcome", Message: "is required"}
	}

	value, err := s.repository.GetByID(ctx, eventID)
	if err != nil {
		return fmt.Errorf("get event for resolution: %w", err)
	}
	if value.Status != "closed" {
		return fmt.Errorf("%w: %s to resolved", ErrInvalidTransition, value.Status)
	}
	if err := s.repository.Resolve(
		ctx,
		eventID,
		value.Status,
		outcome,
		resolutionSource,
		s.now().UTC(),
	); err != nil {
		return fmt.Errorf("resolve event: %w", err)
	}
	s.deleteCachedEvent(ctx, eventID)
	s.invalidateEventLists(ctx)
	return nil
}

func validateCreateRequest(
	req *CreateRequest,
) (*CreateRequest, time.Time, time.Time, error) {
	if req == nil {
		return nil, time.Time{}, time.Time{}, &ValidationError{
			Field:   "request",
			Message: "is required",
		}
	}

	input := *req
	input.SourceType = strings.ToLower(strings.TrimSpace(input.SourceType))
	input.SourceID = strings.TrimSpace(input.SourceID)
	input.Title = strings.TrimSpace(input.Title)
	input.Description = strings.TrimSpace(input.Description)
	input.Category = strings.ToLower(strings.TrimSpace(input.Category))
	if !validSourceType(input.SourceType) {
		return nil, time.Time{}, time.Time{}, &ValidationError{
			Field:   "source_type",
			Message: "must be polymarket, lmb, or custom",
		}
	}
	if input.SourceID == "" {
		return nil, time.Time{}, time.Time{}, &ValidationError{Field: "source_id", Message: "is required"}
	}
	if input.Title == "" {
		return nil, time.Time{}, time.Time{}, &ValidationError{Field: "title", Message: "is required"}
	}
	if input.Category == "" {
		return nil, time.Time{}, time.Time{}, &ValidationError{Field: "category", Message: "is required"}
	}

	endTime, err := time.Parse(time.RFC3339, strings.TrimSpace(input.EndTime))
	if err != nil {
		return nil, time.Time{}, time.Time{}, &ValidationError{
			Field:   "end_time",
			Message: "must be an RFC3339 timestamp",
		}
	}
	resolutionTime, err := time.Parse(time.RFC3339, strings.TrimSpace(input.ResolutionTime))
	if err != nil {
		return nil, time.Time{}, time.Time{}, &ValidationError{
			Field:   "resolution_time",
			Message: "must be an RFC3339 timestamp",
		}
	}
	if resolutionTime.Before(endTime) {
		return nil, time.Time{}, time.Time{}, &ValidationError{
			Field:   "resolution_time",
			Message: "must not be before end_time",
		}
	}
	return &input, endTime.UTC(), resolutionTime.UTC(), nil
}

func validateSyncRequest(
	req *SyncRequest,
) (*SyncRequest, time.Time, time.Time, error) {
	if req == nil {
		return nil, time.Time{}, time.Time{}, &ValidationError{
			Field:   "request",
			Message: "is required",
		}
	}

	input := *req
	createInput := &CreateRequest{
		SourceType:     "polymarket",
		SourceID:       input.SourceID,
		Title:          input.Title,
		Description:    input.Description,
		Category:       input.Category,
		EndTime:        input.EndTime,
		ResolutionTime: input.ResolutionTime,
	}
	normalized, endTime, resolutionTime, err := validateCreateRequest(createInput)
	if err != nil {
		return nil, time.Time{}, time.Time{}, err
	}
	input.SourceID = normalized.SourceID
	input.Title = normalized.Title
	input.Description = normalized.Description
	input.Category = normalized.Category
	input.EndTime = normalized.EndTime
	input.ResolutionTime = normalized.ResolutionTime
	input.Status = strings.ToLower(strings.TrimSpace(input.Status))
	switch input.Status {
	case "pending", "active", "closed":
	default:
		return nil, time.Time{}, time.Time{}, &ValidationError{
			Field:   "status",
			Message: "must be pending, active, or closed",
		}
	}
	return &input, endTime, resolutionTime, nil
}

func normalizeFilters(filters *ListFilters) (ListFilters, error) {
	value := ListFilters{}
	if filters != nil {
		value = *filters
	}
	value.Category = strings.ToLower(strings.TrimSpace(value.Category))
	value.Status = strings.ToLower(strings.TrimSpace(value.Status))
	if value.Status != "" && !validStatus(value.Status) {
		return ListFilters{}, &ValidationError{Field: "status", Message: "is not supported"}
	}
	if value.Page == 0 {
		value.Page = defaultPage
	}
	if value.Limit == 0 {
		value.Limit = defaultLimit
	}
	if value.Page < 1 {
		return ListFilters{}, &ValidationError{Field: "page", Message: "must be at least 1"}
	}
	if value.Page > maxPage {
		return ListFilters{}, &ValidationError{Field: "page", Message: "must not exceed 1000"}
	}
	if value.Limit < 1 || value.Limit > maxLimit {
		return ListFilters{}, &ValidationError{Field: "limit", Message: "must be between 1 and 100"}
	}
	return value, nil
}

func validSourceType(value string) bool {
	return value == "polymarket" || value == "lmb" || value == "custom"
}

func validStatus(value string) bool {
	switch value {
	case "pending", "active", "closed", "resolved":
		return true
	default:
		return false
	}
}

func canTransition(from, to string) bool {
	switch from {
	case "pending":
		return to == "active" || to == "closed"
	case "active":
		return to == "closed"
	default:
		return false
	}
}

func generateEventID(random io.Reader) (string, error) {
	buffer := make([]byte, 16)
	if _, err := io.ReadFull(random, buffer); err != nil {
		return "", err
	}
	buffer[6] = (buffer[6] & 0x0f) | 0x40
	buffer[8] = (buffer[8] & 0x3f) | 0x80
	return fmt.Sprintf(
		"%x-%x-%x-%x-%x",
		buffer[0:4],
		buffer[4:6],
		buffer[6:8],
		buffer[8:10],
		buffer[10:16],
	), nil
}
