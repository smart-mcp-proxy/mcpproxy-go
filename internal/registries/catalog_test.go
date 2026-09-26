package registries

import (
	"context"
	"fmt"
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

// TestSearchAll_PopularSectionNotLimitedByPageSize pins review round 4 F-F:
// sections must be built from the FULL ranked list, before truncation to
// `limit` — building them AFTER truncation meant Popular could never reach
// its own 12-entry cap (catalogSectionCap) at the documented default `limit`
// of 10, silently excluding a real hit that simply didn't survive the
// earlier truncation.
func TestSearchAll_PopularSectionNotLimitedByPageSize(t *testing.T) {
	// Two sources, each returning `limit` (10) entries of their own — every
	// per-source SearchServers call is independently bounded by `limit`
	// (search.go), so a single source can never itself produce more than
	// `limit` hits. The merged total (up to 20) exceeding `limit=10` is what
	// reproduces F-F: only the post-merge truncation-before-sectioning bug
	// can now discard a hit sections should have kept.
	entriesFor := func(prefix string) string {
		var entries []string
		for i := 0; i < 10; i++ {
			entries = append(entries, fmt.Sprintf(`{"id":"%s%02d","name":"Server %s%02d"}`, prefix, i, prefix, i))
		}
		return "[" + strings.Join(entries, ",") + "]"
	}
	src1 := jsonServer(t, entriesFor("a"))
	src2 := jsonServer(t, entriesFor("b"))
	withTestRegistries(t, []RegistryEntry{
		{ID: "src1", Name: "Src1", ServersURL: src1.URL},
		{ID: "src2", Name: "Src2", ServersURL: src2.URL},
	})

	hits, sections, _ := SearchAll(context.Background(), "", "", 10, SearchOptions{})
	if len(hits) != 10 {
		t.Fatalf("expected the flat list truncated to limit=10, got %d", len(hits))
	}
	if sections == nil {
		t.Fatal("expected sections to be populated for an empty query")
	}
	if len(sections.Popular) != 12 {
		t.Errorf("expected Popular capped at catalogSectionCap=12 (not limit=10), got %d", len(sections.Popular))
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

// TestToCatalogResult_SecretLikeOverridesFalsifiedFlag pins T099/FR-065: a
// registry that explicitly sets isSecret:false on a secret-shaped name (or
// omits it) still serves secret_like:true — the D13 name rule is OR'd in,
// never overridden by the registry's own (possibly wrong) flag.
func TestToCatalogResult_SecretLikeOverridesFalsifiedFlag(t *testing.T) {
	hit := CatalogHit{
		Source: "official",
		Entry: ServerEntry{ID: "x", RequiredInputs: []RequiredInput{
			{Name: "GITHUB_TOKEN", Secret: false}, // falsified: registry says not secret
			{Name: "GITHUB_TOKEN_OMITTED"},        // omitted: Go zero value is false
			{Name: "PORT", Secret: true},          // explicit true passes through
		}},
	}
	result := ToCatalogResult(hit, false)
	byName := map[string]bool{}
	for _, in := range result.RequiredInputs {
		byName[in.Name] = in.SecretLike
	}
	if !byName["GITHUB_TOKEN"] {
		t.Error("expected GITHUB_TOKEN secret_like=true despite Secret:false (name rule overrides)")
	}
	if !byName["GITHUB_TOKEN_OMITTED"] {
		t.Error("expected GITHUB_TOKEN_OMITTED secret_like=true (name rule)")
	}
	if !byName["PORT"] {
		t.Error("expected PORT secret_like=true (explicit registry flag passes through)")
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

// TestToCatalogResult_HybridPrefersInstallCmdOverConnectURL pins review
// round 4 F-B: officialServerToEntry documents "package wins for stdio; keep
// the remote as a fallback" for a hybrid entry (both packages[] and
// remotes[] present) — ConnectURL is populated there ONLY as a fallback.
// toCatalogInstall must report stdio/InstallCmd for such an entry, never
// http/ConnectURL, or the reported transport is wrong and the "added" join
// (CatalogInstallTarget) never matches the actually-configured (stdio)
// server.
func TestToCatalogResult_HybridPrefersInstallCmdOverConnectURL(t *testing.T) {
	hit := CatalogHit{
		Source: "official",
		Entry: ServerEntry{
			ID:         "io.github.github/github-mcp-server",
			InstallCmd: "docker run -i --rm ghcr.io/github/github-mcp-server",
			ConnectURL: "https://api.githubcopilot.com/mcp/",
		},
	}
	result := ToCatalogResult(hit, false)
	if result.Transport != "stdio" {
		t.Errorf("expected transport stdio for a hybrid entry, got %s", result.Transport)
	}
	if result.Install.Command != "docker" {
		t.Errorf("expected command 'docker', got %q", result.Install.Command)
	}
	if result.Install.URL != "" {
		t.Errorf("expected no URL on a hybrid entry's install, got %q", result.Install.URL)
	}
}
