package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/httpapi"
)

// Spec 070 keystone regression (T021 / CN-004 / FR-010 / SC-004).
//
// Every add surface (REST, MCP, CLI) funnels through the single keystone
// AddServerFromRegistryRef, so the registry-result -> config.ServerConfig
// normalization lives in exactly one place. This test is the guard that keeps
// it that way: it drives the SAME logical add — same (registry, serverId, name,
// env) — through each surface against its own isolated server, then asserts the
// PERSISTED config.ServerConfig is byte-identical across all three (modulo the
// Created/Updated timestamps) and that every one is quarantined (SC-004).
//
// If a future change lets one surface bypass the keystone (e.g. the Web UI's
// old client-side install_cmd parsing, or a surface that forgets the quarantine
// default), the persisted configs diverge and this test fails.
//
// Surfaces exercised in-process:
//   - MCP: the real upstream_servers handler (operation=add_from_registry),
//     which extracts args from the MCP request (note: env arrives as env_json).
//   - REST: a real HTTP POST to the actual chi router handler
//     (POST /api/v1/registries/{id}/servers/{serverId}/add), exercising JSON
//     body decode + URL param extraction + auth.
//   - CLI add path: the CLI is a thin HTTP client of the REST route, so its
//     config-derivation contribution bottoms out at the same controller method
//     (AddServerFromRegistryRef). The full CLI binary->daemon path is separately
//     covered end-to-end by TestRegistryAddCLIE2E.
func TestCrossSurfaceConsistency_RegistryAdd(t *testing.T) {
	// One stdio entry whose install command declares a required input
	// (${API_KEY}); supplying it via env exercises required-input satisfaction
	// AND env carry-through on the persisted config.
	servers := []map[string]interface{}{
		{"id": "everything", "name": "everything", "installCmd": "npx -y srv --key ${API_KEY}"},
	}
	startTestRegistry(t, servers) // registers id="testreg" against a local httptest server

	const (
		regID    = "testreg"
		serverID = "everything"
		addName  = "consistency-srv"
	)
	env := map[string]string{"API_KEY": "secret-123"}

	// --- Surface 1: MCP -------------------------------------------------------
	srvMCP := newConsistencyServer(t)
	proxy := createTestMCPProxyServer(t)
	proxy.mainServer = srvMCP

	envJSON, err := json.Marshal(env)
	require.NoError(t, err)
	mcpResult := callAddFromRegistry(t, proxy, map[string]interface{}{
		"operation": "add_from_registry",
		"registry":  regID,
		"id":        serverID,
		"name":      addName,
		"env_json":  string(envJSON),
	})
	require.False(t, mcpResult.IsError, "MCP add must succeed: %v", mcpResult.Content)
	mcpCfg := persistedServer(t, srvMCP, addName)

	// --- Surface 2: REST (real HTTP through the chi router) -------------------
	srvREST := newConsistencyServer(t)
	api := httpapi.NewServer(srvREST, zap.NewNop().Sugar(), nil)

	body, err := json.Marshal(contracts.AddFromRegistryRequest{Name: addName, Env: env})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/registries/"+regID+"/servers/"+serverID+"/add", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", consistencyAPIKey)
	rec := httptest.NewRecorder()
	api.Router().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "REST add must succeed: %s", rec.Body.String())
	restCfg := persistedServer(t, srvREST, addName)

	// --- Surface 3: CLI add path (shared controller bottom) -------------------
	srvCLI := newConsistencyServer(t)
	cliCfg, rerr, err := srvCLI.AddServerFromRegistryRef(context.Background(), regID, serverID, addName, env, nil)
	require.NoError(t, err)
	require.Nil(t, rerr)
	require.NotNil(t, cliCfg)
	cliPersisted := persistedServer(t, srvCLI, addName)

	// --- Cross-surface byte-identity (CN-004) ---------------------------------
	mcpJSON := canonicalServerJSON(t, mcpCfg)
	restJSON := canonicalServerJSON(t, restCfg)
	cliJSON := canonicalServerJSON(t, cliPersisted)

	assert.Equal(t, mcpJSON, restJSON, "REST add must persist a byte-identical config to MCP add")
	assert.Equal(t, mcpJSON, cliJSON, "CLI add path must persist a byte-identical config to MCP add")

	// --- Quarantine invariant (SC-004 / CN-002) -------------------------------
	assert.True(t, mcpCfg.Quarantined, "MCP-added server must be quarantined")
	assert.True(t, restCfg.Quarantined, "REST-added server must be quarantined")
	assert.True(t, cliPersisted.Quarantined, "CLI-added server must be quarantined")

	// --- Sanity on the shared derivation -------------------------------------
	assert.Equal(t, "stdio", mcpCfg.Protocol)
	assert.Equal(t, "npx", mcpCfg.Command)
	assert.Equal(t, []string{"-y", "srv", "--key", "${API_KEY}"}, mcpCfg.Args)
	assert.Equal(t, "secret-123", mcpCfg.Env["API_KEY"])
	assert.True(t, mcpCfg.Enabled)
}

const consistencyAPIKey = "t021-consistency-key"

// newConsistencyServer builds an isolated *Server (own data dir + storage) with
// a known API key so the REST surface can authenticate. The storage handle is
// closed on cleanup so the temp-dir removal succeeds on Windows.
func newConsistencyServer(t *testing.T) *Server {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.Listen = "127.0.0.1:0"
	cfg.APIKey = consistencyAPIKey
	srv, err := NewServer(cfg, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Shutdown() })
	return srv
}

// persistedServer reads the actually-persisted ServerConfig back from the
// server's live config snapshot (not the function return value), so the
// comparison reflects what reached storage.
func persistedServer(t *testing.T, srv *Server, name string) *config.ServerConfig {
	t.Helper()
	cfg := srv.runtime.Config()
	require.NotNil(t, cfg, "runtime config must be available")
	for _, sc := range cfg.Servers {
		if sc != nil && sc.Name == name {
			return sc
		}
	}
	t.Fatalf("server %q not found in persisted config", name)
	return nil
}

// canonicalServerJSON serializes a ServerConfig with the per-add timestamps
// zeroed, so byte-comparison reflects only the derived/persisted fields.
func canonicalServerJSON(t *testing.T, sc *config.ServerConfig) string {
	t.Helper()
	clone := *sc
	clone.Created = time.Time{}
	clone.Updated = time.Time{}
	b, err := json.Marshal(&clone)
	require.NoError(t, err)
	return string(b)
}
