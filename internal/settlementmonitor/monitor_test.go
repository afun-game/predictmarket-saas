package settlementmonitor

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestAuditReportsOverdueEventsAndDeadLetters(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)
	service := newService(&fakeRepository{events: []OverdueEvent{{
		ID:             "event-1",
		SourceID:       "source-1",
		Status:         "closed",
		ResolutionTime: now.Add(-time.Hour),
	}}}, fakeInspector{pending: 2})
	service.now = func() time.Time { return now }
	service.grace = 15 * time.Minute

	result, err := service.Audit(context.Background())
	if err != nil {
		t.Fatalf("Audit() error = %v", err)
	}
	if result.OverdueEvents != 1 || result.DeadLetterSize != 2 {
		t.Errorf("Audit() = %#v", result)
	}
}

func TestAuditPropagatesDependencyErrors(t *testing.T) {
	t.Parallel()

	repositoryErr := errors.New("database unavailable")
	service := newService(&fakeRepository{err: repositoryErr}, fakeInspector{})
	if _, err := service.Audit(context.Background()); !errors.Is(err, repositoryErr) {
		t.Errorf("Audit() error = %v, want repository error", err)
	}

	streamErr := errors.New("NATS unavailable")
	service = newService(&fakeRepository{}, fakeInspector{err: streamErr})
	if _, err := service.Audit(context.Background()); !errors.Is(err, streamErr) {
		t.Errorf("Audit() error = %v, want stream error", err)
	}
}

type fakeRepository struct {
	events []OverdueEvent
	err    error
}

func (r *fakeRepository) OverdueEvents(context.Context, time.Time) ([]OverdueEvent, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.events, nil
}

type fakeInspector struct {
	pending int64
	err     error
}

func (i fakeInspector) PendingMessages(context.Context, string) (int64, error) {
	return i.pending, i.err
}
