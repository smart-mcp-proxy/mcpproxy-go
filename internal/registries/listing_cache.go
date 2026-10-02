package registries

import (
	"strings"
	"sync"
	"time"
)

// listing_cache.go: the per-source listing cache behind the catalog's
// "answer from what we last saw" fallback (Spec 109 D35, FR-060).
//
// A registry's server-side search can be far slower than its plain listing
// (the official registry's ?search= took 17s while page 1 of the listing came
// back at once), so a search that times out used to contribute nothing.
// SearchAll remembers every listing a source returned and, when a later live
// fetch fails, filters that memory instead. The source still appears in
// unavailable[] (with fallback and cached_at), so the surfaces stay honest.
//
// The cache is in memory and process-wide. It is not persisted: the storage
// layer is deliberately not involved, and a daemon-lifetime cache is enough.
// The CLI's in-process path (no daemon) therefore starts cold.

const (
	// FallbackCachedListing is the SourceError.Fallback value, and the
	// `fallback` field of GET /catalog/search's unavailable[] on every surface.
	FallbackCachedListing = "cached_listing"

	// listingCacheMaxEntries caps one source's cached listing.
	listingCacheMaxEntries = 2000

	// listingCacheMaxAge is how old a listing may be and still be served. It is
	// the age of the LAST successful fetch from that source.
	listingCacheMaxAge = 24 * time.Hour
)

// listingNow is the cache's clock; tests move it.
var listingNow = time.Now

type listingCacheEntry struct {
	entries   []ServerEntry
	updatedAt time.Time
}

var listingCache = struct {
	mu sync.RWMutex
	m  map[string]listingCacheEntry
}{m: make(map[string]listingCacheEntry)}

// listingKey identifies a source's listing by id AND servers URL, so editing a
// source's URL never serves the listing of the old endpoint.
func listingKey(reg *RegistryEntry) string {
	return reg.ID + "\x00" + reg.ServersURL
}

func listingEntryKey(e *ServerEntry) string {
	if e.ID != "" {
		return e.ID
	}
	return "name:" + e.Name
}

// cacheListing merges the entries a successful fetch returned into the
// source's cached listing. The newest fetch goes first (latest wins on an id
// collision), then the older entries it did not repeat, capped at
// listingCacheMaxEntries. An empty result is a no-op: a query that matched
// nothing must not wipe what the source returned before.
func cacheListing(reg *RegistryEntry, entries []ServerEntry) {
	if reg == nil || len(entries) == 0 {
		return
	}
	key := listingKey(reg)

	listingCache.mu.Lock()
	defer listingCache.mu.Unlock()

	merged := make([]ServerEntry, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for i := range entries {
		k := listingEntryKey(&entries[i])
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		merged = append(merged, entries[i])
	}
	for i := range listingCache.m[key].entries {
		if len(merged) >= listingCacheMaxEntries {
			break
		}
		old := &listingCache.m[key].entries[i]
		if _, dup := seen[listingEntryKey(old)]; dup {
			continue
		}
		merged = append(merged, *old)
	}
	if len(merged) > listingCacheMaxEntries {
		merged = merged[:listingCacheMaxEntries]
	}
	listingCache.m[key] = listingCacheEntry{entries: merged, updatedAt: listingNow()}
}

// cachedListing returns a copy of the source's cached listing and when it was
// last updated, if one exists and is at most listingCacheMaxAge old.
func cachedListing(reg *RegistryEntry) ([]ServerEntry, time.Time, bool) {
	if reg == nil {
		return nil, time.Time{}, false
	}
	listingCache.mu.RLock()
	cached, ok := listingCache.m[listingKey(reg)]
	listingCache.mu.RUnlock()
	if !ok || len(cached.entries) == 0 || listingNow().Sub(cached.updatedAt) > listingCacheMaxAge {
		return nil, time.Time{}, false
	}
	out := make([]ServerEntry, len(cached.entries))
	copy(out, cached.entries)
	return out, cached.updatedAt, true
}

// pruneListingCache drops every cached listing whose (id, servers URL) is not
// in regs. SetRegistriesFromConfig calls it after each config load, so a
// removed or re-pointed source does not keep its listing alive.
func pruneListingCache(regs []RegistryEntry) {
	keep := make(map[string]struct{}, len(regs))
	for i := range regs {
		keep[listingKey(&regs[i])] = struct{}{}
	}
	listingCache.mu.Lock()
	defer listingCache.mu.Unlock()
	for key := range listingCache.m {
		if _, ok := keep[key]; !ok {
			delete(listingCache.m, key)
		}
	}
}

// matchCachedEntry is the fallback filter: a case-insensitive substring of the
// trimmed query in the entry's name, description OR id. The live path's
// filterServers skips the id, but the official registry's own search matches
// names, so including the id is what lets "github" find "io.github.*". An empty
// query matches everything (browse).
func matchCachedEntry(e *ServerEntry, q string) bool {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return true
	}
	return strings.Contains(strings.ToLower(e.Name), q) ||
		strings.Contains(strings.ToLower(e.Description), q) ||
		strings.Contains(strings.ToLower(e.ID), q)
}
