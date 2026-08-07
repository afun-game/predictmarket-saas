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
)

// SourceSynchronizer persists events received from a named authoritative source.
type SourceSynchronizer struct {
	repository Repository
	random     io.Reader
	now        func() time.Time
}

// NewSourceSynchronizer creates a synchronizer backed by repository.
func NewSourceSynchronizer(repository Repository) *SourceSynchronizer {
	return &SourceSynchronizer{
		repository: repository,
		random:     rand.Reader,
		now:        time.Now,
	}
}

// Sync creates or refreshes an event without duplicating its source identity.
func (s *SourceSynchronizer) Sync(ctx context.Context, sourceType string, req *SyncRequest) error {
	if s == nil || s.repository == nil {
		return errors.New("source synchronizer repository is required")
	}
	normalizedSourceType, input, endTime, resolutionTime, err := validateSourceSyncRequest(sourceType, req)
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
		SourceType:     normalizedSourceType,
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
	if _, err := s.repository.UpsertSource(ctx, value); err != nil {
		return fmt.Errorf("upsert source event: %w", err)
	}
	return nil
}

func validateSourceSyncRequest(
	sourceType string,
	req *SyncRequest,
) (string, *SyncRequest, time.Time, time.Time, error) {
	sourceType = strings.ToLower(strings.TrimSpace(sourceType))
	if !validSourceType(sourceType) {
		return "", nil, time.Time{}, time.Time{}, &ValidationError{
			Field:   "source_type",
			Message: "must be polymarket, lmb, custom, or boxrec",
		}
	}
	if req == nil {
		return "", nil, time.Time{}, time.Time{}, &ValidationError{
			Field:   "request",
			Message: "is required",
		}
	}

	input := *req
	createInput := &CreateRequest{
		SourceType:     sourceType,
		SourceID:       input.SourceID,
		Title:          input.Title,
		Description:    input.Description,
		Category:       input.Category,
		EndTime:        input.EndTime,
		ResolutionTime: input.ResolutionTime,
	}
	normalized, endTime, resolutionTime, err := validateCreateRequest(createInput)
	if err != nil {
		return "", nil, time.Time{}, time.Time{}, err
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
		return "", nil, time.Time{}, time.Time{}, &ValidationError{
			Field:   "status",
			Message: "must be pending, active, or closed",
		}
	}
	return normalized.SourceType, &input, endTime, resolutionTime, nil
}
