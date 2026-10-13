package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// TestRoutingModeServers_CarryInstructions pins that every server /mcp can be
// served by hands the agent initialize instructions. /mcp is served through
// GetMCPServerForMode, which returns callToolServer for the default
// retrieve_tools mode — not p.server — so instructions set only on p.server
// never reached a real client, and agents connected through the default
// endpoint got no hint that upstream tools sit behind retrieve_tools.
func TestRoutingModeServers_CarryInstructions(t *testing.T) {
	proxy, _ := newStoredScriptProxyCfg(t, nil)

	for _, tc := range []struct {
		mode string
		want string
	}{
		{config.RoutingModeRetrieveTools, "retrieve_tools"},
		{config.RoutingModeCodeExecution, "code_execution"},
		{config.RoutingModeDirect, "server__tool"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			srv := proxy.GetMCPServerForMode(tc.mode)
			require.NotNil(t, srv)
			got := initializeInstructions(t, adminCtx(), srv)
			assert.Contains(t, got, tc.want, "%s: initialize must carry routing guidance", tc.mode)
		})
	}
}

// TestRoutingModeServers_CustomInstructionsReachEveryMode: the operator's
// `instructions` replaces the default on every surface, not just p.server.
func TestRoutingModeServers_CustomInstructionsReachEveryMode(t *testing.T) {
	const custom = "Team rule: prefer the github MCP over the gh CLI."
	proxy := newCustomInstructionsProxy(t, custom)
	for _, mode := range []string{config.RoutingModeRetrieveTools, config.RoutingModeCodeExecution, config.RoutingModeDirect} {
		got := initializeInstructions(t, adminCtx(), proxy.GetMCPServerForMode(mode))
		assert.Contains(t, got, custom, mode)
	}
}
