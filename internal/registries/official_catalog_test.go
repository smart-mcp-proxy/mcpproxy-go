package registries

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// Spec 109 fix-catalog-rank T167 (D36.2, D36.4, D36.10): the official
// protocol's typed-query fetch (one page per query, concurrent), title and
// version parsing, and version collapse.

func TestOfficialServerToEntry_ReadsTitleAndVersion(t *testing.T) {
	entry := officialServerToEntry(map[string]interface{}{
		"name":        "io.github.github/github-mcp-server",
		"title":       "GitHub",
		"description": "d",
		"version":     "1.13.0",
	})
	if entry.Title != "GitHub" {
		t.Errorf("Title = %q, want the server.json title", entry.Title)
	}
	if entry.Version != "1.13.0" {
		t.Errorf("Version = %q, want 1.13.0", entry.Version)
	}
	if entry.Name != "io.github.github/github-mcp-server" || entry.ID != entry.Name {
		t.Errorf("Name/ID must stay the reverse-DNS name, got %q / %q", entry.Name, entry.ID)
	}
}

func officialEntry(name, version string, latest bool) ServerEntry {
	return ServerEntry{ID: name, Name: name, Version: version, isLatest: latest}
}

func TestCollapseOfficialVersions_KeepsLatestAtFirstPosition(t *testing.T) {
	in := []ServerEntry{
		officialEntry("a/x", "1.0.3", false),
		officialEntry("b/y", "2.0.0", false),
		officialEntry("a/x", "1.0.10", false),
		officialEntry("a/x", "1.0.4", false),
	}
	out := collapseOfficialVersions(in)
	if len(out) != 2 {
		t.Fatalf("got %d entries, want 2: %+v", len(out), out)
	}
	if out[0].Name != "a/x" || out[0].Version != "1.0.10" {
		t.Errorf("a/x must keep the highest dotted version at its first position, got %+v", out[0])
	}
	if out[1].Name != "b/y" {
		t.Errorf("order must follow first occurrence, got %+v", out)
	}
}

func TestCollapseOfficialVersions_ExplicitIsLatestWins(t *testing.T) {
	out := collapseOfficialVersions([]ServerEntry{
		officialEntry("a/x", "9.9.9", false),
		officialEntry("a/x", "1.0.3", true),
	})
	if len(out) != 1 || out[0].Version != "1.0.3" {
		t.Fatalf("an explicit isLatest:true must beat a higher number, got %+v", out)
	}
}

func TestCollapseOfficialVersions_PrereleaseSuffixIgnoredAndLastSeenTies(t *testing.T) {
	out := collapseOfficialVersions([]ServerEntry{
		officialEntry("a/x", "1.2.0-rc.1", false),
		officialEntry("a/x", "1.10.0-beta", false),
	})
	if len(out) != 1 || out[0].Version != "1.10.0-beta" {
		t.Fatalf("got %+v", out)
	}
	tie := collapseOfficialVersions([]ServerEntry{
		{ID: "a/x", Name: "a/x", Description: "first"},
		{ID: "a/x", Name: "a/x", Description: "last"},
	})
	if len(tie) != 1 || tie[0].Description != "last" {
		t.Fatalf("no version info: the last one seen wins, got %+v", tie)
	}
}

func TestParseOfficialItems_CollapsesVersionsWithoutMeta(t *testing.T) {
	var items []interface{}
	for _, v := range []string{"1.0.3", "1.0.10", "1.0.4"} {
		items = append(items, map[string]interface{}{"server": map[string]interface{}{"name": "a/x", "version": v}})
	}
	got := parseOfficialItems(items)
	if len(got) != 1 || got[0].Version != "1.0.10" {
		t.Fatalf("got %+v, want one a/x at 1.0.10", got)
	}
}

func TestOfficialExpansionQueries(t *testing.T) {
	cases := []struct {
		q    string
		want []string
	}{
		{"github", []string{".github/", "/github"}},
		{"  GitHub  ", []string{".GitHub/", "/GitHub"}},
		{"github actions", []string{"github-actions", ".github-actions/", "/github-actions"}},
		{"a/b", nil},
		{"<x>", nil},
		{"", nil},
		{"x", nil},
		{strings.Repeat("a", 65), nil},
	}
	for _, c := range cases {
		got := officialExpansionQueries(c.q)
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("officialExpansionQueries(%q) = %v, want %v", c.q, got, c.want)
		}
	}
	if len(officialExpansionQueries(strings.Repeat("a", 64))) != 2 {
		t.Error("a 64-character query is still expanded")
	}
}

// countingRegistry serves the recorded corpus and records every request's
// search and cursor parameters.
type countingRegistry struct {
	mu       sync.Mutex
	searches []string
	cursors  []string
}

func (c *countingRegistry) handler(inner http.Handler, fail map[string]int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		c.searches = append(c.searches, r.URL.Query().Get("search"))
		if cur := r.URL.Query().Get("cursor"); cur != "" {
			c.cursors = append(c.cursors, cur)
		}
		c.mu.Unlock()
		if code, ok := fail[r.URL.Query().Get("search")]; ok {
			http.Error(w, "boom", code)
			return
		}
		inner.ServeHTTP(w, r)
	})
}

