package event

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestCreateAndGetEvent(t *testing.T) {
	t.Parallel()

	service := newService(newMemoryRepository())
	service.now = func() time.Time {
		return time.Date(2026, time.July, 28, 10, 0, 0, 0, time.UTC)
	}
	created, err := service.Create(context.Background(), validCreateRequest("source-1", "sports"))
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.ID == "" || created.Status != "pending" {
		t.Errorf("Create() = %#v", created)
	}
	if created.EndTime.Location() != time.UTC || created.ResolutionTime.Location() != time.UTC {
		t.Error("Create() did not normalize event timestamps to UTC")
	}

	loaded, err := service.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if loaded.SourceID != "source-1" || loaded.Category != "sports" {
		t.Errorf("Get() = %#v", loaded)
	}
}

func TestCreateRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		req   *CreateRequest
		field string
	}{
		{name: "nil request", field: "request"},
		{
			name: "invalid source",
			req: func() *CreateRequest {
				request := validCreateRequest("source-1", "sports")
				request.SourceType = "other"
				return request
			}(),
			field: "source_type",
		},
		{
			name: "invalid end time",
			req: func() *CreateRequest {
				request := validCreateRequest("source-1", "sports")
				request.EndTime = "tomorrow"
				return request
			}(),
			field: "end_time",
		},
		{
			name: "resolution before end",
			req: func() *CreateRequest {
				request := validCreateRequest("source-1", "sports")
				end, err := time.Parse(time.RFC3339, request.EndTime)
				if err != nil {
					t.Fatal(err)
				}
				request.ResolutionTime = end.Add(-time.Hour).Format(time.RFC3339)
				return request
			}(),
			field: "resolution_time",
		},
		{
			name: "resolution in the past",
			req: func() *CreateRequest {
				request := validCreateRequest("source-1", "sports")
				request.ResolutionTime = time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
				return request
			}(),
			field: "resolution_time",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			service := newService(newMemoryRepository())
			_, err := service.Create(context.Background(), test.req)
			var validationErr *ValidationError
			if !errors.As(err, &validationErr) {
				t.Fatalf("Create() error = %v, want ValidationError", err)
			}
			if validationErr.Field != test.field {
				t.Errorf("Create() field = %q, want %q", validationErr.Field, test.field)
			}
		})
	}
}

func TestCreateRejectsDuplicateSource(t *testing.T) {
	t.Parallel()

	service := newService(newMemoryRepository())
	request := validCreateRequest("duplicate", "sports")
	if _, err := service.Create(context.Background(), request); err != nil {
		t.Fatalf("first Create() error = %v", err)
	}
	_, err := service.Create(context.Background(), request)
	if !errors.Is(err, ErrAlreadyExists) {
		t.Errorf("second Create() error = %v, want ErrAlreadyExists", err)
	}
}

func TestEventLifecycle(t *testing.T) {
	t.Parallel()

	service := newService(newMemoryRepository())
	created, err := service.Create(context.Background(), validCreateRequest("lifecycle", "politics"))
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := service.UpdateStatus(context.Background(), created.ID, "active"); err != nil {
		t.Fatalf("UpdateStatus(active) error = %v", err)
	}
	if err := service.UpdateStatus(context.Background(), created.ID, "closed"); err != nil {
		t.Fatalf("UpdateStatus(closed) error = %v", err)
	}
	if err := service.Resolve(context.Background(), created.ID, "Yes"); err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	resolved, err := service.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if resolved.Status != "resolved" || resolved.Outcome == nil || *resolved.Outcome != "Yes" {
		t.Errorf("resolved event = %#v", resolved)
	}
	if err := service.UpdateStatus(context.Background(), created.ID, "active"); !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("UpdateStatus() after resolution error = %v, want ErrInvalidTransition", err)
	}
}

func TestResolveRequiresClosedEvent(t *testing.T) {
	t.Parallel()

	service := newService(newMemoryRepository())
	created, err := service.Create(context.Background(), validCreateRequest("open", "crypto"))
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := service.Resolve(context.Background(), created.ID, "No"); !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("Resolve() error = %v, want ErrInvalidTransition", err)
	}
}

