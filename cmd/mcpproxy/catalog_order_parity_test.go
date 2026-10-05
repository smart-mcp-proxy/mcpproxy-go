package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/registries"
)

// Spec 109-m SC-008 (T145b, M12): the CLI leg of the catalog order parity
// chain. `catalog search github` over the shared registry fixture
// (internal/registries/testdata/catalog_github_order.json) lists the same ids,
// in the same order, as the REST golden, and `-o json` carries them verbatim.

type p109CatalogOrderFile struct {
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

func TestCatalogOrderParityCLI(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "internal", "registries", "testdata", "catalog_github_order.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f p109CatalogOrderFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	if len(f.IDs) == 0 {
		t.Fatal("the REST golden is missing; run the httpapi catalog order test with UPDATE_GOLDEN=1")
	}

	var entries []registries.RegistryEntry
	for _, src := range f.Sources {
		var h http.Handler
		if src.Protocol == "modelcontextprotocol/registry" {
			h = registries.RecordedRegistryHandlerForTest(src.Corpus)
		} else {
			body, _ := json.Marshal(src.Servers)
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

	resp, err := catalogSearchInProcess(context.Background(), &config.Config{}, f.Query, "", "", 20)
	if err != nil {
		t.Fatalf("catalogSearchInProcess: %v", err)
	}
	var got []string
	for _, r := range resp.Results {
		got = append(got, r.Source+":"+r.ID)
	}
	if strings.Join(got, ",") != strings.Join(f.IDs, ",") {
		t.Errorf("CLI order = %v, want the REST golden %v", got, f.IDs)
	}

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
		Results []registries.CatalogResult `json:"results"`
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
}
