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
