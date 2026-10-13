package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 098 T022 — `mcpproxy activity list` must accept and render the
// `preflight` activity type (FR-014, US3 acceptance 2).
//
// Every test here feeds the renderer a record that went through the same JSON
// round trip the REST client performs, because that is what turns the stored
// `int` counts into `float64` — the exact shape the CLI actually sees.

// preflightRecordJSON builds a stored preflight activity record and round-trips
// it through JSON, mirroring GET /api/v1/activity.
func preflightRecordJSON(t *testing.T, rec runtime.PreflightActivity) map[string]interface{} {
	t.Helper()

	tools := make([]map[string]interface{}, 0, len(rec.Tools))
	reasons := map[string]int{}
	for _, tool := range rec.Tools {
		entry := map[string]interface{}{
			storage.PreflightPerToolKeyID:     tool.ID,
			storage.PreflightPerToolKeyStatus: tool.Status,
		}
		if tool.Reason != "" {
			entry[storage.PreflightPerToolKeyReason] = tool.Reason
			reasons[tool.Reason]++
		}
		tools = append(tools, entry)
	}

	record := map[string]interface{}{
		"id":         "01JPREFLIGHT0001",
		"type":       string(storage.ActivityTypePreflight),
		"source":     "api",
		"status":     runtime.PreflightActivityStatus(rec.Verdict),
		"request_id": rec.RequestID,
		"timestamp":  "2026-08-15T10:00:00Z",
		"metadata": map[string]interface{}{
			storage.MetadataKeyPreflightVerdict:  rec.Verdict,
			storage.MetadataKeyPreflightIDsCount: len(rec.Tools),
			storage.MetadataKeyPreflightReasons:  reasons,
			storage.MetadataKeyPreflightPerTool:  tools,
		},
	}

	encoded, err := json.Marshal(record)
	require.NoError(t, err)
	var decoded map[string]interface{}
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	return decoded
}

func TestActivityFilter_Validate_AcceptsPreflightType(t *testing.T) {
	tests := []struct {
		name string
		typ  string
	}{
		{name: "preflight alone", typ: "preflight"},
		{name: "preflight combined with tool_call", typ: "tool_call,preflight"},
		{name: "preflight with surrounding spaces", typ: "preflight, tool_call"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter := ActivityFilter{Type: tt.typ}
			assert.NoError(t, filter.Validate())
		})
	}
}

func TestActivityFilter_Validate_StillRejectsUnknownType(t *testing.T) {
	filter := ActivityFilter{Type: "preflights"}
	err := filter.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid type")
}

