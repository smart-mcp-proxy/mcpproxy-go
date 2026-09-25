package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/registries"
)

// catalogTestServers/registries build a small, deterministic fixture:
//   - "gamma" is a configured server visible to everyone, manually added (no
//     source_registry_id), whose URL exactly matches the "official" source's
//     "gamma-tool" entry — so it must read added:true for every caller.
//   - "delta" is a configured server that the scoped token below may NOT see,
//     added FROM the "official" source (source_registry_id="official") and
//     matching the "delta-tool" entry.
const (
	catalogGammaURL          = "https://gamma.example.com/mcp"
	catalogDeltaSecretMarker = "DELTA-ONLY-VALUE-1234"
)

func catalogFixtureServers() []contracts.Server {
	return []contracts.Server{
		{
			ID:      "gamma",
			Name:    "gamma",
			URL:     catalogGammaURL,
			Enabled: true,
		},
		{
			ID:               "delta",
			Name:             "delta",
			Command:          "npx",
			Args:             []string{"delta-server"},
			Env:              map[string]string{"DELTA_TOKEN": catalogDeltaSecretMarker},
			SourceRegistryID: "official",
			Enabled:          true,
		},
	}
}

func withCatalogFixtureRegistry(t *testing.T) {
	t.Helper()
	body := `[
		{"id":"gamma-tool","name":"Gamma Tool","url":"` + catalogGammaURL + `"},
		{"id":"delta-tool","name":"Delta Tool","installCmd":"npx delta-server"}
	]`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	t.Cleanup(registries.AllowPrivateRegistryFetchForTest())
	t.Cleanup(registries.SetRegistriesForTest([]registries.RegistryEntry{
		{ID: "official", Name: "Official", ServersURL: srv.URL},
	}))
}

func TestCatalogSearch_OpenToScopedCaller(t *testing.T) {
	withCatalogFixtureRegistry(t)
	ctrl := &scopeController{cfg: scopeFixtureConfig(false), servers: catalogFixtureServers(), withManagement: true}
	srv, token := scopedAgentServer(t, ctrl, []string{"gamma"})

	rec := scopeGet(t, srv, "/api/v1/catalog/search?q=tool", token)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

func TestCatalogSearch_Unauthenticated401(t *testing.T) {
	withCatalogFixtureRegistry(t)
	ctrl := &scopeController{cfg: scopeFixtureConfig(false), servers: catalogFixtureServers(), withManagement: true}
	srv, _ := scopedAgentServer(t, ctrl, []string{"gamma"})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/search", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

// TestCatalogSearch_AddedScopedByCallerVisibility pins FR-007: "added" is
// computed only over servers the caller may enumerate, and a manually added
// server matches by install target alone while a registry-sourced one also
// needs a matching source.
func TestCatalogSearch_AddedScopedByCallerVisibility(t *testing.T) {
	withCatalogFixtureRegistry(t)
	ctrl := &scopeController{cfg: scopeFixtureConfig(false), servers: catalogFixtureServers(), withManagement: true}
	srv, token := scopedAgentServer(t, ctrl, []string{"gamma"})

	// Admin sees both as added.
	adminRec := scopeGet(t, srv, "/api/v1/catalog/search?q=tool", scopeAdminAPIKey)
	require.Equal(t, http.StatusOK, adminRec.Code, adminRec.Body.String())
	adminResults := catalogDecodeResults(t, adminRec)
	assert.True(t, catalogAddedFor(adminResults, "gamma-tool"), "admin: expected gamma-tool added=true")
	assert.True(t, catalogAddedFor(adminResults, "delta-tool"), "admin: expected delta-tool added=true")

	// The scoped token (allowed: gamma only) sees gamma-tool added=true
	// (manual match by target alone) but delta-tool added=false, because
	// delta is out of its scope.
	agentRec := scopeGet(t, srv, "/api/v1/catalog/search?q=tool", token)
	require.Equal(t, http.StatusOK, agentRec.Code, agentRec.Body.String())
	agentResults := catalogDecodeResults(t, agentRec)
	assert.True(t, catalogAddedFor(agentResults, "gamma-tool"), "scoped: expected gamma-tool added=true")
	assert.False(t, catalogAddedFor(agentResults, "delta-tool"), "scoped: expected delta-tool added=false (out of scope)")

	// No configured server field (name, url, command, secret) leaks — the
	// catalog entries only ever carry the source's own public data.
	assert.NotContains(t, agentRec.Body.String(), catalogDeltaSecretMarker)
}

func catalogDecodeResults(t *testing.T, rec *httptest.ResponseRecorder) []map[string]interface{} {
	t.Helper()
	data := scopeDecodeData(t, rec)
	raw, ok := data["results"].([]interface{})
	require.True(t, ok, "no results array in %#v", data)
	out := make([]map[string]interface{}, 0, len(raw))
	for _, r := range raw {
		m, ok := r.(map[string]interface{})
		require.True(t, ok)
		out = append(out, m)
	}
	return out
}

func catalogAddedFor(results []map[string]interface{}, id string) bool {
	for _, r := range results {
		if r["id"] == id {
			added, _ := r["added"].(bool)
			return added
		}
	}
	return false
}
