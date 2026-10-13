package main

import "testing"

// Spec 082 (FR-018): the CLI's --session must speak WORK sessions — one client,
// one project, across reconnects — while still accepting a raw MCP transport
// session id, so existing scripts keep working.
func TestSessionQueryParam(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  string
	}{
		{
			name:  "work session id routes to the work-session filter",
			value: "ws-2d981237ca6e14a5",
			want:  "work_session_id",
		},
		{
			name:  "raw MCP transport session id still filters by transport session",
			value: "mcp-session-0b0ee2f4-d484-47d0-a3b5-dd7887eb1b00",
			want:  "session_id",
		},
		{
			name:  "an unrecognised value is treated as a transport session, not guessed at",
			value: "something-else",
			want:  "session_id",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sessionQueryParam(tc.value); got != tc.want {
				t.Errorf("sessionQueryParam(%q) = %q, want %q", tc.value, got, tc.want)
			}
		})
	}
}