func TestPreflightActivitySummary(t *testing.T) {
	t.Run("all ready", func(t *testing.T) {
		record := preflightRecordJSON(t, runtime.PreflightActivity{
			Verdict: "ready",
			Tools: []runtime.PreflightToolOutcome{
				{ID: "ctl:echo", Status: "ready"},
				{ID: "gh:sync", Status: "ready"},
			},
		})
		assert.Equal(t, "ready (2 tools)", preflightActivitySummary(record))
	})

	t.Run("single tool is not pluralized", func(t *testing.T) {
		record := preflightRecordJSON(t, runtime.PreflightActivity{
			Verdict: "ready",
			Tools:   []runtime.PreflightToolOutcome{{ID: "ctl:echo", Status: "ready"}},
		})
		assert.Equal(t, "ready (1 tool)", preflightActivitySummary(record))
	})

	t.Run("reasons are rolled up, most frequent first", func(t *testing.T) {
		record := preflightRecordJSON(t, runtime.PreflightActivity{
			Verdict: "blocked",
			Tools: []runtime.PreflightToolOutcome{
				{ID: "ctl:echo", Status: "ready"},
				{ID: "gh:sync", Status: "unavailable", Reason: "server_disabled"},
				{ID: "gh:close", Status: "unavailable", Reason: "server_disabled"},
				{ID: "slack:post", Status: "unavailable", Reason: "tool_changed"},
			},
		})
		assert.Equal(t,
			"blocked (4 tools): server_disabled x2, tool_changed x1",
			preflightActivitySummary(record))
	})

	t.Run("ties break alphabetically for a stable line", func(t *testing.T) {
		record := preflightRecordJSON(t, runtime.PreflightActivity{
			Verdict: "unknown_ids",
			Tools: []runtime.PreflightToolOutcome{
				{ID: "a:one", Status: "unavailable", Reason: "not_found"},
				{ID: "b:two", Status: "unavailable", Reason: "server_disabled"},
				{ID: "c:three", Status: "unavailable", Reason: "hash_mismatch"},
			},
		})
		summary := preflightActivitySummary(record)
		for i := 0; i < 5; i++ {
			assert.Equal(t, summary, preflightActivitySummary(record))
		}
		assert.Equal(t,
			"unknown_ids (3 tools): hash_mismatch x1, not_found x1, server_disabled x1",
			summary)
	})

	t.Run("caps the reason list so the table cell stays readable", func(t *testing.T) {
		record := preflightRecordJSON(t, runtime.PreflightActivity{
			Verdict: "blocked",
			Tools: []runtime.PreflightToolOutcome{
				{ID: "a:1", Status: "unavailable", Reason: "server_disabled"},
				{ID: "b:1", Status: "unavailable", Reason: "tool_changed"},
				{ID: "c:1", Status: "unavailable", Reason: "not_found"},
				{ID: "d:1", Status: "unavailable", Reason: "hash_mismatch"},
				{ID: "e:1", Status: "unavailable", Reason: "oauth_required"},
			},
		})
		summary := preflightActivitySummary(record)
		assert.Contains(t, summary, "+2 more")
		assert.Equal(t, 3, strings.Count(summary, " x1"))
	})

	t.Run("falls back to per_tool when the rollup is missing", func(t *testing.T) {
		record := map[string]interface{}{
			"type": string(storage.ActivityTypePreflight),
			"metadata": map[string]interface{}{
				storage.MetadataKeyPreflightVerdict: "unknown_ids",
				storage.MetadataKeyPreflightPerTool: []interface{}{
					map[string]interface{}{
						storage.PreflightPerToolKeyID:     "a:1",
						storage.PreflightPerToolKeyStatus: "unavailable",
						storage.PreflightPerToolKeyReason: "not_found",
					},
				},
			},
		}
		assert.Equal(t, "unknown_ids (1 tool): not_found x1", preflightActivitySummary(record))
	})

	t.Run("non-preflight records get no summary", func(t *testing.T) {
		assert.Equal(t, "", preflightActivitySummary(map[string]interface{}{
			"type": "tool_call",
			"metadata": map[string]interface{}{
				storage.MetadataKeyPreflightVerdict: "ready",
			},
		}))
	})

	t.Run("a preflight record without metadata degrades to empty", func(t *testing.T) {
		assert.Equal(t, "", preflightActivitySummary(map[string]interface{}{
			"type": string(storage.ActivityTypePreflight),
		}))
	})
}

func TestPreflightDetailLines(t *testing.T) {
	record := preflightRecordJSON(t, runtime.PreflightActivity{
		RequestID: "req-098",
		Verdict:   "blocked",
		Tools: []runtime.PreflightToolOutcome{
			{ID: "ctl:echo", Status: "ready"},
			{ID: "gh:sync", Status: "unavailable", Reason: "server_disabled"},
			{ID: "slack:post", Status: "unavailable", Reason: "tool_changed"},
		},
	})

	lines := preflightDetailLines(record)
	require.NotEmpty(t, lines)
	joined := strings.Join(lines, "\n")

	assert.Contains(t, joined, "Preflight:")
	assert.Contains(t, joined, "Verdict:")
	assert.Contains(t, joined, "blocked")
	assert.Contains(t, joined, "Tools Checked:")
	assert.Contains(t, joined, "3")
	assert.Contains(t, joined, "server_disabled x1, tool_changed x1")

	// Per-tool detail keeps the request order and names every reason.
	assert.Contains(t, joined, "ctl:echo")
	assert.Contains(t, joined, "gh:sync")
	assert.Contains(t, joined, "slack:post")
	assert.Less(t, strings.Index(joined, "gh:sync"), strings.Index(joined, "slack:post"))

	// A ready tool carries no reason.
	for _, line := range lines {
		if strings.Contains(line, "ctl:echo") {
			assert.NotContains(t, line, "server_disabled")
		}
	}
}

func TestPreflightDetailLines_NotAPreflightRecord(t *testing.T) {
	assert.Empty(t, preflightDetailLines(map[string]interface{}{"type": "tool_call"}))
	assert.Empty(t, preflightDetailLines(map[string]interface{}{
		"type": string(storage.ActivityTypePreflight),
	}))
}

// Tool ids are caller-supplied strings; an id carrying an ANSI escape must not
// reach the operator's terminal raw (same trust boundary as `tools list`).
func TestPreflightRenderingSanitizesToolIDs(t *testing.T) {
	record := preflightRecordJSON(t, runtime.PreflightActivity{
		Verdict: "unknown_ids",
		Tools: []runtime.PreflightToolOutcome{
			{ID: "\x1b[2J\x1b[1;1Hevil:tool", Status: "unavailable", Reason: "not_found"},
		},
	})

	joined := strings.Join(preflightDetailLines(record), "\n")
	assert.NotContains(t, joined, "\x1b[2J")
	assert.Contains(t, joined, "evil:tool")
}
