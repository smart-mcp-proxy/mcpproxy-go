package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/registries"
)

// Spec 109 D35 (T160): the CLI leg of the cached-fallback chain. While one
// source is down, `catalog search github` lists the same ids in the same order
// as the REST golden (internal/registries/testdata/catalog_cached_fallback_order.json),
// `-o json` carries from_cache and unavailable[].fallback, and the table marks
// cached rows and says why.

type p109CachedFallbackFile struct {
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

func TestCatalogCachedFallbackParityCLI(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "internal", "registries", "testdata", "catalog_cached_fallback_order.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f p109CachedFallbackFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	if len(f.IDs) == 0 {
		t.Fatal("the REST golden is missing; run the httpapi cached-fallback test with UPDATE_GOLDEN=1")
	}

	registries.ResetListingCacheForTest()
	t.Cleanup(registries.ResetListingCacheForTest)
	var broken atomic.Bool
	var entries []registries.RegistryEntry
	for _, src := range f.Sources {
		body, _ := json.Marshal(src.Servers)
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

	cfg := &config.Config{}
	if _, err := catalogSearchInProcess(context.Background(), cfg, "", "", "", 20); err != nil { // prime
		t.Fatal(err)
	}
	broken.Store(true)
	resp, err := catalogSearchInProcess(context.Background(), cfg, f.Query, "", "", 20)
	if err != nil {
		t.Fatalf("catalogSearchInProcess: %v", err)
	}

	var got []string
	for i, r := range resp.Results {
		got = append(got, r.Source+":"+r.ID)
		if r.FromCache != f.Results[i].FromCache {
			t.Errorf("%s: from_cache = %v, want %v", r.ID, r.FromCache, f.Results[i].FromCache)
		}
	}
	if strings.Join(got, ",") != strings.Join(f.IDs, ",") {
		t.Errorf("CLI order = %v, want the REST golden %v", got, f.IDs)
	}
	if len(resp.Unavailable) != len(f.Unavailable) || resp.Unavailable[0].Fallback != f.Unavailable[0].Fallback {
		t.Fatalf("unavailable = %+v, want %+v", resp.Unavailable, f.Unavailable)
	}

	// -o json carries the same fields as REST.
	setOutputGlobals(t, "json", false)
	formatter, err := GetOutputFormatter()
	if err != nil {
		t.Fatal(err)
	}
	out := captureOutput(func() {
		if err := renderCatalogSearch(formatter, resp); err != nil {
			t.Fatalf("renderCatalogSearch: %v", err)
		}
	})
	var decoded struct {
		Results     []registries.CatalogResult `json:"results"`
		Unavailable []registries.SourceError   `json:"unavailable"`
	}
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("-o json did not parse: %v\n%s", err, out)
	}
	var jsonIDs []string
	for _, r := range decoded.Results {
		jsonIDs = append(jsonIDs, r.Source+":"+r.ID)
	}
	if strings.Join(jsonIDs, ",") != strings.Join(f.IDs, ",") {
		t.Errorf("-o json order = %v, want %v", jsonIDs, f.IDs)
	}
	if !strings.Contains(out, `"from_cache": true`) || !strings.Contains(out, `"fallback": "cached_listing"`) {
		t.Errorf("-o json must carry from_cache and unavailable[].fallback, got:\n%s", out)
	}

	// The table marks cached rows and says what happened.
	setOutputGlobals(t, "table", false)
	formatter, err = GetOutputFormatter()
	if err != nil {
		t.Fatal(err)
	}
	table := captureOutput(func() {
		if err := renderCatalogSearch(formatter, resp); err != nil {
			t.Fatalf("renderCatalogSearch: %v", err)
		}
	})
	if !strings.Contains(table, "slowreg (cached)") {
		t.Errorf("cached rows must read `slowreg (cached)` in SOURCE, got:\n%s", table)
	}
	if strings.Contains(table, "official (cached)") {
		t.Errorf("live rows must not be marked cached, got:\n%s", table)
	}
	if !strings.Contains(table, "slowreg unavailable:") || !strings.Contains(table, "showing matches from its cached list") {
		t.Errorf("expected the unavailable line with the fallback sentence, got:\n%s", table)
	}
}
