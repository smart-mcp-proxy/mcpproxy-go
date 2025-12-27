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
	// EventTypeOAuthTokenRefreshed is emitted when proactive token refresh succeeds.
	EventTypeOAuthTokenRefreshed EventType = "oauth.token_refreshed"
	// EventTypeOAuthRefreshFailed is emitted when proactive token refresh fails after retries.
	EventTypeOAuthRefreshFailed EventType = "oauth.refresh_failed"

	// Activity logging events (RFC-003)
	// EventTypeActivityToolCallStarted is emitted when a tool execution begins.
	EventTypeActivityToolCallStarted EventType = "activity.tool_call.started"
	// EventTypeActivityToolCallCompleted is emitted when a tool execution finishes.
	EventTypeActivityToolCallCompleted EventType = "activity.tool_call.completed"
	// EventTypeActivityPolicyDecision is emitted when a policy blocks a tool call.
	EventTypeActivityPolicyDecision EventType = "activity.policy_decision"
	// EventTypeActivityQuarantineChange is emitted when a server's quarantine state changes.
	EventTypeActivityQuarantineChange EventType = "activity.quarantine_change"
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
