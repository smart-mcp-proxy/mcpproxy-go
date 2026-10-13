package registries

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// stubPopularityProvider is a deterministic, non-networked PopularityProvider
// for tests that need known star counts without any HTTP stub or timing
// dependency (T010/T012 — SC-001/SC-002). Resolve is a no-op: everything the
// test cares about is pre-seeded and already "fresh".
type stubPopularityProvider struct {
	mu    sync.Mutex
	stars map[string]int
}

func (s *stubPopularityProvider) Lookup(key string) (int, LookupState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := s.stars[key]; ok {
		return v, LookupFresh
	}
	return 0, LookupAbsent
}

func (s *stubPopularityProvider) Resolve(context.Context, []string, time.Duration) {}

var _ PopularityProvider = (*stubPopularityProvider)(nil)

func idsOf(hits []CatalogHit) []string {
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.Entry.ID
	}
	return out
}

// --- T010: BuildCatalogHit ---------------------------------------------------

func TestBuildCatalogHit_CopiesPopularityAndAddsCacheOnlyStars(t *testing.T) {
	reg := RegistryEntry{ID: "docker", Name: "Docker"}
	installs := 42
	entry := ServerEntry{
		ID: "tool", Name: "Tool",
		SourceCodeURL: "https://github.com/org/tool",
		Popularity:    &Popularity{Installs: &installs},
	}

	restore := SetPopularityProviderForTest(nil)
	hit := BuildCatalogHit(&reg, entry)
	restore()
	if hit.Popularity == nil || hit.Popularity.Installs == nil || *hit.Popularity.Installs != 42 {
		t.Fatalf("expected Installs to carry through with no provider installed, got %+v", hit.Popularity)
	}
	if hit.Popularity.Stars != nil {
		t.Fatalf("expected no Stars with no provider installed, got %+v", hit.Popularity)
	}

	stub := &stubPopularityProvider{stars: map[string]int{"org/tool": 321}}
	restore2 := SetPopularityProviderForTest(stub)
	hit2 := BuildCatalogHit(&reg, entry)
	restore2()
	if hit2.Popularity == nil || hit2.Popularity.Stars == nil || *hit2.Popularity.Stars != 321 {
		t.Fatalf("expected Stars=321 from the cache-only lookup, got %+v", hit2.Popularity)
	}
	if hit2.Popularity.Installs == nil || *hit2.Popularity.Installs != 42 {
		t.Fatalf("expected Installs to still carry through alongside Stars, got %+v", hit2.Popularity)
	}
}

// --- T012 / SC-001 -----------------------------------------------------------

// TestSearchAll_SC001_PopularDiffersFromOfficial pins SC-001: 14 official
// hits in source order (repo-14 has by far the most stars but sits outside
// Official's first-12 cap), plus one non-official Docker hit with pulls.
// Popular must differ from Official, Popular[0] must be the most-starred
// hit, and Popular must contain a hit Official does not.
func TestSearchAll_SC001_PopularDiffersFromOfficial(t *testing.T) {
	entries := make([]string, 0, 14)
	for i := 1; i <= 14; i++ {
		entries = append(entries, fmt.Sprintf(
			`{"id":"repo-%d","name":"Repo %d","source_code_url":"https://github.com/org/repo-%d"}`, i, i, i))
	}
	officialSrv := jsonServer(t, "["+strings.Join(entries, ",")+"]")
	dockerSrv := jsonServer(t, `{"results":[{"name":"popular-tool","pull_count":5000000,"short_description":"d"}]}`)

	withTestRegistries(t, []RegistryEntry{
		{ID: "official", Name: "Official", ServersURL: officialSrv.URL, Provenance: "official"},
		{ID: "docker", Name: "Docker", ServersURL: dockerSrv.URL, Protocol: protocolDocker},
	})

	stub := &stubPopularityProvider{stars: map[string]int{
		"org/repo-14": 99999, // outside Official's first 12; the standout count
		"org/repo-1":  500,
		"org/repo-2":  10,
	}}
	restore := SetPopularityProviderForTest(stub)
	defer restore()

	_, sections, unavailable := SearchAll(context.Background(), "", "", 10, SearchOptions{})
	if len(unavailable) != 0 {
		t.Fatalf("expected no unavailable sources, got %+v", unavailable)
	}
	if sections == nil {
		t.Fatal("expected sections for an empty query")
	}

	if len(sections.Official) != 12 {
		t.Fatalf("expected Official capped at 12, got %d: %+v", len(sections.Official), idsOf(sections.Official))
	}
	for i, h := range sections.Official {
		want := fmt.Sprintf("repo-%d", i+1)
		if h.Entry.ID != want {
			t.Fatalf("expected Official in SOURCE order — Official[%d]=%s, got %s (full: %+v)", i, want, h.Entry.ID, idsOf(sections.Official))
		}
	}

	if len(sections.Popular) == 0 {
		t.Fatal("expected a non-empty Popular section")
	}
	if sections.Popular[0].Entry.ID != "repo-14" {
		t.Fatalf("expected Popular[0] to be the most-starred hit (repo-14), got %s", sections.Popular[0].Entry.ID)
	}

	officialIDs := make(map[string]bool, len(sections.Official))
	for _, h := range sections.Official {
		officialIDs[h.Entry.ID] = true
	}
	foundOutsideOfficial := false
	foundDocker := false
	for _, h := range sections.Popular {
		if !officialIDs[h.Entry.ID] {
			foundOutsideOfficial = true
		}
		if h.Source == "docker" {
			foundDocker = true
		}
	}
	if !foundOutsideOfficial {
		t.Fatalf("expected Popular to contain a hit not in Official, got %+v", idsOf(sections.Popular))
	}
	if !foundDocker {
		t.Fatalf("expected the Docker install-count hit to blend into Popular, got %+v", idsOf(sections.Popular))
	}
	if strings.Join(idsOf(sections.Official), ",") == strings.Join(idsOf(sections.Popular), ",") {
		t.Fatal("expected Popular to differ from Official")
	}
}

