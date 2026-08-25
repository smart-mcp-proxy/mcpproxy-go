package storage

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCountsAsCall(t *testing.T) {
	tests := []struct {
		name    string
		rec     *ActivityRecord
		counted bool
		isError bool
	}{
		{"nil record", nil, false, false},
		{"record with no tool", &ActivityRecord{Type: ActivityTypeToolCall}, false, false},

		{"upstream success", &ActivityRecord{Type: ActivityTypeToolCall, ServerName: "github", ToolName: "search", Status: ActivityStatusSuccess}, true, false},
		{"upstream error", &ActivityRecord{Type: ActivityTypeToolCall, ServerName: "github", ToolName: "search", Status: ActivityStatusError}, true, true},
		{"upstream blocked by policy", &ActivityRecord{Type: ActivityTypeToolCall, ServerName: "evil", ToolName: "exfil", Status: ActivityStatusBlocked}, true, true},
		{"upstream shed by the limiter never ran", &ActivityRecord{Type: ActivityTypeToolCall, ServerName: "github", ToolName: "search", Status: ActivityStatusRejected}, false, false},

		{"retrieve_tools", &ActivityRecord{Type: ActivityTypeInternalToolCall, ToolName: "retrieve_tools", Status: ActivityStatusSuccess}, true, false},
		{"describe_tool", &ActivityRecord{Type: ActivityTypeInternalToolCall, ToolName: "describe_tool", Status: ActivityStatusSuccess}, true, false},
		{"failed retrieve_tools", &ActivityRecord{Type: ActivityTypeInternalToolCall, ToolName: "retrieve_tools", Status: ActivityStatusError}, true, true},

		{"successful script bars through its sub-calls", &ActivityRecord{Type: ActivityTypeInternalToolCall, ToolName: "code_execution", Status: ActivityStatusSuccess}, false, false},
		{"failed script has nothing else to represent it", &ActivityRecord{Type: ActivityTypeInternalToolCall, ToolName: "code_execution", Status: ActivityStatusError}, true, true},

		{"management chatter on success", &ActivityRecord{Type: ActivityTypeInternalToolCall, ToolName: "upstream_servers", Status: ActivityStatusSuccess}, false, false},
		{"management chatter on failure", &ActivityRecord{Type: ActivityTypeInternalToolCall, ToolName: "quarantine_security", Status: ActivityStatusError}, false, false},

		{"call_tool_ mirror of a direct dispatch", &ActivityRecord{Type: ActivityTypeInternalToolCall, ToolName: "call_tool_read", Status: ActivityStatusSuccess}, false, false},
		{"failed call_tool_ mirror", &ActivityRecord{Type: ActivityTypeInternalToolCall, ToolName: "call_tool_write", Status: ActivityStatusError}, false, false},

		{"policy block", &ActivityRecord{Type: ActivityTypePolicyDecision, ServerName: "evil", ToolName: "exfil", Status: ActivityStatusBlocked}, true, true},
		{"policy shed", &ActivityRecord{Type: ActivityTypePolicyDecision, ServerName: "evil", ToolName: "exfil", Status: ActivityStatusRejected}, true, true},
		{"policy allow is bookkeeping", &ActivityRecord{Type: ActivityTypePolicyDecision, ServerName: "github", ToolName: "search", Status: ActivityStatusSuccess}, false, false},

		// The events that made the Activity Log's "N calls" a lie (F24, #1046).
		{"system start", &ActivityRecord{Type: ActivityTypeSystemStart, ToolName: "startup", Status: ActivityStatusSuccess}, false, false},
		{"security scan", &ActivityRecord{Type: ActivityTypeSecurityScan, ServerName: "github", ToolName: "search", Status: ActivityStatusSuccess}, false, false},
		{"tool auto-approval", &ActivityRecord{Type: ActivityTypeToolQuarantineChange, ServerName: "github", ToolName: "search", Status: "tool_auto_approved"}, false, false},
		{"config change", &ActivityRecord{Type: ActivityTypeConfigChange, ToolName: "github", Status: ActivityStatusSuccess}, false, false},
		{"prompt fetch", &ActivityRecord{Type: ActivityTypePromptGet, ServerName: "github", ToolName: "review", Status: ActivityStatusSuccess}, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			counted, isError := CountsAsCall(tt.rec)
			assert.Equal(t, tt.counted, counted, "counted")
			assert.Equal(t, tt.isError, isError, "isError")
		})
	}
}

// An error can only be classified as one if it is a call in the first place.
func TestCountsAsCall_ErrorsAreAlwaysCalls(t *testing.T) {
	for _, typ := range ValidActivityTypes {
		for _, status := range ValidActivityStatuses {
			rec := &ActivityRecord{Type: ActivityType(typ), ServerName: "s", ToolName: "t", Status: status}
			counted, isError := CountsAsCall(rec)
			if isError {
				assert.True(t, counted, "%s/%s: classified as a failed call without being a call", typ, status)
			}
		}
	}
}
