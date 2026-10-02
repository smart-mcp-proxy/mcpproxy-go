package main

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// The move_client label names a concrete destination profile, so the command
// beside it must too (FR-035). A daemon that predates Fix.Profile sends none,
// and the hint then falls back to the placeholder.
func TestAccessExplain_MoveClientFixNamesDestinationProfile(t *testing.T) {
	for _, tc := range []struct {
		name string
		fix  runtime.Fix
		want string
	}{
		{"names destination", runtime.Fix{Action: profile.FixMoveClient, Target: "cursor", Profile: "work-full"}, "mcpproxy client set-profile cursor work-full"},
		{"old daemon, no field", runtime.Fix{Action: profile.FixMoveClient, Target: "cursor"}, "mcpproxy client set-profile cursor <profile>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, fixCommand(tc.fix, "github:create_issue"))
		})
	}
}

// A tool_approval failure targets one tool, so its hint must approve only that
// tool: `upstream approve <server>` with no tool list approves every pending
// tool of the server. Only a quarantined SERVER (target = the server name) is
// approved as a whole.
func TestAccessExplain_ApproveToolFixNamesOnlyThatTool(t *testing.T) {
	for _, tc := range []struct {
		name, target, want string
	}{
		{"tool approval", "github:create_issue", "mcpproxy upstream approve github create_issue"},
		{"server quarantine", "github", "mcpproxy upstream approve github"},
		{"cut at the first colon only", "github:ns:create_issue", "mcpproxy upstream approve github ns:create_issue"},
		{"empty tool part is the server", "github:", "mcpproxy upstream approve github:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := fixCommand(runtime.Fix{Action: profile.FixApproveTool, Target: tc.target}, "github:create_issue")
			require.Equal(t, tc.want, got)
		})
	}
}
