package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	mcp "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/registries"
)

// newAddFromRegistryTestServer builds an MCPProxyServer whose mainServer is a
// real *Server backed by a live runtime+storage, so add_from_registry can run
// through the keystone op (resolve → derive → persist) end-to-end. The base
// createTestMCPProxyServer wires mainServer=nil, which is enough for read paths
// but not for this write op.
func newAddFromRegistryTestServer(t *testing.T) *MCPProxyServer {
	t.Helper()

	proxy := createTestMCPProxyServer(t)

	logger := zap.NewNop()
	cfg := config.DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.Listen = "127.0.0.1:0"

	mainSrv, err := NewServer(cfg, logger)
	require.NoError(t, err)

	proxy.mainServer = mainSrv
	return proxy
}

// startTestRegistry registers an in-memory registry (id="testreg") whose server
// list is served by a local httptest server, so add_from_registry can resolve a
// registry reference without touching the network. SetRegistriesFromConfig
// replaces the global catalog; tests run sequentially so the last writer wins.
func startTestRegistry(t *testing.T, servers []map[string]interface{}) {
	t.Helper()

	payload := map[string]interface{}{"servers": servers}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}))
	t.Cleanup(srv.Close)

	registries.SetRegistriesFromConfig(&config.Config{
		Registries: []config.RegistryEntry{
			{ID: "testreg", Name: "testreg", ServersURL: srv.URL, Protocol: "modelcontextprotocol/registry"},
		},
	})
}

// callAddFromRegistry drives the upstream_servers handler with the
// add_from_registry operation and returns the raw tool result.
func callAddFromRegistry(t *testing.T, srv *MCPProxyServer, args map[string]interface{}) *mcp.CallToolResult {
	t.Helper()

	req := mcp.CallToolRequest{}
	req.Params.Name = "upstream_servers"
	req.Params.Arguments = args

	result, err := srv.handleUpstreamServers(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	return result
}

// toolResultJSON extracts and unmarshals the JSON text payload from a tool result.
func toolResultJSON(t *testing.T, result *mcp.CallToolResult) map[string]interface{} {
	t.Helper()

	require.NotEmpty(t, result.Content)
	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok, "expected text content")

	var payload map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &payload))
	return payload
}

// Happy path: operation=add_from_registry {registry,id} resolves the entry,
// re-derives the runnable config server-side, and persists it quarantined —
// equivalent to manual construction (spec 070 checkpoint / CN-004).
func TestHandleUpstreamServers_AddFromRegistry_HappyPath(t *testing.T) {
	startTestRegistry(t, []map[string]interface{}{
		{"id": "everything", "name": "everything", "installCmd": "npx -y @modelcontextprotocol/server-everything"},
	})

	srv := newAddFromRegistryTestServer(t)

	result := callAddFromRegistry(t, srv, map[string]interface{}{
		"operation": "add_from_registry",
		"registry":  "testreg",
		"id":        "everything",
	})

	require.False(t, result.IsError, "happy path must not be an error result")
	payload := toolResultJSON(t, result)
	assert.Equal(t, true, payload["success"])

	server, ok := payload["server"].(map[string]interface{})
	require.True(t, ok, "success payload must carry a server object")
	assert.Equal(t, "everything", server["name"])
	assert.Equal(t, "stdio", server["protocol"])
	assert.Equal(t, "npx", server["command"])
	assert.Equal(t, true, server["quarantined"], "new registry server must be quarantined (CN-002)")
}

// Missing required input: the entry declares ${GITHUB_TOKEN} but the request
// supplies no env. The handler must return a structured error (isError=true)
// carrying the stable cross-surface code and the offending input names (FR-003).
func TestHandleUpstreamServers_AddFromRegistry_MissingRequiredInput(t *testing.T) {
	startTestRegistry(t, []map[string]interface{}{
		{"id": "gh", "name": "gh", "installCmd": "npx github-mcp --token ${GITHUB_TOKEN}"},
	})

	srv := newAddFromRegistryTestServer(t)

	result := callAddFromRegistry(t, srv, map[string]interface{}{
		"operation": "add_from_registry",
		"registry":  "testreg",
		"id":        "gh",
	})

	require.True(t, result.IsError, "missing required input must be an error result")
	payload := toolResultJSON(t, result)
	assert.Equal(t, false, payload["success"])
	assert.Equal(t, "missing_required_input", payload["code"])

	missing, ok := payload["missing_inputs"].([]interface{})
	require.True(t, ok, "missing_required_input must list the offending inputs")
	assert.Equal(t, []interface{}{"GITHUB_TOKEN"}, missing)
}
