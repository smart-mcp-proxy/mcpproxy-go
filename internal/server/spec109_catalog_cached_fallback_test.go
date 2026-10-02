package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/registries"
)

// p109CachedFallback is the shared Spec 109 D35 fixture
// (internal/registries/testdata/catalog_cached_fallback_order.json): the
// sources are the input (one is flaky), ids/results/unavailable are the REST
// answer the golden pins while that source is down.
type p109CachedFallback struct {
	Query   string `json:"query"`
	Sources []struct {
		ID         string            `json:"id"`
		Name       string            `json:"name"`
		Provenance string            `json:"provenance"`
		Flaky      bool              `json:"flaky"`
		Servers    []json.RawMessage `json:"servers"`
	} `json:"sources"`
	IDs     []string `json:"ids"`
	Results []struct {
		Source    string `json:"source"`
		ID        string `json:"id"`
		FromCache bool   `json:"from_cache"`
	} `json:"results"`
	Unavailable []struct {
		Source   string `json:"source"`
		Fallback string `json:"fallback"`
	} `json:"unavailable"`
}

// TestSearchServers_CachedFallbackMatchesTheRESTGolden is the MCP leg of the
// cached-fallback chain: search_servers {search:"github"} while one source is
// down returns the same ids in the same order as GET /catalog/search, with
// from_cache on the cached ones and the source still in unavailable[] with
// fallback "cached_listing".
func TestSearchServers_CachedFallbackMatchesTheRESTGolden(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "registries", "testdata", "catalog_cached_fallback_order.json"))
	require.NoError(t, err)
	var f p109CachedFallback
	require.NoError(t, json.Unmarshal(raw, &f))
	require.NotEmpty(t, f.IDs, "the REST golden is missing; run the httpapi cached-fallback test with UPDATE_GOLDEN=1")

	registries.ResetListingCacheForTest()
	t.Cleanup(registries.ResetListingCacheForTest)
	var broken atomic.Bool
	var entries []registries.RegistryEntry
	for _, src := range f.Sources {
		body, err := json.Marshal(src.Servers)
		require.NoError(t, err)
		flaky := src.Flaky
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if flaky && broken.Load() {
				http.Error(w, "down", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(body)
		}))
		t.Cleanup(srv.Close)
		entries = append(entries, registries.RegistryEntry{ID: src.ID, Name: src.Name, ServersURL: srv.URL, Provenance: src.Provenance})
	}
	t.Cleanup(registries.AllowPrivateRegistryFetchForTest())
	t.Cleanup(registries.SetRegistriesForTest(entries))

	proxy := createTestMCPProxyServer(t)
	call := func(search string) map[string]json.RawMessage {
		result, err := proxy.handleSearchServers(context.Background(), mcp.CallToolRequest{Params: mcp.CallToolParams{
			Name: "search_servers", Arguments: map[string]interface{}{"search": search},
		}})
		require.NoError(t, err)
		require.False(t, result.IsError, "%+v", result.Content)
		var payload map[string]json.RawMessage
		require.NoError(t, json.Unmarshal([]byte(toolResultText(t, result)), &payload))
		return payload
	}

	call("") // browse while healthy: primes the per-source listing cache
	broken.Store(true)
	payload := call(f.Query)

	var servers []struct {
		ID        string `json:"id"`
		Source    string `json:"source"`
		FromCache bool   `json:"from_cache"`
	}
	require.NoError(t, json.Unmarshal(payload["servers"], &servers))
	var got []string
	for i, s := range servers {
		got = append(got, s.Source+":"+s.ID)
		assert.Equal(t, f.Results[i].FromCache, s.FromCache, "from_cache of %s", s.ID)
	}
	assert.Equal(t, f.IDs, got, "MCP search_servers order must equal the REST golden")

	var unavailable []struct {
		Source   string  `json:"source"`
		Fallback string  `json:"fallback"`
		CachedAt *string `json:"cached_at"`
	}
	require.NoError(t, json.Unmarshal(payload["unavailable"], &unavailable))
	require.Len(t, unavailable, len(f.Unavailable))
	for i, u := range unavailable {
		assert.Equal(t, f.Unavailable[i].Source, u.Source)
		assert.Equal(t, f.Unavailable[i].Fallback, u.Fallback)
		assert.NotNil(t, u.CachedAt, "cached_at must accompany the fallback")
	}
}
