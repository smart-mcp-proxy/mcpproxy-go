package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/registries"
)

// Spec 109-m SC-008 (T145b, M12): the REST leg of the catalog order parity
// chain. internal/registries/testdata/catalog_github_order.json holds the
// registry fixtures (input) and the REST result order (golden output). The MCP
// `search_servers`, CLI `catalog search`, vitest and XCTest legs read the same
// file. UPDATE_GOLDEN=1 rewrites ids and results from the real handler.

const p109CatalogOrderFixture = "internal/registries/testdata/catalog_github_order.json"

// A source is either flat (servers, the community fork) or registry-shaped
// (protocol + corpus: wrapped {server,_meta} items recorded from the live
// official registry and served by registries.RecordedRegistryHandlerForTest).
type p109CatalogOrderSource struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Provenance string            `json:"provenance"`
	Protocol   string            `json:"protocol,omitempty"`
	Corpus     []json.RawMessage `json:"corpus,omitempty"`
	Servers    []json.RawMessage `json:"servers,omitempty"`
}

type p109CatalogOrderFile struct {
	Comment  string                     `json:"_comment"`
	Recorded json.RawMessage            `json:"recorded"`
	Query    string                     `json:"query"`
	Sources  []p109CatalogOrderSource   `json:"sources"`
	IDs      []string                   `json:"ids"`
	Results  []registries.CatalogResult `json:"results"`
}

// p109InstallCatalogFixture serves each source from httptest and installs the
// registry list, the same way the other catalog tests do.
func p109InstallCatalogFixture(t *testing.T, f p109CatalogOrderFile) {
	t.Helper()
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
}

func p109CatalogIDs(results []registries.CatalogResult) []string {
	ids := make([]string, 0, len(results))
	for _, r := range results {
		ids = append(ids, r.Source+":"+r.ID)
	}
	return ids
}

func TestCatalogOrderParity_RESTWritesTheGolden(t *testing.T) {
	var f p109CatalogOrderFile
	p108ReadJSON(t, p109CatalogOrderFixture, &f)
	p109InstallCatalogFixture(t, f)

	ctrl := &scopeController{cfg: scopeFixtureConfig(false), servers: nil, withManagement: true}
	srv, _ := scopedAgentServer(t, ctrl, []string{"alpha"})
	rec := scopeGet(t, srv, "/api/v1/catalog/search?limit=20&q="+f.Query, scopeAdminAPIKey)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	var body struct {
		Data struct {
			Results []registries.CatalogResult `json:"results"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	got := body.Data.Results
	require.NotEmpty(t, got)

	// SC-008: GitHub's own server, from the official registry, is first. It
	// is Official (a built-in source, D36.6) and Verified (its publisher
	// owns the repository, D36.5), titled by its own server.json title.
	assert.Equal(t, "official:io.github.github/github-mcp-server", got[0].Source+":"+got[0].ID)
	assert.True(t, got[0].Official)
	assert.True(t, got[0].Verified)
	assert.Equal(t, "GitHub", got[0].Title)
	assert.Equal(t, "github", got[0].Publisher)

	// Every id once, and no namespace-only match (io.github.06ketan/slideshot)
	// anywhere in the 20: those are tier 0 and rank last.
	seen := map[string]bool{}
	for _, r := range got {
		key := r.Source + ":" + r.ID
		assert.False(t, seen[key], "%s repeats", key)
		seen[key] = true
		assert.NotContains(t, r.ID, "slideshot")
		assert.NotEqual(t, "No description available", r.Description)
	}

	golden := filepath.Join(p109Root(t), p109CatalogOrderFixture)
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		f.IDs = p109CatalogIDs(got)
		f.Results = got
		require.NoError(t, os.WriteFile(golden, p109MarshalGolden(t, f), 0o644))
		return
	}
	assert.Equal(t, f.IDs, p109CatalogIDs(got), "REST order must equal the golden ids (UPDATE_GOLDEN=1 regenerates)")
	want, err := json.Marshal(f.Results)
	require.NoError(t, err)
	have, err := json.Marshal(got)
	require.NoError(t, err)
	assert.JSONEq(t, string(want), string(have))
}
