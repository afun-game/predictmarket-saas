// Command sports-ingest imports official LMB fixtures as candidate prediction
// events. It does not resolve, settle, void, or refund events.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/afun-game/predictmarket-saas/internal/event"
	"github.com/afun-game/predictmarket-saas/internal/sports"
	"github.com/afun-game/predictmarket-saas/internal/sportsingest"
	"github.com/afun-game/predictmarket-saas/internal/sportsingest/provider"
	"github.com/afun-game/predictmarket-saas/pkg/sportsfeed/lmb"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type syncJob interface {
	Sync(context.Context, time.Time, int) (sportsingest.Result, error)
}

type ticker interface {
	Chan() <-chan time.Time
	Stop()
}

type standardTicker struct {
	ticker *time.Ticker
}

func (t *standardTicker) Chan() <-chan time.Time { return t.ticker.C }

func (t *standardTicker) Stop() { t.ticker.Stop() }

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger := slog.Default()
	config, err := loadConfig(os.Getenv)
	if err == nil {
		err = runCommand(ctx, config, logger)
	}
	if err != nil {
		logger.Error("sports ingest stopped", "error", err)
		os.Exit(1)
	}
}

func runCommand(ctx context.Context, config config, logger *slog.Logger) error {
	database, err := sql.Open("pgx", config.DatabaseURL)
	if err != nil {
		return fmt.Errorf("open sports ingest database: %w", err)
	}
	defer func() { _ = database.Close() }()
	if err := database.PingContext(ctx); err != nil {
		return fmt.Errorf("ping sports ingest database: %w", err)
	}

	client := lmb.NewClient(
		lmb.WithBaseURL(config.LMBBaseURL),
		lmb.WithHTTPClient(&http.Client{Timeout: config.LMBRequestTimeout}),
		lmb.WithCalendarLocation(config.LMBCalendarLocation),
	)
	source := provider.NewLMBSource(client, time.Now)
	events := event.NewSourceSynchronizer(event.NewPostgresRepository(database))
	metadata := sports.NewPostgresRepository(database)
	job := sportsingest.New(source, events, metadata)

	return runWorker(ctx, config, job, time.Now, newStandardTicker, logger)
}

func newStandardTicker(interval time.Duration) ticker {
	return &standardTicker{ticker: time.NewTicker(interval)}
}

func runWorker(
	ctx context.Context,
	config config,
	job syncJob,
	now func() time.Time,
	newTicker func(time.Duration) ticker,
	logger *slog.Logger,
) error {
	if job == nil {
		return errors.New("sports ingest job is required")
	}
	if config.LMBCalendarLocation == nil {
		return errors.New("LMB calendar timezone is required")
	}
	if now == nil {
		return errors.New("sports ingest clock is required")
	}
	if logger == nil {
		logger = slog.Default()
	}

	run := func() error {
		result, err := job.Sync(ctx, calendarDay(now(), config.LMBCalendarLocation), config.LookaheadDays)
		if err != nil {
			logger.ErrorContext(ctx, "sports ingest run failed", "error", err)
			return err
		}
		logger.InfoContext(
			ctx,
			"sports ingest run completed",
			"fetched", result.Fetched,
			"synced", result.Synced,
			"skipped", result.Skipped,
			"market_timezone", locationName(config.LMBMarketLocation),
			"storage_timezone", "UTC",
		)
		return nil
	}

	if err := run(); err != nil {
		return err
	}
	if config.RunOnce {
		return nil
	}
	if config.PollInterval <= 0 {
		return errors.New("sports ingest poll interval must be greater than zero")
	}
	if newTicker == nil {
		return errors.New("sports ingest ticker factory is required")
	}
	workerTicker := newTicker(config.PollInterval)
	if workerTicker == nil {
		return errors.New("sports ingest ticker is required")
	}
	defer workerTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-workerTicker.Chan():
			// A later scheduled failure is logged and retried on the next interval.
			_ = run()
		}
	}
}

func calendarDay(now time.Time, location *time.Location) time.Time {
	local := now.In(location)
	year, month, day := local.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, location)
}

func locationName(location *time.Location) string {
	if location == nil {
		return ""
	}
	return location.String()
}
