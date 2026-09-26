package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
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

	// A second source whose entry shares delta-tool's exact install target
	// (same command) but under a DIFFERENT source id — the regression fixture
	// for the "registry-sourced match needs (source, target), not target
	// alone" rule.
	otherBody := `[{"id":"lookalike-tool","name":"Lookalike Tool","installCmd":"npx delta-server"}]`
	otherSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(otherBody))
	}))
	t.Cleanup(otherSrv.Close)

	t.Cleanup(registries.AllowPrivateRegistryFetchForTest())
	t.Cleanup(registries.SetRegistriesForTest([]registries.RegistryEntry{
		{ID: "official", Name: "Official", ServersURL: srv.URL},
		{ID: "other", Name: "Other", ServersURL: otherSrv.URL},
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

// TestCatalogSearch_AddedRequiresMatchingSourceForRegistryAdd is the
// regression for a bug caught while implementing this: a registry-sourced
// configured server (source_registry_id set) must NOT cause a different
// source's entry with the same install target to also read added:true —
// only a server with NO source_registry_id (a manual add) matches by target
// alone (contracts/rest-api.md#catalog "added").
func TestCatalogSearch_AddedRequiresMatchingSourceForRegistryAdd(t *testing.T) {
	withCatalogFixtureRegistry(t)
	ctrl := &scopeController{cfg: scopeFixtureConfig(false), servers: catalogFixtureServers(), withManagement: true}
	srv, _ := scopedAgentServer(t, ctrl, []string{"gamma"})

	rec := scopeGet(t, srv, "/api/v1/catalog/search?q=tool", scopeAdminAPIKey)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	results := catalogDecodeResults(t, rec)

	assert.True(t, catalogAddedFor(results, "delta-tool"), "delta-tool (source=official) must match delta (source_registry_id=official)")
	assert.False(t, catalogAddedFor(results, "lookalike-tool"),
		"lookalike-tool (source=other) shares delta's install target but must NOT read added=true — delta was added from a different source")
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

// TestCatalogSearch_AddedScopedForNonAdminUserContext pins that the FR-007
// scoping is not agent-token-specific: a non-admin AuthTypeUser session
// (server edition's OAuth user identity, e.g. a tenant) is scoped by the same
// visibleServers/CanEnumerateServer predicate an agent token goes through,
// driven directly at the handler (like TestRequireAdminRead_DeniesNonAdminUserContext)
// since apiKeyAuthMiddleware itself only ever installs admin/agent contexts.
func TestCatalogSearch_AddedScopedForNonAdminUserContext(t *testing.T) {
	withCatalogFixtureRegistry(t)
	ctrl := &scopeController{cfg: scopeFixtureConfig(false), servers: catalogFixtureServers(), withManagement: true}
	srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)

	userCtx := &auth.AuthContext{
		Type:           auth.AuthTypeUser,
		UserID:         "alice",
		AllowedServers: []string{"gamma"},
		CredentialKind: auth.CredentialKindCookie,
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/search?q=tool", http.NoBody)
	req = req.WithContext(auth.WithAuthContext(req.Context(), userCtx))
	rec := httptest.NewRecorder()
	srv.handleCatalogSearch(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	results := catalogDecodeResults(t, rec)
	assert.True(t, catalogAddedFor(results, "gamma-tool"), "user session scoped to gamma: expected gamma-tool added=true")
	assert.False(t, catalogAddedFor(results, "delta-tool"), "user session scoped to gamma: expected delta-tool added=false (out of scope)")
}

// TestCatalogSearch_EmptyQueryReturnsEmptyResults pins contracts/rest-api.md#catalog:
// "Empty q → results: [], sections: {...}". Before this fix, Results was set
// unconditionally to the ranked hit list even when Sections was populated.
func TestCatalogSearch_EmptyQueryReturnsEmptyResults(t *testing.T) {
	withCatalogFixtureRegistry(t)
	ctrl := &scopeController{cfg: scopeFixtureConfig(false), servers: catalogFixtureServers(), withManagement: true}
	srv, _ := scopedAgentServer(t, ctrl, []string{"gamma"})

	rec := scopeGet(t, srv, "/api/v1/catalog/search", scopeAdminAPIKey)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	data := scopeDecodeData(t, rec)

	results, ok := data["results"].([]interface{})
	require.True(t, ok, "expected results to be an array, got %#v", data["results"])
	assert.Empty(t, results, "contracts/rest-api.md#catalog: empty q must return results: []")

	sections, ok := data["sections"].(map[string]interface{})
	require.True(t, ok, "expected sections to be populated for an empty q")
	popular, ok := sections["popular"].([]interface{})
	require.True(t, ok)
	assert.NotEmpty(t, popular, "expected the fixture's entries in sections.popular")
}

// TestCatalogSearch_SourceFilterAppliesBeforeTruncation is the regression for
// a bug where `source=` was applied AFTER registries.SearchAll had already
// truncated the ranked, merged list to `limit`: an official source ranks
// ahead of everything else, so with limit=1 it fills the only truncated
// slot, and a `source=other&limit=1` query would silently come back empty
// even though "other" has a real matching entry (just ranked below the
// truncation point pre-filter).
func TestCatalogSearch_SourceFilterAppliesBeforeTruncation(t *testing.T) {
	officialBody := `[{"id":"official-tool","name":"Official Tool"}]`
	officialSrc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(officialBody))
	}))
	t.Cleanup(officialSrc.Close)

	otherBody := `[{"id":"other-tool","name":"Other Tool"}]`
	otherSrc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(otherBody))
	}))
	t.Cleanup(otherSrc.Close)

	t.Cleanup(registries.AllowPrivateRegistryFetchForTest())
	t.Cleanup(registries.SetRegistriesForTest([]registries.RegistryEntry{
		{ID: "official", Name: "Official", ServersURL: officialSrc.URL, Provenance: "official"},
		{ID: "other", Name: "Other", ServersURL: otherSrc.URL},
	}))

	ctrl := &scopeController{cfg: scopeFixtureConfig(false), servers: nil, withManagement: true}
	srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)

	// Sanity check: with no source filter and limit=1, the official (ranked
	// first) entry fills the only slot — "other-tool" is truncated away.
	rec := scopeGet(t, srv, "/api/v1/catalog/search?q=tool&limit=1", scopeAdminAPIKey)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	unfiltered := catalogDecodeResults(t, rec)
	require.Len(t, unfiltered, 1)
	assert.Equal(t, "official-tool", unfiltered[0]["id"])

	// Narrowing to source=other must still find other-tool, not come back
	// empty just because it didn't survive the pre-filter truncation.
	rec = scopeGet(t, srv, "/api/v1/catalog/search?q=tool&source=other&limit=1", scopeAdminAPIKey)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	filtered := catalogDecodeResults(t, rec)
	require.Len(t, filtered, 1, "expected other-tool to survive source filtering despite limit=1")
	assert.Equal(t, "other-tool", filtered[0]["id"])
}
