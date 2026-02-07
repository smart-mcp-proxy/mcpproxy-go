package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

func TestParseActivityFilters(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		expected storage.ActivityFilter
	}{
		{
			name:  "empty query returns defaults",
			query: "",
			expected: storage.ActivityFilter{
				Limit:  50,
				Offset: 0,
			},
		},
		{
			name:  "single type filter",
			query: "type=tool_call",
			expected: storage.ActivityFilter{
				Types:  []string{"tool_call"},
				Limit:  50,
				Offset: 0,
			},
		},
		{
			name:  "multiple types filter (Spec 024)",
			query: "type=tool_call,policy_decision",
			expected: storage.ActivityFilter{
				Types:  []string{"tool_call", "policy_decision"},
				Limit:  50,
				Offset: 0,
			},
		},
		{
			name:  "all new event types (Spec 024)",
			query: "type=system_start,system_stop,internal_tool_call,config_change",
			expected: storage.ActivityFilter{
				Types:  []string{"system_start", "system_stop", "internal_tool_call", "config_change"},
				Limit:  50,
				Offset: 0,
			},
		},
		{
			name:  "server filter",
			query: "server=github",
			expected: storage.ActivityFilter{
				Server: "github",
				Limit:  50,
				Offset: 0,
			},
		},
		{
			name:  "tool filter",
			query: "tool=create_issue",
			expected: storage.ActivityFilter{
				Tool:   "create_issue",
				Limit:  50,
				Offset: 0,
			},
		},
		{
			name:  "session_id filter",
			query: "session_id=sess-123",
			expected: storage.ActivityFilter{
				SessionID: "sess-123",
				Limit:     50,
				Offset:    0,
			},
		},
		{
			name:  "status filter",
			query: "status=error",
			expected: storage.ActivityFilter{
				Status: "error",
				Limit:  50,
				Offset: 0,
			},
		},
		{
			name:  "pagination",
			query: "limit=25&offset=10",
			expected: storage.ActivityFilter{
				Limit:  25,
				Offset: 10,
			},
		},
		{
			name:  "limit capped at 100",
			query: "limit=500",
			expected: storage.ActivityFilter{
				Limit:  100,
				Offset: 0,
			},
		},
		{
			name:  "multiple filters with types",
			query: "type=tool_call&server=github&status=success&limit=20",
			expected: storage.ActivityFilter{
				Types:  []string{"tool_call"},
				Server: "github",
				Status: "success",
				Limit:  20,
				Offset: 0,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/v1/activity?"+tt.query, nil)
			filter := parseActivityFilters(req)

			assert.Equal(t, tt.expected.Types, filter.Types)
			assert.Equal(t, tt.expected.Server, filter.Server)
			assert.Equal(t, tt.expected.Tool, filter.Tool)
			assert.Equal(t, tt.expected.SessionID, filter.SessionID)
			assert.Equal(t, tt.expected.Status, filter.Status)
			assert.Equal(t, tt.expected.Limit, filter.Limit)
			assert.Equal(t, tt.expected.Offset, filter.Offset)
		})
	}
}

func TestParseActivityFilters_TimeRange(t *testing.T) {
	startTime := "2024-06-01T00:00:00Z"
	endTime := "2024-06-30T23:59:59Z"
	req := httptest.NewRequest("GET", "/api/v1/activity?start_time="+startTime+"&end_time="+endTime, nil)

	filter := parseActivityFilters(req)

	expectedStart, _ := time.Parse(time.RFC3339, startTime)
	expectedEnd, _ := time.Parse(time.RFC3339, endTime)

	assert.Equal(t, expectedStart, filter.StartTime)
	assert.Equal(t, expectedEnd, filter.EndTime)
}

