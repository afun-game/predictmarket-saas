package settlementworker

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/afun-game/predictmarket-saas/internal/messaging"
	"github.com/afun-game/predictmarket-saas/internal/settlement"
	"github.com/nxsky/twill/runtime/resource"
)

func TestDispatch(t *testing.T) {
	repository := &fakeOutboxRepository{count: 2}
	pubSub := &fakePubSub{}
	service := newService(repository, pubSub, &fakeSettlementService{})
	count, err := service.Dispatch(context.Background())
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if count != 2 || repository.publisher != pubSub {
		t.Errorf("Dispatch() = %d, publisher configured = %v", count, repository.publisher == pubSub)
	}
}

func TestProcessUntilAcknowledged(t *testing.T) {
	t.Run("settles and acknowledges", func(t *testing.T) {
		settler := &fakeSettlementService{}
		subscription := &fakeSubscription{}
		service := newService(&fakeOutboxRepository{}, &fakePubSub{}, settler)
		service.subscription = subscription
		message := resolvedEventJSON(t, "00000000-0000-4000-8000-000000000001")
		if err := service.processUntilAcknowledged(context.Background(), message); err != nil {
			t.Fatalf("processUntilAcknowledged() error = %v", err)
		}
		if settler.calls != 1 || subscription.acks != 1 {
			t.Errorf("settlement calls = %d, acks = %d, want 1, 1", settler.calls, subscription.acks)
		}
	})

	t.Run("acknowledges poison message", func(t *testing.T) {
		subscription := &fakeSubscription{}
		pubSub := &fakePubSub{}
		service := newService(
			&fakeOutboxRepository{},
			pubSub,
			&fakeSettlementService{},
		)
		service.subscription = subscription
		err := service.processUntilAcknowledged(context.Background(), []byte("not-json"))
		if err != nil || subscription.acks != 1 {
			t.Errorf("error = %v, acks = %d, want nil and one ack", err, subscription.acks)
		}
		assertDeadLetter(t, pubSub, "invalid event resolution message")
	})

	t.Run("dead letters permanent settlement failure", func(t *testing.T) {
		settler := &fakeSettlementService{err: settlement.ErrOutcomeNotOption}
		subscription := &fakeSubscription{}
		pubSub := &fakePubSub{}
		service := newService(&fakeOutboxRepository{}, pubSub, settler)
		service.subscription = subscription
		if err := service.processUntilAcknowledged(
			context.Background(),
			resolvedEventJSON(t, "00000000-0000-4000-8000-000000000003"),
		); err != nil {
			t.Fatalf("processUntilAcknowledged() error = %v", err)
		}
		if settler.calls != 1 || subscription.acks != 1 {
			t.Errorf("settlement calls = %d, acks = %d, want 1, 1", settler.calls, subscription.acks)
		}
		assertDeadLetter(t, pubSub, settlement.ErrOutcomeNotOption.Error())
	})

	t.Run("dead letters after bounded transient retries", func(t *testing.T) {
		settler := &fakeSettlementService{err: errors.New("database unavailable")}
		subscription := &fakeSubscription{}
		pubSub := &fakePubSub{}
		service := newService(&fakeOutboxRepository{}, pubSub, settler)
		service.subscription = subscription
		service.retryInitial = time.Millisecond
		service.retryMaxDelay = time.Millisecond
		service.retryAttempts = 3
		if err := service.processUntilAcknowledged(
			context.Background(),
			resolvedEventJSON(t, "00000000-0000-4000-8000-000000000004"),
		); err != nil {
			t.Fatalf("processUntilAcknowledged() error = %v", err)
		}
		if settler.calls != 3 || subscription.acks != 1 {
			t.Errorf("settlement calls = %d, acks = %d, want 3, 1", settler.calls, subscription.acks)
		}
		assertDeadLetter(t, pubSub, "retry limit reached")
	})

	t.Run("does not acknowledge when dead letter publishing fails", func(t *testing.T) {
		subscription := &fakeSubscription{}
		pubSub := &fakePubSub{publishErr: errors.New("NATS unavailable")}
		service := newService(&fakeOutboxRepository{}, pubSub, &fakeSettlementService{})
		service.subscription = subscription
		err := service.processUntilAcknowledged(context.Background(), []byte("not-json"))
		if err == nil || subscription.acks != 0 {
			t.Errorf("error = %v, acks = %d, want publish failure and zero acks", err, subscription.acks)
		}
	})

	t.Run("does not acknowledge settlement failure", func(t *testing.T) {
		settler := &fakeSettlementService{err: errors.New("database unavailable")}
		subscription := &fakeSubscription{}
		service := newService(&fakeOutboxRepository{}, &fakePubSub{}, settler)
		service.subscription = subscription
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		err := service.processUntilAcknowledged(
			ctx,
			resolvedEventJSON(t, "00000000-0000-4000-8000-000000000002"),
		)
		if !errors.Is(err, context.DeadlineExceeded) || subscription.acks != 0 {
			t.Errorf("error = %v, acks = %d, want deadline and zero acks", err, subscription.acks)
		}
	})

	t.Run("dead letter does not block the next event", func(t *testing.T) {
		permanentEventID := "00000000-0000-4000-8000-000000000005"
		successfulEventID := "00000000-0000-4000-8000-000000000006"
		settler := &fakeSettlementService{
			errorsByEvent: map[string]error{permanentEventID: settlement.ErrOutcomeNotOption},
			processed:     make(chan struct{}, 2),
		}
		subscription := &sequenceSubscription{
			messages: [][]byte{
				resolvedEventJSON(t, permanentEventID),
				resolvedEventJSON(t, successfulEventID),
			},
		}
		pubSub := &fakePubSub{}
		service := newService(&fakeOutboxRepository{}, pubSub, settler)
		service.subscription = subscription
		ctx, cancel := context.WithCancel(context.Background())
		finished := make(chan struct{})
		go func() {
			service.runConsumer(ctx)
			close(finished)
		}()

		for range 2 {
			select {
			case <-settler.processed:
			case <-time.After(time.Second):
				cancel()
				t.Fatal("consumer did not process the event following the dead letter")
			}
		}
		cancel()
		<-finished
		if subscription.acks != 2 {
			t.Errorf("acks = %d, want 2", subscription.acks)
		}
		assertDeadLetter(t, pubSub, settlement.ErrOutcomeNotOption.Error())
	})
}

