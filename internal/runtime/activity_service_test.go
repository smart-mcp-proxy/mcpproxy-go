package runtime

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// TestEmitActivitySystemStart verifies system_start event emission (Spec 024)
func TestEmitActivitySystemStart(t *testing.T) {
	logger, err := zap.NewDevelopment()
	require.NoError(t, err)
	defer logger.Sync()

	rt := &Runtime{
		logger:    logger,
		eventSubs: make(map[chan Event]struct{}),
	}

	// Subscribe to events
	eventChan := rt.SubscribeEvents()
	defer rt.UnsubscribeEvents(eventChan)

	done := make(chan Event, 1)

	// Listen for activity.system.start event
	go func() {
		select {
		case evt := <-eventChan:
			if evt.Type == EventTypeActivitySystemStart {
				done <- evt
			}
		case <-time.After(2 * time.Second):
			t.Log("Timeout waiting for activity.system.start event")
		}
	}()

	// Emit system start event
	rt.EmitActivitySystemStart("v1.2.3", "127.0.0.1:8080", 150, "/path/to/config.json")

	// Wait for event
	select {
	case evt := <-done:
		assert.Equal(t, EventTypeActivitySystemStart, evt.Type, "Event type should be activity.system.start")
		assert.NotNil(t, evt.Payload, "Event payload should not be nil")
		assert.Equal(t, "v1.2.3", evt.Payload["version"], "Event should contain version")
		assert.Equal(t, "127.0.0.1:8080", evt.Payload["listen_address"], "Event should contain listen_address")
		assert.Equal(t, int64(150), evt.Payload["startup_duration_ms"], "Event should contain startup_duration_ms")
		assert.Equal(t, "/path/to/config.json", evt.Payload["config_path"], "Event should contain config_path")
		assert.NotZero(t, evt.Timestamp, "Event should have a timestamp")
	case <-time.After(2 * time.Second):
		t.Fatal("Did not receive activity.system.start event within timeout")
	}
}

// TestEmitActivitySystemStop verifies system_stop event emission (Spec 024)
func TestEmitActivitySystemStop(t *testing.T) {
	logger, err := zap.NewDevelopment()
	require.NoError(t, err)
	defer logger.Sync()

	rt := &Runtime{
		logger:    logger,
		eventSubs: make(map[chan Event]struct{}),
	}

	// Subscribe to events
	eventChan := rt.SubscribeEvents()
	defer rt.UnsubscribeEvents(eventChan)

	done := make(chan Event, 1)

	// Listen for activity.system.stop event
	go func() {
		select {
		case evt := <-eventChan:
			if evt.Type == EventTypeActivitySystemStop {
				done <- evt
			}
		case <-time.After(2 * time.Second):
			t.Log("Timeout waiting for activity.system.stop event")
		}
	}()

	// Emit system stop event
	rt.EmitActivitySystemStop("signal", "SIGTERM", 3600, "")

	// Wait for event
	select {
	case evt := <-done:
		assert.Equal(t, EventTypeActivitySystemStop, evt.Type, "Event type should be activity.system.stop")
		assert.NotNil(t, evt.Payload, "Event payload should not be nil")
		assert.Equal(t, "signal", evt.Payload["reason"], "Event should contain reason")
		assert.Equal(t, "SIGTERM", evt.Payload["signal"], "Event should contain signal")
		assert.Equal(t, int64(3600), evt.Payload["uptime_seconds"], "Event should contain uptime_seconds")
		assert.Equal(t, "", evt.Payload["error_message"], "Event should contain error_message")
		assert.NotZero(t, evt.Timestamp, "Event should have a timestamp")
	case <-time.After(2 * time.Second):
		t.Fatal("Did not receive activity.system.stop event within timeout")
	}
}

