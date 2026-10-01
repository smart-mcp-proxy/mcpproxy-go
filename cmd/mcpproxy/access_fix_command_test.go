package main

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

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
