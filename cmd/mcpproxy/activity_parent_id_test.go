package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Activity transparency: a code_execution parent and its sandboxed sub-calls are
// joined by ParentID (the parent record's request_id). The CLI has to be able to
// (a) ask for the children, (b) mark them in the table, (c) point back at the
// parent from the detail view, and (d) carry the filter into export.

func TestActivityFilter_ToQueryParams_ParentID(t *testing.T) {
	tests := []struct {
		name     string
		filter   ActivityFilter
		wantKey  string
		wantVal  string
		wantNone bool
	}{
		{
			name:    "parent id is sent as parent_id",
			filter:  ActivityFilter{ParentID: "req-parent-123"},
			wantKey: "parent_id",
			wantVal: "req-parent-123",
		},
		{
			name:     "empty parent id is omitted",
			filter:   ActivityFilter{},
			wantKey:  "parent_id",
			wantNone: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := tt.filter.ToQueryParams()
			if tt.wantNone {
				assert.False(t, params.Has(tt.wantKey), "param %s should be omitted", tt.wantKey)
				return
			}
			assert.Equal(t, tt.wantVal, params.Get(tt.wantKey))
		})
	}
}

func TestActivityListCmd_ParentIDFlag(t *testing.T) {
	f := activityListCmd.Flags().Lookup("parent-id")
	assert.NotNil(t, f, "--parent-id flag should exist on activity list")
	if f != nil {
		assert.Equal(t, "", f.DefValue)
		assert.Contains(t, f.Usage, "code_execution")
	}
}

func TestActivityExportCmd_ParentIDFlag(t *testing.T) {
	f := activityExportCmd.Flags().Lookup("parent-id")
	assert.NotNil(t, f, "--parent-id flag should exist on activity export")
}

// The export command hand-builds its query string (it does not go through
// ToQueryParams), so the flag has to be wired there separately or it is a
// silent no-op.
func TestActivityExportQueryParams_ParentID(t *testing.T) {
	prevParent := activityParentID
	prevFormat := activityExportFormat
	t.Cleanup(func() {
		activityParentID = prevParent
		activityExportFormat = prevFormat
	})

	activityExportFormat = "json"

	activityParentID = ""
	assert.False(t, activityExportQueryParams().Has("parent_id"), "empty parent id should be omitted")

	activityParentID = "req-parent-123"
	q := activityExportQueryParams()
	assert.Equal(t, "req-parent-123", q.Get("parent_id"))
	assert.Equal(t, "json", q.Get("format"))
}

func TestActivityChildToolCell(t *testing.T) {
	child := map[string]interface{}{"parent_id": "req-parent-123"}
	assert.Equal(t, "└ github:create_issue", activityChildToolCell(child, "github:create_issue"))

	parent := map[string]interface{}{}
	assert.Equal(t, "code_execution", activityChildToolCell(parent, "code_execution"),
		"a record with no parent_id must not be marked as a child")
}

func TestFormatParentMarker(t *testing.T) {
	withParent := map[string]interface{}{"parent_id": "req-parent-123"}
	marker := formatParentMarker(withParent)
	assert.Contains(t, marker, "child of")
	assert.Contains(t, marker, "req-parent-123")

	assert.Equal(t, "", formatParentMarker(map[string]interface{}{}))
}

func TestIsCodeExecutionParent(t *testing.T) {
	byToolName := map[string]interface{}{
		"type":      "internal_tool_call",
		"tool_name": "code_execution",
	}
	assert.True(t, isCodeExecutionParent(byToolName))

	byMetadata := map[string]interface{}{
		"type": "internal_tool_call",
		"metadata": map[string]interface{}{
			"internal_tool_name": "code_execution",
		},
	}
	assert.True(t, isCodeExecutionParent(byMetadata))

	otherInternal := map[string]interface{}{
		"type":      "internal_tool_call",
		"tool_name": "retrieve_tools",
	}
	assert.False(t, isCodeExecutionParent(otherInternal))

	// A plain tool_call that happens to be named code_execution is not a parent
	// record — only the internal_tool_call record spawns sub-calls.
	wrongType := map[string]interface{}{
		"type":      "tool_call",
		"tool_name": "code_execution",
	}
	assert.False(t, isCodeExecutionParent(wrongType))
}

func TestFormatToolCallEvent_ChildMarker(t *testing.T) {
	event := map[string]interface{}{
		"source":      "mcp",
		"server_name": "github",
		"tool_name":   "create_issue",
		"status":      "success",
		"parent_id":   "req-parent-123",
	}
	line := formatToolCallEvent(event, "12:00:00")
	assert.True(t, strings.Contains(line, "[child of req-parent-123]"),
		"watch output should mark sub-calls, got: %s", line)
}
