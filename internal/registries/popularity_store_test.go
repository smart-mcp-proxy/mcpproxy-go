package registries

import (
	"path/filepath"
	"testing"
	"time"

	"go.etcd.io/bbolt"
)

func openTempPopularityDB(t *testing.T) *bbolt.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "popularity.db")
	db, err := bbolt.Open(path, 0o600, &bbolt.Options{Timeout: time.Second})
	if err != nil {
		t.Fatalf("bbolt.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// TestPopularityStore_RoundTrip pins the basic put/get round trip (T007).
func TestPopularityStore_RoundTrip(t *testing.T) {
	db := openTempPopularityDB(t)
	store, err := newPopularityStore(db)
	if err != nil {
		t.Fatalf("newPopularityStore: %v", err)
	}

	entry := &starsEntry{Stars: 42, ETag: `"abc"`, FetchedAt: time.Now().UTC().Truncate(time.Second), Status: 200}
	if err := store.put("o/r", entry); err != nil {
		t.Fatalf("put: %v", err)
	}

	got, ok := store.get("o/r")
	if !ok {
		t.Fatal("expected the entry to round-trip")
	}
	if got.Stars != entry.Stars || got.ETag != entry.ETag || got.Status != entry.Status || !got.FetchedAt.Equal(entry.FetchedAt) {
		t.Fatalf("round-tripped entry mismatch: got %+v, want %+v", got, entry)
	}

	if _, ok := store.get("missing/key"); ok {
		t.Fatal("expected a missing key to report ok=false")
	}
}

// TestPopularityStore_Delete pins that delete removes a persisted entry.
func TestPopularityStore_Delete(t *testing.T) {
	db := openTempPopularityDB(t)
	store, err := newPopularityStore(db)
	if err != nil {
		t.Fatalf("newPopularityStore: %v", err)
	}
	_ = store.put("o/r", &starsEntry{Stars: 1, FetchedAt: time.Now()})
	if err := store.delete("o/r"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok := store.get("o/r"); ok {
		t.Fatal("expected the entry to be gone after delete")
	}
}

// TestGitHubStarsProvider_LazyLoadFromStore pins that a provider restart
// (fresh githubStarsProvider over the SAME bbolt db) sees a previously
// fetched entry via Lookup with NO fetch — the lazy-load-on-miss path
// (plan.md data model) survives a restart.
func TestGitHubStarsProvider_LazyLoadFromStore(t *testing.T) {
	db := openTempPopularityDB(t)
	store, err := newPopularityStore(db)
	if err != nil {
		t.Fatalf("newPopularityStore: %v", err)
	}
	fetchedAt := time.Now()
	if err := store.put("o/r", &starsEntry{Stars: 77, Status: 200, FetchedAt: fetchedAt}); err != nil {
		t.Fatalf("seed put: %v", err)
	}

	t.Setenv("MCPPROXY_CATALOG_POPULARITY", "false") // no workers; this test is Lookup-only
	provider := NewGitHubStarsProvider(PopularityOptions{DB: db})
	defer provider.Close()

	stars, state := provider.Lookup("o/r")
	if state != LookupFresh {
		t.Fatalf("expected LookupFresh from the pre-seeded store, got state=%d stars=%d", state, stars)
	}
	if stars != 77 {
		t.Fatalf("expected stars=77 from the pre-seeded store, got %d", stars)
	}
}

// TestGitHubStarsProvider_CapEviction pins FR-008's 5000-key cap: inserting
// one more than the cap evicts the single oldest-FetchedAt entry (from both
// memory and the store).
func TestGitHubStarsProvider_CapEviction(t *testing.T) {
	db := openTempPopularityDB(t)
	t.Setenv("MCPPROXY_CATALOG_POPULARITY", "false")
	provider := NewGitHubStarsProvider(PopularityOptions{DB: db})
	defer provider.Close()

	base := time.Now().Add(-time.Hour)
	provider.mu.Lock()
	for i := 0; i < githubMaxCacheKeys; i++ {
		key := keyForIndex(i)
		provider.entries[key] = &starsEntry{Stars: 1, Status: 200, FetchedAt: base.Add(time.Duration(i) * time.Second)}
	}
	provider.mu.Unlock()

	// The oldest key (index 0) should be evicted once we go one over cap.
	provider.applyResult("new/key", nil, 200, 5, "", rateLimitHeaders{}, nil)

	provider.mu.Lock()
	count := len(provider.entries)
	_, oldestStillPresent := provider.entries[keyForIndex(0)]
	provider.mu.Unlock()

	if count != githubMaxCacheKeys {
		t.Fatalf("expected the entry count to stay capped at %d, got %d", githubMaxCacheKeys, count)
	}
	if oldestStillPresent {
		t.Fatal("expected the oldest entry to have been evicted")
	}
}

func keyForIndex(i int) string {
	return "owner/repo-" + string(rune('a'+(i%26))) + string(rune('a'+((i/26)%26))) + string(rune('a'+((i/676)%26)))
}
