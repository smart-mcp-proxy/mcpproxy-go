package server

import (
	"encoding/json"

	mcpserver "github.com/mark3labs/mcp-go/server"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// BenchProxyToolDef is a static built-in proxy/management tool definition
// (name + description + JSON input schema) exposed for the in-repo benchmark
// harness (bench/).
//
// The benchmark scores the per-mode context cost an agent pays for mcpproxy's
// own tools. That cost MUST reflect every tool the live routing-mode servers
// expose — including the shared management tool set (upstream_servers,
// quarantine_security, search_servers, list_registries) that both modes append
// via buildManagementTools — or the benchmark overstates the token savings
// (MCP-3161 / Codex finding on PR #747).
//
// Schema is the exact JSON input schema the proxy advertises via tools/list,
// captured from the live builder. The benchmark's exact-token headline counts
// schemas on BOTH the baseline upstream tools and these proxy tools; omitting
// the proxy schemas while counting the baseline's would overstate savings (the
// MCP-3161 error), so Schema makes the headline honest without drift.
type BenchProxyToolDef struct {
	Name        string
	Description string
	Schema      json.RawMessage
}

// ProxyModeToolDefs returns the static built-in proxy + management tool
// definitions an agent sees in its context window for the given routing mode
// (config.RoutingModeRetrieveTools or config.RoutingModeCodeExecution).
//
// It is built from the SAME builders the live server uses
// (buildCallToolModeTools / buildCodeExecModeTools in mcp_routing.go) so the
// benchmark catalog can never drift from production. Code execution is enabled
// so the real code_execution tool description (not the disabled stub) is scored
// — the code_execution routing mode only makes sense with the tool enabled.
func ProxyModeToolDefs(routingMode string) []BenchProxyToolDef {
	p := &MCPProxyServer{
		logger: zap.NewNop(),
		config: &config.Config{
			EnableCodeExecution: true,
		},
	}

	var serverTools []mcpserver.ServerTool
	switch routingMode {
	case config.RoutingModeCodeExecution:
		serverTools = p.buildCodeExecModeTools()
	default: // retrieve_tools — the default routing mode
		serverTools = p.buildCallToolModeTools()
	}

	defs := make([]BenchProxyToolDef, 0, len(serverTools))
	for _, st := range serverTools {
		def := BenchProxyToolDef{
			Name:        st.Tool.Name,
			Description: st.Tool.Description,
		}
		// InputSchema marshals to the exact {"type":"object","properties":...}
		// an agent receives via tools/list. A marshal failure leaves Schema nil
		// (the benchmark then withholds the headline rather than undercount).
		if raw, err := json.Marshal(st.Tool.InputSchema); err == nil {
			def.Schema = raw
		}
		defs = append(defs, def)
	}
	return defs
}