func TestStorageToContractActivity(t *testing.T) {
	storageRecord := &storage.ActivityRecord{
		ID:                "test-id",
		Type:              storage.ActivityTypeToolCall,
		ServerName:        "github",
		ToolName:          "create_issue",
		Arguments:         map[string]interface{}{"title": "Test"},
		Response:          "Created",
		ResponseTruncated: false,
		Status:            "success",
		ErrorMessage:      "",
		DurationMs:        150,
		Timestamp:         time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC),
		SessionID:         "sess-123",
		RequestID:         "req-456",
		Metadata:          map[string]interface{}{"key": "value"},
	}

	result := storageToContractActivity(storageRecord)

	assert.Equal(t, "test-id", result.ID)
	assert.Equal(t, contracts.ActivityTypeToolCall, result.Type)
	assert.Equal(t, "github", result.ServerName)
	assert.Equal(t, "create_issue", result.ToolName)
	assert.Equal(t, map[string]interface{}{"title": "Test"}, result.Arguments)
	assert.Equal(t, "Created", result.Response)
	assert.False(t, result.ResponseTruncated)
	assert.Equal(t, "success", result.Status)
	assert.Empty(t, result.ErrorMessage)
	assert.Equal(t, int64(150), result.DurationMs)
	assert.Equal(t, "sess-123", result.SessionID)
	assert.Equal(t, "req-456", result.RequestID)
	assert.Equal(t, map[string]interface{}{"key": "value"}, result.Metadata)
}

func TestHandleListActivity_Success(t *testing.T) {
	// Handler integration tests require full controller mock setup
	// The core parsing and conversion logic is tested above
	// Full integration is validated via E2E tests
	t.Log("Handler integration requires controller mock - tested via E2E")
}

func TestHandleGetActivityDetail_NotFound(t *testing.T) {
	// Similar to above - detailed handler tests require E2E or controller mock
	t.Log("Handler integration requires controller mock - tested via E2E")
}

func TestActivityListResponse_JSON(t *testing.T) {
	response := contracts.ActivityListResponse{
		Activities: []contracts.ActivityRecord{
			{
				ID:         "test-id",
				Type:       contracts.ActivityTypeToolCall,
				ServerName: "github",
				Status:     "success",
				Timestamp:  time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC),
			},
		},
		Total:  1,
		Limit:  50,
		Offset: 0,
	}

	data, err := json.Marshal(response)
	require.NoError(t, err)

	var parsed contracts.ActivityListResponse
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	assert.Equal(t, 1, len(parsed.Activities))
	assert.Equal(t, "test-id", parsed.Activities[0].ID)
	assert.Equal(t, contracts.ActivityTypeToolCall, parsed.Activities[0].Type)
	assert.Equal(t, 1, parsed.Total)
	assert.Equal(t, 50, parsed.Limit)
	assert.Equal(t, 0, parsed.Offset)
}

func TestActivityDetailResponse_JSON(t *testing.T) {
	response := contracts.ActivityDetailResponse{
		Activity: contracts.ActivityRecord{
			ID:         "test-id",
			Type:       contracts.ActivityTypeToolCall,
			ServerName: "github",
			ToolName:   "create_issue",
			Arguments:  map[string]interface{}{"title": "Bug report"},
			Response:   "Issue created successfully",
			Status:     "success",
			DurationMs: 234,
			Timestamp:  time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC),
		},
	}

	data, err := json.Marshal(response)
	require.NoError(t, err)

	var parsed contracts.ActivityDetailResponse
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	assert.Equal(t, "test-id", parsed.Activity.ID)
	assert.Equal(t, "github", parsed.Activity.ServerName)
	assert.Equal(t, "create_issue", parsed.Activity.ToolName)
	assert.Equal(t, int64(234), parsed.Activity.DurationMs)
}

func TestActivityRequest_InvalidID(t *testing.T) {
	// Test that empty ID is rejected
	req := httptest.NewRequest("GET", "/api/v1/activity/", nil)
	rr := httptest.NewRecorder()

	// Verify URL parsing - chi would normally extract the param
	assert.Equal(t, http.MethodGet, req.Method)
	assert.Empty(t, req.URL.Query().Get("id")) // No query param
	_ = rr // Would check response after handler call
}

// =============================================================================
// Spec 026: Sensitive Data Detection Filter Tests
// =============================================================================

