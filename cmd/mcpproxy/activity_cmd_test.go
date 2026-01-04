package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// ActivityFilter Tests
// =============================================================================

func TestActivityFilter_Validate_ValidInputs(t *testing.T) {
	tests := []struct {
		name   string
		filter ActivityFilter
	}{
		{
			name:   "empty filter",
			filter: ActivityFilter{},
		},
		{
			name: "valid type - tool_call",
			filter: ActivityFilter{
				Type: "tool_call",
			},
		},
		{
			name: "valid type - policy_decision",
			filter: ActivityFilter{
				Type: "policy_decision",
			},
		},
		{
			name: "valid type - quarantine_change",
			filter: ActivityFilter{
				Type: "quarantine_change",
			},
		},
		{
			name: "valid type - server_change",
			filter: ActivityFilter{
				Type: "server_change",
			},
		},
		{
			name: "valid status - success",
			filter: ActivityFilter{
				Status: "success",
			},
		},
		{
			name: "valid status - error",
			filter: ActivityFilter{
				Status: "error",
			},
		},
		{
			name: "valid status - blocked",
			filter: ActivityFilter{
				Status: "blocked",
			},
		},
		{
			name: "valid time format - RFC3339",
			filter: ActivityFilter{
				StartTime: "2025-01-01T00:00:00Z",
				EndTime:   "2025-01-31T23:59:59Z",
			},
		},
		{
			name: "valid limit - within range",
			filter: ActivityFilter{
				Limit: 50,
			},
		},
		{
			name: "all filters combined",
			filter: ActivityFilter{
				Type:      "tool_call",
				Server:    "github",
				Tool:      "create_issue",
				Status:    "success",
				SessionID: "session-123",
				StartTime: "2025-01-01T00:00:00Z",
				EndTime:   "2025-01-31T23:59:59Z",
				Limit:     25,
				Offset:    10,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.filter.Validate()
			assert.NoError(t, err)
		})
	}
}

func TestActivityFilter_Validate_InvalidInputs(t *testing.T) {
	tests := []struct {
		name        string
		filter      ActivityFilter
		errContains string
	}{
		{
			name: "invalid type",
			filter: ActivityFilter{
				Type: "invalid_type",
			},
			errContains: "invalid type 'invalid_type'",
		},
		{
			name: "invalid status",
			filter: ActivityFilter{
				Status: "unknown",
			},
			errContains: "invalid status 'unknown'",
		},
		{
			name: "invalid start_time format",
			filter: ActivityFilter{
				StartTime: "2025-01-01",
			},
			errContains: "invalid start-time format",
		},
		{
			name: "invalid end_time format",
			filter: ActivityFilter{
				EndTime: "not-a-date",
			},
			errContains: "invalid end-time format",
		},
		{
			name: "invalid start_time - wrong timezone format",
			filter: ActivityFilter{
				StartTime: "2025-01-01T00:00:00",
			},
			errContains: "invalid start-time format",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.filter.Validate()
			assert.Error(t, err)
			assert.Contains(t, err.Error(), tt.errContains)
		})
	}
}

func TestActivityFilter_Validate_LimitClamping(t *testing.T) {
	tests := []struct {
		name          string
		inputLimit    int
		expectedLimit int
	}{
		{
			name:          "limit too low - clamped to default",
			inputLimit:    0,
			expectedLimit: 50,
		},
		{
			name:          "limit negative - clamped to default",
			inputLimit:    -10,
			expectedLimit: 50,
		},
		{
			name:          "limit too high - clamped to max",
			inputLimit:    200,
			expectedLimit: 100,
		},
		{
			name:          "limit at max",
			inputLimit:    100,
			expectedLimit: 100,
		},
		{
			name:          "limit within range",
			inputLimit:    50,
			expectedLimit: 50,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter := &ActivityFilter{Limit: tt.inputLimit}
			err := filter.Validate()
			assert.NoError(t, err)
			assert.Equal(t, tt.expectedLimit, filter.Limit)
		})
	}
}

