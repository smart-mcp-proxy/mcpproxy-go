package runtime

import "time"

// EventType represents a runtime event category broadcast to subscribers.
type EventType string

const (
	// EventTypeServersChanged is emitted whenever the set of servers or their state changes.
	EventTypeServersChanged EventType = "servers.changed"
	// EventTypeConfigReloaded is emitted after configuration reload completes.
	EventTypeConfigReloaded EventType = "config.reloaded"
	// EventTypeConfigSaved is emitted after configuration is successfully saved to disk.
	EventTypeConfigSaved EventType = "config.saved"
	// EventTypeSecretsChanged is emitted when secrets are added, updated, or deleted.
	EventTypeSecretsChanged EventType = "secrets.changed"
)

// Event is a typed notification published by the runtime event bus.
type Event struct {
	Type      EventType      `json:"type"`
	Timestamp time.Time      `json:"timestamp"`
	Payload   map[string]any `json:"payload,omitempty"`
}

func newEvent(eventType EventType, payload map[string]any) Event {
	return Event{
		Type:      eventType,
		Timestamp: time.Now().UTC(),
		Payload:   payload,
	}
}
