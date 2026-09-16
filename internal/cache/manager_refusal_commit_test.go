package cache

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"go.etcd.io/bbolt"
	"go.uber.org/zap"
)

// Spec 105 FR-001 / Definitions "non-disclosing refusal" (critique round 1,
// finding 1): a scoped caller's refusal must match a miss in status, body AND
// timing class. Body parity was pinned by T025; the timing class was not. An
// absent key commits a stats write (one bbolt fsync, ~10 ms on a laptop) while
// a guard refusal returned an error from the Update closure — bbolt rolled
// the transaction back and wrote nothing (~5 µs). Three orders of magnitude,
// stable and repeatable, so one probe told a narrow token whether a live
// entry (a broader token's page, or a guessable internal registry/guesser
// key) sat behind the key.
//
// Timing itself is not asserted — that would flake in CI. The MECHANISM is:
// every refused path must take the same committing branch a miss takes,
// proven through bbolt's own write counter, and must count as a miss in the
// stats (the same signal an absent key leaves).
func TestGetRecordsAs_RefusalCommitsLikeAMiss(t *testing.T) {
	db, err := bbolt.Open(filepath.Join(t.TempDir(), "cache.db"), 0644, &bbolt.Options{Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	m, err := NewManager(db, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	broad := Authorization{CallerKind: CallerKindAgent, Principal: "broad",
		AllowedServers: []string{"github", "weather"}, Permissions: []string{"read"}}
	narrow := Authorization{CallerKind: CallerKindAgent, Principal: "narrow",
		AllowedServers: []string{"weather"}, Permissions: []string{"read"}}
	if err := m.StoreAs("broad-entry", "t", nil, `[1]`, "", 1, broad); err != nil {
		t.Fatal(err)
	}
	if err := m.StoreAs("admin-entry", "t", nil, `[1]`, "", 1, Authorization{CallerKind: CallerKindAdmin}); err != nil {
		t.Fatal(err)
	}
	if err := m.StoreAs("npm:@acme/mcp-server", "repo_guess", nil, `[1]`, "", 1, Authorization{CallerKind: CallerKindInternal}); err != nil {
		t.Fatal(err)
	}

	// writesFor returns how many bbolt page writes the read performed —
	// zero means the transaction was rolled back, never fsynced.
	writesFor := func(key string) int64 {
		t.Helper()
		before := db.Stats().TxStats.Write
		resp, err := m.GetRecordsAs(key, 0, 10, narrow)
		if err == nil || resp != nil {
			t.Fatalf("%s: expected a refusal, got resp=%+v err=%v", key, resp, err)
		}
		return db.Stats().TxStats.Write - before
	}

	missBefore := m.GetStats().MissCount
	absent := writesFor("no-such-key")
	if absent == 0 {
		t.Fatal("premise: a miss commits a stats write")
	}
	for _, key := range []string{"broad-entry", "admin-entry", "npm:@acme/mcp-server"} {
		if got := writesFor(key); got != absent {
			t.Errorf("%s: refusal performed %d bbolt writes, a miss performs %d — different timing class (a rolled-back refusal is a ~3000x timing oracle)", key, got, absent)
		}
	}
	if got, want := m.GetStats().MissCount, missBefore+4; got != want {
		t.Errorf("MissCount = %d, want %d: every refusal must count as a miss, exactly as an absent key does", got, want)
	}

	// The refused entries are untouched: no hit, no access bump, no eviction.
	for _, key := range []string{"broad-entry", "admin-entry", "npm:@acme/mcp-server"} {
		rec, ok := m.Peek(key)
		if !ok {
			t.Fatalf("%s: a refused read must not evict", key)
		}
		if rec.AccessCount != 0 {
			t.Errorf("%s: a refused read must not count as an access: %+v", key, rec)
		}
	}
	if m.GetStats().HitCount != 0 {
		t.Errorf("HitCount = %d, want 0", m.GetStats().HitCount)
	}
}

// Critique round 1, finding 3: the doc comment on getGuarded promised that
// m.stats is mutated only on committing paths, but the expiry and legacy
// branches decremented the counters BEFORE saveStats ran. When the stats
// write fails bbolt rolls the delete back while the in-memory counters keep
// the decrement — the on-disk == in-memory invariant T023 pins held only on
// the happy path. A failed write must leave the counters agreeing with the
// bucket.
func TestGetRecordsAs_FailedCommitRestoresStats(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.db")
	m, db := openManagerAt(t, path)
	defer db.Close()
	defer m.Close()

	if err := m.Store("legacy", "t", nil, `[1]`, "", 1); err != nil {
		t.Fatal(err)
	}
	before := *m.GetStats()
	if before.TotalEntries != 1 {
		t.Fatalf("premise: stats count the stored entry: %+v", before)
	}

	// Make the stats write fail: drop the stats bucket. The read must then
	// surface a storage error (not a verdict) and change nothing.
	if err := db.Update(func(tx *bbolt.Tx) error {
		return tx.DeleteBucket([]byte(CacheStatsBucket))
	}); err != nil {
		t.Fatal(err)
	}

	var readErr error
	func() {
		defer func() {
			if r := recover(); r != nil {
				readErr = errors.New("getGuarded panicked on a missing stats bucket")
			}
		}()
		_, readErr = m.GetRecordsAs("legacy", 0, 10, Authorization{CallerKind: CallerKindAdmin})
	}()
	if readErr == nil || errors.Is(readErr, ErrUnauthorizedRead) || errors.Is(readErr, ErrKeyNotFound) {
		t.Fatalf("a failed stats write must surface as a storage error, got %v", readErr)
	}

	onDisk := onDiskEntryCount(t, db)
	if onDisk != 1 {
		t.Fatalf("the rolled-back transaction must leave the entry on disk, count=%d", onDisk)
	}
	after := *m.GetStats()
	if after != before {
		t.Fatalf("in-memory stats mutated by a rolled-back transaction: before=%+v after=%+v (bucket holds %d)", before, after, onDisk)
	}
}
