package event

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestSourceSynchronizerSyncStoresLMBSource(t *testing.T) {
	t.Parallel()

	repository := newMemoryRepository()
	synchronizer := NewSourceSynchronizer(repository)
	request := validSyncRequest("846703")
	if err := synchronizer.Sync(context.Background(), " LMB ", request); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if err := synchronizer.Sync(context.Background(), "lmb", request); err != nil {
		t.Fatalf("second Sync() error = %v", err)
	}

	stored, err := repository.GetBySource(context.Background(), "lmb", "846703")
	if err != nil {
		t.Fatalf("GetBySource() error = %v", err)
	}
	if stored.SourceType != "lmb" || stored.SourceID != "846703" {
		t.Errorf("stored source = (%q, %q), want (lmb, 846703)", stored.SourceType, stored.SourceID)
	}
	values, total, err := repository.List(context.Background(), ListFilters{Page: 1, Limit: 20})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if total != 1 || len(values) != 1 {
		t.Errorf("stored events = %d, want 1", total)
	}
}

func TestSourceSynchronizerSyncRejectsUnsupportedSource(t *testing.T) {
	t.Parallel()

	err := NewSourceSynchronizer(newMemoryRepository()).Sync(
		context.Background(),
		"unsupported",
		validSyncRequest("846703"),
	)
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) || validationErr.Field != "source_type" {
		t.Errorf("Sync() error = %v, want source_type ValidationError", err)
	}
}

func TestSourceSynchronizerSyncRejectsNilRepository(t *testing.T) {
	t.Parallel()

	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("Sync() panicked: %v", recovered)
		}
	}()
	err := NewSourceSynchronizer(nil).Sync(context.Background(), "lmb", validSyncRequest("846703"))
	if err == nil || !strings.Contains(err.Error(), "repository") {
		t.Errorf("Sync() error = %v, want repository error", err)
	}
}
