package event

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/afun-game/predictmarket-saas/pkg/types"
)

type memoryRepository struct {
	mu         sync.RWMutex
	byID       map[string]*types.Event
	idBySource map[string]string
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{
		byID:       map[string]*types.Event{},
		idBySource: map[string]string{},
	}
}

func (r *memoryRepository) Create(ctx context.Context, value *types.Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	sourceKey := value.SourceType + "\x00" + value.SourceID
	if _, exists := r.idBySource[sourceKey]; exists {
		return ErrAlreadyExists
	}
	if _, exists := r.byID[value.ID]; exists {
		return fmt.Errorf("event ID already exists: %s", value.ID)
	}
	r.byID[value.ID] = cloneEvent(value)
	r.idBySource[sourceKey] = value.ID
	return nil
}

func (r *memoryRepository) UpsertSource(ctx context.Context, value *types.Event) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	sourceKey := value.SourceType + "\x00" + value.SourceID
	eventID, exists := r.idBySource[sourceKey]
	if !exists {
		if _, idExists := r.byID[value.ID]; idExists {
			return "", fmt.Errorf("event ID already exists: %s", value.ID)
		}
		r.byID[value.ID] = cloneEvent(value)
		r.idBySource[sourceKey] = value.ID
		return value.ID, nil
	}

	existing := r.byID[eventID]
	existing.Title = value.Title
	existing.Description = value.Description
	existing.Category = value.Category
	existing.EndTime = value.EndTime
	existing.ResolutionTime = value.ResolutionTime
	if existing.Status != "closed" && existing.Status != "resolved" {
		existing.Status = value.Status
	}
	existing.UpdatedAt = value.UpdatedAt
	return eventID, nil
}

func (r *memoryRepository) GetByID(ctx context.Context, eventID string) (*types.Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	value, exists := r.byID[eventID]
	if !exists {
		return nil, ErrNotFound
	}
	return cloneEvent(value), nil
}

func (r *memoryRepository) GetBySource(
	ctx context.Context,
	sourceType string,
	sourceID string,
) (*types.Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	eventID, exists := r.idBySource[sourceType+"\x00"+sourceID]
	if !exists {
		return nil, ErrNotFound
	}
	return cloneEvent(r.byID[eventID]), nil
}

func (r *memoryRepository) List(
	ctx context.Context,
	filters ListFilters,
) ([]*types.Event, int, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	values := make([]*types.Event, 0, len(r.byID))
	for _, value := range r.byID {
		matchesCategory := filters.Category == "" || value.Category == filters.Category
		matchesStatus := filters.Status == "" || value.Status == filters.Status
		if matchesCategory && matchesStatus {
			values = append(values, cloneEvent(value))
		}
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].EndTime.Equal(values[j].EndTime) {
			return values[i].ID < values[j].ID
		}
		return values[i].EndTime.Before(values[j].EndTime)
	})

	total := len(values)
	offset := (filters.Page - 1) * filters.Limit
	if offset >= total {
		return []*types.Event{}, total, nil
	}
	end := min(offset+filters.Limit, total)
	return values[offset:end], total, nil
}

func (r *memoryRepository) UpdateStatus(
	ctx context.Context,
	eventID string,
	expectedStatus string,
	status string,
	updatedAt time.Time,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	value, exists := r.byID[eventID]
	if !exists {
		return ErrNotFound
	}
	if value.Status != expectedStatus {
		return ErrInvalidTransition
	}
	value.Status = status
	value.UpdatedAt = updatedAt
	return nil
}

// Update persists editable event fields.
func (r *memoryRepository) Update(ctx context.Context, value *types.Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	existing, exists := r.byID[value.ID]
	if !exists {
		return ErrNotFound
	}
	clone := *value
	clone.CreatedAt = existing.CreatedAt
	clone.Status = existing.Status
	clone.Outcome = existing.Outcome
	r.byID[value.ID] = &clone
	return nil
}

func (r *memoryRepository) Resolve(
	ctx context.Context,
	eventID string,
	expectedStatus string,
	outcome string,
	resolutionSource string,
	updatedAt time.Time,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	value, exists := r.byID[eventID]
	if !exists {
		return ErrNotFound
	}
	if value.Status != expectedStatus {
		return ErrInvalidTransition
	}
	if resolutionSource == "" {
		return errors.New("resolution source is required")
	}
	value.Status = "resolved"
	value.Outcome = &outcome
	value.UpdatedAt = updatedAt
	return nil
}

func cloneEvent(value *types.Event) *types.Event {
	if value == nil {
		return nil
	}
	clone := *value
	if value.Outcome != nil {
		outcome := *value.Outcome
		clone.Outcome = &outcome
	}
	return &clone
}
