package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/registries"
)

// p109CatalogOrder is the shared Spec 109-m SC-008 fixture
// (internal/registries/testdata/catalog_github_order.json): the registry
// sources are the input, ids are the REST order the golden pins.
type p109CatalogOrder struct {
	Query   string `json:"query"`
	Sources []struct {
		ID         string            `json:"id"`
		Name       string            `json:"name"`
		Provenance string            `json:"provenance"`
		Protocol   string            `json:"protocol"`
		Corpus     []json.RawMessage `json:"corpus"`
		Servers    []json.RawMessage `json:"servers"`
	} `json:"sources"`
	IDs []string `json:"ids"`
}

// TestSearchServers_CatalogOrderMatchesTheRESTGolden is the MCP leg of SC-008
// (Spec 109-m M12): search_servers {search:"github"} over the fixture sources
// returns the SAME order as GET /catalog/search, official GitHub first.
func TestSearchServers_CatalogOrderMatchesTheRESTGolden(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "registries", "testdata", "catalog_github_order.json"))
	require.NoError(t, err)
	var f p109CatalogOrder
	require.NoError(t, json.Unmarshal(raw, &f))
	require.NotEmpty(t, f.IDs, "the REST golden is missing; run the httpapi catalog order test with UPDATE_GOLDEN=1")

	var entries []registries.RegistryEntry
	for _, src := range f.Sources {
		var h http.Handler
		if src.Protocol == "modelcontextprotocol/registry" {
			h = registries.RecordedRegistryHandlerForTest(src.Corpus)
		} else {
			body, err := json.Marshal(src.Servers)
			require.NoError(t, err)
			h = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(body)
			})
		}
		srv := httptest.NewServer(h)
		t.Cleanup(srv.Close)
		entries = append(entries, registries.RegistryEntry{ID: src.ID, Name: src.Name, ServersURL: srv.URL + "/v0.1/servers", Protocol: src.Protocol, Provenance: src.Provenance})
	}
	t.Cleanup(registries.AllowPrivateRegistryFetchForTest())
	t.Cleanup(registries.SetRegistriesForTest(entries))

	proxy := createTestMCPProxyServer(t)
	result, err := proxy.handleSearchServers(context.Background(), mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name: "search_servers", Arguments: map[string]interface{}{"search": f.Query, "limit": 20},
	}})
	require.NoError(t, err)
	require.False(t, result.IsError, "%+v", result.Content)

	var payload struct {
		Servers []struct {
			ID     string `json:"id"`
			Source string `json:"source"`
			Title  string `json:"title"`
		} `json:"servers"`
	}
	require.NoError(t, json.Unmarshal([]byte(toolResultText(t, result)), &payload))
	var got []string
	for _, s := range payload.Servers {
		got = append(got, s.Source+":"+s.ID)
	}
	assert.Equal(t, f.IDs, got, "MCP search_servers order must equal the REST golden")
	require.NotEmpty(t, got)
	assert.Equal(t, "official:io.github.github/github-mcp-server", got[0], "SC-008: the official GitHub server is first")
	assert.Equal(t, "GitHub", payload.Servers[0].Title, "the server.json title, not the reverse-DNS name")
}