// TestEmitActivitySystemStop_WithError verifies system_stop event includes error info
func TestEmitActivitySystemStop_WithError(t *testing.T) {
	logger, err := zap.NewDevelopment()
	require.NoError(t, err)
	defer logger.Sync()

	rt := &Runtime{
		logger:    logger,
		eventSubs: make(map[chan Event]struct{}),
	}

	// Subscribe to events
	eventChan := rt.SubscribeEvents()
	defer rt.UnsubscribeEvents(eventChan)

	done := make(chan Event, 1)

	// Listen for activity.system.stop event
	go func() {
		select {
		case evt := <-eventChan:
			if evt.Type == EventTypeActivitySystemStop {
				done <- evt
			}
		case <-time.After(2 * time.Second):
			t.Log("Timeout waiting for activity.system.stop event")
		}
	}()

	// Emit system stop event with error
	rt.EmitActivitySystemStop("error", "", 120, "database connection lost")

	// Wait for event
	select {
	case evt := <-done:
		assert.Equal(t, EventTypeActivitySystemStop, evt.Type)
		assert.Equal(t, "error", evt.Payload["reason"])
		assert.Equal(t, "", evt.Payload["signal"])
		assert.Equal(t, int64(120), evt.Payload["uptime_seconds"])
		assert.Equal(t, "database connection lost", evt.Payload["error_message"])
	case <-time.After(2 * time.Second):
		t.Fatal("Did not receive activity.system.stop event within timeout")
	}
}

// TestEmitActivityInternalToolCall verifies internal_tool_call event emission (Spec 024)
func TestEmitActivityInternalToolCall(t *testing.T) {
	logger, err := zap.NewDevelopment()
	require.NoError(t, err)
	defer logger.Sync()

	rt := &Runtime{
		logger:    logger,
		eventSubs: make(map[chan Event]struct{}),
	}

	// Subscribe to events
	eventChan := rt.SubscribeEvents()
	defer rt.UnsubscribeEvents(eventChan)

	done := make(chan Event, 1)

	// Listen for activity.internal_tool_call.completed event
	go func() {
		select {
		case evt := <-eventChan:
			if evt.Type == EventTypeActivityInternalToolCall {
				done <- evt
			}
		case <-time.After(2 * time.Second):
			t.Log("Timeout waiting for activity.internal_tool_call.completed event")
		}
	}()

	// Emit internal tool call event
	intent := map[string]interface{}{
		"operation_type":   "read",
		"data_sensitivity": "public",
	}
	testArgs := map[string]interface{}{
		"username": "octocat",
	}
	testResponse := map[string]interface{}{
		"login": "octocat",
		"id":    1,
	}
	rt.EmitActivityInternalToolCall(
		"call_tool_read",
		"github",
		"get_user",
		"call_tool_read",
		"sess-123",
		"req-456",
		"success",
		"",
		250,
		testArgs,
		testResponse,
		intent,
		"",
	)

	// Wait for event
	select {
	case evt := <-done:
		assert.Equal(t, EventTypeActivityInternalToolCall, evt.Type)
		assert.Equal(t, "call_tool_read", evt.Payload["internal_tool_name"])
		assert.Equal(t, "github", evt.Payload["target_server"])
		assert.Equal(t, "get_user", evt.Payload["target_tool"])
		assert.Equal(t, "call_tool_read", evt.Payload["tool_variant"])
		assert.Equal(t, "sess-123", evt.Payload["session_id"])
		assert.Equal(t, "req-456", evt.Payload["request_id"])
		assert.Equal(t, "success", evt.Payload["status"])
		assert.Equal(t, "", evt.Payload["error_message"])
		assert.Equal(t, int64(250), evt.Payload["duration_ms"])
		assert.NotNil(t, evt.Payload["intent"])
		// Verify arguments and response are captured
		assert.NotNil(t, evt.Payload["arguments"])
		args := evt.Payload["arguments"].(map[string]interface{})
		assert.Equal(t, "octocat", args["username"])
		assert.NotNil(t, evt.Payload["response"])
		resp := evt.Payload["response"].(map[string]interface{})
		assert.Equal(t, "octocat", resp["login"])
	case <-time.After(2 * time.Second):
		t.Fatal("Did not receive activity.internal_tool_call.completed event within timeout")
	}
}

