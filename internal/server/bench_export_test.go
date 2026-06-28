package server

import (
	"testing"

	mcpserver "github.com/mark3labs/mcp-go/server"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// TestProxyModeToolDefs_IncludesManagementTools guards the benchmark integrity
// fix (MCP-3161): every routing mode exposes the shared management tool set, so
// the benchmark catalog must include it or it undercounts the proxy-mode context
// cost and overstates the savings.
func TestProxyModeToolDefs_IncludesManagementTools(t *testing.T) {
	mgmt := []string{"upstream_servers", "quarantine_security", "search_servers", "list_registries"}
	for _, mode := range []string{config.RoutingModeRetrieveTools, config.RoutingModeCodeExecution} {
		defs := ProxyModeToolDefs(mode)
		if len(defs) == 0 {
			t.Fatalf("mode %s: no proxy tool defs", mode)
		}
		names := map[string]bool{}
		for _, d := range defs {
			names[d.Name] = true
			if d.Description == "" {
				t.Errorf("mode %s: tool %q has empty description", mode, d.Name)
			}
		}
		for _, m := range mgmt {
			if !names[m] {
				t.Errorf("mode %s: missing management tool %q", mode, m)
			}
		}
	}
}

// TestProxyModeToolDefs_MatchesBuilders pins ProxyModeToolDefs to the live tool
// builders. If a mode's tool set changes in mcp_routing.go, the benchmark
// catalog tracks it automatically and this test proves the coupling holds.
func TestProxyModeToolDefs_MatchesBuilders(t *testing.T) {
	p := &MCPProxyServer{
		logger: zap.NewNop(),
		config: &config.Config{EnableCodeExecution: true},
	}
	cases := map[string][]mcpserver.ServerTool{
		config.RoutingModeRetrieveTools: p.buildCallToolModeTools(),
		config.RoutingModeCodeExecution: p.buildCodeExecModeTools(),
	}
	for mode, builderTools := range cases {
		defs := ProxyModeToolDefs(mode)
		if len(defs) != len(builderTools) {
			t.Fatalf("mode %s: ProxyModeToolDefs len %d != builder len %d", mode, len(defs), len(builderTools))
		}
		for i := range builderTools {
			if defs[i].Name != builderTools[i].Tool.Name {
				t.Errorf("mode %s: def[%d] name %q != builder %q", mode, i, defs[i].Name, builderTools[i].Tool.Name)
			}
			if defs[i].Description != builderTools[i].Tool.Description {
				t.Errorf("mode %s: def[%d] description mismatch for %q", mode, i, defs[i].Name)
			}
		}
	}
}
