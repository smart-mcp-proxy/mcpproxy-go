package bench

import (
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/server"
)

// ProxyToolsForMode returns the built-in mcpproxy proxy + management tool
// definitions that occupy the agent's context window in the given routing mode.
//
// The catalog is derived directly from the live server tool builders
// (internal/server.ProxyModeToolDefs → buildCallToolModeTools /
// buildCodeExecModeTools in internal/server/mcp_routing.go). This is the single
// source of truth: both routing modes append the shared management tool set
// (upstream_servers, quarantine_security, search_servers, list_registries), so
// deriving from the builders guarantees the benchmark counts the real per-mode
// context cost and can never drift from production by re-introducing the
// undercount that inflated the headline savings (MCP-3161).
func ProxyToolsForMode(mode string) []Tool {
	var routingMode string
	switch mode {
	case ModeCodeExecution:
		routingMode = config.RoutingModeCodeExecution
	case ModeRetrieveTools:
		routingMode = config.RoutingModeRetrieveTools
	default:
		return nil
	}

	defs := server.ProxyModeToolDefs(routingMode)
	out := make([]Tool, 0, len(defs))
	for _, d := range defs {
		out = append(out, Tool{
			ToolID:      "mcpproxy:" + d.Name,
			Name:        d.Name,
			Description: d.Description,
			Schema:      d.Schema,
		})
	}
	return out
}