// TestEmitActivityConfigChange verifies config_change event emission (Spec 024)
func TestEmitActivityConfigChange(t *testing.T) {
	logger, err := zap.NewDevelopment()
	require.NoError(t, err)
	defer logger.Sync()

	rt := &Runtime{
		logger:    logger,
		eventSubs: make(map[chan Event]struct{}),
	}

	// Subscribe to events
	eventChan := rt.SubscribeEvents()
	defer rt.UnsubscribeEvents(eventChan)

	done := make(chan Event, 1)

	// Listen for activity.config_change event
	go func() {
		select {
		case evt := <-eventChan:
			if evt.Type == EventTypeActivityConfigChange {
				done <- evt
			}
		case <-time.After(2 * time.Second):
			t.Log("Timeout waiting for activity.config_change event")
		}
	}()

	// Emit config change event
	prevValues := map[string]interface{}{"enabled": true}
	newValues := map[string]interface{}{"enabled": false}
	rt.EmitActivityConfigChange(
		"server_updated",
		"github",
		"mcp",
		[]string{"enabled"},
		prevValues,
		newValues,
	)

	// Wait for event
	select {
	case evt := <-done:
		assert.Equal(t, EventTypeActivityConfigChange, evt.Type)
		assert.Equal(t, "server_updated", evt.Payload["action"])
		assert.Equal(t, "github", evt.Payload["affected_entity"])
		assert.Equal(t, "mcp", evt.Payload["source"])
		assert.NotNil(t, evt.Payload["changed_fields"])
		assert.NotNil(t, evt.Payload["previous_values"])
		assert.NotNil(t, evt.Payload["new_values"])
	case <-time.After(2 * time.Second):
		t.Fatal("Did not receive activity.config_change event within timeout")
	}
}

// TestEmitSensitiveDataDetected verifies sensitive_data.detected event emission (Spec 026)
func TestEmitSensitiveDataDetected(t *testing.T) {
	logger, err := zap.NewDevelopment()
	require.NoError(t, err)
	defer logger.Sync()

	rt := &Runtime{
		logger:    logger,
		eventSubs: make(map[chan Event]struct{}),
	}

	// Subscribe to events
	eventChan := rt.SubscribeEvents()
	defer rt.UnsubscribeEvents(eventChan)

	done := make(chan Event, 1)

	// Listen for sensitive_data.detected event
	go func() {
		select {
		case evt := <-eventChan:
			if evt.Type == EventTypeSensitiveDataDetected {
				done <- evt
			}
		case <-time.After(2 * time.Second):
			t.Log("Timeout waiting for sensitive_data.detected event")
		}
	}()

	// Emit sensitive data detected event
	detectionTypes := []string{"credit_card", "api_key"}
	rt.EmitSensitiveDataDetected(
		"activity-123",
		3,
		"high",
		detectionTypes,
	)

	// Wait for event
	select {
	case evt := <-done:
		assert.Equal(t, EventTypeSensitiveDataDetected, evt.Type, "Event type should be sensitive_data.detected")
		assert.NotNil(t, evt.Payload, "Event payload should not be nil")
		assert.Equal(t, "activity-123", evt.Payload["activity_id"], "Event should contain activity_id")
		assert.Equal(t, 3, evt.Payload["detection_count"], "Event should contain detection_count")
		assert.Equal(t, "high", evt.Payload["max_severity"], "Event should contain max_severity")
		assert.NotNil(t, evt.Payload["detection_types"], "Event should contain detection_types")
		types := evt.Payload["detection_types"].([]string)
		assert.Equal(t, 2, len(types), "Should have 2 detection types")
		assert.Contains(t, types, "credit_card", "Should contain credit_card")
		assert.Contains(t, types, "api_key", "Should contain api_key")
		assert.NotZero(t, evt.Timestamp, "Event should have a timestamp")
	case <-time.After(2 * time.Second):
		t.Fatal("Did not receive sensitive_data.detected event within timeout")
	}
}

