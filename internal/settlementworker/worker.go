// Package settlementworker delivers resolved-event outbox messages and consumes them.
package settlementworker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"time"

	"github.com/afun-game/predictmarket-saas/internal/messaging"
	"github.com/afun-game/predictmarket-saas/internal/settlement"
	"github.com/nxsky/twill"
	"github.com/nxsky/twill/runtime/resource"
)

const (
	consumerName            = "market-settlement"
	outboxBatchSize         = 20
	dispatchInterval        = time.Second
	settlementRetryInitial  = time.Second
	settlementRetryMaxDelay = 30 * time.Second
	settlementRetryAttempts = 5
)

var ErrInvalidMessage = errors.New("invalid event resolution message")

// Service dispatches pending event outbox records. Its consumer runs in the background.
type Service interface {
	Dispatch(ctx context.Context) (int, error)
}

type settlementService interface {
	SettleEvent(ctx context.Context, eventID string) error
}

type outboxRepository interface {
	Dispatch(
		ctx context.Context,
		limit int,
		publisher resource.PubSub,
	) (int, error)
}

type implementation struct {
	twill.Implements[Service]

	database      twill.Database `twill:"primary-db"`
	events        twill.PubSub   `twill:"event-stream"`
	settlementRef twill.Ref[settlement.Service]

	repository      outboxRepository
	pubSub          resource.PubSub
	settler         settlementService
	subscription    resource.Subscription
	topic           string
	deadLetterTopic string
	consumer        string
	interval        time.Duration
	retryInitial    time.Duration
	retryMaxDelay   time.Duration
	retryAttempts   int
}

func newService(
	repository outboxRepository,
	pubSub resource.PubSub,
	settler settlementService,
) *implementation {
	return &implementation{
		repository:      repository,
		pubSub:          pubSub,
		settler:         settler,
		topic:           messaging.EventResolvedTopic,
		deadLetterTopic: messaging.DeadLetterTopic,
		consumer:        consumerName,
		interval:        dispatchInterval,
		retryInitial:    settlementRetryInitial,
		retryMaxDelay:   settlementRetryMaxDelay,
		retryAttempts:   settlementRetryAttempts,
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
	if s.pubSub == nil {
		s.pubSub = s.events.Get()
	}
	if s.settler == nil {
		s.settler = s.settlementRef.Get()
	}
	if s.pubSub == nil || s.settler == nil {
		return errors.New("event stream and settlement service are required")
	}
	if s.topic == "" {
		s.topic = messaging.EventResolvedTopic
	}
	if s.deadLetterTopic == "" {
		s.deadLetterTopic = messaging.DeadLetterTopic
	}
	if s.consumer == "" {
		s.consumer = consumerName
	}
	if s.interval <= 0 {
		s.interval = dispatchInterval
	}
	if s.retryInitial <= 0 {
		s.retryInitial = settlementRetryInitial
	}
	if s.retryMaxDelay <= 0 {
		s.retryMaxDelay = settlementRetryMaxDelay
	}
	if s.retryAttempts <= 0 {
		s.retryAttempts = settlementRetryAttempts
	}
	subscription, err := s.pubSub.Subscribe(ctx, s.topic, s.consumer)
	if err != nil {
		return fmt.Errorf("subscribe to resolved events: %w", err)
	}
	s.subscription = subscription
	go s.runSafely(ctx, "outbox dispatcher", s.runDispatcher)
	go s.runSafely(ctx, "settlement consumer", s.runConsumer)
	return nil
}

func (s *implementation) runSafely(ctx context.Context, name string, run func(context.Context)) {
	defer func() {
		if recovered := recover(); recovered != nil {
			slog.ErrorContext(
				ctx,
				"background worker panic recovered",
				"worker", name,
				"panic", recovered,
				"stack", string(debug.Stack()),
			)
		}
	}()
	run(ctx)
}

func (s *implementation) Dispatch(ctx context.Context) (int, error) {
	if s.repository == nil || s.pubSub == nil {
		return 0, errors.New("settlement outbox dispatcher is not configured")
	}
	count, err := s.repository.Dispatch(ctx, outboxBatchSize, s.pubSub)
	if err != nil {
		return 0, fmt.Errorf("dispatch resolved event outbox: %w", err)
	}
	return count, nil
}

func (s *implementation) runDispatcher(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			count, err := s.Dispatch(ctx)
			if err != nil {
				slog.ErrorContext(ctx, "resolved event outbox dispatch failed", "error", err)
				continue
			}
			if count > 0 {
				slog.InfoContext(ctx, "resolved event outbox dispatched", "messages", count)
			}
		}
	}
}

