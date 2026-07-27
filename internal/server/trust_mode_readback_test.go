package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/httpapi"
)

// Spec 086 read-back regression (QA #919 CFG-04 / CFG-06 / SRF-07).
//
// contracts.Server.TrustMode and the generated OpenAPI schema both promise that
// the per-server trust tier is "surfaced on the GET path so clients can read
// back the persisted mode". Before this test the write path worked (POST/PATCH
// persisted trust_mode) but every read surface dropped it: the runtime server
// projections never emitted a "trust_mode" key, so management.ListServers —
// which feeds GET /api/v1/servers, `mcpproxy upstream list -o json` (the same
// REST route) and the SSE servers.changed embed — always left the field empty.
//
// This drives the real chi router against a real *Server: POST a server with
// trust_mode=scan, then GET the list back and require the tier to survive the
// round trip.
func TestTrustModeReadBack_RESTGetServers(t *testing.T) {
	srv := newConsistencyServer(t)
	api := httpapi.NewServer(srv, zap.NewNop().Sugar(), nil)

	const serverName = "qa919-trust-scan"

	// --- write: POST /api/v1/servers with an explicit trust tier -------------
	addBody := `{"name":"` + serverName + `","url":"http://127.0.0.1:9/mcp",` +
		`"protocol":"http","enabled":false,"trust_mode":"scan"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/servers", bytes.NewReader([]byte(addBody)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", consistencyAPIKey)
	rec := httptest.NewRecorder()
	api.Router().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "POST /api/v1/servers must succeed: %s", rec.Body.String())

	// Sanity: the write path persisted it (this half already worked).
	require.Equal(t, "scan", persistedServer(t, srv, serverName).TrustMode,
		"precondition: POST must persist trust_mode")

	// --- read: GET /api/v1/servers ------------------------------------------
	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/servers", http.NoBody)
	getReq.Header.Set("X-API-Key", consistencyAPIKey)
	getRec := httptest.NewRecorder()
	api.Router().ServeHTTP(getRec, getReq)
	require.Equal(t, http.StatusOK, getRec.Code, "GET /api/v1/servers must succeed: %s", getRec.Body.String())

	var envelope struct {
		Success bool                         `json:"success"`
		Data    contracts.GetServersResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(getRec.Body.Bytes(), &envelope))
	require.True(t, envelope.Success)

	var found *contracts.Server
	for i := range envelope.Data.Servers {
		if envelope.Data.Servers[i].Name == serverName {
			found = &envelope.Data.Servers[i]
			break
		}
	}
	require.NotNil(t, found, "GET /api/v1/servers must list the server just added: %s", getRec.Body.String())
	assert.Equal(t, "scan", found.TrustMode,
		"trust_mode must be readable back on the GET payload (CFG-06/SRF-07)")

	// The wire form matters too: an empty string would be omitted by omitempty,
	// so assert the key is actually present in the serialized response.
	assert.Contains(t, getRec.Body.String(), `"trust_mode":"scan"`,
		"the serialized GET payload must carry the trust_mode key")
}

// A server that never configured a trust tier must keep the field omitted
// (empty), not gain a synthesized default — the contract says "Omitted when
// empty (server predates the field / relies on legacy flags)".
func TestTrustModeReadBack_UnsetStaysEmpty(t *testing.T) {
	srv := newConsistencyServer(t)
	api := httpapi.NewServer(srv, zap.NewNop().Sugar(), nil)

	const serverName = "qa919-trust-unset"
	addBody := `{"name":"` + serverName + `","url":"http://127.0.0.1:9/mcp",` +
		`"protocol":"http","enabled":false}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/servers", bytes.NewReader([]byte(addBody)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", consistencyAPIKey)
	rec := httptest.NewRecorder()
	api.Router().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "POST must succeed: %s", rec.Body.String())
	require.Empty(t, persistedServer(t, srv, serverName).TrustMode,
		"precondition: no trust_mode was configured")

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/servers", http.NoBody)
	getReq.Header.Set("X-API-Key", consistencyAPIKey)
	getRec := httptest.NewRecorder()
	api.Router().ServeHTTP(getRec, getReq)
	require.Equal(t, http.StatusOK, getRec.Code)

	var envelope struct {
		Success bool                         `json:"success"`
		Data    contracts.GetServersResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(getRec.Body.Bytes(), &envelope))
	for i := range envelope.Data.Servers {
		if envelope.Data.Servers[i].Name == serverName {
			assert.Empty(t, envelope.Data.Servers[i].TrustMode,
				"an unset trust_mode must stay empty/omitted, not be synthesized")
			return
		}
	}
	t.Fatalf("server %q missing from GET payload: %s", serverName, getRec.Body.String())
}

// The MCP surface reads a different seam (handleListUpstreams projects straight
// from storage), so it needs its own guard: `upstream_servers operation=list`
// must expose the persisted trust tier too.
func TestTrustModeReadBack_MCPUpstreamServersList(t *testing.T) {
	proxy := createTestMCPProxyServer(t)

	require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{
		Name:      "qa919-mcp-scan",
		URL:       "http://127.0.0.1:9/mcp",
		Protocol:  "http",
		Enabled:   false,
		TrustMode: string(config.TrustModeScan),
	}))
	require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{
		Name:     "qa919-mcp-unset",
		URL:      "http://127.0.0.1:9/mcp",
		Protocol: "http",
		Enabled:  false,
	}))

	result, err := proxy.handleUpstreamServers(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "upstream_servers",
			Arguments: map[string]interface{}{"operation": "list"},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError, "list must succeed: %v", result.Content)

	payload := toolResultJSON(t, result)
	servers, ok := payload["servers"].([]interface{})
	require.True(t, ok, "list payload must carry servers: %v", payload)

	byName := map[string]map[string]interface{}{}
	for _, raw := range servers {
		m, ok := raw.(map[string]interface{})
		require.True(t, ok)
		name, _ := m["name"].(string)
		byName[name] = m
	}

	require.Contains(t, byName, "qa919-mcp-scan")
	assert.Equal(t, "scan", byName["qa919-mcp-scan"]["trust_mode"],
		"MCP upstream_servers list must surface the persisted trust_mode")

	require.Contains(t, byName, "qa919-mcp-unset")
	_, present := byName["qa919-mcp-unset"]["trust_mode"]
	assert.False(t, present, "an unset trust_mode must stay absent from the MCP list payload")
}
