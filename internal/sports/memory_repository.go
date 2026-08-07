package sports

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"
)

type memoryRepository struct {
	mu         sync.RWMutex
	byID       map[string]*SportsEvent
	idBySource map[string]string
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{byID: map[string]*SportsEvent{}, idBySource: map[string]string{}}
}

func (r *memoryRepository) UpsertSource(ctx context.Context, sourceType, sourceID string, value *SportsEvent, _ time.Time) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	sourceKey := sourceType + "\x00" + sourceID
	id := r.idBySource[sourceKey]
	if id == "" && value.Event != nil {
		id = value.Event.ID
	}
	if id == "" {
		id = sourceType + ":" + sourceID
	}
	clone := cloneSportsEvent(value)
	if clone.Event != nil {
		clone.Event.ID = id
	}
	r.byID[id], r.idBySource[sourceKey] = clone, id
	return id, nil
}

func (r *memoryRepository) GetByID(ctx context.Context, eventID string) (*SportsEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	value, ok := r.byID[eventID]
	if !ok {
		return nil, ErrNotFound
	}
	return cloneSportsEvent(value), nil
}

func (r *memoryRepository) List(ctx context.Context, filters EventFilters) ([]*SportsEvent, int, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	values := make([]*SportsEvent, 0, len(r.byID))
	for _, value := range r.byID {
		if filters.League != "" && value.League != filters.League {
			continue
		}
		if filters.Status != "" && (value.Event == nil || value.Event.Status != filters.Status) {
			continue
		}
		if filters.Team != "" && !hasTeam(value.Teams, filters.Team) {
			continue
		}
		values = append(values, cloneSportsEvent(value))
	}
	sort.Slice(values, func(i, j int) bool {
		var left, right time.Time
		if values[i].StartTime != nil {
			left = *values[i].StartTime
		}
		if values[j].StartTime != nil {
			right = *values[j].StartTime
		}
		if left.Equal(right) {
			return values[i].Event.ID < values[j].Event.ID
		}
		return left.Before(right)
	})
	total := len(values)
	offset := (filters.Page - 1) * filters.Limit
	if offset >= total {
		return []*SportsEvent{}, total, nil
	}
	return values[offset:min(offset+filters.Limit, total)], total, nil
}

func hasTeam(teams []Team, query string) bool {
	query = strings.ToLower(strings.TrimSpace(query))
	for _, team := range teams {
		if strings.Contains(strings.ToLower(team.Name), query) || strings.Contains(strings.ToLower(team.Abbreviation), query) {
			return true
		}
	}
	return false
}

func cloneSportsEvent(value *SportsEvent) *SportsEvent {
	if value == nil {
		return nil
	}
	clone := *value
	if value.Event != nil {
		eventClone := *value.Event
		if value.Event.Outcome != nil {
			outcome := *value.Event.Outcome
			eventClone.Outcome = &outcome
		}
		clone.Event = &eventClone
	}
	if value.StartTime != nil {
		start := *value.StartTime
		clone.StartTime = &start
	}
	clone.Teams = append([]Team(nil), value.Teams...)
	return &clone
}
