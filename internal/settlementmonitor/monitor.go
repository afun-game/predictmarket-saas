// Package settlementmonitor alerts operators about stalled automatic settlement.
package settlementmonitor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/nxsky/twill"
	"github.com/afun-game/predictmarket-saas/internal/messaging"
)

const (
	defaultSchedule      = "@every 5m"
	defaultGracePeriod   = 15 * time.Minute
	monitorJobName       = "settlement-alerts"
	defaultDLQTopic      = messaging.DeadLetterTopic
	maximumGraceInterval = 24 * time.Hour
)

// Service audits unresolved source events and retained dead-letter messages.
type Service interface {
	Audit(ctx context.Context) (*Result, error)
}

// Result summarizes a single settlement safety audit.
type Result struct {
	twill.AutoMarshal

	OverdueEvents  int   `json:"overdue_events"`
	DeadLetterSize int64 `json:"dead_letter_size"`
}

type messageInspector interface {
	PendingMessages(ctx context.Context, topic string) (int64, error)
}

type implementation struct {
	twill.Implements[Service]

	database twill.Database `twill:"primary-db"`
	events   twill.PubSub   `twill:"event-stream"`
	cron     twill.Cron     `twill:"settlement-alerts"`

	repository Repository
	inspector  messageInspector
	schedule   string
	grace      time.Duration
	now        func() time.Time
}

// NewService creates the scheduled settlement safety monitor.
func NewService() Service {
	return &implementation{}
}

func newService(repository Repository, inspector messageInspector) *implementation {
	return &implementation{
		repository: repository,
		inspector:  inspector,
		now:        time.Now,
	}
}

func (s *implementation) Init(ctx context.Context) error {
	if s.repository == nil {
		database := s.database.Get()
		if database == nil || database.StdDB() == nil {
			return errors.New("primary database is not configured")
		}
		s.repository = newPostgresRepository(database.StdDB())
	}
	if s.inspector == nil {
		stream := s.events.Get()
		inspector, ok := stream.(messageInspector)
		if !ok {
			return errors.New("event stream does not expose durable message inspection")
		}
		s.inspector = inspector
	}
	if s.inspector == nil {
		return errors.New("event stream is not configured")
	}
	if s.schedule == "" {
		s.schedule = strings.TrimSpace(os.Getenv("SETTLEMENT_ALERT_INTERVAL"))
	}
	if s.schedule == "" {
		s.schedule = defaultSchedule
	}
	if s.grace == 0 {
		s.grace = settlementGracePeriod()
	}
	if s.now == nil {
		s.now = time.Now
	}

	scheduler := s.cron.Get()
	if scheduler == nil {
		return errors.New("settlement alert cron is not configured")
	}
	if err := scheduler.Add(ctx, monitorJobName, s.schedule, func(jobCtx context.Context) {
		if _, err := s.Audit(jobCtx); err != nil {
			slog.ErrorContext(jobCtx, "settlement safety audit failed", "error", err)
		}
	}); err != nil {
		return fmt.Errorf("register settlement alert job: %w", err)
	}
	return nil
}

func (s *implementation) Audit(ctx context.Context) (*Result, error) {
	if s.repository == nil || s.inspector == nil {
		return nil, errors.New("settlement safety monitor is not configured")
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.grace <= 0 {
		s.grace = defaultGracePeriod
	}
	events, err := s.repository.OverdueEvents(ctx, s.now().UTC().Add(-s.grace))
	if err != nil {
		return nil, fmt.Errorf("find overdue settlement events: %w", err)
	}
	for _, event := range events {
		slog.WarnContext(
			ctx,
			"event resolution is overdue",
			"event_id", event.ID,
			"source_id", event.SourceID,
			"status", event.Status,
			"resolution_time", event.ResolutionTime,
		)
	}

	deadLetters, err := s.inspector.PendingMessages(ctx, defaultDLQTopic)
	if err != nil {
		return nil, fmt.Errorf("inspect settlement dead-letter queue: %w", err)
	}
	if deadLetters > 0 {
		slog.WarnContext(ctx, "settlement dead-letter queue is not empty", "messages", deadLetters)
	}
	return &Result{OverdueEvents: len(events), DeadLetterSize: deadLetters}, nil
}

func settlementGracePeriod() time.Duration {
	value := strings.TrimSpace(os.Getenv("SETTLEMENT_ALERT_GRACE_MINUTES"))
	if value == "" {
		return defaultGracePeriod
	}
	minutes, err := strconv.Atoi(value)
	if err != nil || minutes < 1 || time.Duration(minutes)*time.Minute > maximumGraceInterval {
		slog.Warn("invalid SETTLEMENT_ALERT_GRACE_MINUTES; using default", "value", value)
		return defaultGracePeriod
	}
	return time.Duration(minutes) * time.Minute
}
