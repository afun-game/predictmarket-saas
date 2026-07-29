package infra

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nxsky/twill/runtime/resource"
)

const (
	natsStreamName       = "PREDICTMARKET_EVENTS"
	natsSubjects         = "predictmarket.>"
	maxEventAge          = 30 * 24 * time.Hour
	ackWait              = 30 * time.Second
	maxDeliveries        = 20
	natsOperationTimeout = 5 * time.Second
)

// PendingMessageInspector exposes durable-message depth for operational
// checks. It deliberately reports stream state instead of consuming messages.
type PendingMessageInspector interface {
	PendingMessages(ctx context.Context, topic string) (int64, error)
}

// RegisterNATSPubSubProvider configures Twill pub/sub resources to use NATS JetStream.
func RegisterNATSPubSubProvider() {
	resource.RegisterPubSubProvider(natsPubSubProvider{})
}

type natsPubSubProvider struct{}

func (natsPubSubProvider) Open(config resource.Config) (resource.PubSub, error) {
	if config.Type != "nats" && config.Type != "jetstream" {
		return nil, fmt.Errorf("unsupported pubsub type %q", config.Type)
	}
	return NewNATSPubSub(config.DSN)
}

// NewNATSPubSub opens a NATS connection and ensures the application stream exists.
func NewNATSPubSub(url string) (resource.PubSub, error) {
	connection, err := nats.Connect(url, nats.Name("predictmarket-saas"))
	if err != nil {
		return nil, fmt.Errorf("connect NATS: %w", err)
	}
	jetStream, err := connection.JetStream()
	if err != nil {
		connection.Close()
		return nil, fmt.Errorf("open NATS JetStream: %w", err)
	}
	if err := ensureEventStream(jetStream); err != nil {
		connection.Close()
		return nil, err
	}
	return &natsPubSub{connection: connection, jetStream: jetStream}, nil
}

func ensureEventStream(jetStream nats.JetStreamContext) error {
	if _, err := jetStream.StreamInfo(natsStreamName); err == nil {
		return nil
	} else if !errors.Is(err, nats.ErrStreamNotFound) {
		return fmt.Errorf("inspect NATS event stream: %w", err)
	}
	_, err := jetStream.AddStream(&nats.StreamConfig{
		Name:      natsStreamName,
		Subjects:  []string{natsSubjects},
		Retention: nats.LimitsPolicy,
		Storage:   nats.FileStorage,
		MaxAge:    maxEventAge,
		Discard:   nats.DiscardOld,
	})
	if err != nil {
		return fmt.Errorf("create NATS event stream: %w", err)
	}
	return nil
}

type natsPubSub struct {
	connection *nats.Conn
	jetStream  nats.JetStreamContext
}

func (p *natsPubSub) Publish(ctx context.Context, topic string, message []byte) error {
	operationCtx, cancel := context.WithTimeout(ctx, natsOperationTimeout)
	defer cancel()
	_, err := p.jetStream.Publish(topic, message, nats.Context(operationCtx))
	if err != nil {
		return fmt.Errorf("publish NATS topic %q: %w", topic, err)
	}
	return nil
}

func (p *natsPubSub) Subscribe(
	ctx context.Context,
	topic string,
	subscription string,
) (resource.Subscription, error) {
	consumer, err := ensureConsumer(p.jetStream, topic, subscription)
	if err != nil {
		return nil, err
	}
	natsSub, err := p.jetStream.PullSubscribe(
		topic,
		consumer,
		nats.Bind(natsStreamName, consumer),
	)
	if err != nil {
		return nil, fmt.Errorf("bind NATS consumer %q: %w", consumer, err)
	}
	return &natsSubscription{subscription: natsSub}, nil
}

// PendingMessages reports the number of retained messages for a subject.
// The settlement dead-letter subject has no consumer by design, so retained
// stream depth is the actionable alert signal.
func (p *natsPubSub) PendingMessages(ctx context.Context, topic string) (int64, error) {
	operationCtx, cancel := context.WithTimeout(ctx, natsOperationTimeout)
	defer cancel()
	info, err := p.jetStream.StreamInfo(natsStreamName, nats.Context(operationCtx))
	if err != nil {
		return 0, fmt.Errorf("inspect NATS stream %q: %w", natsStreamName, err)
	}
	return int64(info.State.Subjects[topic]), nil
}

func ensureConsumer(
	jetStream nats.JetStreamContext,
	topic string,
	consumer string,
) (string, error) {
	info, err := jetStream.ConsumerInfo(natsStreamName, consumer)
	if err == nil {
		if info.Config.FilterSubject != topic {
			return "", fmt.Errorf(
				"NATS consumer %q filters %q, not %q",
				consumer,
				info.Config.FilterSubject,
				topic,
			)
		}
		return info.Name, nil
	}
	if !errors.Is(err, nats.ErrConsumerNotFound) {
		return "", fmt.Errorf("inspect NATS consumer %q: %w", consumer, err)
	}
	info, err = jetStream.AddConsumer(natsStreamName, &nats.ConsumerConfig{
		Durable:       consumer,
		AckPolicy:     nats.AckExplicitPolicy,
		AckWait:       ackWait,
		MaxDeliver:    maxDeliveries,
		FilterSubject: topic,
		ReplayPolicy:  nats.ReplayInstantPolicy,
	})
	if err != nil {
		return "", fmt.Errorf("create NATS consumer %q: %w", consumer, err)
	}
	return info.Name, nil
}

type natsSubscription struct {
	subscription *nats.Subscription

	mu      sync.Mutex
	pending *nats.Msg
}

func (s *natsSubscription) Next(ctx context.Context) ([]byte, error) {
	s.mu.Lock()
	if s.pending != nil {
		s.mu.Unlock()
		return nil, errors.New("NATS subscription has an unacknowledged message")
	}
	s.mu.Unlock()

	for {
		operationCtx, cancel := context.WithTimeout(ctx, natsOperationTimeout)
		messages, err := s.subscription.Fetch(1, nats.Context(operationCtx))
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			isIdleTimeout := errors.Is(err, context.DeadlineExceeded) || errors.Is(err, nats.ErrTimeout)
			if isIdleTimeout {
				continue
			}
			return nil, fmt.Errorf("fetch NATS message: %w", err)
		}
		if len(messages) != 1 {
			return nil, fmt.Errorf("fetch NATS message: received %d messages", len(messages))
		}
		s.mu.Lock()
		s.pending = messages[0]
		s.mu.Unlock()
		return append([]byte{}, messages[0].Data...), nil
	}
}

func (s *natsSubscription) Ack(ctx context.Context) error {
	s.mu.Lock()
	message := s.pending
	s.mu.Unlock()
	if message == nil {
		return nil
	}
	operationCtx, cancel := context.WithTimeout(ctx, natsOperationTimeout)
	defer cancel()
	if err := message.AckSync(nats.Context(operationCtx)); err != nil {
		return fmt.Errorf("acknowledge NATS message: %w", err)
	}
	s.mu.Lock()
	if s.pending == message {
		s.pending = nil
	}
	s.mu.Unlock()
	return nil
}

func (s *natsSubscription) Close() error {
	if err := s.subscription.Drain(); err != nil {
		return fmt.Errorf("drain NATS subscription: %w", err)
	}
	return nil
}