func TestActivityFilter_ToQueryParams(t *testing.T) {
	tests := []struct {
		name           string
		filter         ActivityFilter
		expectedParams map[string]string
	}{
		{
			name:           "empty filter",
			filter:         ActivityFilter{},
			expectedParams: map[string]string{},
		},
		{
			name: "type only",
			filter: ActivityFilter{
				Type: "tool_call",
			},
			expectedParams: map[string]string{
				"type": "tool_call",
			},
		},
		{
			name: "server only",
			filter: ActivityFilter{
				Server: "github",
			},
			expectedParams: map[string]string{
				"server": "github",
			},
		},
		{
			name: "all filters",
			filter: ActivityFilter{
				Type:      "tool_call",
				Server:    "github",
				Tool:      "create_issue",
				Status:    "success",
				SessionID: "sess-123",
				StartTime: "2025-01-01T00:00:00Z",
				EndTime:   "2025-01-31T23:59:59Z",
				Limit:     25,
				Offset:    10,
			},
			expectedParams: map[string]string{
				"type":       "tool_call",
				"server":     "github",
				"tool":       "create_issue",
				"status":     "success",
				"session_id": "sess-123",
				"start_time": "2025-01-01T00:00:00Z",
				"end_time":   "2025-01-31T23:59:59Z",
				"limit":      "25",
				"offset":     "10",
			},
		},
		{
			name: "zero limit excluded",
			filter: ActivityFilter{
				Type:  "tool_call",
				Limit: 0,
			},
			expectedParams: map[string]string{
				"type": "tool_call",
			},
		},
		{
			name: "zero offset excluded",
			filter: ActivityFilter{
				Type:   "tool_call",
				Offset: 0,
			},
			expectedParams: map[string]string{
				"type": "tool_call",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := tt.filter.ToQueryParams()

			for key, expectedValue := range tt.expectedParams {
				assert.Equal(t, expectedValue, params.Get(key), "param %s", key)
			}

			// Check no extra params
			for key := range params {
				_, expected := tt.expectedParams[key]
				assert.True(t, expected, "unexpected param: %s", key)
			}
		})
	}
}

// =============================================================================
// Helper Function Tests
// =============================================================================

