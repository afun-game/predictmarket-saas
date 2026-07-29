// Package messaging defines durable integration event contracts.
package messaging

import (
	"encoding/base64"
	"time"
)

const (
	EventResolvedType  = "event_resolved"
	EventResolvedTopic = "predictmarket.event_resolved"
	DeadLetterTopic    = "predictmarket.event_resolved.dead_letter"
)

// Envelope is the common JSON shape published to the event stream.
type Envelope[T any] struct {
	Type      string    `json:"type"`
	Timestamp time.Time `json:"timestamp"`
	Data      T         `json:"data"`
}

// EventResolved identifies the resolved event and authoritative outcome.
type EventResolved struct {
	EventID string `json:"event_id"`
	Outcome string `json:"outcome"`
}

// DeadLetter records an event payload that could not be processed safely.
// Payload is base64 encoded so malformed JSON can be retained for diagnosis.
type DeadLetter struct {
	SourceTopic   string    `json:"source_topic"`
	PayloadBase64 string    `json:"payload_base64"`
	Reason        string    `json:"reason"`
	FailedAt      time.Time `json:"failed_at"`
}

// NewEventResolved creates an event resolution integration event.
func NewEventResolved(eventID, outcome string, timestamp time.Time) Envelope[EventResolved] {
	return Envelope[EventResolved]{
		Type:      EventResolvedType,
		Timestamp: timestamp,
		Data: EventResolved{
			EventID: eventID,
			Outcome: outcome,
		},
	}
}

// NewDeadLetter preserves the original message and its terminal processing error.
func NewDeadLetter(sourceTopic string, payload []byte, reason string, failedAt time.Time) DeadLetter {
	return DeadLetter{
		SourceTopic:   sourceTopic,
		PayloadBase64: base64.StdEncoding.EncodeToString(payload),
		Reason:        reason,
		FailedAt:      failedAt,
	}
}