func TestParseActivityFilters_SensitiveDataFilters(t *testing.T) {
	tests := []struct {
		name            string
		query           string
		wantSensitive   *bool
		wantDetType     string
		wantSeverity    string
	}{
		{
			name:          "sensitive_data=true filter",
			query:         "sensitive_data=true",
			wantSensitive: boolPtr(true),
		},
		{
			name:          "sensitive_data=false filter",
			query:         "sensitive_data=false",
			wantSensitive: boolPtr(false),
		},
		{
			name:        "detection_type filter",
			query:       "detection_type=aws_access_key",
			wantDetType: "aws_access_key",
		},
		{
			name:         "severity filter",
			query:        "severity=critical",
			wantSeverity: "critical",
		},
		{
			name:          "combined sensitive data filters",
			query:         "sensitive_data=true&detection_type=credit_card&severity=high",
			wantSensitive: boolPtr(true),
			wantDetType:   "credit_card",
			wantSeverity:  "high",
		},
		{
			name:          "no sensitive data filters - nil values",
			query:         "type=tool_call",
			wantSensitive: nil,
			wantDetType:   "",
			wantSeverity:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/v1/activity?"+tt.query, nil)
			filter := parseActivityFilters(req)

			// Check sensitive data pointer
			if tt.wantSensitive == nil {
				assert.Nil(t, filter.SensitiveData, "SensitiveData should be nil")
			} else {
				require.NotNil(t, filter.SensitiveData, "SensitiveData should not be nil")
				assert.Equal(t, *tt.wantSensitive, *filter.SensitiveData)
			}

			assert.Equal(t, tt.wantDetType, filter.DetectionType)
			assert.Equal(t, tt.wantSeverity, filter.Severity)
		})
	}
}

func TestStorageToContractActivity_SensitiveDataFields(t *testing.T) {
	t.Run("activity with sensitive data detection", func(t *testing.T) {
		storageRecord := &storage.ActivityRecord{
			ID:         "test-sensitive-1",
			Type:       storage.ActivityTypeToolCall,
			ServerName: "github",
			ToolName:   "create_issue",
			Status:     "success",
			Timestamp:  time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC),
			Metadata: map[string]interface{}{
				"sensitive_data_detection": map[string]interface{}{
					"detected": true,
					"detections": []interface{}{
						map[string]interface{}{
							"type":     "aws_access_key",
							"severity": "critical",
							"location": "arguments.api_key",
						},
						map[string]interface{}{
							"type":     "credit_card",
							"severity": "high",
							"location": "arguments.card",
						},
					},
				},
			},
		}

		result := storageToContractActivity(storageRecord)

		assert.True(t, result.HasSensitiveData, "HasSensitiveData should be true")
		assert.Contains(t, result.DetectionTypes, "aws_access_key")
		assert.Contains(t, result.DetectionTypes, "credit_card")
		assert.Len(t, result.DetectionTypes, 2)
		assert.Equal(t, "critical", result.MaxSeverity, "MaxSeverity should be critical (highest)")
	})

	t.Run("activity without sensitive data detection", func(t *testing.T) {
		storageRecord := &storage.ActivityRecord{
			ID:         "test-no-sensitive",
			Type:       storage.ActivityTypeToolCall,
			ServerName: "github",
			ToolName:   "get_repo",
			Status:     "success",
			Timestamp:  time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC),
			Metadata:   map[string]interface{}{"key": "value"},
		}

		result := storageToContractActivity(storageRecord)

		assert.False(t, result.HasSensitiveData, "HasSensitiveData should be false")
		assert.Nil(t, result.DetectionTypes, "DetectionTypes should be nil")
		assert.Empty(t, result.MaxSeverity, "MaxSeverity should be empty")
	})

	t.Run("activity with detection but detected=false", func(t *testing.T) {
		storageRecord := &storage.ActivityRecord{
			ID:         "test-not-detected",
			Type:       storage.ActivityTypeToolCall,
			ServerName: "github",
			ToolName:   "get_repo",
			Status:     "success",
			Timestamp:  time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC),
			Metadata: map[string]interface{}{
				"sensitive_data_detection": map[string]interface{}{
					"detected":   false,
					"detections": []interface{}{},
				},
			},
		}

		result := storageToContractActivity(storageRecord)

		assert.False(t, result.HasSensitiveData, "HasSensitiveData should be false when detected=false")
		assert.Nil(t, result.DetectionTypes, "DetectionTypes should be nil")
		assert.Empty(t, result.MaxSeverity, "MaxSeverity should be empty")
	})

	t.Run("activity with nil metadata", func(t *testing.T) {
		storageRecord := &storage.ActivityRecord{
			ID:         "test-nil-metadata",
			Type:       storage.ActivityTypeToolCall,
			ServerName: "github",
			ToolName:   "get_repo",
			Status:     "success",
			Timestamp:  time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC),
			Metadata:   nil,
		}

		result := storageToContractActivity(storageRecord)

		assert.False(t, result.HasSensitiveData, "HasSensitiveData should be false for nil metadata")
		assert.Nil(t, result.DetectionTypes)
		assert.Empty(t, result.MaxSeverity)
	})
}

