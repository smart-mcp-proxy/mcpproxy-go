package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/registries"
)

func withMCPCatalogFixture(t *testing.T) {
	t.Helper()
	fast := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"one","name":"Alpha Tool","description":"d1"}]`))
	}))
	t.Cleanup(fast.Close)
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(slow.Close)

	t.Cleanup(registries.AllowPrivateRegistryFetchForTest())
	t.Cleanup(registries.SetRegistriesForTest([]registries.RegistryEntry{
		{ID: "fast", Name: "Fast", ServersURL: fast.URL},
	}))
}

// TestSearchServers_RegistryOptional_SearchesAllSources pins FR-067: omitting
// 'registry' fans out across every enabled catalog source via
// registries.SearchAll (FR-060) and returns the merged ServerEntry list,
// carrying no "added" field (contracts/rest-api.md#catalog: REST-only).
func TestSearchServers_RegistryOptional_SearchesAllSources(t *testing.T) {
	withMCPCatalogFixture(t)
	proxy := createTestMCPProxyServer(t)

	req := mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name:      "search_servers",
		Arguments: map[string]interface{}{"search": "Alpha"},
	}}
	result, err := proxy.handleSearchServers(context.Background(), req)
	require.NoError(t, err)
	require.False(t, result.IsError, "unexpected tool error: %+v", result.Content)

	text := toolResultText(t, result)
	var payload map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(text), &payload))

	servers, ok := payload["servers"].([]interface{})
	require.True(t, ok, "expected a servers array, got %#v", payload)
	require.Len(t, servers, 1)
	entry, ok := servers[0].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "one", entry["id"])
	assert.Equal(t, "Alpha Tool", entry["name"])
	assert.NotContains(t, entry, "added", "the MCP surface must never carry the REST-only 'added' field")
	if _, hasRegistryArg := payload["registry"]; hasRegistryArg {
		t.Errorf("expected no 'registry' field echoed back when omitted, got %#v", payload["registry"])
	}

	// contracts/mcp-tools.md: omitting 'registry' must gain the same
	// catalog-ranking evidence REST/CLI callers already get — title,
	// publisher, verified, official, popularity, source — not just the bare
	// registries.ServerEntry.
	assert.Equal(t, "Alpha Tool", entry["title"], "expected the catalog title field")
	assert.Equal(t, "fast", entry["source"], "expected the catalog source field")
	assert.Contains(t, entry, "verified", "expected the catalog verified field")
	assert.Contains(t, entry, "official", "expected the catalog official field")
	assert.Contains(t, entry, "publisher", "expected the catalog publisher field")
}

// TestSearchServers_RegistryOptional_UnavailableSourceReported pins that a
// failed source is listed unavailable rather than failing the whole search.
func TestSearchServers_RegistryOptional_UnavailableSourceReported(t *testing.T) {
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer failing.Close()

	restoreAllow := registries.AllowPrivateRegistryFetchForTest()
	defer restoreAllow()
	restore := registries.SetRegistriesForTest([]registries.RegistryEntry{
		{ID: "broken", Name: "Broken", ServersURL: failing.URL},
	})
	defer restore()

	proxy := createTestMCPProxyServer(t)
	req := mcp.CallToolRequest{Params: mcp.CallToolParams{Name: "search_servers", Arguments: map[string]interface{}{}}}
	result, err := proxy.handleSearchServers(context.Background(), req)
	require.NoError(t, err)
	require.False(t, result.IsError)

	var payload map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(toolResultText(t, result)), &payload))
	unavailable, ok := payload["unavailable"].([]interface{})
	require.True(t, ok, "expected unavailable[] in the response, got %#v", payload)
	require.Len(t, unavailable, 1)
}

// TestSearchServers_RegistryOptional_SecretLikeMatchesCatalogResult pins
// FR-067/FR-065 cross-surface parity: MCP search_servers with 'registry'
// omitted must compute required_inputs[].secret via the same
// registries.ToCatalogResult OR-with-name-heuristic REST/CLI's
// /catalog/search already applies (secretlike.LooksSecret(name)), not the
// raw ServerEntry.RequiredInputs[].Secret field a registry entry declares.
// A registry that reports secret:false for a name that still looks
// secret-shaped (e.g. "Authorization") must still surface secret:true here,
// exactly as GET /catalog/search and `mcpproxy catalog search` do.
func TestSearchServers_RegistryOptional_SecretLikeMatchesCatalogResult(t *testing.T) {
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"one","name":"Alpha Tool","description":"d1","required_inputs":[{"name":"Authorization","secret":false}]}]`))
	}))
	t.Cleanup(fixture.Close)

	t.Cleanup(registries.AllowPrivateRegistryFetchForTest())
	t.Cleanup(registries.SetRegistriesForTest([]registries.RegistryEntry{
		{ID: "fast", Name: "Fast", ServersURL: fixture.URL},
	}))

	proxy := createTestMCPProxyServer(t)

	req := mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name:      "search_servers",
		Arguments: map[string]interface{}{"search": "Alpha"},
	}}
	result, err := proxy.handleSearchServers(context.Background(), req)
	require.NoError(t, err)
	require.False(t, result.IsError, "unexpected tool error: %+v", result.Content)

	text := toolResultText(t, result)
	var payload map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(text), &payload))

	servers, ok := payload["servers"].([]interface{})
	require.True(t, ok, "expected a servers array, got %#v", payload)
	require.Len(t, servers, 1)
	entry, ok := servers[0].(map[string]interface{})
	require.True(t, ok)

	requiredInputs, ok := entry["required_inputs"].([]interface{})
	require.True(t, ok, "expected required_inputs in the entry, got %#v", entry)
	require.Len(t, requiredInputs, 1)
	input, ok := requiredInputs[0].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "Authorization", input["name"])
	assert.Equal(t, true, input["secret"],
		"expected secret:true via the D13 name heuristic (FR-065), matching REST/CLI's ToCatalogResult, despite the registry declaring secret:false")
}

// TestSearchServers_RegistrySpecified_UnchangedBehavior pins that passing
// 'registry' still uses the single-source path unchanged (FR-067's ONLY
// requirement is making it optional).
func TestSearchServers_RegistrySpecified_UnchangedBehavior(t *testing.T) {
	withMCPCatalogFixture(t)
	proxy := createTestMCPProxyServer(t)

	req := mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name:      "search_servers",
		Arguments: map[string]interface{}{"registry": "fast"},
	}}
	result, err := proxy.handleSearchServers(context.Background(), req)
	require.NoError(t, err)
	require.False(t, result.IsError, "unexpected tool error: %+v", result.Content)

	var payload map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(toolResultText(t, result)), &payload))
	assert.Equal(t, "fast", payload["registry"])
	servers, ok := payload["servers"].([]interface{})
	require.True(t, ok)
	require.Len(t, servers, 1)
}
