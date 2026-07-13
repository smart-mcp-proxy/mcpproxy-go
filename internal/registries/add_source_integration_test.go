package registries

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MCP-866 acceptance: a user-added generic modelcontextprotocol/registry v0.1
// endpoint is searchable through the normal SearchServers path, and the
// registry it came from is tagged custom/unverified (so the keystone add op
// will quarantine its servers).
func TestUserAddedV01Source_IsSearchableAndCustom(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"servers":[{"server":{"name":"acme/widget","description":"a widget server","packages":[{"registryType":"npm","identifier":"@acme/widget","runtimeHint":"npx"}]},"_meta":{"io.modelcontextprotocol.registry/official":{"status":"active","isLatest":true}}}],"metadata":{"nextCursor":""}}`)
	}))
	defer srv.Close()

	cfg := config.DefaultConfig()
	cfg.Registries = []config.RegistryEntry{{
		ID:         "acme",
		Name:       "Acme",
		URL:        srv.URL,
		ServersURL: srv.URL,
		Protocol:   "modelcontextprotocol/registry",
	}}

	SetRegistriesFromConfig(cfg)

	// The merged registry is tagged custom/unverified (computed, not asserted).
	acme := FindRegistry("acme")
	require.NotNil(t, acme)
	assert.Equal(t, config.RegistryProvenanceCustom, acme.Provenance)
	assert.False(t, acme.IsTrusted())

	// And it is searchable through the standard discovery path.
	servers, err := SearchServers(context.Background(), "acme", "", "", 10, nil)
	require.NoError(t, err)
	require.NotEmpty(t, servers, "user-added v0.1 endpoint must be searchable")
	assert.Equal(t, "acme/widget", servers[0].Name)
}

// TestUserAddedStaticJSONSource_IsSearchable is the GH #783 acceptance test: a
// static JSON document (the Fleur app-registry apps.json) added as a custom
// source is browsable end-to-end, and MCPProxy fetches it EXACTLY as configured
// — no /v0.1/servers route and no version/limit/search query appended, both of
// which are official-protocol details that a static file 404s or ignores.
func TestUserAddedStaticJSONSource_IsSearchable(t *testing.T) {
	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		if r.URL.Path != "/fleuristes/app-registry/refs/heads/main/apps.json" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `[
		  {"name":"Fetch","description":"web pages","sourceUrl":"https://github.com/modelcontextprotocol/servers","config":{"mcpKey":"fetch","runtime":"uvx","args":["mcp-server-fetch"]}},
		  {"name":"Memory","description":"knowledge graph","config":{"mcpKey":"memory","runtime":"npx","args":["-y","@modelcontextprotocol/server-memory"]}}
		]`)
	}))
	defer srv.Close()

	appsURL := srv.URL + "/fleuristes/app-registry/refs/heads/main/apps.json"

	// A probe of the pasted URL is what produces this config entry.
	probe, err := ProbeRegistrySource(context.Background(), appsURL)
	require.NoError(t, err, "the pasted static JSON URL must probe cleanly")
	require.Equal(t, appsURL, probe.ServersURL)
	require.Equal(t, protocolGenericJSON, probe.Protocol)

	cfg := config.DefaultConfig()
	cfg.Registries = []config.RegistryEntry{{
		ID:         "fleur-static",
		Name:       "Fleur",
		URL:        appsURL,
		ServersURL: probe.ServersURL,
		Protocol:   probe.Protocol,
	}}
	SetRegistriesFromConfig(cfg)

	servers, err := SearchServers(context.Background(), "fleur-static", "", "", 10, nil)
	require.NoError(t, err, "a static JSON registry must be searchable")
	require.Len(t, servers, 2)
	assert.Equal(t, "Fetch", servers[0].Name)
	assert.Equal(t, "uvx mcp-server-fetch", servers[0].InstallCmd, "the entry must carry enough info to be added")
	assert.Equal(t, "Fleur", servers[0].Registry)

	assert.Equal(t, "/fleuristes/app-registry/refs/heads/main/apps.json", gotPath, "no route may be appended to a static source")
	assert.Empty(t, gotQuery, "no official-protocol query may be appended to a static source")

	// A query still filters, client-side.
	filtered, err := SearchServers(context.Background(), "fleur-static", "", "memory", 10, nil)
	require.NoError(t, err)
	require.Len(t, filtered, 1)
	assert.Equal(t, "Memory", filtered[0].Name)
}