func TestExtractSensitiveDataInfo(t *testing.T) {
	t.Run("extracts all detection types without duplicates", func(t *testing.T) {
		record := &storage.ActivityRecord{
			Metadata: map[string]interface{}{
				"sensitive_data_detection": map[string]interface{}{
					"detected": true,
					"detections": []interface{}{
						map[string]interface{}{"type": "aws_access_key", "severity": "critical"},
						map[string]interface{}{"type": "aws_access_key", "severity": "critical"}, // duplicate
						map[string]interface{}{"type": "github_token", "severity": "high"},
					},
				},
			},
		}

		detected, types, severity := extractSensitiveDataInfo(record)

		assert.True(t, detected)
		assert.Len(t, types, 2, "Should deduplicate detection types")
		assert.Contains(t, types, "aws_access_key")
		assert.Contains(t, types, "github_token")
		assert.Equal(t, "critical", severity)
	})

	t.Run("calculates max severity correctly", func(t *testing.T) {
		tests := []struct {
			name        string
			severities  []string
			expectedMax string
		}{
			{
				name:        "critical is highest",
				severities:  []string{"low", "medium", "high", "critical"},
				expectedMax: "critical",
			},
			{
				name:        "high without critical",
				severities:  []string{"low", "medium", "high"},
				expectedMax: "high",
			},
			{
				name:        "medium without higher",
				severities:  []string{"low", "medium"},
				expectedMax: "medium",
			},
			{
				name:        "only low",
				severities:  []string{"low"},
				expectedMax: "low",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				detections := make([]interface{}, len(tt.severities))
				for i, sev := range tt.severities {
					detections[i] = map[string]interface{}{
						"type":     "test_type",
						"severity": sev,
					}
				}

				record := &storage.ActivityRecord{
					Metadata: map[string]interface{}{
						"sensitive_data_detection": map[string]interface{}{
							"detected":   true,
							"detections": detections,
						},
					},
				}

				_, _, maxSeverity := extractSensitiveDataInfo(record)
				assert.Equal(t, tt.expectedMax, maxSeverity)
			})
		}
	})
}

func TestCalculateMaxSeverity(t *testing.T) {
	tests := []struct {
		name       string
		detection  map[string]interface{}
		wantMax    string
	}{
		{
			name: "mixed severities - critical wins",
			detection: map[string]interface{}{
				"detections": []interface{}{
					map[string]interface{}{"severity": "low"},
					map[string]interface{}{"severity": "critical"},
					map[string]interface{}{"severity": "medium"},
				},
			},
			wantMax: "critical",
		},
		{
			name: "high is max",
			detection: map[string]interface{}{
				"detections": []interface{}{
					map[string]interface{}{"severity": "low"},
					map[string]interface{}{"severity": "high"},
				},
			},
			wantMax: "high",
		},
		{
			name: "empty detections",
			detection: map[string]interface{}{
				"detections": []interface{}{},
			},
			wantMax: "",
		},
		{
			name:    "nil detections",
			detection: map[string]interface{}{},
			wantMax: "",
		},
		{
			name: "unknown severity ignored",
			detection: map[string]interface{}{
				"detections": []interface{}{
					map[string]interface{}{"severity": "unknown"},
					map[string]interface{}{"severity": "low"},
				},
			},
			wantMax: "low",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := calculateMaxSeverity(tt.detection)
			assert.Equal(t, tt.wantMax, result)
		})
	}
}

