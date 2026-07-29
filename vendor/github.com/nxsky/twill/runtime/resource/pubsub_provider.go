// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package resource

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// PubSubProvider creates a production PubSub handle (for example Redis Streams
// or SQS). Users register a provider via RegisterPubSubProvider to replace the
// default in-memory pub/sub.
//
// The provider is called once per resource name and the result is
// singleton-managed by the Manager.
type PubSubProvider interface {
	// Open creates a PubSub handle for the given resource config. The provider
	// should use cfg.DSN as the connection address and cfg.Type to select an
	// implementation (for example "redis", "redis-streams", "sqs").
	Open(cfg Config) (PubSub, error)
}

var pubsubProvider PubSubProvider

// RegisterPubSubProvider registers a production pub/sub provider. Call once at
// program startup, before twill.Run. If no provider is registered, the default
// in-memory pub/sub is used.
//
// Example with a Redis Streams adapter:
//
//	type redisStreamsProvider struct{ client resource.StreamClient }
//	func (p redisStreamsProvider) Open(cfg resource.Config) (resource.PubSub, error) {
//	    return resource.NewStreamPubSub(p.client), nil
//	}
//	func init() {
//	    resource.RegisterPubSubProvider(redisStreamsProvider{client: myClient})
//	}
func RegisterPubSubProvider(p PubSubProvider) {
	pubsubProvider = p
}

// StreamMessage is one message read from a stream-backed pub/sub.
type StreamMessage struct {
	ID     string
	Values map[string]string
}

// StreamClient is the minimal interface for a Redis Streams-like backend.
// Users implement this to bridge go-redis, redigo, or another client without
// adding a direct dependency to the runtime.
type StreamClient interface {
	// XAdd appends a message to the named stream and returns the entry ID.
	XAdd(ctx context.Context, stream string, values map[string]string) (string, error)
	// XGroupCreateMkStream creates a consumer group, creating the stream if it
	// does not exist. Implementations should treat "BUSYGROUP" (group already
	// exists) as success.
	XGroupCreateMkStream(ctx context.Context, stream, group, start string) error
	// XReadGroup reads up to count messages for the consumer group. Block for
	// the given duration when no messages are available; a zero or negative
	// block means the implementation default.
	XReadGroup(ctx context.Context, group, consumer, stream string, count int64, block time.Duration) ([]StreamMessage, error)
	// XAck acknowledges processed message IDs.
	XAck(ctx context.Context, stream, group string, ids ...string) error
}

const streamPayloadField = "payload"

// StreamPubSub is a PubSub backed by a StreamClient (typically Redis Streams).
// Topic names map to stream keys. Subscription names map to consumer groups;
// each Subscribe call uses a unique consumer name within the group so that
// competing consumers can share work.
//
// Delivery is at-least-once: Next leaves the message pending until Ack. If a
// consumer crashes after Next and before Ack, the entry remains in the
// consumer group's pending list for redelivery (via the same consumer name or
// backend claim tooling such as XAUTOCLAIM).
type StreamPubSub struct {
	client StreamClient

	mu        sync.Mutex
	consumers map[string]int // group key -> next consumer id
}

// NewStreamPubSub returns a PubSub backed by the provided StreamClient.
func NewStreamPubSub(client StreamClient) *StreamPubSub {
	return &StreamPubSub{
		client:    client,
		consumers: map[string]int{},
	}
}

// Publish implements PubSub.
func (p *StreamPubSub) Publish(ctx context.Context, topic string, message []byte) error {
	if p == nil || p.client == nil {
		return fmt.Errorf("stream pubsub: nil client")
	}
	_, err := p.client.XAdd(ctx, topic, map[string]string{
		streamPayloadField: string(message),
	})
	if err != nil {
		return fmt.Errorf("stream publish %q: %w", topic, err)
	}
	return nil
}

// Subscribe implements PubSub.
func (p *StreamPubSub) Subscribe(ctx context.Context, topic, subscription string) (Subscription, error) {
	if p == nil || p.client == nil {
		return nil, fmt.Errorf("stream pubsub: nil client")
	}
	if err := p.client.XGroupCreateMkStream(ctx, topic, subscription, "0"); err != nil {
		return nil, fmt.Errorf("stream subscribe create group %q/%q: %w", topic, subscription, err)
	}
	consumer := p.nextConsumer(topic, subscription)
	return &streamSubscription{
		client:       p.client,
		stream:       topic,
		group:        subscription,
		consumer:     consumer,
		pending:      nil,
		pendingIndex: 0,
	}, nil
}

func (p *StreamPubSub) nextConsumer(topic, subscription string) string {
	key := topic + "\x00" + subscription
	p.mu.Lock()
	defer p.mu.Unlock()
	n := p.consumers[key]
	p.consumers[key] = n + 1
	return fmt.Sprintf("%s-%d", subscription, n)
}

type streamSubscription struct {
	client       StreamClient
	stream       string
	group        string
	consumer     string
	pending      []StreamMessage
	pendingIndex int
	// unackedID is the ID of the last message returned by Next that has not
	// yet been acknowledged. Empty when there is no outstanding message.
	unackedID string
	closed    bool
	mu        sync.Mutex
}

// Next implements Subscription. The returned message stays unacked until Ack.
// Calling Next again while a previous message is unacked returns an error so
// callers cannot silently drop work.
func (s *streamSubscription) Next(ctx context.Context) ([]byte, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, fmt.Errorf("subscription closed")
	}
	if s.unackedID != "" {
		s.mu.Unlock()
		return nil, fmt.Errorf("subscription has unacked message %q; call Ack before Next", s.unackedID)
	}
	if s.pendingIndex < len(s.pending) {
		msg := s.pending[s.pendingIndex]
		s.pendingIndex++
		s.unackedID = msg.ID
		s.mu.Unlock()
		return []byte(msg.Values[streamPayloadField]), nil
	}
	s.mu.Unlock()

	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		msgs, err := s.client.XReadGroup(ctx, s.group, s.consumer, s.stream, 1, time.Second)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("stream read %q/%q: %w", s.stream, s.group, err)
		}
		if len(msgs) == 0 {
			continue
		}
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return nil, fmt.Errorf("subscription closed")
		}
		if s.unackedID != "" {
			// Concurrent Next is not supported; keep the batch for later.
			s.pending = append(msgs, s.pending[s.pendingIndex:]...)
			s.pendingIndex = 0
			s.mu.Unlock()
			return nil, fmt.Errorf("subscription has unacked message %q; call Ack before Next", s.unackedID)
		}
		s.pending = msgs
		s.pendingIndex = 1
		s.unackedID = msgs[0].ID
		payload := msgs[0].Values[streamPayloadField]
		s.mu.Unlock()
		return []byte(payload), nil
	}
}

// Ack implements Subscription. It acknowledges the message last returned by
// Next only after the caller has finished processing it.
func (s *streamSubscription) Ack(ctx context.Context) error {
	s.mu.Lock()
	id := s.unackedID
	if id == "" {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	if err := s.client.XAck(ctx, s.stream, s.group, id); err != nil {
		return fmt.Errorf("stream ack %q: %w", id, err)
	}

	s.mu.Lock()
	// Only clear if still the same outstanding id (no concurrent Ack/Next).
	if s.unackedID == id {
		s.unackedID = ""
	}
	s.mu.Unlock()
	return nil
}

// Close implements Subscription.
func (s *streamSubscription) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}
