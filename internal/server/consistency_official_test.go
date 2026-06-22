package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/httpapi"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/registries"
)

// startOfficialTestRegistry registers an in-memory registry (id="officialreg")
// speaking the official v0.1 protocol: a wrapped { server, _meta } list with one
// packages-only and one remotes-only server. This exercises the real official
// parser + per-entry classification (the GH #567 root fix) end-to-end.
func startOfficialTestRegistry(t *testing.T) {
	t.Helper()

	const payload = `{
      "servers": [
        {
          "server": {
            "name": "pkg-server",
            "description": "packages-only stdio server",
            "version": "1.0.0",
            "packages": [
              { "registryType": "npm", "identifier": "@scope/pkg-server", "version": "1.0.0", "runtimeHint": "npx", "runtimeArguments": [{"type":"named","name":"-y"}] }
            ]
          },
          "_meta": { "io.modelcontextprotocol.registry/official": { "status": "active", "isLatest": true } }
        },
        {
          "server": {
            "name": "remote-server",
            "description": "remotes-only streamable-http server",
            "version": "2.0.0",
            "remotes": [ { "type": "streamable-http", "url": "https://mcp.example.com/mcp" } ]
          },
          "_meta": { "io.modelcontextprotocol.registry/official": { "status": "active", "isLatest": true } }
        }
      ],
      "metadata": { "nextCursor": "" }
    }`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(payload))
	}))
	t.Cleanup(srv.Close)

	registries.SetRegistriesFromConfig(&config.Config{
		// Loopback httptest registry; opt past the SSRF guard (MCP-1076).
		AllowPrivateRegistryFetch: true,
		Registries: []config.RegistryEntry{
			{ID: "officialreg", Name: "officialreg", ServersURL: srv.URL, Protocol: "modelcontextprotocol/registry"},
		},
	})
}

// TestCrossSurfaceConsistency_OfficialClassification is the GH #567 regression:
// classification is per transport entry, never "remotes present ⇒ remote". A
// packages-only official server must persist as a stdio transport (InstallCmd,
// empty URL); a remotes-only server must persist as an http transport. Both the
// REST surface and the shared CLI keystone path must agree.
func TestCrossSurfaceConsistency_OfficialClassification(t *testing.T) {
	startOfficialTestRegistry(t)
	const regID = "officialreg"

	addViaREST := func(t *testing.T, serverID, name string) *config.ServerConfig {
		t.Helper()
		srv := newConsistencyServer(t)
		api := httpapi.NewServer(srv, zap.NewNop().Sugar(), nil)
		body, err := json.Marshal(contracts.AddFromRegistryRequest{Name: name})
		require.NoError(t, err)
		req := httptest.NewRequest(http.MethodPost,
			"/api/v1/registries/"+regID+"/servers/"+serverID+"/add", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-Key", consistencyAPIKey)
		rec := httptest.NewRecorder()
		api.Router().ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, "REST add must succeed: %s", rec.Body.String())
		return persistedServer(t, srv, name)
	}

	addViaCLI := func(t *testing.T, serverID, name string) *config.ServerConfig {
		t.Helper()
		srv := newConsistencyServer(t)
		_, rerr, err := srv.AddServerFromRegistryRef(context.Background(), regID, serverID, name, nil, nil)
		require.NoError(t, err)
		require.Nil(t, rerr)
		return persistedServer(t, srv, name)
	}

	t.Run("packages-only => stdio across surfaces", func(t *testing.T) {
		rest := addViaREST(t, "pkg-server", "pkg-rest")
		cli := addViaCLI(t, "pkg-server", "pkg-cli")
		for _, sc := range []*config.ServerConfig{rest, cli} {
			assert.Equal(t, "stdio", sc.Protocol)
			assert.Equal(t, "npx", sc.Command)
			assert.Equal(t, []string{"-y", "@scope/pkg-server@1.0.0"}, sc.Args)
			assert.Empty(t, sc.URL, "stdio server must not have a URL (issues #483/#567)")
			assert.True(t, sc.Quarantined)
		}
	})

	t.Run("remotes-only => http across surfaces", func(t *testing.T) {
		rest := addViaREST(t, "remote-server", "remote-rest")
		cli := addViaCLI(t, "remote-server", "remote-cli")
		for _, sc := range []*config.ServerConfig{rest, cli} {
			assert.Equal(t, "http", sc.Protocol)
			assert.Equal(t, "https://mcp.example.com/mcp", sc.URL)
			assert.Empty(t, sc.Command, "remote server must not have a stdio command")
			assert.True(t, sc.Quarantined)
		}
	})
}