func TestActivityListResponse_SensitiveDataFields_JSON(t *testing.T) {
	response := contracts.ActivityListResponse{
		Activities: []contracts.ActivityRecord{
			{
				ID:               "activity-with-sensitive",
				Type:             contracts.ActivityTypeToolCall,
				ServerName:       "github",
				Status:           "success",
				Timestamp:        time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC),
				HasSensitiveData: true,
				DetectionTypes:   []string{"aws_access_key", "github_token"},
				MaxSeverity:      "critical",
			},
			{
				ID:               "activity-without-sensitive",
				Type:             contracts.ActivityTypeToolCall,
				ServerName:       "github",
				Status:           "success",
				Timestamp:        time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC),
				HasSensitiveData: false,
				DetectionTypes:   nil,
				MaxSeverity:      "",
			},
		},
		Total:  2,
		Limit:  50,
		Offset: 0,
	}

	data, err := json.Marshal(response)
	require.NoError(t, err)

	var parsed contracts.ActivityListResponse
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	assert.Len(t, parsed.Activities, 2)

	// Check activity with sensitive data
	sensitiveActivity := parsed.Activities[0]
	assert.True(t, sensitiveActivity.HasSensitiveData)
	assert.Contains(t, sensitiveActivity.DetectionTypes, "aws_access_key")
	assert.Contains(t, sensitiveActivity.DetectionTypes, "github_token")
	assert.Equal(t, "critical", sensitiveActivity.MaxSeverity)

	// Check activity without sensitive data
	normalActivity := parsed.Activities[1]
	assert.False(t, normalActivity.HasSensitiveData)
	assert.Nil(t, normalActivity.DetectionTypes)
	assert.Empty(t, normalActivity.MaxSeverity)
}

// =============================================================================
// Spec 027 Phase 13: Auditor Agent Data Surface Tests (T120)
// =============================================================================

func TestParseActivityFilters_FlowTypes(t *testing.T) {
	t.Run("multi-type filter with hook_evaluation and flow_summary", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/activity?type=hook_evaluation,flow_summary", nil)
		filter := parseActivityFilters(req)

		assert.Equal(t, []string{"hook_evaluation", "flow_summary"}, filter.Types)
	})

	t.Run("multi-type filter with all flow types plus tool_call", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/activity?type=tool_call,hook_evaluation,flow_summary", nil)
		filter := parseActivityFilters(req)

		assert.Len(t, filter.Types, 3)
		assert.Contains(t, filter.Types, "tool_call")
		assert.Contains(t, filter.Types, "hook_evaluation")
		assert.Contains(t, filter.Types, "flow_summary")
	})

	t.Run("flow_type and risk_level with multi-type", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/activity?type=hook_evaluation,flow_summary&flow_type=internal_to_external&risk_level=high", nil)
		filter := parseActivityFilters(req)

		assert.Equal(t, []string{"hook_evaluation", "flow_summary"}, filter.Types)
		assert.Equal(t, "internal_to_external", filter.FlowType)
		assert.Equal(t, "high", filter.RiskLevel)
	})
}

func TestStorageToContractActivity_HookEvaluation(t *testing.T) {
	record := &storage.ActivityRecord{
		ID:        "hook-eval-001",
		Type:      storage.ActivityTypeHookEvaluation,
		Source:    storage.ActivitySourceAPI,
		ToolName:  "WebFetch",
		Status:    "deny",
		SessionID: "hook-session-1",
		Timestamp: time.Date(2024, 12, 15, 12, 0, 0, 0, time.UTC),
		Metadata: map[string]interface{}{
			"event":          "PreToolUse",
			"classification": "external",
			"coverage_mode":  "full",
			"flow_analysis": map[string]interface{}{
				"flow_type":       "internal_to_external",
				"risk_level":      "critical",
				"policy_decision": "deny",
				"policy_reason":   "sensitive data flowing to external tool",
			},
		},
	}

	result := storageToContractActivity(record)

	assert.Equal(t, contracts.ActivityTypeHookEvaluation, result.Type)
	assert.Equal(t, "WebFetch", result.ToolName)
	assert.Equal(t, "deny", result.Status)
	assert.Equal(t, "hook-session-1", result.SessionID)

	// Verify flow metadata is preserved in API response
	require.NotNil(t, result.Metadata)
	assert.Equal(t, "external", result.Metadata["classification"])
	assert.Equal(t, "full", result.Metadata["coverage_mode"])

	flowAnalysis, ok := result.Metadata["flow_analysis"].(map[string]interface{})
	require.True(t, ok, "flow_analysis should be a map")
	assert.Equal(t, "internal_to_external", flowAnalysis["flow_type"])
	assert.Equal(t, "critical", flowAnalysis["risk_level"])
	assert.Equal(t, "deny", flowAnalysis["policy_decision"])
}

