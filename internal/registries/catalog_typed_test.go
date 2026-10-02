package registries

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// Spec 109 fix-catalog-rank T171 (D36.3, D36.7, D36.8): a typed catalog query
// is fetched wide, ranked, then truncated, and the GitHub star budget is spent
// only on the hits that survive.

// installCatalogFixtureSources serves every source of the shared SC-008 fixture
// (testdata/catalog_github_order.json) the way the parity legs do: the official
// source from its recorded corpus, the others from their flat servers.
func installCatalogFixtureSources(t *testing.T, wrap func(http.Handler, string) http.Handler) {
	t.Helper()
	f, _ := loadCatalogFixture(t)
	var regs []RegistryEntry
	for _, raw := range f.Sources {
		var src struct {
			ID         string            `json:"id"`
			Name       string            `json:"name"`
			Provenance string            `json:"provenance"`
			Protocol   string            `json:"protocol"`
			Corpus     []json.RawMessage `json:"corpus"`
			Servers    []json.RawMessage `json:"servers"`
		}
		if err := json.Unmarshal(raw, &src); err != nil {
			t.Fatal(err)
		}
		var h http.Handler
		if src.Protocol == protocolOfficial {
			h = RecordedRegistryHandlerForTest(src.Corpus)
		} else {
			body, _ := json.Marshal(src.Servers)
			h = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(body)
			})
		}
		if wrap != nil {
			h = wrap(h, src.ID)
		}
		srv := httptest.NewServer(h)
		t.Cleanup(srv.Close)
		regs = append(regs, RegistryEntry{ID: src.ID, Name: src.Name, ServersURL: srv.URL + "/v0.1/servers", Protocol: src.Protocol, Provenance: src.Provenance})
	}
	t.Cleanup(AllowPrivateRegistryFetchForTest())
	t.Cleanup(SetRegistriesForTest(regs))
	t.Cleanup(ResetListingCacheForTest)
}

func TestSearchAll_RecordedRegistry_GitHubFirst(t *testing.T) {
	installCatalogFixtureSources(t, nil)
	hits, _, unavailable := SearchAll(context.Background(), "github", "", 20, SearchOptions{PopularityWait: -1})
	if len(unavailable) != 0 {
		t.Fatalf("unavailable = %+v", unavailable)
	}
	if len(hits) != 20 {
		t.Fatalf("got %d hits, want 20", len(hits))
	}
	first := hits[0]
	if first.Source != "official" || first.Entry.ID != "io.github.github/github-mcp-server" {
		t.Fatalf("first hit = %s:%s, want GitHub's own server", first.Source, first.Entry.ID)
	}
	if first.Title != "GitHub" || first.Publisher != "github" || !first.Verified || !first.Official {
		t.Fatalf("first hit = %+v", first)
	}

	seen := map[string]bool{}
	lastTier4, firstTier3 := -1, len(hits)
	for i, h := range hits {
		key := h.Source + ":" + h.Entry.ID
		if seen[key] {
			t.Errorf("%s appears twice", key)
		}
		seen[key] = true
		tier := matchTier(h, "github")
		if tier == 0 {
			t.Errorf("tier-0 (namespace-only) hit %s must not make the top 20", key)
		}
		if tier >= 4 {
			lastTier4 = i
		}
		if tier == 3 && i < firstTier3 {
			firstTier3 = i
		}
	}
	if lastTier4 > firstTier3 {
		t.Errorf("an exact-name hit (idx %d) ranks after a prefix hit (idx %d)", lastTier4, firstTier3)
	}
	if !seen["official:com.mcparmory/github"] {
		t.Error("com.mcparmory/github (exact name) must be in the top 20")
	}
	t.Logf("top 20: %v", idsOf(hits))
}

func TestSearchAll_TypedQueryRanksBeforeTruncation(t *testing.T) {
	// 60 sources-order-first entries only mention github in their description
	// (tier 1); the real match is LAST. With limit 10 it must still lead.
	var items []string
	for i := 0; i < 60; i++ {
		items = append(items, fmt.Sprintf(`{"id":"acme/n%02d","name":"note %02d","description":"works with github"}`, i, i))
	}
	items = append(items, `{"id":"acme/github","name":"github","description":"the real one"}`)
	src := jsonServer(t, "["+strings.Join(items, ",")+"]")
	withTestRegistries(t, []RegistryEntry{{ID: "flat", Name: "Flat", ServersURL: src.URL}})

	hits, _, _ := SearchAll(context.Background(), "github", "", 10, SearchOptions{PopularityWait: -1})
	if len(hits) != 10 {
		t.Fatalf("got %d hits, want 10", len(hits))
	}
	if hits[0].Entry.ID != "acme/github" {
		t.Fatalf("the exact name must lead, got %s", hits[0].Entry.ID)
	}
}

