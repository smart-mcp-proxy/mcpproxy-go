package runtime

import (
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

// TestEmitActivityHookEvaluation verifies hook_evaluation event emission (Spec 027)
func TestEmitActivityHookEvaluation(t *testing.T) {
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

	// Listen for activity.hook_evaluation.completed event
	go func() {
		select {
		case evt := <-eventChan:
			if evt.Type == EventTypeActivityHookEvaluation {
				done <- evt
			}
		case <-time.After(2 * time.Second):
			t.Log("Timeout waiting for activity.hook_evaluation.completed event")
		}
	}()

	// Emit hook evaluation event
	rt.EmitActivityHookEvaluation(
		"WebFetch",
		"hook-session-123",
		"PreToolUse",
		"external",
		"internal_to_external",
		"high",
		"deny",
		"sensitive data flowing to external tool",
		"full",
	)

	// Wait for event
	select {
	case evt := <-done:
		assert.Equal(t, EventTypeActivityHookEvaluation, evt.Type)
		assert.NotNil(t, evt.Payload)
		assert.Equal(t, "WebFetch", evt.Payload["tool_name"])
		assert.Equal(t, "hook-session-123", evt.Payload["session_id"])
		assert.Equal(t, "PreToolUse", evt.Payload["event"])
		assert.Equal(t, "external", evt.Payload["classification"])
		assert.Equal(t, "internal_to_external", evt.Payload["flow_type"])
		assert.Equal(t, "high", evt.Payload["risk_level"])
		assert.Equal(t, "deny", evt.Payload["policy_decision"])
		assert.Equal(t, "sensitive data flowing to external tool", evt.Payload["policy_reason"])
		assert.Equal(t, "full", evt.Payload["coverage_mode"])
		assert.NotZero(t, evt.Timestamp)
	case <-time.After(2 * time.Second):
		t.Fatal("Did not receive activity.hook_evaluation.completed event within timeout")
	}
}

// TestEmitFlowAlert verifies flow.alert event emission for high/critical risk (Spec 027)
func TestEmitFlowAlert(t *testing.T) {
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

	// Listen for flow.alert event
	go func() {
		select {
		case evt := <-eventChan:
			if evt.Type == EventTypeFlowAlert {
				done <- evt
			}
		case <-time.After(2 * time.Second):
			t.Log("Timeout waiting for flow.alert event")
		}
	}()

	// Emit flow alert
	rt.EmitFlowAlert("activity-456", "hook-session-789", "internal_to_external", "critical", "WebFetch", true)

	// Wait for event
	select {
	case evt := <-done:
		assert.Equal(t, EventTypeFlowAlert, evt.Type)
		assert.Equal(t, "activity-456", evt.Payload["activity_id"])
		assert.Equal(t, "hook-session-789", evt.Payload["session_id"])
		assert.Equal(t, "internal_to_external", evt.Payload["flow_type"])
		assert.Equal(t, "critical", evt.Payload["risk_level"])
		assert.Equal(t, "WebFetch", evt.Payload["tool_name"])
		assert.Equal(t, true, evt.Payload["has_sensitive_data"])
	case <-time.After(2 * time.Second):
		t.Fatal("Did not receive flow.alert event within timeout")
	}
}

// TestHandleHookEvaluation verifies handleHookEvaluation creates correct activity record (Spec 027)
func TestHandleHookEvaluation(t *testing.T) {
	logger, err := zap.NewDevelopment()
	require.NoError(t, err)
	defer logger.Sync()

	// Create a test storage manager
	tmpDir := t.TempDir()
	sm, err := newTestStorageManager(tmpDir, logger)
	require.NoError(t, err)
	defer sm.Close()

	svc := NewActivityService(sm, logger)

	// Create a hook evaluation event
	evt := newEvent(EventTypeActivityHookEvaluation, map[string]any{
		"tool_name":       "WebFetch",
		"session_id":      "hook-sess-001",
		"event":           "PreToolUse",
		"classification":  "external",
		"flow_type":       "internal_to_external",
		"risk_level":      "critical",
		"policy_decision": "deny",
		"policy_reason":   "sensitive data exfiltration detected",
		"coverage_mode":   "full",
	})

	// Handle the event
	svc.handleEvent(evt)

	// Verify the activity record was saved
	filter := storage.DefaultActivityFilter()
	filter.Types = []string{"hook_evaluation"}
	records, total, err := sm.ListActivities(filter)
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Len(t, records, 1)

	record := records[0]
	assert.Equal(t, storage.ActivityTypeHookEvaluation, record.Type)
	assert.Equal(t, "WebFetch", record.ToolName)
	assert.Equal(t, "hook-sess-001", record.SessionID)
	assert.Equal(t, "deny", record.Status)

	// Verify metadata
	require.NotNil(t, record.Metadata)
	assert.Equal(t, "PreToolUse", record.Metadata["event"])
	assert.Equal(t, "external", record.Metadata["classification"])
	assert.Equal(t, "full", record.Metadata["coverage_mode"])

	// Verify flow_analysis nested object
	flowAnalysis, ok := record.Metadata["flow_analysis"].(map[string]interface{})
	require.True(t, ok, "metadata should contain flow_analysis map")
	assert.Equal(t, "internal_to_external", flowAnalysis["flow_type"])
	assert.Equal(t, "critical", flowAnalysis["risk_level"])
	assert.Equal(t, "deny", flowAnalysis["policy_decision"])
	assert.Equal(t, "sensitive data exfiltration detected", flowAnalysis["policy_reason"])
}

// TestHandleHookEvaluation_AllowDecision verifies allow decisions are recorded (Spec 027)
func TestHandleHookEvaluation_AllowDecision(t *testing.T) {
	logger, err := zap.NewDevelopment()
	require.NoError(t, err)
	defer logger.Sync()

	tmpDir := t.TempDir()
	sm, err := newTestStorageManager(tmpDir, logger)
	require.NoError(t, err)
	defer sm.Close()

	svc := NewActivityService(sm, logger)

	evt := newEvent(EventTypeActivityHookEvaluation, map[string]any{
		"tool_name":       "Read",
		"session_id":      "hook-sess-002",
		"event":           "PostToolUse",
		"classification":  "internal",
		"flow_type":       "",
		"risk_level":      "none",
		"policy_decision": "allow",
		"policy_reason":   "origin recorded",
		"coverage_mode":   "full",
	})

	svc.handleEvent(evt)

	filter := storage.DefaultActivityFilter()
	filter.Types = []string{"hook_evaluation"}
	records, total, err := sm.ListActivities(filter)
	require.NoError(t, err)
	require.Equal(t, 1, total)
	assert.Equal(t, "allow", records[0].Status)
	assert.Equal(t, "Read", records[0].ToolName)
}

// TestActivityFilter_FlowType verifies flow_type filter (Spec 027)
func TestActivityFilter_FlowType(t *testing.T) {
	// Build a record with flow_analysis metadata
	record := &storage.ActivityRecord{
		Type:   storage.ActivityTypeHookEvaluation,
		Status: "deny",
		Metadata: map[string]interface{}{
			"flow_analysis": map[string]interface{}{
				"flow_type":  "internal_to_external",
				"risk_level": "critical",
			},
		},
	}

	// Should match internal_to_external
	filter := storage.DefaultActivityFilter()
	filter.FlowType = "internal_to_external"
	assert.True(t, filter.Matches(record))

	// Should not match internal_to_internal
	filter.FlowType = "internal_to_internal"
	assert.False(t, filter.Matches(record))

	// Empty flow_type should match everything
	filter.FlowType = ""
	filter.RiskLevel = ""
	assert.True(t, filter.Matches(record))
}

// TestActivityFilter_RiskLevel verifies risk_level >= filter (Spec 027)
func TestActivityFilter_RiskLevel(t *testing.T) {
	// Record with critical risk
	record := &storage.ActivityRecord{
		Type:   storage.ActivityTypeHookEvaluation,
		Status: "deny",
		Metadata: map[string]interface{}{
			"flow_analysis": map[string]interface{}{
				"flow_type":  "internal_to_external",
				"risk_level": "critical",
			},
		},
	}

	// Filter for high should match critical (critical >= high)
	filter := storage.DefaultActivityFilter()
	filter.RiskLevel = "high"
	assert.True(t, filter.Matches(record))

	// Filter for critical should match critical
	filter.RiskLevel = "critical"
	assert.True(t, filter.Matches(record))

	// Record with low risk
	lowRecord := &storage.ActivityRecord{
		Type:   storage.ActivityTypeHookEvaluation,
		Status: "allow",
		Metadata: map[string]interface{}{
			"flow_analysis": map[string]interface{}{
				"flow_type":  "internal_to_internal",
				"risk_level": "low",
			},
		},
	}

	// Filter for high should NOT match low
	filter.RiskLevel = "high"
	assert.False(t, filter.Matches(lowRecord))

	// Filter for low should match low
	filter.RiskLevel = "low"
	assert.True(t, filter.Matches(lowRecord))
}

// TestActivityFilter_HookEvaluationType verifies type=hook_evaluation filter (Spec 027)
func TestActivityFilter_HookEvaluationType(t *testing.T) {
	hookRecord := &storage.ActivityRecord{
		Type:   storage.ActivityTypeHookEvaluation,
		Status: "deny",
	}
	toolCallRecord := &storage.ActivityRecord{
		Type:   storage.ActivityTypeToolCall,
		Status: "success",
	}

	filter := storage.DefaultActivityFilter()
	filter.Types = []string{"hook_evaluation"}

	assert.True(t, filter.Matches(hookRecord))
	assert.False(t, filter.Matches(toolCallRecord))
}

// newTestStorageManager creates a temporary storage manager for testing
func newTestStorageManager(dir string, logger *zap.Logger) (*storage.Manager, error) {
	return storage.NewManager(dir, logger.Sugar())
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