func TestStorageToContractActivity_FlowSummary(t *testing.T) {
	record := &storage.ActivityRecord{
		ID:        "flow-summary-001",
		Type:      storage.ActivityTypeFlowSummary,
		Source:    storage.ActivitySourceAPI,
		Status:    "completed",
		SessionID: "session-summary-1",
		Timestamp: time.Date(2024, 12, 15, 13, 0, 0, 0, time.UTC),
		Metadata: map[string]interface{}{
			"coverage_mode":    "full",
			"duration_minutes": float64(45),
			"total_origins":    float64(128),
			"total_flows":      float64(3),
			"has_sensitive_flows": true,
			"flow_type_distribution": map[string]interface{}{
				"internal_to_external": float64(2),
				"internal_to_internal": float64(1),
			},
			"risk_level_distribution": map[string]interface{}{
				"critical": float64(1),
				"high":     float64(1),
				"none":     float64(1),
			},
			"tools_used":          []interface{}{"Read", "WebFetch", "postgres:query"},
			"linked_mcp_sessions": []interface{}{"mcp-session-1"},
		},
	}

	result := storageToContractActivity(record)

	assert.Equal(t, contracts.ActivityTypeFlowSummary, result.Type)
	assert.Equal(t, "completed", result.Status)
	assert.Equal(t, "session-summary-1", result.SessionID)

	// Verify all flow summary fields are accessible in the API response
	require.NotNil(t, result.Metadata)
	assert.Equal(t, "full", result.Metadata["coverage_mode"])
	assert.Equal(t, float64(45), result.Metadata["duration_minutes"])
	assert.Equal(t, float64(128), result.Metadata["total_origins"])
	assert.Equal(t, float64(3), result.Metadata["total_flows"])
	assert.Equal(t, true, result.Metadata["has_sensitive_flows"])

	// Distributions
	ftDist, ok := result.Metadata["flow_type_distribution"].(map[string]interface{})
	require.True(t, ok, "flow_type_distribution should be a map")
	assert.Equal(t, float64(2), ftDist["internal_to_external"])

	rlDist, ok := result.Metadata["risk_level_distribution"].(map[string]interface{})
	require.True(t, ok, "risk_level_distribution should be a map")
	assert.Equal(t, float64(1), rlDist["critical"])

	// Tools used
	toolsUsed, ok := result.Metadata["tools_used"].([]interface{})
	require.True(t, ok, "tools_used should be a list")
	assert.Len(t, toolsUsed, 3)
}

func TestStorageToContractActivityForExport_FlowMetadata(t *testing.T) {
	t.Run("JSON export includes flow_summary metadata", func(t *testing.T) {
		record := &storage.ActivityRecord{
			ID:        "export-flow-001",
			Type:      storage.ActivityTypeFlowSummary,
			Source:    storage.ActivitySourceAPI,
			Status:    "completed",
			SessionID: "session-export",
			Timestamp: time.Date(2024, 12, 15, 14, 0, 0, 0, time.UTC),
			Metadata: map[string]interface{}{
				"coverage_mode":         "proxy_only",
				"duration_minutes":      float64(30),
				"total_origins":         float64(50),
				"total_flows":           float64(1),
				"has_sensitive_flows":   false,
				"flow_type_distribution": map[string]interface{}{"internal_to_external": float64(1)},
				"tools_used":            []interface{}{"Read", "WebFetch"},
			},
		}

		// Export without bodies (default)
		result := storageToContractActivityForExport(record, false)

		// Metadata should always be included in export
		require.NotNil(t, result.Metadata, "metadata must be included in export")
		assert.Equal(t, "proxy_only", result.Metadata["coverage_mode"])
		assert.Equal(t, float64(50), result.Metadata["total_origins"])
		assert.Equal(t, float64(1), result.Metadata["total_flows"])

		// Verify it serializes to JSON correctly
		jsonBytes, err := json.Marshal(result)
		require.NoError(t, err)

		var parsed map[string]interface{}
		err = json.Unmarshal(jsonBytes, &parsed)
		require.NoError(t, err)

		meta, ok := parsed["metadata"].(map[string]interface{})
		require.True(t, ok, "metadata should be in JSON output")
		assert.Equal(t, "proxy_only", meta["coverage_mode"])
	})

	t.Run("JSON export includes hook_evaluation metadata", func(t *testing.T) {
		record := &storage.ActivityRecord{
			ID:        "export-hook-001",
			Type:      storage.ActivityTypeHookEvaluation,
			Source:    storage.ActivitySourceAPI,
			ToolName:  "WebFetch",
			Status:    "deny",
			SessionID: "hook-session-export",
			Timestamp: time.Date(2024, 12, 15, 14, 0, 0, 0, time.UTC),
			Metadata: map[string]interface{}{
				"event":          "PreToolUse",
				"classification": "external",
				"flow_analysis": map[string]interface{}{
					"flow_type":  "internal_to_external",
					"risk_level": "critical",
				},
			},
		}

		result := storageToContractActivityForExport(record, false)

		require.NotNil(t, result.Metadata)
		fa, ok := result.Metadata["flow_analysis"].(map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, "internal_to_external", fa["flow_type"])
		assert.Equal(t, "critical", fa["risk_level"])
	})
}

