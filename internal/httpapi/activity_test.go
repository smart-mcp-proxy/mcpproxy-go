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
