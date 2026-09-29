package registries

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
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

// TestGitHubStarsProvider_LoadsPersistedEntriesOnStartup pins that a provider
// restart (fresh githubStarsProvider over the SAME bbolt db) eagerly preloads
// every persisted entry, so Lookup serves a previously fetched value with NO
// fetch and never reads bbolt afterwards.
func TestGitHubStarsProvider_LoadsPersistedEntriesOnStartup(t *testing.T) {
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

	// The preload is a one-time snapshot: an entry written to the store after
	// construction is not visible (memory is authoritative).
	if err := store.put("o/late", &starsEntry{Stars: 5, Status: 200, FetchedAt: fetchedAt}); err != nil {
		t.Fatalf("late put: %v", err)
	}
	if _, state := provider.Lookup("o/late"); state != LookupAbsent {
		t.Fatalf("expected a post-construction store write to stay invisible, got state=%d", state)
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

// TestGitHubStarsProvider_CapHoldsAcrossRestart pins that the FR-008 key cap
// bounds the bbolt bucket, not just the in-memory map: a bucket already over
// the cap (written by an earlier process) is trimmed to the cap, oldest
// FetchedAt first, when the next provider opens it. With lazy loading the
// in-memory count restarted at zero and the bucket grew without bound.
func TestGitHubStarsProvider_CapHoldsAcrossRestart(t *testing.T) {
	db := openTempPopularityDB(t)
	if _, err := newPopularityStore(db); err != nil {
		t.Fatalf("newPopularityStore: %v", err)
	}
	const over = 3
	base := time.Now().Add(-time.Hour)
	if err := db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(popularityBucketName))
		for i := 0; i < githubMaxCacheKeys+over; i++ {
			v, err := json.Marshal(&starsEntry{Stars: 1, Status: 200, FetchedAt: base.Add(time.Duration(i) * time.Second)})
			if err != nil {
				return err
			}
			if err := b.Put([]byte(keyForIndex(i)), v); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	t.Setenv("MCPPROXY_CATALOG_POPULARITY", "false")
	provider := NewGitHubStarsProvider(PopularityOptions{DB: db})
	defer provider.Close()

	if got := len(provider.store.all()); got != githubMaxCacheKeys {
		t.Fatalf("bucket holds %d keys after reopen, want the cap %d", got, githubMaxCacheKeys)
	}
	for i := 0; i < over; i++ {
		if _, state := provider.Lookup(keyForIndex(i)); state != LookupAbsent {
			t.Errorf("oldest key %d should have been evicted, got state %d", i, state)
		}
	}
	if _, state := provider.Lookup(keyForIndex(githubMaxCacheKeys + over - 1)); state != LookupFresh {
		t.Errorf("newest key should survive, got state %d", state)
	}
}

// holdStoreWriter blocks every other bbolt writer (store.put/delete) until
// the returned release func is called.
func holdStoreWriter(t *testing.T, db *bbolt.DB) (release func()) {
	t.Helper()
	held := make(chan struct{})
	done := make(chan struct{})
	rel := make(chan struct{})
	go func() {
		defer close(done)
		_ = db.Update(func(_ *bbolt.Tx) error {
			close(held)
			<-rel
			return nil
		})
	}()
	<-held
	var once sync.Once
	release = func() { once.Do(func() { close(rel); <-done }) }
	t.Cleanup(release)
	return release
}

// TestGitHubStarsProvider_EvictionDoesNotBlockLookupOnStore pins that cap
// eviction never holds the provider mutex across the bbolt delete: with the
// store's writer lock held (so the delete cannot finish), Lookup must still
// return promptly and observe the eviction in memory.
func TestGitHubStarsProvider_EvictionDoesNotBlockLookupOnStore(t *testing.T) {
	db := openTempPopularityDB(t)
	t.Setenv("MCPPROXY_CATALOG_POPULARITY", "false")
	provider := NewGitHubStarsProvider(PopularityOptions{DB: db})
	defer provider.Close()

	base := time.Now().Add(-time.Hour)
	provider.mu.Lock()
	for i := 0; i < githubMaxCacheKeys; i++ {
		provider.entries[keyForIndex(i)] = &starsEntry{Stars: 1, Status: 200, FetchedAt: base.Add(time.Duration(i) * time.Second)}
	}
	provider.mu.Unlock()
	// Mirror the to-be-evicted entry into the store so the delete has work.
	if err := provider.store.put(keyForIndex(0), provider.entries[keyForIndex(0)]); err != nil {
		t.Fatalf("seed put: %v", err)
	}

	release := holdStoreWriter(t, db)
	applied := make(chan struct{})
	go func() {
		defer close(applied)
		provider.applyResult("new/key", nil, 200, 5, "", rateLimitHeaders{}, nil)
	}()

	oldest := keyForIndex(0)
	deadline := time.After(3 * time.Second)
	observed := make(chan struct{})
	go func() {
		defer close(observed)
		for {
			if _, state := provider.Lookup(oldest); state == LookupAbsent {
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()
	select {
	case <-observed:
	case <-deadline:
		t.Fatal("Lookup blocked (or eviction never became visible) while the store delete was pending")
	}
	select {
	case <-applied:
		t.Fatal("applyResult finished although the store writer lock was held")
	default:
	}

	release()
	<-applied
	if _, ok := provider.store.get(oldest); ok {
		t.Fatal("expected the evicted entry to be deleted from the store once it unblocked")
	}
	if _, ok := provider.store.get("new/key"); !ok {
		t.Fatal("expected the new entry to be persisted")
	}
	provider.mu.Lock()
	count := len(provider.entries)
	provider.mu.Unlock()
	if count != githubMaxCacheKeys {
		t.Fatalf("entry count = %d, want %d", count, githubMaxCacheKeys)
	}
}

// TestGitHubStarsProvider_PersistGuardsAgainstEvictionRaces pins the ordering
// guards: a delayed put never resurrects an evicted key, and a delayed delete
// never drops a key that a newer fetch re-added.
func TestGitHubStarsProvider_PersistGuardsAgainstEvictionRaces(t *testing.T) {
	db := openTempPopularityDB(t)
	t.Setenv("MCPPROXY_CATALOG_POPULARITY", "false")
	provider := NewGitHubStarsProvider(PopularityOptions{DB: db})
	defer provider.Close()

	// Stale put: the entry was evicted from memory before its put ran.
	stale := &starsEntry{Stars: 1, Status: 200, FetchedAt: time.Now()}
	provider.persist("gone/key", stale, "")
	if _, ok := provider.store.get("gone/key"); ok {
		t.Fatal("a put for an entry no longer in memory must be skipped")
	}

	// Stale delete: the key was re-added before the delete ran.
	fresh := &starsEntry{Stars: 2, Status: 200, FetchedAt: time.Now()}
	provider.mu.Lock()
	provider.entries["back/key"] = fresh
	provider.mu.Unlock()
	provider.persist("back/key", fresh, "")
	provider.persist("", nil, "back/key")
	if _, ok := provider.store.get("back/key"); !ok {
		t.Fatal("a delete for a key that is back in memory must be skipped")
	}
}

// TestGitHubStarsProvider_ConcurrentApplyAndLookupUnderCap is a -race
// regression test: concurrent applyResult (evicting past the cap) and Lookup
// keep memory at the cap and leave the store consistent with memory.
func TestGitHubStarsProvider_ConcurrentApplyAndLookupUnderCap(t *testing.T) {
	db := openTempPopularityDB(t)
	t.Setenv("MCPPROXY_CATALOG_POPULARITY", "false")
	provider := NewGitHubStarsProvider(PopularityOptions{DB: db})
	defer provider.Close()

	base := time.Now().Add(-time.Hour)
	provider.mu.Lock()
	for i := 0; i < githubMaxCacheKeys; i++ {
		provider.entries[keyForIndex(i)] = &starsEntry{Stars: 1, Status: 200, FetchedAt: base.Add(time.Duration(i) * time.Second)}
	}
	provider.mu.Unlock()

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					provider.Lookup(keyForIndex(0))
				}
			}
		}()
	}
	var writers sync.WaitGroup
	for g := 0; g < 4; g++ {
		writers.Add(1)
		go func(g int) {
			defer writers.Done()
			for i := 0; i < 10; i++ {
				provider.applyResult(fmt.Sprintf("w%d/repo%d", g, i), nil, 200, 5, "", rateLimitHeaders{}, nil)
			}
		}(g)
	}
	writers.Wait()
	close(stop)
	wg.Wait()

	provider.mu.Lock()
	count := len(provider.entries)
	mem := make(map[string]struct{}, count)
	for k := range provider.entries {
		mem[k] = struct{}{}
	}
	provider.mu.Unlock()
	if count != githubMaxCacheKeys {
		t.Fatalf("entry count = %d, want %d", count, githubMaxCacheKeys)
	}
	// Only the 40 written keys were persisted (the seed was memory-only), so
	// every persisted key must still be in memory: no resurrected evictions.
	for k := range provider.store.all() {
		if _, ok := mem[k]; !ok {
			t.Errorf("store holds %q which is no longer in memory", k)
		}
	}
}
