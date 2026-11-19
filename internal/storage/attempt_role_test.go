package storage

import "testing"

func TestAttemptRoleOf(t *testing.T) {
	cases := []struct {
		name string
		rec  *ActivityRecord
		want AttemptRole
	}{
		{"nil", nil, AttemptRole{}},
		{"tool_call success", &ActivityRecord{Type: ActivityTypeToolCall, Status: ActivityStatusSuccess}, AttemptRole{Counted: true}},
		{"tool_call error", &ActivityRecord{Type: ActivityTypeToolCall, Status: ActivityStatusError}, AttemptRole{Counted: true, Failed: true}},
		{"tool_call error refused by the proxy", &ActivityRecord{Type: ActivityTypeToolCall, Status: ActivityStatusError, ErrorClass: ErrorClassProxyPolicy}, AttemptRole{Counted: true, Failed: true, Block: true}},
		{"tool_call blocked (sandbox child)", &ActivityRecord{Type: ActivityTypeToolCall, Status: ActivityStatusBlocked}, AttemptRole{Counted: true, Failed: true, Block: true}},
		{"tool_call rejected (limiter shed)", &ActivityRecord{Type: ActivityTypeToolCall, Status: ActivityStatusRejected, ErrorClass: ErrorClassProxyPolicy}, AttemptRole{Counted: true, Failed: true, Block: true}},
		{"a success never is a block, whatever its class", &ActivityRecord{Type: ActivityTypeToolCall, Status: ActivityStatusSuccess, ErrorClass: ErrorClassProxyPolicy}, AttemptRole{Counted: true}},
		{"mirror of a tool_call", &ActivityRecord{Type: ActivityTypeInternalToolCall, ToolName: "call_tool_read", Status: ActivityStatusError}, AttemptRole{}},
		{"builtin success", &ActivityRecord{Type: ActivityTypeInternalToolCall, ToolName: "retrieve_tools", Status: ActivityStatusSuccess}, AttemptRole{Counted: true}},
		{"builtin error", &ActivityRecord{Type: ActivityTypeInternalToolCall, ToolName: "upstream_servers", Status: ActivityStatusError}, AttemptRole{Counted: true, Failed: true}},
		{"builtin refused by policy", &ActivityRecord{Type: ActivityTypeInternalToolCall, ToolName: "upstream_servers", Status: ActivityStatusError, ErrorClass: ErrorClassProxyPolicy}, AttemptRole{Counted: true, Failed: true, Block: true}},
		{"sole blocking decision", &ActivityRecord{Type: ActivityTypePolicyDecision, Status: "blocked"}, AttemptRole{Counted: true, Failed: true, Block: true}},
		{"sole blocking decision, old spelling", &ActivityRecord{Type: ActivityTypePolicyDecision, Status: "block"}, AttemptRole{Counted: true, Failed: true, Block: true}},
		{"blocking decision with a tool_call companion", &ActivityRecord{Type: ActivityTypePolicyDecision, Status: "blocked", Companion: true}, AttemptRole{}},
		{"warning decision", &ActivityRecord{Type: ActivityTypePolicyDecision, Status: "warn"}, AttemptRole{}},
		{"redaction decision", &ActivityRecord{Type: ActivityTypePolicyDecision, Status: "redacted"}, AttemptRole{}},
		{"prompt", &ActivityRecord{Type: ActivityTypePromptGet, Status: ActivityStatusError}, AttemptRole{}},
		{"system row", &ActivityRecord{Type: ActivityTypeServerChange, Status: ActivityStatusSuccess}, AttemptRole{}},
	}
	for _, tc := range cases {
		if got := AttemptRoleOf(tc.rec); got != tc.want {
			t.Errorf("%s: got %+v, want %+v", tc.name, got, tc.want)
		}
	}
}