func assertDeadLetter(t *testing.T, pubSub *fakePubSub, wantReason string) {
	t.Helper()
	if len(pubSub.published) != 1 {
		t.Fatalf("dead-letter publishes = %d, want 1", len(pubSub.published))
	}
	published := pubSub.published[0]
	if published.topic != messaging.DeadLetterTopic {
		t.Fatalf("dead-letter topic = %q, want %q", published.topic, messaging.DeadLetterTopic)
	}
	var deadLetter messaging.DeadLetter
	if err := json.Unmarshal(published.message, &deadLetter); err != nil {
		t.Fatalf("decode dead letter: %v", err)
	}
	if !strings.Contains(deadLetter.Reason, wantReason) || deadLetter.PayloadBase64 == "" {
		t.Errorf("dead letter = %#v", deadLetter)
	}
}

func resolvedEventJSON(t *testing.T, eventID string) []byte {
	t.Helper()
	message, err := json.Marshal(messaging.NewEventResolved(eventID, "Yes", time.Now().UTC()))
	if err != nil {
		t.Fatalf("marshal resolved event: %v", err)
	}
	return message
}

type fakeOutboxRepository struct {
	count     int
	err       error
	publisher resource.PubSub
}

func (r *fakeOutboxRepository) Dispatch(
	_ context.Context,
	_ int,
	publisher resource.PubSub,
) (int, error) {
	r.publisher = publisher
	return r.count, r.err
}

type fakeSettlementService struct {
	calls         int
	err           error
	errorsByEvent map[string]error
	processed     chan struct{}
}

func (s *fakeSettlementService) SettleEvent(_ context.Context, eventID string) error {
	s.calls++
	if s.processed != nil {
		s.processed <- struct{}{}
	}
	if err := s.errorsByEvent[eventID]; err != nil {
		return err
	}
	return s.err
}

type publishedMessage struct {
	topic   string
	message []byte
}

type fakePubSub struct {
	published  []publishedMessage
	publishErr error
}

func (p *fakePubSub) Publish(_ context.Context, topic string, message []byte) error {
	if p.publishErr != nil {
		return p.publishErr
	}
	p.published = append(p.published, publishedMessage{topic: topic, message: message})
	return nil
}

func (*fakePubSub) Subscribe(
	context.Context,
	string,
	string,
) (resource.Subscription, error) {
	return &fakeSubscription{}, nil
}

type fakeSubscription struct {
	acks int
}

func (*fakeSubscription) Next(context.Context) ([]byte, error) { return nil, errors.New("unused") }

func (s *fakeSubscription) Ack(context.Context) error {
	s.acks++
	return nil
}

func (*fakeSubscription) Close() error { return nil }

type sequenceSubscription struct {
	messages [][]byte
	next     int
	acks     int
}

func (s *sequenceSubscription) Next(ctx context.Context) ([]byte, error) {
	if s.next < len(s.messages) {
		message := s.messages[s.next]
		s.next++
		return message, nil
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func (s *sequenceSubscription) Ack(context.Context) error {
	s.acks++
	return nil
}

func (*sequenceSubscription) Close() error { return nil }
