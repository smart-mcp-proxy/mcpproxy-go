package registries

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// This file exposes a narrowly-scoped test seam so that packages OTHER than
// registries (notably internal/httpapi, for the FR-007 caller-scoping tests
// on GET /catalog/search) can drive SearchAll against a small, deterministic,
// hermetic set of sources instead of the real default registry list (which
// would otherwise make every catalog test a live network call).
//
// It is intentionally named *ForTest and does nothing a caller would want in
// production; keeping it in a non-_test.go file is the only way to make it
// reachable from a sibling package's test binary (Go's export_test.go trick
// is package-local). Do NOT call this outside tests.

// SetRegistriesForTest replaces the effective registry list wholesale (no
// merge with the shipped defaults, unlike SetRegistriesFromConfig) and
// returns a restore func that reinstalls the previous list.
func SetRegistriesForTest(regs []RegistryEntry) (restore func()) {
	prev := registryList
	registryList = regs
	return func() { registryList = prev }
}

// AllowPrivateRegistryFetchForTest relaxes the SSRF guard (internal/registries
// SetAllowPrivateRegistryFetch, MCP-1076) so a test's registry fixture can be
// an httptest.Server on loopback, and returns a restore func.
func AllowPrivateRegistryFetchForTest() (restore func()) {
	prevForce, prev := testForceAllowPrivate.Load(), registryAllowPrivateFetch.Load()
	testForceAllowPrivate.Store(true)
	registryAllowPrivateFetch.Store(true)
	return func() {
		testForceAllowPrivate.Store(prevForce)
		registryAllowPrivateFetch.Store(prev)
	}
}

// SetPopularityProviderForTest installs p as the process-wide popularity
// provider (Spec 110 FR-010) and returns a restore func reinstalling
// whatever was previously installed (typically nil).
func SetPopularityProviderForTest(p PopularityProvider) (restore func()) {
	prev := getPopularityProvider()
	SetPopularityProvider(p)
	return func() { SetPopularityProvider(prev) }
}

// SetGitHubAPIBaseForTest overrides the GitHub API base URL new
// githubStarsProvider instances read at construction (default
// githubAPIBaseURLDefault), so a test can point NewGitHubStarsProvider at an
// httptest.Server. Combine with AllowPrivateRegistryFetchForTest, since the
// SSRF guard otherwise blocks a loopback target. Returns a restore func.
func SetGitHubAPIBaseForTest(base string) (restore func()) {
	prev := currentGitHubAPIBase()
	b := base
	githubAPIBaseOverride.Store(&b)
	return func() {
		p := prev
		githubAPIBaseOverride.Store(&p)
	}
}

// ResetListingCacheForTest empties the per-source listing cache (Spec 109 D35)
// so a test neither inherits nor leaks cached listings.
func ResetListingCacheForTest() {
	listingCache.mu.Lock()
	defer listingCache.mu.Unlock()
	listingCache.m = make(map[string]listingCacheEntry)
}

// SetCatalogWarmBehindForTest overrides the warm-behind background timeout and
// the per-source slot count (Spec 109 D36.11) and returns a restore func.
func SetCatalogWarmBehindForTest(timeout time.Duration, slots int) (restore func()) {
	warmBehind.mu.Lock()
	prevTimeout, prevSlots := warmBehind.timeout, warmBehind.slots
	warmBehind.timeout, warmBehind.slots = timeout, slots
	warmBehind.mu.Unlock()
	return func() {
		warmBehind.mu.Lock()
		warmBehind.timeout, warmBehind.slots = prevTimeout, prevSlots
		warmBehind.mu.Unlock()
	}
}

// RecordedRegistryHandlerForTest serves an official-protocol v0.1 registry
// (GET /v0.1/servers) from a recorded corpus of wrapped {server, _meta} items
// (Spec 109 D36.12). It reproduces the live registry's search semantics, which
// is what makes the catalog bug reproducible offline: `search` is a
// case-insensitive substring of server.name only, results are in byte order of
// the name, `version=latest` keeps only isLatest entries, `limit` defaults to
// 100, and `cursor` is the last name of the previous page (exclusive).
func RecordedRegistryHandlerForTest(corpus []json.RawMessage) http.Handler {
	type row struct {
		name     string
		isLatest bool
		raw      json.RawMessage
	}
	rows := make([]row, 0, len(corpus))
	for _, raw := range corpus {
		var item struct {
			Server struct {
				Name string `json:"name"`
			} `json:"server"`
			Meta map[string]struct {
				IsLatest *bool `json:"isLatest"`
			} `json:"_meta"`
		}
		if err := json.Unmarshal(raw, &item); err != nil {
			continue
		}
		latest := true
		if m, ok := item.Meta[officialMetaKey]; ok && m.IsLatest != nil {
			latest = *m.IsLatest
		}
		rows = append(rows, row{name: item.Server.Name, isLatest: latest, raw: raw})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].name < rows[j].name })

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		search := strings.ToLower(q.Get("search"))
		latestOnly := q.Get("version") == "latest"
		cursor := q.Get("cursor")
		limit := 100
		if n, err := strconv.Atoi(q.Get("limit")); err == nil && n > 0 {
			limit = n
		}

		page := []json.RawMessage{}
		lastName, more := "", false
		for _, rw := range rows {
			if latestOnly && !rw.isLatest {
				continue
			}
			if search != "" && !strings.Contains(strings.ToLower(rw.name), search) {
				continue
			}
			if cursor != "" && rw.name <= cursor {
				continue
			}
			if len(page) == limit {
				more = true // more remain: this page's last name is the cursor
				break
			}
			page = append(page, rw.raw)
			lastName = rw.name
		}
		meta := map[string]interface{}{"count": len(page)}
		if more {
			meta["nextCursor"] = lastName
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"servers": page, "metadata": meta})
	})
}
