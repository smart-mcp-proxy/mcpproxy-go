package main

import (
	"bytes"
	"context"
	"encoding/json"
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
// target; a manual add matches on install target alone. The resolver also
// reports the server name, but only for a unique match (REST parity).
func TestCatalogAddedFromConfig(t *testing.T) {
	cfg := &config.Config{
		Servers: []*config.ServerConfig{
			{Name: "manual", URL: "https://manual.example.com/mcp"},
			{Name: "from-official", Command: "npx", Args: []string{"server-x"}, SourceRegistryID: "official"},
			{Name: "dup-a", Command: "npx", Args: []string{"server-dup"}},
			{Name: "dup-b", Command: "npx", Args: []string{"server-dup"}},
		},
	}
	added := catalogAddedFromConfig(cfg)

	manualHit := registries.CatalogHit{Source: "smithery", Entry: registries.ServerEntry{ID: "m", URL: "https://manual.example.com/mcp"}}
	if ok, name := added(manualHit); !ok || name != "manual" {
		t.Errorf("expected a manual add to match by install target regardless of source, got (%v, %q)", ok, name)
	}

	officialHit := registries.CatalogHit{Source: "official", Entry: registries.ServerEntry{ID: "x", InstallCmd: "npx server-x"}}
	if ok, name := added(officialHit); !ok || name != "from-official" {
		t.Errorf("expected the registry-sourced server to match its own source + target, got (%v, %q)", ok, name)
	}

	wrongSourceHit := registries.CatalogHit{Source: "smithery", Entry: registries.ServerEntry{ID: "x", InstallCmd: "npx server-x"}}
	if ok, name := added(wrongSourceHit); ok || name != "" {
		t.Errorf("a registry-sourced configured server must not match a different source with the same target, got (%v, %q)", ok, name)
	}

	noMatchHit := registries.CatalogHit{Source: "official", Entry: registries.ServerEntry{ID: "y", InstallCmd: "npx server-y"}}
	if ok, name := added(noMatchHit); ok || name != "" {
		t.Errorf("expected no match for an unrelated entry, got (%v, %q)", ok, name)
	}

	ambiguousHit := registries.CatalogHit{Source: "official", Entry: registries.ServerEntry{ID: "d", InstallCmd: "npx server-dup"}}
	if ok, name := added(ambiguousHit); !ok || name != "" {
		t.Errorf("expected added=true with no name when two servers match, got (%v, %q)", ok, name)
	}
}

// TestCatalogSearchInProcess_AddedServerName pins CLI/REST parity: the offline
// search path emits added_server_name for a uniquely matched configured server.
func TestCatalogSearchInProcess_AddedServerName(t *testing.T) {
	withCatalogCLIFixture(t, `[{"id":"gh","name":"GitHub Tool","description":"desc","url":"https://x.example.com/mcp"}]`)
	cfg := &config.Config{Servers: []*config.ServerConfig{{Name: "my-gh", URL: "https://x.example.com/mcp"}}}

	resp, err := catalogSearchInProcess(context.Background(), cfg, "GitHub", "", "", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("expected 1 result, got %+v", resp.Results)
	}
	if !resp.Results[0].Added || resp.Results[0].AddedServerName != "my-gh" {
		t.Errorf("expected added=true added_server_name=my-gh, got %+v", resp.Results[0])
	}

	empty, err := catalogSearchInProcess(context.Background(), cfg, "", "", "", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if empty.Sections == nil {
		t.Fatal("expected sections for an empty query")
	}
	for _, r := range append(append([]registries.CatalogResult{}, empty.Sections.Official...), empty.Sections.Popular...) {
		if r.ID == "gh" && r.AddedServerName != "my-gh" {
			t.Errorf("section entry missing added_server_name: %+v", r)
		}
	}
}

func TestCatalogAddedFromConfig_DoesNotFlattenArgumentBoundaries(t *testing.T) {
	cfg := &config.Config{Servers: []*config.ServerConfig{
		{Name: "from-official", Command: "npx", Args: []string{"a b", "c"}, SourceRegistryID: "official"},
	}}
	added := catalogAddedFromConfig(cfg)

	otherArgv := registries.CatalogHit{
		Source: "official",
		Entry:  registries.ServerEntry{ID: "different-argv", InstallCmd: `npx a "b c"`},
	}
	if added(otherArgv) {
		t.Fatal("distinct command argv values must not match a configured server")
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

// TestCatalogResultForConfig pins that 'catalog show' builds its result with
// the added_server_name join, like REST.
func TestCatalogResultForConfig(t *testing.T) {
	cfg := &config.Config{Servers: []*config.ServerConfig{{Name: "my-gh", URL: "https://x.example.com/mcp"}}}
	hit := registries.CatalogHit{Source: "official", Entry: registries.ServerEntry{ID: "gh", URL: "https://x.example.com/mcp"}}
	result := catalogResultForConfig(cfg, hit)
	if !result.Added || result.AddedServerName != "my-gh" {
		t.Errorf("expected added=true added_server_name=my-gh, got %+v", result)
	}
	data, err := json.Marshal(result)
	if err != nil || !strings.Contains(string(data), `"added_server_name":"my-gh"`) {
		t.Errorf("expected added_server_name in JSON, got %s (err %v)", data, err)
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

// TestRejectCatalogTag pins F-K (#1398): --tag cannot be honoured, so a
// non-empty value is an error rather than a silent no-op.
func TestRejectCatalogTag(t *testing.T) {
	if err := rejectCatalogTag(""); err != nil {
		t.Errorf("empty tag should be accepted, got %v", err)
	}
	if err := rejectCatalogTag("database"); err == nil {
		t.Error("non-empty tag should be rejected")
	}
}
