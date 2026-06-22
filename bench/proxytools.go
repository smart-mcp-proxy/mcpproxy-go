package bench

import (
	_ "embed"
	"encoding/json"
)

//go:embed proxy_tools_v1.json
var proxyToolsJSON []byte

// proxyTool is a built-in mcpproxy tool definition plus the routing modes that
// expose it in the agent's context.
type proxyTool struct {
	ToolID      string   `json:"tool_id"`
	Name        string   `json:"tool"`
	Description string   `json:"description"`
	Modes       []string `json:"modes"`
}

type proxyToolFixture struct {
	Version string      `json:"version"`
	Tools   []proxyTool `json:"tools"`
}

var proxyTools proxyToolFixture

func init() {
	if err := json.Unmarshal(proxyToolsJSON, &proxyTools); err != nil {
		// The fixture is embedded at build time; a parse failure is a build/test
		// bug, not a runtime condition.
		panic("bench: invalid embedded proxy_tools_v1.json: " + err.Error())
	}
}

// ProxyToolsForMode returns the built-in proxy tool definitions that occupy the
// agent's context window in the given routing mode. Provenance for each
// definition is in proxy_tools_v1.json (captured from internal/server/mcp.go).
func ProxyToolsForMode(mode string) []Tool {
	var out []Tool
	for _, pt := range proxyTools.Tools {
		for _, m := range pt.Modes {
			if m == mode {
				out = append(out, Tool{
					ToolID:      pt.ToolID,
					Name:        pt.Name,
					Description: pt.Description,
				})
				break
			}
		}
	}
	return out
}
