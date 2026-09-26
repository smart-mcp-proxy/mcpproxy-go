package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/registries"
)

// TestParseCatalogRef_SplitsOnFirstSlashOnly pins that an official-protocol
// id (itself reverse-DNS-shaped, e.g. "io.github.github/github-mcp-server")
// is not mangled by the "<source>/<id>" split (FR-066).
func TestParseCatalogRef_SplitsOnFirstSlashOnly(t *testing.T) {
	source, id, err := parseCatalogRef("official/io.github.github/github-mcp-server")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if source != "official" {
		t.Errorf("source = %q, want %q", source, "official")
	}
	if id != "io.github.github/github-mcp-server" {
		t.Errorf("id = %q, want %q", id, "io.github.github/github-mcp-server")
	}
}

func TestParseCatalogRef_RejectsMissingSlash(t *testing.T) {
	if _, _, err := parseCatalogRef("no-slash-here"); err == nil {
		t.Fatal("expected an error for a ref with no '/'")
	}
}

// TestCatalogAddedFromConfig pins the join rule (contracts/rest-api.md#catalog
// "added"): a registry-sourced configured server needs source AND install
// target; a manual add matches on install target alone.
func TestCatalogAddedFromConfig(t *testing.T) {
	cfg := &config.Config{
		Servers: []*config.ServerConfig{
			{Name: "manual", URL: "https://manual.example.com/mcp"},
			{Name: "from-official", Command: "npx", Args: []string{"server-x"}, SourceRegistryID: "official"},
		},
	}
	added := catalogAddedFromConfig(cfg)

	manualHit := registries.CatalogHit{Source: "smithery", Entry: registries.ServerEntry{ID: "m", URL: "https://manual.example.com/mcp"}}
	if !added(manualHit) {
		t.Error("expected a manual add to match by install target regardless of source")
	}

	officialHit := registries.CatalogHit{Source: "official", Entry: registries.ServerEntry{ID: "x", InstallCmd: "npx server-x"}}
	if !added(officialHit) {
		t.Error("expected the registry-sourced server to match its own source + target")
	}

	wrongSourceHit := registries.CatalogHit{Source: "smithery", Entry: registries.ServerEntry{ID: "x", InstallCmd: "npx server-x"}}
	if added(wrongSourceHit) {
		t.Error("a registry-sourced configured server must not match a different source with the same target")
	}

	noMatchHit := registries.CatalogHit{Source: "official", Entry: registries.ServerEntry{ID: "y", InstallCmd: "npx server-y"}}
	if added(noMatchHit) {
		t.Error("expected no match for an unrelated entry")
	}
}

func withCatalogCLIFixture(t *testing.T, body string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(registries.AllowPrivateRegistryFetchForTest())
	t.Cleanup(registries.SetRegistriesForTest([]registries.RegistryEntry{
		{ID: "official", Name: "Official", ServersURL: srv.URL, Provenance: "official"},
	}))
}

// TestCatalogSearchInProcess_Search is the T100 "search" golden: a non-empty
// query prints a flat results table.
func TestCatalogSearchInProcess_Search(t *testing.T) {
	withCatalogCLIFixture(t, `[{"id":"gh","name":"GitHub Tool","description":"d"}]`)
	cfg := &config.Config{}

	resp, err := catalogSearchInProcess(context.Background(), cfg, "github", "", "", 20)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Results) != 1 || resp.Results[0].ID != "gh" {
		t.Fatalf("unexpected results: %+v", resp.Results)
	}
	if resp.Sections != nil {
		t.Fatalf("expected nil sections for a non-empty query, got %+v", resp.Sections)
	}

	formatter, err := GetOutputFormatter()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := captureOutput(func() {
		if err := renderCatalogSearch(formatter, resp); err != nil {
			t.Fatalf("render error: %v", err)
		}
	})
	if !strings.Contains(out, "GitHub Tool") || !strings.Contains(out, "official") || !strings.Contains(out, "gh") {
		t.Errorf("expected the table to name the source, id and title, got:\n%s", out)
	}
}

