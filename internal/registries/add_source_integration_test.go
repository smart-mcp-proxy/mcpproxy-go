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

// TestUserAddedStaticJSONSource_IsRefusedAtAddTime is the GH #783 acceptance
// test. MCPProxy implements exactly one registry protocol (official v0.1), so a
// static JSON catalog cannot be browsed. What #783 was really about is HOW that
// failed: the URL was silently rewritten to ".../apps.json/v0.1/servers" and
// 404'd on every later search, with nothing telling the user why.
//
// Now the pasted URL is fetched verbatim at add time and the source is refused
// with a reason — and, critically, NOTHING is appended to the URL we request.
func TestUserAddedStaticJSONSource_IsRefusedAtAddTime(t *testing.T) {
	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		if r.URL.Path != "/fleuristes/app-registry/refs/heads/main/apps.json" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `[{"name":"Fetch","description":"web pages","config":{"runtime":"uvx","args":["mcp-server-fetch"]}}]`)
	}))
	defer srv.Close()

	appsURL := srv.URL + "/fleuristes/app-registry/refs/heads/main/apps.json"

	_, err := ProbeRegistrySource(context.Background(), appsURL)
	require.Error(t, err, "a static catalog is not an official v0.1 registry")
	assert.ErrorIs(t, err, ErrRegistrySourceUnusable)

	assert.Equal(t, "/fleuristes/app-registry/refs/heads/main/apps.json", gotPath,
		"the pasted URL must be fetched verbatim — no route may be appended to it")
	assert.Empty(t, gotQuery, "no official-protocol query may be appended to it either")
}