// TestSearchAll_SC002_NoSignalMeansEmptyPopular pins SC-002: with no
// popularity known for any hit, Popular is empty (never padded with
// zero-popularity hits) and Official is unaffected.
func TestSearchAll_SC002_NoSignalMeansEmptyPopular(t *testing.T) {
	officialSrv := jsonServer(t, `[{"id":"a","name":"A"},{"id":"b","name":"B"}]`)
	withTestRegistries(t, []RegistryEntry{
		{ID: "official", Name: "Official", ServersURL: officialSrv.URL, Provenance: "official"},
	})

	restore := SetPopularityProviderForTest(&stubPopularityProvider{stars: map[string]int{}})
	defer restore()

	_, sections, _ := SearchAll(context.Background(), "", "", 10, SearchOptions{})
	if sections == nil {
		t.Fatal("expected sections for an empty query")
	}
	if len(sections.Popular) != 0 {
		t.Fatalf("expected an empty Popular section with no signal, got %+v", idsOf(sections.Popular))
	}
	if len(sections.Official) != 2 {
		t.Fatalf("expected Official unchanged (2 hits), got %+v", idsOf(sections.Official))
	}
}

// TestSearchAll_NoProviderInstalledMeansEmptyPopular is SC-002's "GitHub
// unreachable / no popularity wiring at all" variant: a nil provider (the
// package default, and what most tests run with) must behave the same as an
// installed-but-empty one.
func TestSearchAll_NoProviderInstalledMeansEmptyPopular(t *testing.T) {
	officialSrv := jsonServer(t, `[{"id":"a","name":"A","source_code_url":"https://github.com/org/a"}]`)
	withTestRegistries(t, []RegistryEntry{
		{ID: "official", Name: "Official", ServersURL: officialSrv.URL, Provenance: "official"},
	})

	restore := SetPopularityProviderForTest(nil)
	defer restore()

	_, sections, _ := SearchAll(context.Background(), "", "", 10, SearchOptions{})
	if sections == nil || len(sections.Popular) != 0 {
		t.Fatalf("expected an empty Popular section with no provider installed, got %+v", sections)
	}
}

// --- T013 / SC-003 ------------------------------------------------------------

// TestSearchAll_SC003_ReturnsWithinPopularityWaitBudget pins SC-003: SearchAll
// returns within PopularityWait (+ generous slack for CI jitter) even when
// the GitHub fetch is slow, unavailable[] stays untouched (popularity is
// never a catalog source), and a second call after the stub answers carries
// the stars. The spec's illustrative "sleeps 5s" is shortened here (a fast,
// deterministic stand-in for "slower than the wait") so the suite stays
// fast; the property under test — SearchAll's return time is bounded by the
// wait, not by the upstream's latency — is unaffected by the absolute
// duration chosen.
func TestSearchAll_SC003_ReturnsWithinPopularityWaitBudget(t *testing.T) {
	officialSrv := jsonServer(t, `[{"id":"a","name":"A","source_code_url":"https://github.com/org/repo"}]`)
	withTestRegistries(t, []RegistryEntry{
		{ID: "official", Name: "Official", ServersURL: officialSrv.URL, Provenance: "official"},
	})

	block := make(chan struct{})
	var once sync.Once
	closeBlock := func() { once.Do(func() { close(block) }) }
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"stargazers_count": 777}`))
	}))
	defer func() { closeBlock(); slow.Close() }()

	restoreBase := SetGitHubAPIBaseForTest(slow.URL)
	defer restoreBase()
	provider := NewGitHubStarsProvider(PopularityOptions{})
	defer provider.Close()
	defer SetPopularityProviderForTest(provider)()

	wait := 80 * time.Millisecond
	start := time.Now()
	hits, _, unavailable := SearchAll(context.Background(), "", "", 10, SearchOptions{PopularityWait: wait})
	elapsed := time.Since(start)

	if len(unavailable) != 0 {
		t.Fatalf("expected unavailable[] untouched by a slow popularity fetch, got %+v", unavailable)
	}

	slack := 400 * time.Millisecond // generous: CI jitter, not the property under test
	if elapsed > wait+slack {
		t.Fatalf("expected SearchAll to return within wait(%s)+slack(%s), took %s", wait, slack, elapsed)
	}
	for _, h := range hits {
		if h.Popularity != nil && h.Popularity.Stars != nil {
			t.Fatalf("expected no stars yet on the first call (stub still blocked), got %+v", *h.Popularity.Stars)
		}
	}

	closeBlock() // let the slow stub answer

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, state := provider.Lookup("org/repo"); state == LookupFresh {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	hits2, _, _ := SearchAll(context.Background(), "", "", 10, SearchOptions{PopularityWait: wait})
	found := false
	for _, h := range hits2 {
		if h.Popularity != nil && h.Popularity.Stars != nil && *h.Popularity.Stars == 777 {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected the second SearchAll call to carry the resolved stars, got %+v", hits2)
	}
}
