package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/registries"
)

// Spec 109 D35 (T160): the cached-fallback golden.
// internal/registries/testdata/catalog_cached_fallback_order.json holds the
// registry fixtures (input, one flaky source) and the REST answer while that
// source is down (golden output). MCP, CLI, vitest and XCTest replay it.
// UPDATE_GOLDEN=1 rewrites ids, results and unavailable from the real handler.

const p109CachedFallbackFixture = "internal/registries/testdata/catalog_cached_fallback_order.json"

type p109CachedFallbackSource struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Provenance string            `json:"provenance"`
	Flaky      bool              `json:"flaky,omitempty"`
	Servers    []json.RawMessage `json:"servers"`
}

// p109CachedFallbackUnavailable is the part of unavailable[] that is stable
// across runs: which source fell back, and how.
type p109CachedFallbackUnavailable struct {
	Source   string `json:"source"`
	Fallback string `json:"fallback"`
}

type p109CachedFallbackFile struct {
	Comment     string                          `json:"_comment"`
	Query       string                          `json:"query"`
	Sources     []p109CachedFallbackSource      `json:"sources"`
	IDs         []string                        `json:"ids"`
	Results     []registries.CatalogResult      `json:"results"`
	Unavailable []p109CachedFallbackUnavailable `json:"unavailable"`
}

// p109InstallCachedFallbackFixture serves each source from httptest. A flaky
// source answers 500 once the returned breaker is tripped.
func p109InstallCachedFallbackFixture(t *testing.T, f p109CachedFallbackFile) (trip func()) {
	t.Helper()
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
	return func() { broken.Store(true) }
}

func TestCatalogCachedFallback_RESTWritesTheGolden(t *testing.T) {
	var f p109CachedFallbackFile
	p108ReadJSON(t, p109CachedFallbackFixture, &f)
	trip := p109InstallCachedFallbackFixture(t, f)

	ctrl := &scopeController{cfg: scopeFixtureConfig(false), servers: nil, withManagement: true}
	srv, _ := scopedAgentServer(t, ctrl, []string{"alpha"})

	// Browse once while every source is healthy: this primes the per-source
	// listing cache. Then the flaky source goes down.
	prime := scopeGet(t, srv, "/api/v1/catalog/search?q=", scopeAdminAPIKey)
	require.Equal(t, http.StatusOK, prime.Code, "body: %s", prime.Body.String())
	trip()

	rec := scopeGet(t, srv, "/api/v1/catalog/search?q="+f.Query, scopeAdminAPIKey)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	var body struct {
		Data struct {
			Results     []registries.CatalogResult `json:"results"`
			Unavailable []registries.SourceError   `json:"unavailable"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	got := body.Data.Results
	require.NotEmpty(t, got)

	// The live official hit is first and is not marked cached; every slowreg
	// hit is marked cached.
	assert.Equal(t, "official:io.github.github/github-mcp-server", got[0].Source+":"+got[0].ID)
	assert.False(t, got[0].FromCache)
	var sawCached bool
	for _, r := range got {
		if r.Source == "slowreg" {
			assert.True(t, r.FromCache, "%s must be marked from_cache", r.ID)
			sawCached = true
		} else {
			assert.False(t, r.FromCache, "%s is live", r.ID)
		}
	}
	require.True(t, sawCached, "expected cached slowreg hits")

	// The source stays in unavailable[] with the fallback marker and a time.
	require.Len(t, body.Data.Unavailable, 1)
	u := body.Data.Unavailable[0]
	assert.Equal(t, "slowreg", u.Source)
	assert.Equal(t, registries.FallbackCachedListing, u.Fallback)
	require.NotNil(t, u.CachedAt)

	var unavailable []p109CachedFallbackUnavailable
	for _, e := range body.Data.Unavailable {
		unavailable = append(unavailable, p109CachedFallbackUnavailable{Source: e.Source, Fallback: e.Fallback})
	}

	golden := filepath.Join(p109Root(t), p109CachedFallbackFixture)
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		f.IDs = p109CatalogIDs(got)
		f.Results = got
		f.Unavailable = unavailable
		require.NoError(t, os.WriteFile(golden, p109MarshalGolden(t, f), 0o644))
		return
	}
	assert.Equal(t, f.IDs, p109CatalogIDs(got), "REST order must equal the golden ids (UPDATE_GOLDEN=1 regenerates)")
	assert.Equal(t, f.Unavailable, unavailable)
	want, err := json.Marshal(f.Results)
	require.NoError(t, err)
	have, err := json.Marshal(got)
	require.NoError(t, err)
	assert.JSONEq(t, string(want), string(have))
}