func (s *implementation) runConsumer(ctx context.Context) {
	defer func() {
		if err := s.subscription.Close(); err != nil {
			slog.ErrorContext(ctx, "close settlement subscription failed", "error", err)
		}
	}()
	for {
		message, err := s.subscription.Next(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.ErrorContext(ctx, "receive resolved event failed", "error", err)
			if !waitForRetry(ctx, time.Second) {
				return
			}
			continue
		}
		if err := s.processUntilAcknowledged(ctx, message); err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.ErrorContext(ctx, "process resolved event failed", "error", err)
		}
	}
}

func (s *implementation) processUntilAcknowledged(ctx context.Context, message []byte) error {
	event, err := decodeResolvedEvent(message)
	if err != nil {
		return s.deadLetterAndAcknowledge(ctx, message, err)
	}
	delay := s.retryInitial
	for attempt := 1; attempt <= s.retryAttempts; attempt++ {
		err := s.settler.SettleEvent(ctx, event.Data.EventID)
		if err == nil {
			if err := s.subscription.Ack(ctx); err != nil {
				return fmt.Errorf("acknowledge settled event: %w", err)
			}
			return nil
		}
		permanent := isPermanentSettlementError(err)
		lastAttempt := attempt == s.retryAttempts
		if permanent || lastAttempt {
			reason := err
			if lastAttempt && !permanent {
				reason = fmt.Errorf("settlement retry limit reached after %d attempts: %w", attempt, err)
			}
			return s.deadLetterAndAcknowledge(ctx, message, reason)
		}
		slog.ErrorContext(
			ctx,
			"market settlement will retry",
			"event_id",
			event.Data.EventID,
			"error",
			err,
		)
		if !waitForRetry(ctx, delay) {
			return ctx.Err()
		}
		delay = min(delay*2, s.retryMaxDelay)
	}
	return errors.New("settlement retry loop exited unexpectedly")
}

func (s *implementation) deadLetterAndAcknowledge(
	ctx context.Context,
	message []byte,
	reason error,
) error {
	deadLetter := messaging.NewDeadLetter(s.topic, message, reason.Error(), time.Now().UTC())
	payload, err := json.Marshal(deadLetter)
	if err != nil {
		return fmt.Errorf("marshal settlement dead letter: %w", err)
	}
	if err := s.pubSub.Publish(ctx, s.deadLetterTopic, payload); err != nil {
		return fmt.Errorf("publish settlement dead letter: %w", err)
	}
	if err := s.subscription.Ack(ctx); err != nil {
		return fmt.Errorf("acknowledge dead-lettered settlement event: %w", err)
	}
	slog.ErrorContext(
		ctx,
		"settlement event moved to dead letter topic",
		"topic",
		s.deadLetterTopic,
		"error",
		reason,
	)
	return nil
}

func isPermanentSettlementError(err error) bool {
	return errors.Is(err, settlement.ErrEventNotFound) ||
		errors.Is(err, settlement.ErrEventUnresolved) ||
		errors.Is(err, settlement.ErrOutcomeNotOption) ||
		errors.Is(err, settlement.ErrOrderWalletNotFound)
}

func decodeResolvedEvent(
	message []byte,
) (*messaging.Envelope[messaging.EventResolved], error) {
	var event messaging.Envelope[messaging.EventResolved]
	if err := json.Unmarshal(message, &event); err != nil {
		return nil, fmt.Errorf("%w: decode JSON: %v", ErrInvalidMessage, err)
	}
	valid := event.Type == messaging.EventResolvedType &&
		event.Data.EventID != "" && event.Data.Outcome != "" && !event.Timestamp.IsZero()
	if !valid {
		return nil, ErrInvalidMessage
	}
	return &event, nil
}

func waitForRetry(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