func TestResolveSourcePreservesManualResolution(t *testing.T) {
	t.Parallel()

	service := newService(newMemoryRepository())
	request := validSyncRequest("source-result")
	request.Status = "closed"
	if err := service.SyncSource(context.Background(), request); err != nil {
		t.Fatalf("SyncSource() error = %v", err)
	}
	if err := service.ResolveSource(context.Background(), request.SourceID, "Yes"); err != nil {
		t.Fatalf("ResolveSource() error = %v", err)
	}
	if err := service.ResolveSource(context.Background(), request.SourceID, "No"); err != nil {
		t.Fatalf("second ResolveSource() error = %v", err)
	}
	values, _, err := service.List(context.Background(), &ListFilters{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if values[0].Outcome == nil || *values[0].Outcome != "Yes" {
		t.Errorf("source resolution overwrote outcome: %#v", values[0])
	}
}

func TestListEventsWithFiltersAndPagination(t *testing.T) {
	t.Parallel()

	service := newService(newMemoryRepository())
	base := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Hour)
	for index, category := range []string{"sports", "politics", "sports"} {
		request := validCreateRequest(fmt.Sprintf("source-%d", index), category)
		request.EndTime = base.Add(time.Duration(index) * time.Hour).Format(time.RFC3339)
		request.ResolutionTime = base.Add(time.Duration(index+1) * time.Hour).Format(time.RFC3339)
		if _, err := service.Create(context.Background(), request); err != nil {
			t.Fatalf("Create(%d) error = %v", index, err)
		}
	}

	values, total, err := service.List(context.Background(), &ListFilters{
		Category: "SPORTS",
		Page:     2,
		Limit:    1,
	})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if total != 2 || len(values) != 1 || values[0].SourceID != "source-2" {
		t.Errorf("List() values = %#v, total = %d", values, total)
	}
}

func TestSyncSourceIsIdempotentAndPreservesTerminalStatus(t *testing.T) {
	t.Parallel()

	service := newService(newMemoryRepository())
	request := validSyncRequest("source-sync")
	if err := service.SyncSource(context.Background(), request); err != nil {
		t.Fatalf("first SyncSource() error = %v", err)
	}

	request.Title = "Updated title"
	request.Status = "closed"
	if err := service.SyncSource(context.Background(), request); err != nil {
		t.Fatalf("second SyncSource() error = %v", err)
	}
	values, total, err := service.List(context.Background(), &ListFilters{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if total != 1 || len(values) != 1 {
		t.Fatalf("synced events = %d, want 1", total)
	}
	if values[0].Title != "Updated title" || values[0].Status != "closed" {
		t.Errorf("synced event = %#v", values[0])
	}

	request.Status = "active"
	if err := service.SyncSource(context.Background(), request); err != nil {
		t.Fatalf("third SyncSource() error = %v", err)
	}
	values, _, err = service.List(context.Background(), &ListFilters{})
	if err != nil {
		t.Fatalf("List() after terminal update error = %v", err)
	}
	if values[0].Status != "closed" {
		t.Errorf("terminal status = %q, want closed", values[0].Status)
	}
}

func TestSyncSourceRejectsInvalidStatus(t *testing.T) {
	t.Parallel()

	request := validSyncRequest("invalid-status")
	request.Status = "resolved"
	err := newService(newMemoryRepository()).SyncSource(context.Background(), request)
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) || validationErr.Field != "status" {
		t.Errorf("SyncSource() error = %v, want status ValidationError", err)
	}
}

func TestUpdateRejectsPastResolutionTime(t *testing.T) {
	t.Parallel()

	service := newService(newMemoryRepository())
	created, err := service.Create(context.Background(), validCreateRequest("update-past", "sports"))
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	past := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	_, err = service.Update(context.Background(), created.ID, &UpdateRequest{ResolutionTime: &past})
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) || validationErr.Field != "resolution_time" {
		t.Errorf("Update() error = %v, want resolution_time ValidationError", err)
	}
}

func validCreateRequest(sourceID, category string) *CreateRequest {
	start := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Minute)
	return &CreateRequest{
		SourceType:     "polymarket",
		SourceID:       sourceID,
		Title:          "Will the event happen?",
		Description:    "An event used by unit tests.",
		Category:       category,
		EndTime:        start.Format(time.RFC3339),
		ResolutionTime: start.Add(time.Hour).Format(time.RFC3339),
	}
}

func validSyncRequest(sourceID string) *SyncRequest {
	start := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Minute)
	return &SyncRequest{
		SourceID:       sourceID,
		Title:          "Synced event",
		Description:    "Synced from Polymarket.",
		Category:       "politics",
		EndTime:        start.Format(time.RFC3339),
		ResolutionTime: start.Format(time.RFC3339),
		Status:         "active",
	}
}