func TestSearchAll_MainTimesOutExpansionHitsStillShown(t *testing.T) {
	released := make(chan struct{})
	t.Cleanup(func() { close(released) })
	installCatalogFixtureSources(t, func(h http.Handler, id string) http.Handler {
		if id != "official" {
			return h
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("search") == "github" { // the slow main query
				select {
				case <-released:
				case <-r.Context().Done():
				}
				http.Error(w, "late", http.StatusGatewayTimeout)
				return
			}
			h.ServeHTTP(w, r)
		})
	})
	t.Cleanup(SetCatalogWarmBehindForTest(2*time.Second, 2))

	hits, _, unavailable := SearchAll(context.Background(), "github", "", 20, SearchOptions{SourceTimeout: 400 * time.Millisecond, PopularityWait: -1})
	if len(unavailable) != 1 || unavailable[0].Source != "official" || !strings.HasPrefix(unavailable[0].Reason, "timeout after") {
		t.Fatalf("unavailable = %+v, want the official source timing out", unavailable)
	}
	if len(hits) == 0 || hits[0].Entry.ID != "io.github.github/github-mcp-server" {
		t.Fatalf("the expansion hits must still be shown, GitHub first; got %v", idsOf(hits))
	}
	if hits[0].FromCache {
		t.Error("expansion hits are live, not from the cache")
	}
}

// R3.1: the MAIN query decides availability (D36.2). A main query that lands
// inside the budget while an expansion is still running must not turn the
// source unavailable: the hits that arrived are shown, with no timeout error.
func TestSearchAll_MainLandsExpansionSlowSourceStaysAvailable(t *testing.T) {
	released := make(chan struct{})
	t.Cleanup(func() { close(released) })
	installCatalogFixtureSources(t, func(h http.Handler, id string) http.Handler {
		if id != "official" {
			return h
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("search") != "github" { // every expansion is slow
				select {
				case <-released:
				case <-r.Context().Done():
				}
				http.Error(w, "late", http.StatusGatewayTimeout)
				return
			}
			h.ServeHTTP(w, r)
		})
	})
	t.Cleanup(SetCatalogWarmBehindForTest(2*time.Second, 2))

	hits, _, unavailable := SearchAll(context.Background(), "github", "", 20, SearchOptions{SourceTimeout: 400 * time.Millisecond, PopularityWait: -1})
	if len(unavailable) != 0 {
		t.Fatalf("unavailable = %+v, want none: the main query answered", unavailable)
	}
	if len(hits) == 0 {
		t.Fatal("the main query's hits must be shown")
	}
	for _, h := range hits {
		if h.FromCache {
			t.Errorf("%s: main-query hits are live, not from the cache", h.Entry.ID)
		}
	}
}

// recordingProvider is a PopularityProvider that holds no stars and records
// every key SearchAll asks it to resolve.
type recordingProvider struct {
	mu   sync.Mutex
	keys []string
}

func (r *recordingProvider) Lookup(string) (int, LookupState) { return 0, LookupAbsent }
func (r *recordingProvider) Resolve(_ context.Context, keys []string, _ time.Duration) {
	r.mu.Lock()
	r.keys = append(r.keys, keys...)
	r.mu.Unlock()
}

func TestSearchAll_PopularityPrefetchCappedAtLimit(t *testing.T) {
	var items []string
	for i := 0; i < 40; i++ {
		items = append(items, fmt.Sprintf(`{"id":"acme/github-%02d","name":"github %02d","source_code_url":"https://github.com/org/r%02d"}`, i, i, i))
	}
	src := jsonServer(t, "["+strings.Join(items, ",")+"]")
	withTestRegistries(t, []RegistryEntry{{ID: "flat", Name: "Flat", ServersURL: src.URL}})
	rec := &recordingProvider{}
	t.Cleanup(SetPopularityProviderForTest(rec))

	hits, _, _ := SearchAll(context.Background(), "github", "", 5, SearchOptions{PopularityWait: -1})
	if len(hits) != 5 {
		t.Fatalf("got %d hits", len(hits))
	}
	if len(rec.keys) == 0 || len(rec.keys) > 5 {
		t.Fatalf("enqueued %d keys, want 1..5 (the typed-query cap is `limit`)", len(rec.keys))
	}
	top := map[string]bool{}
	for _, h := range hits {
		k, _ := GitHubRepoKey(h.Entry.SourceCodeURL)
		top[k] = true
	}
	for _, k := range rec.keys {
		if !top[k] {
			t.Errorf("enqueued %s, which is not among the returned top hits", k)
		}
	}
}

