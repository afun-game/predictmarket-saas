---
name: event_stream
type: pubsub
description: Event streaming for real-time updates
---

# Event Stream

## Provider
NATS JetStream

## Lifecycle
External - managed outside the application

## Topics

### market_updates
- Event: Market price changes
- Subscribers: Analytics service, WebSocket server
- Retention: 24 hours

### order_filled
- Event: Order execution completed
- Subscribers: Wallet service, Analytics service
- Retention: 7 days

### event_resolved
- Event: Prediction event resolved with outcome
- NATS subject: `predictmarket.event_resolved`
- Durable consumer: `market-settlement`
- Producer: Event Service transactional outbox
- Subscriber: Settlement Worker
- Delivery: At least once; settlement is idempotent
- Retention: 30 days

### event_resolved.dead_letter
- NATS subject: `predictmarket.event_resolved.dead_letter`
- Producer: Settlement Worker
- Payload: source topic, terminal failure reason, UTC failure time, and the
  original payload as base64
- Handling: repair the underlying event or market data, then replay the stored
  original payload to `predictmarket.event_resolved`

## Message Format
All messages are JSON with schema:

```json
{
  "type": "market_update|order_filled|event_resolved",
  "timestamp": "2024-07-28T10:00:00Z",
  "data": {}
}
```

## Monitoring
Monitor consumer lag, throughput, redeliveries, and error rates per stream.

## Reliability

Event resolution and `event_outbox` insertion commit in the same PostgreSQL
transaction. The Settlement Worker publishes pending outbox rows to JetStream
and only marks them published after NATS confirms persistence. A crash between
those operations can redeliver a message, so consumers must remain idempotent.

The durable consumer settles markets independently within an event. Invalid
messages, permanent business failures, and failures that exhaust the bounded
retry budget are published to `predictmarket.event_resolved.dead_letter` before
the original message is acknowledged. Transient settlement errors use bounded
exponential backoff without acknowledging the message.
