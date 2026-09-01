package server

import (
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// TestBuildPatchConfig_RejectsUnknownIsolationMode covers the MCP twin of the
// REST `mode_override` hole (GH #1142): `upstream_servers patch` unmarshalled
// isolation_json straight into config.IsolationConfig, so a bogus mode was
// persisted to BBolt and ~/.mcpproxy/mcp_config.json and only surfaced as a
// failed config validation on the NEXT daemon start.
func TestBuildPatchConfig_RejectsUnknownIsolationMode(t *testing.T) {
	p := &MCPProxyServer{logger: zap.NewNop()}
	existing := &config.ServerConfig{Name: "srv", Protocol: "stdio", Command: "npx"}

	for _, bad := range []string{"bogus", "Docker", "docker "} {
		t.Run(bad, func(t *testing.T) {
			request := mcp.CallToolRequest{Params: mcp.CallToolParams{
				Name: "upstream_servers",
				Arguments: map[string]interface{}{
					"operation":      "patch",
					"name":           "srv",
					"isolation_json": `{"mode":"` + bad + `"}`,
				},
			}}

			patch, _, err := p.buildPatchConfigFromRequest(request, existing)
			if err == nil {
				t.Fatalf("expected an error for isolation mode %q, got patch %+v", bad, patch)
			}
			for _, want := range []string{"docker", "sandbox", "none"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not name the accepted value %q", err, want)
				}
			}
		})
	}
}

// TestBuildPatchConfig_AcceptsKnownIsolationModes guards against over-rejecting
// (an empty mode is the documented "clear / inherit" value).
func TestBuildPatchConfig_AcceptsKnownIsolationModes(t *testing.T) {
	p := &MCPProxyServer{logger: zap.NewNop()}
	existing := &config.ServerConfig{Name: "srv", Protocol: "stdio", Command: "npx"}

	for _, good := range []string{"docker", "sandbox", "none", ""} {
		t.Run("mode="+good, func(t *testing.T) {
			request := mcp.CallToolRequest{Params: mcp.CallToolParams{
				Name: "upstream_servers",
				Arguments: map[string]interface{}{
					"operation":      "patch",
					"name":           "srv",
					"isolation_json": `{"mode":"` + good + `"}`,
				},
			}}

			patch, _, err := p.buildPatchConfigFromRequest(request, existing)
			if err != nil {
				t.Fatalf("mode %q must be accepted, got %v", good, err)
			}
			if patch == nil || patch.Isolation == nil {
				t.Fatalf("expected an isolation patch, got %+v", patch)
			}
		})
	}
}