func TestSearchAll_PopularityPrefetchSkipsBorrowedRepos(t *testing.T) {
	corpus := []json.RawMessage{
		mustJSON(map[string]interface{}{
			"server": map[string]interface{}{"name": "agency.ottobot/github", "repository": map[string]string{"url": "https://github.com/modelcontextprotocol/registry"}},
			"_meta":  map[string]interface{}{officialMetaKey: map[string]interface{}{"status": "active", "isLatest": true}},
		}),
		mustJSON(map[string]interface{}{
			"server": map[string]interface{}{"name": "io.github.acme/github", "repository": map[string]string{"url": "https://github.com/acme/github"}},
			"_meta":  map[string]interface{}{officialMetaKey: map[string]interface{}{"status": "active", "isLatest": true}},
		}),
	}
	srv := httptest.NewServer(RecordedRegistryHandlerForTest(corpus))
	t.Cleanup(srv.Close)
	withTestRegistries(t, []RegistryEntry{{ID: "official", Name: "Official", ServersURL: srv.URL + "/v0.1/servers", Protocol: protocolOfficial, Provenance: "official"}})
	t.Cleanup(AllowPrivateRegistryFetchForTest())
	t.Cleanup(ResetListingCacheForTest)
	rec := &recordingProvider{}
	t.Cleanup(SetPopularityProviderForTest(rec))

	SearchAll(context.Background(), "github", "", 10, SearchOptions{PopularityWait: -1})
	if strings.Join(rec.keys, ",") != "acme/github" {
		t.Fatalf("enqueued %v, want only the repo its publisher owns", rec.keys)
	}
}

func mustJSON(v interface{}) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func TestBuildSections_PopularDedupesSameTitleAcrossSources(t *testing.T) {
	pool := []CatalogHit{
		{Source: "reference", Title: "fetch", Popularity: intPop(900), Entry: ServerEntry{ID: "fetch"}},
		{Source: "docker", Title: "mcp/fetch", Popularity: &Popularity{Installs: intPtr(1000000)}, Entry: ServerEntry{ID: "mcp/fetch"}},
		{Source: "docker", Title: "fetch-mcp", Popularity: &Popularity{Installs: intPtr(5)}, Entry: ServerEntry{ID: "fetch-mcp"}},
	}
	sections := buildSections(pool, "")
	var titles []string
	for _, h := range sections.Popular {
		titles = append(titles, h.Title)
	}
	if len(titles) != 2 {
		t.Fatalf("Popular = %v, want fetch once plus fetch-mcp", titles)
	}
	hasFetchMCP := false
	fetchCount := 0
	for _, title := range titles {
		if title == "fetch-mcp" {
			hasFetchMCP = true
		} else {
			fetchCount++
		}
	}
	if !hasFetchMCP || fetchCount != 1 {
		t.Fatalf("Popular = %v", titles)
	}
}

func TestBuildSections_PopularSkipsBorrowedStars(t *testing.T) {
	stub := &stubPopularityProvider{stars: map[string]int{"modelcontextprotocol/registry": 5000, "github/github-mcp-server": 21000}}
	t.Cleanup(SetPopularityProviderForTest(stub))
	reg := officialReg()
	pool := []CatalogHit{
		BuildCatalogHit(reg, ServerEntry{ID: "agency.ottobot/x", Name: "agency.ottobot/x", SourceCodeURL: "https://github.com/modelcontextprotocol/registry"}),
		BuildCatalogHit(reg, ServerEntry{ID: "io.github.github/github-mcp-server", Name: "io.github.github/github-mcp-server", SourceCodeURL: "https://github.com/github/github-mcp-server"}),
	}
	sections := buildSections(pool, "")
	if len(sections.Popular) != 1 || sections.Popular[0].Entry.ID != "io.github.github/github-mcp-server" {
		t.Fatalf("Popular = %v, want only the publisher-owned repo", idsOf(sections.Popular))
	}
}
