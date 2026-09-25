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