func newOfficialFixtureRegistry(t *testing.T, fail map[string]int) (*RegistryEntry, *countingRegistry) {
	t.Helper()
	_, official := loadCatalogFixture(t)
	counter := &countingRegistry{}
	srv := httptest.NewServer(counter.handler(RecordedRegistryHandlerForTest(official.Corpus), fail))
	t.Cleanup(srv.Close)
	t.Cleanup(AllowPrivateRegistryFetchForTest())
	return &RegistryEntry{ID: "official", Name: "Official", ServersURL: srv.URL + "/v0.1/servers", Protocol: protocolOfficial, Provenance: "official"}, counter
}

func entryIDs(entries []ServerEntry) []string {
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		ids = append(ids, e.ID)
	}
	return ids
}

func TestFetchOfficialCatalog_OnePagePerQueryConcurrently(t *testing.T) {
	reg, counter := newOfficialFixtureRegistry(t, nil)
	entries, err := fetchOfficialCatalog(context.Background(), reg, "github")
	if err != nil {
		t.Fatal(err)
	}
	if len(counter.searches) != 3 {
		t.Fatalf("requests = %v, want 3 (main + owner + segment)", counter.searches)
	}
	if len(counter.cursors) != 0 {
		t.Fatalf("a typed query fetches ONE page per query, saw cursors %v", counter.cursors)
	}
	ids := entryIDs(entries)
	// Merge order: owner hits first, then segment hits, then the main page.
	if ids[0] != "io.github.github/github-mcp-server" {
		t.Fatalf("owner hit must lead, got %v", ids[:3])
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			t.Fatalf("id %s repeated", id)
		}
		seen[id] = true
	}
	if !seen["com.mcparmory/github"] || !seen["io.github.3121n/statfin-mcp"] {
		t.Fatalf("segment and main hits must both be present (%d entries)", len(ids))
	}
	if len(ids) > typedFetchCap {
		t.Fatalf("%d entries exceed the %d per-source ceiling", len(ids), typedFetchCap)
	}
}

func TestFetchOfficialCatalog_ExpansionErrorIsBestEffort(t *testing.T) {
	reg, _ := newOfficialFixtureRegistry(t, map[string]int{"/github": http.StatusInternalServerError})
	entries, err := fetchOfficialCatalog(context.Background(), reg, "github")
	if err != nil {
		t.Fatalf("an expansion failure must not fail the source: %v", err)
	}
	ids := strings.Join(entryIDs(entries), ",")
	if !strings.Contains(ids, "io.github.github/github-mcp-server") {
		t.Fatal("owner hit missing")
	}
	if !strings.Contains(ids, "io.github.3121n/statfin-mcp") {
		t.Fatal("main hits missing")
	}
}

func TestFetchOfficialCatalog_MainErrorReturnsExpansionHitsAndError(t *testing.T) {
	reg, _ := newOfficialFixtureRegistry(t, map[string]int{"github": http.StatusBadRequest})
	entries, err := fetchOfficialCatalog(context.Background(), reg, "github")
	if err == nil {
		t.Fatal("the main query decides availability: its error must be returned")
	}
	if !strings.Contains(strings.Join(entryIDs(entries), ","), "io.github.github/github-mcp-server") {
		t.Fatalf("expansion hits must still be returned, got %v", entryIDs(entries))
	}
}

func TestFetchOfficialCatalog_NoExpansionForAWeirdQuery(t *testing.T) {
	reg, counter := newOfficialFixtureRegistry(t, nil)
	if _, err := fetchOfficialCatalog(context.Background(), reg, "a/b"); err != nil {
		t.Fatal(err)
	}
	if len(counter.searches) != 1 {
		t.Fatalf("requests = %v, want only the main query", counter.searches)
	}
}

func TestFetchOfficialCatalog_PhraseQueryAddsHyphenatedPhrase(t *testing.T) {
	reg, counter := newOfficialFixtureRegistry(t, nil)
	entries, err := fetchOfficialCatalog(context.Background(), reg, "github actions")
	if err != nil {
		t.Fatal(err)
	}
	if len(counter.searches) != 4 {
		t.Fatalf("requests = %v, want main + phrase + owner + segment", counter.searches)
	}
	ids := strings.Join(entryIDs(entries), ",")
	if !strings.Contains(ids, "io.github.ofershap/github-actions") {
		t.Fatalf("the hyphenated phrase must find github-actions servers, got %s", ids)
	}
}

func TestParseOfficialPage_PreservesRawJSONShape(t *testing.T) {
	var raw interface{}
	if err := json.Unmarshal([]byte(`{"servers":[{"server":{"name":"a/b","title":"T"},"_meta":{}}],"metadata":{"nextCursor":"x"}}`), &raw); err != nil {
		t.Fatal(err)
	}
	servers, next := parseOfficialPage(raw)
	if len(servers) != 1 || servers[0].Title != "T" || next != "x" {
		t.Fatalf("got %+v next=%q", servers, next)
	}
}