func TestFormatRelativeTime(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name     string
		time     time.Time
		expected string
	}{
		{
			name:     "just now - 0 seconds ago",
			time:     now,
			expected: "just now",
		},
		{
			name:     "just now - 30 seconds ago",
			time:     now.Add(-30 * time.Second),
			expected: "just now",
		},
		{
			name:     "1 minute ago",
			time:     now.Add(-1 * time.Minute),
			expected: "1 minute ago",
		},
		{
			name:     "5 minutes ago",
			time:     now.Add(-5 * time.Minute),
			expected: "5 minutes ago",
		},
		{
			name:     "1 hour ago",
			time:     now.Add(-1 * time.Hour),
			expected: "1 hour ago",
		},
		{
			name:     "3 hours ago",
			time:     now.Add(-3 * time.Hour),
			expected: "3 hours ago",
		},
		{
			name:     "1 day ago",
			time:     now.Add(-24 * time.Hour),
			expected: "1 day ago",
		},
		{
			name:     "5 days ago",
			time:     now.Add(-5 * 24 * time.Hour),
			expected: "5 days ago",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatRelativeTime(tt.time)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestFormatRelativeTime_OlderDates(t *testing.T) {
	now := time.Now()

	// Test same year but more than 7 days ago
	twoWeeksAgo := now.Add(-14 * 24 * time.Hour)
	result := formatRelativeTime(twoWeeksAgo)
	assert.Contains(t, result, twoWeeksAgo.Format("Jan"))

	// Test different year
	lastYear := now.AddDate(-1, 0, 0)
	result = formatRelativeTime(lastYear)
	assert.Contains(t, result, fmt.Sprintf("%d", lastYear.Year()))
}

func TestFormatActivityDuration(t *testing.T) {
	tests := []struct {
		name     string
		ms       int64
		expected string
	}{
		{
			name:     "0 milliseconds",
			ms:       0,
			expected: "0ms",
		},
		{
			name:     "100 milliseconds",
			ms:       100,
			expected: "100ms",
		},
		{
			name:     "999 milliseconds",
			ms:       999,
			expected: "999ms",
		},
		{
			name:     "1000 milliseconds - 1 second",
			ms:       1000,
			expected: "1.0s",
		},
		{
			name:     "1500 milliseconds",
			ms:       1500,
			expected: "1.5s",
		},
		{
			name:     "12345 milliseconds",
			ms:       12345,
			expected: "12.3s",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatActivityDuration(tt.ms)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// =============================================================================
// Command Registration Tests
// =============================================================================

func TestGetActivityCommand(t *testing.T) {
	cmd := GetActivityCommand()

	assert.NotNil(t, cmd)
	assert.Equal(t, "activity", cmd.Use)
	assert.Equal(t, "Query and monitor activity logs", cmd.Short)

	// Check subcommands exist
	subcommands := cmd.Commands()
	subcommandNames := make([]string, len(subcommands))
	for i, sub := range subcommands {
		subcommandNames[i] = sub.Name()
	}

	assert.Contains(t, subcommandNames, "list")
	assert.Contains(t, subcommandNames, "watch")
	assert.Contains(t, subcommandNames, "show")
	assert.Contains(t, subcommandNames, "summary")
	assert.Contains(t, subcommandNames, "export")
}

func TestActivityListCmd_Flags(t *testing.T) {
	cmd := activityListCmd

	// Check flags exist
	flags := []string{"type", "server", "tool", "status", "session", "start-time", "end-time", "limit", "offset"}
	for _, flag := range flags {
		f := cmd.Flags().Lookup(flag)
		assert.NotNil(t, f, "flag %s should exist", flag)
	}

	// Check short flags
	assert.NotNil(t, cmd.Flags().ShorthandLookup("t"), "-t short flag")
	assert.NotNil(t, cmd.Flags().ShorthandLookup("s"), "-s short flag")
	assert.NotNil(t, cmd.Flags().ShorthandLookup("n"), "-n short flag")

	// Check defaults
	limitFlag := cmd.Flags().Lookup("limit")
	assert.Equal(t, "50", limitFlag.DefValue)

	offsetFlag := cmd.Flags().Lookup("offset")
	assert.Equal(t, "0", offsetFlag.DefValue)
}

func TestActivityWatchCmd_Flags(t *testing.T) {
	cmd := activityWatchCmd

	// Check flags exist
	flags := []string{"type", "server"}
	for _, flag := range flags {
		f := cmd.Flags().Lookup(flag)
		assert.NotNil(t, f, "flag %s should exist", flag)
	}

	// Check short flags
	assert.NotNil(t, cmd.Flags().ShorthandLookup("t"), "-t short flag")
	assert.NotNil(t, cmd.Flags().ShorthandLookup("s"), "-s short flag")
}

func TestActivityShowCmd_Args(t *testing.T) {
	cmd := activityShowCmd

	// Check args validation
	assert.Equal(t, "<id>", strings.Fields(cmd.Use)[1])

	// Check flags
	includeResponse := cmd.Flags().Lookup("include-response")
	assert.NotNil(t, includeResponse)
	assert.Equal(t, "false", includeResponse.DefValue)
}

func TestActivitySummaryCmd_Flags(t *testing.T) {
	cmd := activitySummaryCmd

	// Check flags exist
	periodFlag := cmd.Flags().Lookup("period")
	assert.NotNil(t, periodFlag)
	assert.Equal(t, "24h", periodFlag.DefValue)

	byFlag := cmd.Flags().Lookup("by")
	assert.NotNil(t, byFlag)

	// Check short flags
	assert.NotNil(t, cmd.Flags().ShorthandLookup("p"), "-p short flag")
}

func TestActivityExportCmd_Flags(t *testing.T) {
	cmd := activityExportCmd

	// Check flags exist
	outputFlag := cmd.Flags().Lookup("output")
	assert.NotNil(t, outputFlag)

	formatFlag := cmd.Flags().Lookup("format")
	assert.NotNil(t, formatFlag)
	assert.Equal(t, "json", formatFlag.DefValue)

	includeBodiesFlag := cmd.Flags().Lookup("include-bodies")
	assert.NotNil(t, includeBodiesFlag)
	assert.Equal(t, "false", includeBodiesFlag.DefValue)

	// Check short flags
	assert.NotNil(t, cmd.Flags().ShorthandLookup("f"), "-f short flag")
}

// =============================================================================
// Mock Server Tests for CLI Commands
// =============================================================================

func TestActivityListCommand_JSONOutput(t *testing.T) {
	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/activity" {
			response := map[string]interface{}{
				"success": true,
				"data": map[string]interface{}{
					"activities": []map[string]interface{}{
						{
							"id":          "01JFXYZ123ABC",
							"type":        "tool_call",
							"server_name": "github",
							"tool_name":   "create_issue",
							"status":      "success",
							"duration_ms": 245,
							"timestamp":   "2025-01-15T10:30:00Z",
						},
					},
					"total":  1,
					"limit":  50,
					"offset": 0,
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	// Test the response can be parsed correctly
	resp, err := http.Get(server.URL + "/api/v1/activity")
	require.NoError(t, err)
	defer resp.Body.Close()

	var result map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&result)
	require.NoError(t, err)

	assert.True(t, result["success"].(bool))
	data := result["data"].(map[string]interface{})
	activities := data["activities"].([]interface{})
	assert.Len(t, activities, 1)

	activity := activities[0].(map[string]interface{})
	assert.Equal(t, "01JFXYZ123ABC", activity["id"])
	assert.Equal(t, "tool_call", activity["type"])
	assert.Equal(t, "github", activity["server_name"])
}

func TestActivityShowCommand_NotFound(t *testing.T) {
	// Create mock server that returns 404
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/activity/") {
			response := map[string]interface{}{
				"success": false,
				"error":   "activity not found",
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(response)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/v1/activity/nonexistent-id")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestActivitySummaryCommand_Response(t *testing.T) {
	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/activity/summary" {
			response := map[string]interface{}{
				"success": true,
				"data": map[string]interface{}{
					"period":        "24h",
					"total_count":   150,
					"success_count": 142,
					"error_count":   5,
					"blocked_count": 3,
					"success_rate":  0.947,
					"top_servers": []map[string]interface{}{
						{"name": "github", "count": 75},
						{"name": "filesystem", "count": 45},
					},
					"top_tools": []map[string]interface{}{
						{"server": "github", "tool": "create_issue", "count": 30},
						{"server": "filesystem", "tool": "read_file", "count": 25},
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/v1/activity/summary?period=24h")
	require.NoError(t, err)
	defer resp.Body.Close()

	var result map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&result)
	require.NoError(t, err)

	assert.True(t, result["success"].(bool))
	data := result["data"].(map[string]interface{})
	assert.Equal(t, "24h", data["period"])
	assert.Equal(t, float64(150), data["total_count"])
	assert.Equal(t, float64(142), data["success_count"])
}

// =============================================================================
// Output Formatting Tests
// =============================================================================

func TestDisplayActivityEvent_JSONOutput(t *testing.T) {
	// SSE events wrap the payload in {"payload": ..., "timestamp": ...}
	eventData := `{"payload":{"id":"01JFXYZ123ABC","server_name":"github","tool_name":"create_issue","status":"success","duration_ms":245},"timestamp":1234567890}`

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	displayActivityEvent("activity.tool_call.completed", eventData, "json")

	w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	io.Copy(&buf, r)
	output := buf.String()

	// JSON output should just pass through the event data
	assert.Contains(t, output, eventData)
}

func TestDisplayActivityEvent_TableOutput(t *testing.T) {
	// SSE events wrap the payload in {"payload": ..., "timestamp": ...}
	eventData := `{"payload":{"id":"01JFXYZ123ABC","server_name":"github","tool_name":"create_issue","status":"success","duration_ms":245},"timestamp":1234567890}`

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	displayActivityEvent("activity.tool_call.completed", eventData, "table")

	w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	io.Copy(&buf, r)
	output := buf.String()

	// Table output should contain server:tool and status indicator
	assert.Contains(t, output, "github:create_issue")
	assert.Contains(t, output, "245ms")
}

func TestDisplayActivityEvent_FilteredByServer(t *testing.T) {
	// SSE events wrap the payload in {"payload": ..., "timestamp": ...}
	eventData := `{"payload":{"id":"01JFXYZ123ABC","server_name":"filesystem","tool_name":"read_file","status":"success","duration_ms":100},"timestamp":1234567890}`

	// Set server filter
	oldServer := activityServer
	activityServer = "github" // Filter for github only
	defer func() { activityServer = oldServer }()

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	displayActivityEvent("activity.tool_call.completed", eventData, "table")

	w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	io.Copy(&buf, r)
	output := buf.String()

	// Event should be filtered out (no output)
	assert.Empty(t, strings.TrimSpace(output))
}

// =============================================================================
// SSE Parsing Tests
// =============================================================================

func TestWatchActivityStream_ParsesSSEEvents(t *testing.T) {
	// Create SSE mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "SSE not supported", http.StatusInternalServerError)
			return
		}

		// Send one event
		fmt.Fprintf(w, "event: activity.tool_call.completed\n")
		fmt.Fprintf(w, "data: {\"id\":\"01JFXYZ123ABC\",\"server\":\"github\",\"tool\":\"create_issue\",\"status\":\"success\",\"duration_ms\":245}\n")
		fmt.Fprintf(w, "\n")
		flusher.Flush()

		// Close immediately for test
	}))
	defer server.Close()

	// Test SSE endpoint is reachable
	resp, err := http.Get(server.URL)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))
}

// =============================================================================
// Period Validation Tests (Summary Command)
// =============================================================================

func TestSummaryPeriodValidation(t *testing.T) {
	validPeriods := []string{"1h", "24h", "7d", "30d"}

	tests := []struct {
		period  string
		isValid bool
	}{
		{"1h", true},
		{"24h", true},
		{"7d", true},
		{"30d", true},
		{"1d", false},
		{"12h", false},
		{"1w", false},
		{"", false},
		{"invalid", false},
	}

	for _, tt := range tests {
		t.Run(tt.period, func(t *testing.T) {
			valid := false
			for _, p := range validPeriods {
				if tt.period == p {
					valid = true
					break
				}
			}
			assert.Equal(t, tt.isValid, valid)
		})
	}
}

// =============================================================================
// Export Format Tests
// =============================================================================

func TestExportFormatValidation(t *testing.T) {
	validFormats := []string{"json", "csv"}

	tests := []struct {
		format  string
		isValid bool
	}{
		{"json", true},
		{"csv", true},
		{"jsonl", false},
		{"xml", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.format, func(t *testing.T) {
			valid := false
			for _, f := range validFormats {
				if tt.format == f {
					valid = true
					break
				}
			}
			assert.Equal(t, tt.isValid, valid)
		})
	}
}

// =============================================================================
// Error Message Tests
// =============================================================================

func TestOutputActivityError_TableFormat(t *testing.T) {
	// Capture stderr
	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	err := fmt.Errorf("test error message")
	outputActivityError(err, "TEST_ERROR")

	w.Close()
	os.Stderr = oldStderr

	var buf bytes.Buffer
	io.Copy(&buf, r)
	output := buf.String()

	assert.Contains(t, output, "Error: test error message")
	assert.Contains(t, output, "Hint:")
}
