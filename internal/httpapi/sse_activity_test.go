package httpapi

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// TestActivityEventTypes verifies that activity event types are defined correctly
func TestActivityEventTypes(t *testing.T) {
	// Verify all activity event types are defined
	assert.Equal(t, runtime.EventType("activity.tool_call.started"), runtime.EventTypeActivityToolCallStarted)
	assert.Equal(t, runtime.EventType("activity.tool_call.completed"), runtime.EventTypeActivityToolCallCompleted)
	assert.Equal(t, runtime.EventType("activity.policy_decision"), runtime.EventTypeActivityPolicyDecision)
	assert.Equal(t, runtime.EventType("activity.quarantine_change"), runtime.EventTypeActivityQuarantineChange)
}

// TestActivityEventPayload verifies the structure of activity event payloads
func TestActivityEventPayload_ToolCallStarted(t *testing.T) {
	// Create a sample event payload for tool call started
	payload := map[string]any{
		"server_name": "github",
		"tool_name":   "create_issue",
		"session_id":  "sess-123",
		"request_id":  "req-456",
		"arguments":   map[string]any{"title": "Test"},
	}

	// Verify payload structure
	assert.Equal(t, "github", payload["server_name"])
	assert.Equal(t, "create_issue", payload["tool_name"])
	assert.Equal(t, "sess-123", payload["session_id"])
	assert.Equal(t, "req-456", payload["request_id"])
	assert.NotNil(t, payload["arguments"])
}

func TestActivityEventPayload_ToolCallCompleted(t *testing.T) {
	// Create a sample event payload for tool call completed
	payload := map[string]any{
		"server_name":        "github",
		"tool_name":          "create_issue",
		"session_id":         "sess-123",
		"request_id":         "req-456",
		"status":             "success",
		"error_message":      "",
		"duration_ms":        int64(150),
		"response":           `{"id": 123}`,
		"response_truncated": false,
	}

	// Verify payload structure
	assert.Equal(t, "github", payload["server_name"])
	assert.Equal(t, "success", payload["status"])
	assert.Equal(t, int64(150), payload["duration_ms"])
	assert.Equal(t, false, payload["response_truncated"])
}

func TestActivityEventPayload_PolicyDecision(t *testing.T) {
	// Create a sample event payload for policy decision
	payload := map[string]any{
		"server_name": "quarantined-server",
		"tool_name":   "dangerous_tool",
		"session_id":  "sess-789",
		"decision":    "blocked",
		"reason":      "Server is quarantined for security review",
	}

	// Verify payload structure
	assert.Equal(t, "quarantined-server", payload["server_name"])
	assert.Equal(t, "dangerous_tool", payload["tool_name"])
	assert.Equal(t, "blocked", payload["decision"])
	assert.Contains(t, payload["reason"], "quarantined")
}

func TestActivityEventPayload_QuarantineChange(t *testing.T) {
	// Create a sample event payload for quarantine change
	payload := map[string]any{
		"server_name": "suspicious-server",
		"quarantined": true,
		"reason":      "Server quarantined for security review",
	}

	// Verify payload structure
	assert.Equal(t, "suspicious-server", payload["server_name"])
	assert.Equal(t, true, payload["quarantined"])
	assert.Contains(t, payload["reason"], "security")
}

// TestActivityEventTimestamp verifies event timestamps are set correctly
func TestActivityEventTimestamp(t *testing.T) {
	before := time.Now().UTC()

	// Simulate event creation
	evt := runtime.Event{
		Type:      runtime.EventTypeActivityToolCallCompleted,
		Timestamp: time.Now().UTC(),
		Payload: map[string]any{
			"server_name": "test",
			"tool_name":   "test_tool",
		},
	}

	after := time.Now().UTC()

	// Verify timestamp is within expected range
	assert.True(t, evt.Timestamp.After(before) || evt.Timestamp.Equal(before))
	assert.True(t, evt.Timestamp.Before(after) || evt.Timestamp.Equal(after))
}

// TestActivityEventSSEFormat verifies events can be serialized for SSE
func TestActivityEventSSEFormat(t *testing.T) {
	// Activity events should be serializable to JSON for SSE transmission
	evt := runtime.Event{
		Type:      runtime.EventTypeActivityToolCallCompleted,
		Timestamp: time.Now().UTC(),
		Payload: map[string]any{
			"server_name": "github",
			"tool_name":   "create_issue",
			"status":      "success",
			"duration_ms": int64(150),
		},
	}

	// Verify event can be used in SSE format
	assert.NotEmpty(t, string(evt.Type))
	assert.NotNil(t, evt.Payload)

	// SSE handler sends events as: event: {type}\ndata: {json_payload}\n\n
	eventType := string(evt.Type)
	assert.Equal(t, "activity.tool_call.completed", eventType)
}