// TestCatalogSearchInProcess_BrowseSections is the T100 "browse sections"
// golden: an empty query prints Official/Popular sections.
func TestCatalogSearchInProcess_BrowseSections(t *testing.T) {
	withCatalogCLIFixture(t, `[{"id":"gh","name":"GitHub Tool"}]`)
	cfg := &config.Config{}

	resp, err := catalogSearchInProcess(context.Background(), cfg, "", "", "", 20)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Sections == nil {
		t.Fatal("expected sections for an empty query")
	}

	formatter, err := GetOutputFormatter()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := captureOutput(func() {
		if err := renderCatalogSearch(formatter, resp); err != nil {
			t.Fatalf("render error: %v", err)
		}
	})
	if !strings.Contains(out, "Official:") || !strings.Contains(out, "Popular:") {
		t.Errorf("expected Official/Popular section headers, got:\n%s", out)
	}
}

// TestCatalogSearchInProcess_SourceFilterAppliesBeforeTruncation is the CLI
// counterpart of the REST regression: with limit=1, an official source's
// single entry fills the only truncated slot ahead of a lower-ranked
// "other" source's entry. Narrowing to source=other must still surface it
// rather than coming back empty just because it lost the pre-filter
// truncation race.
func TestCatalogSearchInProcess_SourceFilterAppliesBeforeTruncation(t *testing.T) {
	officialSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"official-tool","name":"Official Tool"}]`))
	}))
	t.Cleanup(officialSrv.Close)
	otherSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"other-tool","name":"Other Tool"}]`))
	}))
	t.Cleanup(otherSrv.Close)
	t.Cleanup(registries.AllowPrivateRegistryFetchForTest())
	t.Cleanup(registries.SetRegistriesForTest([]registries.RegistryEntry{
		{ID: "official", Name: "Official", ServersURL: officialSrv.URL, Provenance: "official"},
		{ID: "other", Name: "Other", ServersURL: otherSrv.URL},
	}))
	cfg := &config.Config{}

	resp, err := catalogSearchInProcess(context.Background(), cfg, "tool", "other", "", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Results) != 1 || resp.Results[0].ID != "other-tool" {
		t.Fatalf("expected other-tool to survive source filtering despite limit=1, got: %+v", resp.Results)
	}
}

// TestCatalogShow is the T100 "show" golden.
func TestCatalogShow(t *testing.T) {
	withCatalogCLIFixture(t, `[{"id":"gh","name":"GitHub Tool","description":"desc","url":"https://x.example.com/mcp"}]`)

	// withCatalogCLIFixture already installed the fixture registry list —
	// unlike catalogSearch's production path, this test must NOT also call
	// registries.SetRegistriesFromConfig, which would replace it with the
	// real built-in defaults and go to the network.
	reg := registries.FindRegistry("official")
	if reg == nil {
		t.Fatal("expected the fixture 'official' source to be registered")
	}
	entry, err := registries.FindServerByID(context.Background(), "official", "gh", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	hit := registries.BuildCatalogHit(reg, *entry)
	result := registries.ToCatalogResult(hit, false)

	formatter, err := GetOutputFormatter()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := captureOutput(func() {
		if err := renderCatalogShow(formatter, result); err != nil {
			t.Fatalf("render error: %v", err)
		}
	})
	if !strings.Contains(out, "GitHub Tool") || !strings.Contains(out, "official/gh") || !strings.Contains(out, "https://x.example.com/mcp") {
		t.Errorf("unexpected show output:\n%s", out)
	}
}

// TestPrintCatalogDeprecationNotice pins FR-066: 'registry search'/'registry
// add' print a deprecation note pointing at the 'catalog' equivalent.
func TestPrintCatalogDeprecationNotice(t *testing.T) {
	old := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	printCatalogDeprecationNotice("registry add", "catalog add official/gh")

	w.Close()
	os.Stderr = old
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)

	out := buf.String()
	if !strings.Contains(out, "deprecated") || !strings.Contains(out, "catalog add official/gh") {
		t.Errorf("unexpected deprecation notice: %q", out)
	}
}