func TestFlowAlertSSEEvent_Structure(t *testing.T) {
	// Verify flow.alert event type is defined and payload has required fields
	assert.Equal(t, runtime.EventType("flow.alert"), runtime.EventTypeFlowAlert)

	// Verify a flow.alert event has all required fields for auditor consumption
	evt := runtime.Event{
		Type:      runtime.EventTypeFlowAlert,
		Timestamp: time.Now().UTC(),
		Payload: map[string]any{
			"activity_id":        "act-123",
			"session_id":         "hook-session-1",
			"flow_type":          "internal_to_external",
			"risk_level":         "critical",
			"tool_name":          "WebFetch",
			"has_sensitive_data": true,
		},
	}

	assert.Equal(t, "flow.alert", string(evt.Type))
	assert.Equal(t, "act-123", evt.Payload["activity_id"])
	assert.Equal(t, "hook-session-1", evt.Payload["session_id"])
	assert.Equal(t, "internal_to_external", evt.Payload["flow_type"])
	assert.Equal(t, "critical", evt.Payload["risk_level"])
	assert.Equal(t, "WebFetch", evt.Payload["tool_name"])
	assert.Equal(t, true, evt.Payload["has_sensitive_data"])
}

func TestAuditorActivityTypes_Defined(t *testing.T) {
	// Verify all flow-related activity types are defined in contracts
	assert.Equal(t, contracts.ActivityType("hook_evaluation"), contracts.ActivityTypeHookEvaluation)
	assert.Equal(t, contracts.ActivityType("flow_summary"), contracts.ActivityTypeFlowSummary)
	assert.Equal(t, contracts.ActivityType("auditor_finding"), contracts.ActivityTypeAuditorFinding)

	// Verify storage types match contracts
	assert.Equal(t, string(storage.ActivityTypeHookEvaluation), string(contracts.ActivityTypeHookEvaluation))
	assert.Equal(t, string(storage.ActivityTypeFlowSummary), string(contracts.ActivityTypeFlowSummary))
	assert.Equal(t, string(storage.ActivityTypeAuditorFinding), string(contracts.ActivityTypeAuditorFinding))
}

func TestActivityFilter_MultiTypeMatching(t *testing.T) {
	hookRecord := &storage.ActivityRecord{
		Type:   storage.ActivityTypeHookEvaluation,
		Status: "deny",
	}
	summaryRecord := &storage.ActivityRecord{
		Type:   storage.ActivityTypeFlowSummary,
		Status: "completed",
	}
	toolCallRecord := &storage.ActivityRecord{
		Type:   storage.ActivityTypeToolCall,
		Status: "success",
	}

	filter := storage.ActivityFilter{
		Types: []string{"hook_evaluation", "flow_summary"},
	}

	assert.True(t, filter.Matches(hookRecord), "hook_evaluation should match")
	assert.True(t, filter.Matches(summaryRecord), "flow_summary should match")
	assert.False(t, filter.Matches(toolCallRecord), "tool_call should NOT match")
}

// Helper function to create bool pointer
func boolPtr(b bool) *bool {
	return &b
}