// TestActivityService_ExtractMaxSeverity verifies severity ordering logic (Spec 026)
func TestActivityService_ExtractMaxSeverity(t *testing.T) {
	logger, err := zap.NewDevelopment()
	require.NoError(t, err)
	defer logger.Sync()

	svc := NewActivityService(nil, logger)

	tests := []struct {
		name       string
		detections []security.Detection
		expected   string
	}{
		{
			name:       "empty detections",
			detections: []security.Detection{},
			expected:   "",
		},
		{
			name: "single low severity",
			detections: []security.Detection{
				{Type: "test", Severity: "low"},
			},
			expected: "low",
		},
		{
			name: "critical highest",
			detections: []security.Detection{
				{Type: "test1", Severity: "low"},
				{Type: "test2", Severity: "critical"},
				{Type: "test3", Severity: "medium"},
			},
			expected: "critical",
		},
		{
			name: "high beats medium and low",
			detections: []security.Detection{
				{Type: "test1", Severity: "low"},
				{Type: "test2", Severity: "medium"},
				{Type: "test3", Severity: "high"},
			},
			expected: "high",
		},
		{
			name: "medium beats low",
			detections: []security.Detection{
				{Type: "test1", Severity: "low"},
				{Type: "test2", Severity: "medium"},
			},
			expected: "medium",
		},
		{
			name: "unknown severity fallback",
			detections: []security.Detection{
				{Type: "test", Severity: "unknown"},
			},
			expected: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := svc.extractMaxSeverity(tt.detections)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestActivityService_ExtractDetectionTypes verifies unique type extraction (Spec 026)
func TestActivityService_ExtractDetectionTypes(t *testing.T) {
	logger, err := zap.NewDevelopment()
	require.NoError(t, err)
	defer logger.Sync()

	svc := NewActivityService(nil, logger)

	tests := []struct {
		name       string
		detections []security.Detection
		expected   []string
	}{
		{
			name:       "empty detections",
			detections: []security.Detection{},
			expected:   []string{},
		},
		{
			name: "single type",
			detections: []security.Detection{
				{Type: "credit_card", Severity: "high"},
			},
			expected: []string{"credit_card"},
		},
		{
			name: "multiple unique types",
			detections: []security.Detection{
				{Type: "credit_card", Severity: "high"},
				{Type: "api_key", Severity: "critical"},
				{Type: "ssh_private_key", Severity: "critical"},
			},
			expected: []string{"credit_card", "api_key", "ssh_private_key"},
		},
		{
			name: "duplicate types filtered",
			detections: []security.Detection{
				{Type: "credit_card", Severity: "high"},
				{Type: "credit_card", Severity: "high"},
				{Type: "api_key", Severity: "critical"},
			},
			expected: []string{"credit_card", "api_key"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := svc.extractDetectionTypes(tt.detections)
			assert.Equal(t, len(tt.expected), len(result))
			for _, expectedType := range tt.expected {
				assert.Contains(t, result, expectedType)
			}
		})
	}
}

// setupTestStorage creates a temporary storage manager for tests.
func setupTestStorage(t *testing.T) (*storage.Manager, func()) {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "activity_svc_test_*")
	require.NoError(t, err)
	mgr, err := storage.NewManager(tmpDir, zap.NewNop().Sugar())
	require.NoError(t, err)
	return mgr, func() {
		mgr.Close()
		os.RemoveAll(tmpDir)
	}
}

// TestHandleToolCallCompleted_UserIdentityExtraction verifies that handleToolCallCompleted
// extracts UserID and UserEmail from _auth_ prefixed arguments and sets them on the record.
func TestHandleToolCallCompleted_UserIdentityExtraction(t *testing.T) {
	store, cleanup := setupTestStorage(t)
	defer cleanup()

	logger := zap.NewNop()
	svc := NewActivityService(store, logger)

	evt := Event{
		Type:      EventTypeActivityToolCallCompleted,
		Timestamp: time.Now().UTC(),
		Payload: map[string]any{
			"server_name": "github",
			"tool_name":   "list_repos",
			"session_id":  "sess-001",
			"request_id":  "req-001",
			"source":      "mcp",
			"status":      "success",
			"duration_ms": int64(100),
			"arguments": map[string]interface{}{
				"owner":            "octocat",
				"_auth_auth_type":  "user",
				"_auth_user_id":    "01HUSER123",
				"_auth_user_email": "alice@example.com",
			},
			"response": `{"repos": []}`,
		},
	}

	svc.handleEvent(evt)

	// Retrieve the saved record
	records, _, err := store.ListActivities(storage.DefaultActivityFilter())
	require.NoError(t, err)
	require.Len(t, records, 1)

	record := records[0]
	assert.Equal(t, "01HUSER123", record.UserID, "UserID should be extracted from _auth_user_id")
	assert.Equal(t, "alice@example.com", record.UserEmail, "UserEmail should be extracted from _auth_user_email")
	assert.Equal(t, "github", record.ServerName)
	assert.Equal(t, "list_repos", record.ToolName)
}

// TestHandleToolCallCompleted_NoUserIdentity verifies records without _auth_ user fields
// have empty UserID/UserEmail (backwards compatibility with personal edition).
func TestHandleToolCallCompleted_NoUserIdentity(t *testing.T) {
	store, cleanup := setupTestStorage(t)
	defer cleanup()

	logger := zap.NewNop()
	svc := NewActivityService(store, logger)

	evt := Event{
		Type:      EventTypeActivityToolCallCompleted,
		Timestamp: time.Now().UTC(),
		Payload: map[string]any{
			"server_name": "github",
			"tool_name":   "list_repos",
			"status":      "success",
			"duration_ms": int64(50),
			"arguments": map[string]interface{}{
				"owner":           "octocat",
				"_auth_auth_type": "admin",
			},
			"response": `{"repos": []}`,
		},
	}

	svc.handleEvent(evt)

	records, _, err := store.ListActivities(storage.DefaultActivityFilter())
	require.NoError(t, err)
	require.Len(t, records, 1)

	record := records[0]
	assert.Empty(t, record.UserID, "UserID should be empty when no _auth_user_id is present")
	assert.Empty(t, record.UserEmail, "UserEmail should be empty when no _auth_user_email is present")
}

// TestHandleToolCallCompleted_NilArguments verifies no panic when arguments is nil.
func TestHandleToolCallCompleted_NilArguments(t *testing.T) {
	store, cleanup := setupTestStorage(t)
	defer cleanup()

	logger := zap.NewNop()
	svc := NewActivityService(store, logger)

	evt := Event{
		Type:      EventTypeActivityToolCallCompleted,
		Timestamp: time.Now().UTC(),
		Payload: map[string]any{
			"server_name": "github",
			"tool_name":   "list_repos",
			"status":      "success",
			"duration_ms": int64(50),
			"response":    `{"repos": []}`,
		},
	}

	// Should not panic
	svc.handleEvent(evt)

	records, _, err := store.ListActivities(storage.DefaultActivityFilter())
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Empty(t, records[0].UserID)
	assert.Empty(t, records[0].UserEmail)
}

// TestHandleInternalToolCall_UserIdentityExtraction verifies that handleInternalToolCall
// extracts UserID and UserEmail from _auth_ prefixed arguments.
func TestHandleInternalToolCall_UserIdentityExtraction(t *testing.T) {
	store, cleanup := setupTestStorage(t)
	defer cleanup()

	logger := zap.NewNop()
	svc := NewActivityService(store, logger)

	evt := Event{
		Type:      EventTypeActivityInternalToolCall,
		Timestamp: time.Now().UTC(),
		Payload: map[string]any{
			"internal_tool_name": "retrieve_tools",
			"target_server":      "",
			"target_tool":        "",
			"tool_variant":       "",
			"session_id":         "sess-002",
			"request_id":         "req-002",
			"status":             "success",
			"error_message":      "",
			"duration_ms":        int64(25),
			"arguments": map[string]interface{}{
				"query":            "github repos",
				"_auth_auth_type":  "admin_user",
				"_auth_user_id":    "01HADMIN789",
				"_auth_user_email": "admin@example.com",
			},
			"response": "Found 5 tools",
		},
	}

	svc.handleEvent(evt)

	// Use a filter that includes internal tool calls
	filter := storage.DefaultActivityFilter()
	filter.ExcludeCallToolSuccess = false
	records, _, err := store.ListActivities(filter)
	require.NoError(t, err)
	require.Len(t, records, 1)

	record := records[0]
	assert.Equal(t, "01HADMIN789", record.UserID, "UserID should be extracted from _auth_user_id")
	assert.Equal(t, "admin@example.com", record.UserEmail, "UserEmail should be extracted from _auth_user_email")
	assert.Equal(t, storage.ActivityTypeInternalToolCall, record.Type)
	assert.Equal(t, "retrieve_tools", record.ToolName)
}

// TestHandleToolCallCompleted_ContentTrust verifies that content_trust metadata
// is extracted from the event payload and stored in the activity record's metadata (Spec 035).
func TestHandleToolCallCompleted_ContentTrust(t *testing.T) {
	tests := []struct {
		name         string
		contentTrust string
		wantInMeta   bool
		wantValue    string
	}{
		{
			name:         "untrusted content (open-world tool)",
			contentTrust: "untrusted",
			wantInMeta:   true,
			wantValue:    "untrusted",
		},
		{
			name:         "trusted content (closed-world tool)",
			contentTrust: "trusted",
			wantInMeta:   true,
			wantValue:    "trusted",
		},
		{
			name:         "empty content trust (not set)",
			contentTrust: "",
			wantInMeta:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, cleanup := setupTestStorage(t)
			defer cleanup()

			logger := zap.NewNop()
			svc := NewActivityService(store, logger)

			payload := map[string]any{
				"server_name":  "github",
				"tool_name":    "search_code",
				"session_id":   "sess-ct",
				"request_id":   "req-ct",
				"source":       "mcp",
				"status":       "success",
				"duration_ms":  int64(200),
				"tool_variant": "call_tool_read",
				"response":     `{"results": []}`,
			}
			if tt.contentTrust != "" {
				payload["content_trust"] = tt.contentTrust
			}

			evt := Event{
				Type:      EventTypeActivityToolCallCompleted,
				Timestamp: time.Now().UTC(),
				Payload:   payload,
			}

			svc.handleEvent(evt)

			records, _, err := store.ListActivities(storage.DefaultActivityFilter())
			require.NoError(t, err)
			require.Len(t, records, 1)

			record := records[0]
			if tt.wantInMeta {
				require.NotNil(t, record.Metadata, "Metadata should not be nil when content_trust is set")
				ct, ok := record.Metadata["content_trust"]
				assert.True(t, ok, "content_trust should be present in metadata")
				assert.Equal(t, tt.wantValue, ct, "content_trust value mismatch")
			} else {
				// When content_trust is empty, metadata may still exist (from tool_variant)
				if record.Metadata != nil {
					_, ok := record.Metadata["content_trust"]
					assert.False(t, ok, "content_trust should not be in metadata when not set")
				}
			}
		})
	}
}

// TestHandleInternalToolCall_ContentTrust verifies that content_trust metadata
// is extracted from internal tool call events and stored in metadata (Spec 035).
func TestHandleInternalToolCall_ContentTrust(t *testing.T) {
	store, cleanup := setupTestStorage(t)
	defer cleanup()

	logger := zap.NewNop()
	svc := NewActivityService(store, logger)

	evt := Event{
		Type:      EventTypeActivityInternalToolCall,
		Timestamp: time.Now().UTC(),
		Payload: map[string]any{
			"internal_tool_name": "code_execution",
			"session_id":         "sess-ce",
			"request_id":         "req-ce",
			"status":             "success",
			"error_message":      "",
			"duration_ms":        int64(500),
			"content_trust":      "untrusted",
			"arguments": map[string]interface{}{
				"code":     "call_tool('github', 'search_code', {q: 'test'})",
				"language": "javascript",
			},
			"response": "ok",
		},
	}

	svc.handleEvent(evt)

	filter := storage.DefaultActivityFilter()
	filter.ExcludeCallToolSuccess = false
	records, _, err := store.ListActivities(filter)
	require.NoError(t, err)
	require.Len(t, records, 1)

	record := records[0]
	require.NotNil(t, record.Metadata, "Metadata should not be nil")
	ct, ok := record.Metadata["content_trust"]
	assert.True(t, ok, "content_trust should be present in metadata")
	assert.Equal(t, "untrusted", ct, "content_trust should be untrusted for code_execution calling open-world tools")
}

// TestHandleInternalToolCall_NoUserIdentity verifies internal tool calls without user identity work.
func TestHandleInternalToolCall_NoUserIdentity(t *testing.T) {
	store, cleanup := setupTestStorage(t)
	defer cleanup()

	logger := zap.NewNop()
	svc := NewActivityService(store, logger)

	evt := Event{
		Type:      EventTypeActivityInternalToolCall,
		Timestamp: time.Now().UTC(),
		Payload: map[string]any{
			"internal_tool_name": "retrieve_tools",
			"status":             "success",
			"duration_ms":        int64(10),
			"arguments": map[string]interface{}{
				"query": "search",
			},
			"response": "Found 3 tools",
		},
	}

	svc.handleEvent(evt)

	filter := storage.DefaultActivityFilter()
	filter.ExcludeCallToolSuccess = false
	records, _, err := store.ListActivities(filter)
	require.NoError(t, err)
	require.Len(t, records, 1)

	assert.Empty(t, records[0].UserID)
	assert.Empty(t, records[0].UserEmail)
}
