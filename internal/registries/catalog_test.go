package registries

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// withTestRegistries swaps registryList for the duration of a test.
func withTestRegistries(t *testing.T, regs []RegistryEntry) {
	t.Helper()
	original := registryList
	registryList = regs
	t.Cleanup(func() { registryList = original })
}

func jsonServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func hangingServer(t *testing.T) *httptest.Server {
	t.Helper()
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() {
		close(block)
		srv.Close()
	})
	return srv
}

// TestSearchAll_MergesAcrossSources pins the basic fan-out + merge (FR-060):
// results from two healthy sources both appear.
func TestSearchAll_MergesAcrossSources(t *testing.T) {
	src1 := jsonServer(t, `[{"id":"one","name":"GitHub One","description":"d1"}]`)
	src2 := jsonServer(t, `[{"id":"two","name":"GitHub Two","description":"d2"}]`)
	withTestRegistries(t, []RegistryEntry{
		{ID: "src1", Name: "Src1", ServersURL: src1.URL},
		{ID: "src2", Name: "Src2", ServersURL: src2.URL},
	})

	hits, _, unavailable := SearchAll(context.Background(), "github", "", 10, SearchOptions{})
	if len(unavailable) != 0 {
		t.Fatalf("expected no unavailable sources, got %+v", unavailable)
	}
	if len(hits) != 2 {
		t.Fatalf("expected 2 hits, got %d: %+v", len(hits), hits)
	}
	sources := map[string]bool{}
	for _, h := range hits {
		sources[h.Source] = true
	}
	if !sources["src1"] || !sources["src2"] {
		t.Fatalf("expected hits from both sources, got %+v", sources)
	}
}

// TestSearchAll_DedupsBySourceAndID pins the (source, id) de-dup rule.
func TestSearchAll_DedupsBySourceAndID(t *testing.T) {
	src := jsonServer(t, `[{"id":"dup","name":"Dup"},{"id":"dup","name":"Dup"}]`)
	withTestRegistries(t, []RegistryEntry{{ID: "src", Name: "Src", ServersURL: src.URL}})

	hits, _, _ := SearchAll(context.Background(), "", "", 10, SearchOptions{})
	count := 0
	for _, h := range hits {
		if h.Entry.ID == "dup" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected the duplicate id to collapse to 1 hit, got %d", count)
	}
}

// TestSearchAll_SourceTimeoutMarkedUnavailable pins FR-060: a hung source
// times out and is reported unavailable, while the other sources' results
// still come back.
func TestSearchAll_SourceTimeoutMarkedUnavailable(t *testing.T) {
	slow := hangingServer(t)
	fast := jsonServer(t, `[{"id":"ok","name":"OK"}]`)
	withTestRegistries(t, []RegistryEntry{
		{ID: "slow", Name: "Slow", ServersURL: slow.URL},
		{ID: "fast", Name: "Fast", ServersURL: fast.URL},
	})

	hits, _, unavailable := SearchAll(context.Background(), "", "", 10, SearchOptions{SourceTimeout: 200 * time.Millisecond})
	if len(unavailable) != 1 || unavailable[0].Source != "slow" {
		t.Fatalf("expected 'slow' to be reported unavailable, got %+v", unavailable)
	}
	if unavailable[0].Reason == "" {
		t.Fatal("expected a non-empty reason")
	}
	found := false
	for _, h := range hits {
		if h.Source == "fast" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected the fast source's results despite the slow one timing out, got %+v", hits)
	}
}

// TestSearchAll_EmptyQueryReturnsSections pins FR-060: an empty q returns
// official + popular sections (≤ 12 each), capped and populated.
func TestSearchAll_EmptyQueryReturnsSections(t *testing.T) {
	official := jsonServer(t, `[{"id":"a","name":"A"}]`)
	custom := jsonServer(t, `[{"id":"b","name":"B"}]`)
	withTestRegistries(t, []RegistryEntry{
		{ID: "official-src", Name: "Official", ServersURL: official.URL, Provenance: "official"},
		{ID: "custom-src", Name: "Custom", ServersURL: custom.URL, Provenance: "custom"},
	})

	hits, sections, _ := SearchAll(context.Background(), "", "", 10, SearchOptions{})
	if len(hits) != 2 {
		t.Fatalf("expected 2 merged hits, got %d", len(hits))
	}
	if sections == nil {
		t.Fatal("expected sections to be populated for an empty query")
	}
	foundOfficial := false
	for _, h := range sections.Official {
		if h.Source == "official-src" {
			foundOfficial = true
		}
	}
	if !foundOfficial {
		t.Fatalf("expected the official source's entry in the official section, got %+v", sections.Official)
	}
}

// TestSearchAll_NonEmptyQueryHasNoSections pins that sections stay nil (→ REST
// null) once a query narrows the results.
func TestSearchAll_NonEmptyQueryHasNoSections(t *testing.T) {
	src := jsonServer(t, `[{"id":"a","name":"A"}]`)
	withTestRegistries(t, []RegistryEntry{{ID: "src", Name: "Src", ServersURL: src.URL}})

	_, sections, _ := SearchAll(context.Background(), "a", "", 10, SearchOptions{})
	if sections != nil {
		t.Fatalf("expected nil sections for a non-empty query, got %+v", sections)
	}
}

// TestToCatalogResult_HTTPInstall pins the Transport/Install derivation for a
// remote server (data-model §9).
func TestToCatalogResult_HTTPInstall(t *testing.T) {
	hit := CatalogHit{
		Source: "official", Title: "GitHub", Publisher: "github", Verified: true, Official: true,
		Entry: ServerEntry{ID: "io.github.github/github-mcp-server", URL: "https://api.githubcopilot.com/mcp/",
			Description: "…", RequiredInputs: []RequiredInput{{Name: "GITHUB_TOKEN"}}},
	}
	result := ToCatalogResult(hit, false)
	if result.Transport != "http" {
		t.Errorf("expected transport http, got %s", result.Transport)
	}
	if result.Install.URL != "https://api.githubcopilot.com/mcp/" {
		t.Errorf("unexpected install url: %+v", result.Install)
	}
	if result.Install.Command != "" {
		t.Errorf("expected no command for an http install, got %q", result.Install.Command)
	}
	if len(result.RequiredInputs) != 1 || !result.RequiredInputs[0].SecretLike {
		t.Errorf("expected GITHUB_TOKEN to be secret_like via the name rule, got %+v", result.RequiredInputs)
	}
}

// TestToCatalogResult_StdioInstall pins command/args splitting for a local
// install target.
func TestToCatalogResult_StdioInstall(t *testing.T) {
	hit := CatalogHit{
		Source: "official",
		Entry:  ServerEntry{ID: "fs", Name: "Filesystem", InstallCmd: "npx -y @modelcontextprotocol/server-filesystem /tmp"},
	}
	result := ToCatalogResult(hit, true)
	if result.Transport != "stdio" {
		t.Errorf("expected transport stdio, got %s", result.Transport)
	}
	if result.Install.Command != "npx" {
		t.Errorf("expected command 'npx', got %q", result.Install.Command)
	}
	wantArgs := []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp"}
	if strings.Join(result.Install.Args, " ") != strings.Join(wantArgs, " ") {
		t.Errorf("unexpected args: %+v", result.Install.Args)
	}
	if !result.Added {
		t.Error("expected Added to be passed through as true")
	}
}
