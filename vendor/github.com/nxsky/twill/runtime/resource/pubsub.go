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
)

// PubSub is a publish/subscribe messaging abstraction. A Topic is a named
// destination for messages; a Subscription consumes messages from a topic.
type PubSub interface {
	// Publish sends a message to the named topic.
	Publish(ctx context.Context, topic string, message []byte) error
	// Subscribe returns a Subscription that receives messages from the named
	// topic. The subscription is identified by name within the process.
	Subscribe(ctx context.Context, topic, subscription string) (Subscription, error)
}

// Subscription receives messages from a topic.
//
// Delivery is at-least-once for backends that support acknowledgements:
// callers must process a message returned by Next and then call Ack. If the
// process crashes after Next and before Ack, the backend may redeliver the
// message. In-memory subscriptions treat Ack as a no-op.
type Subscription interface {
	// Next blocks until the next message is available or ctx is canceled.
	// The message is not acknowledged until Ack succeeds.
	Next(ctx context.Context) ([]byte, error)
	// Ack acknowledges the most recent message returned by Next. It is a
	// no-op when there is no outstanding message (including memory backends).
	Ack(ctx context.Context) error
	// Close stops the subscription. Outstanding unacked messages remain
	// pending on backends that track acknowledgements.
	Close() error
}

// NewMemoryPubSub returns a process-local in-memory pub/sub implementation
// useful for tests and local development. It is not suitable for production or
// multi-process use.
func NewMemoryPubSub() PubSub {
	return &memoryPubSub{
		topics: map[string]*memoryTopic{},
	}
}

type memoryPubSub struct {
	mu     sync.Mutex
	topics map[string]*memoryTopic
}

type memoryTopic struct {
	mu          sync.Mutex
	queues      map[string]chan []byte // subscription name -> channel
	nextQueueID int
}

func (p *memoryPubSub) topic(name string) *memoryTopic {
	p.mu.Lock()
	defer p.mu.Unlock()
	t, ok := p.topics[name]
	if !ok {
		t = &memoryTopic{queues: map[string]chan []byte{}}
		p.topics[name] = t
	}
	return t
}

func (p *memoryPubSub) Publish(ctx context.Context, topic string, message []byte) error {
	t := p.topic(topic)
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, ch := range t.queues {
		select {
		case ch <- message:
		default:
			// Drop if the subscriber is slow. Local dev only.
		}
	}
	return nil
}

func (p *memoryPubSub) Subscribe(ctx context.Context, topic, subscription string) (Subscription, error) {
	t := p.topic(topic)
	t.mu.Lock()
	defer t.mu.Unlock()
	ch, ok := t.queues[subscription]
	if !ok {
		ch = make(chan []byte, 64)
		t.queues[subscription] = ch
	}
	return &memorySubscription{ch: ch}, nil
}

type memorySubscription struct {
	ch chan []byte
}

func (s *memorySubscription) Next(ctx context.Context) ([]byte, error) {
	select {
	case msg, ok := <-s.ch:
		if !ok {
			return nil, fmt.Errorf("subscription closed")
		}
		return msg, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *memorySubscription) Ack(ctx context.Context) error {
	return nil
}

func (s *memorySubscription) Close() error {
	return nil
}
