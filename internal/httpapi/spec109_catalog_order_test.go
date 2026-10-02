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

type p109CatalogOrderSource struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Provenance string            `json:"provenance"`
	Servers    []json.RawMessage `json:"servers"`
}

type p109CatalogOrderFile struct {
	Comment string                     `json:"_comment"`
	Query   string                     `json:"query"`
	Sources []p109CatalogOrderSource   `json:"sources"`
	IDs     []string                   `json:"ids"`
	Results []registries.CatalogResult `json:"results"`
}

// p109InstallCatalogFixture serves each source from httptest and installs the
// registry list, the same way the other catalog tests do.
func p109InstallCatalogFixture(t *testing.T, f p109CatalogOrderFile) {
	t.Helper()
	var entries []registries.RegistryEntry
	for _, src := range f.Sources {
		body, err := json.Marshal(src.Servers)
		require.NoError(t, err)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(body)
		}))
		t.Cleanup(srv.Close)
		entries = append(entries, registries.RegistryEntry{ID: src.ID, Name: src.Name, ServersURL: srv.URL, Provenance: src.Provenance})
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
	rec := scopeGet(t, srv, "/api/v1/catalog/search?q="+f.Query, scopeAdminAPIKey)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	var body struct {
		Data struct {
			Results []registries.CatalogResult `json:"results"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	got := body.Data.Results
	require.NotEmpty(t, got)

	// SC-008: the official, verified GitHub server is first.
	assert.Equal(t, "official:io.github.github/github-mcp-server", got[0].Source+":"+got[0].ID)
	assert.True(t, got[0].Official)
	assert.True(t, got[0].Verified)

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
