package main

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/afun-game/predictmarket-saas/internal/sportsingest"
)

func TestRunWorkerRunsOnceWithoutCreatingTicker(t *testing.T) {
	now := time.Date(2026, time.August, 7, 12, 0, 0, 0, time.UTC)
	job := &recordingJob{}
	tickerFactoryCalled := false
	config := config{
		LMBCalendarLocation: time.FixedZone("UTC+8", 8*60*60),
		LookaheadDays:       7,
		RunOnce:             true,
	}

	err := runWorker(
		context.Background(),
		config,
		job,
		func() time.Time { return now },
		func(time.Duration) ticker { tickerFactoryCalled = true; return nil },
		discardLogger(),
	)
	if err != nil {
		t.Fatalf("runWorker() error = %v", err)
	}
	if tickerFactoryCalled {
		t.Error("runWorker() created a ticker in run-once mode")
	}
	calls := job.Calls()
	if len(calls) != 1 {
		t.Fatalf("Sync() calls = %d, want 1", len(calls))
	}
	if got, want := calls[0].day, time.Date(2026, time.August, 7, 0, 0, 0, 0, config.LMBCalendarLocation); !got.Equal(want) || got.Location() != want.Location() {
		t.Errorf("Sync() day = %v (%s), want %v (%s)", got, got.Location(), want, want.Location())
	}
	if calls[0].lookaheadDays != 7 {
		t.Errorf("Sync() lookahead days = %d, want 7", calls[0].lookaheadDays)
	}
}

func TestRunWorkerStopsTickerWhenContextIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	job := &recordingJob{started: make(chan struct{})}
	workerTicker := &manualTicker{ticks: make(chan time.Time)}
	errCh := make(chan error, 1)
	go func() {
		errCh <- runWorker(
			ctx,
			config{
				LMBCalendarLocation: time.UTC,
				LookaheadDays:       0,
				PollInterval:        time.Minute,
			},
			job,
			time.Now,
			func(time.Duration) ticker { return workerTicker },
			discardLogger(),
		)
	}()

	<-job.started
	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("runWorker() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runWorker() did not return after context cancellation")
	}
	if !workerTicker.Stopped() {
		t.Error("ticker was not stopped after context cancellation")
	}
}

type syncCall struct {
	day           time.Time
	lookaheadDays int
}

type recordingJob struct {
	mu      sync.Mutex
	calls   []syncCall
	started chan struct{}
	once    sync.Once
}

func (j *recordingJob) Sync(_ context.Context, day time.Time, lookaheadDays int) (sportsingest.Result, error) {
	j.mu.Lock()
	j.calls = append(j.calls, syncCall{day: day, lookaheadDays: lookaheadDays})
	j.mu.Unlock()
	// start is closed exactly once; the field is never reassigned so the
	// test's unlocked receive races nothing.
	j.once.Do(func() {
		if j.started != nil {
			close(j.started)
		}
	})
	return sportsingest.Result{}, nil
}

func (j *recordingJob) Calls() []syncCall {
	j.mu.Lock()
	defer j.mu.Unlock()
	return append([]syncCall(nil), j.calls...)
}

type manualTicker struct {
	ticks   chan time.Time
	mu      sync.Mutex
	stopped bool
}

func (t *manualTicker) Chan() <-chan time.Time { return t.ticks }

func (t *manualTicker) Stop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stopped = true
}

func (t *manualTicker) Stopped() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stopped
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
