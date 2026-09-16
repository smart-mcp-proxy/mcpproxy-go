package cache

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"go.etcd.io/bbolt"
)

// Codex round 3. The gated read decides admission on the frame HEADER and
// then decodes the BODY — two encodings of the same record that a binary this
// repository ships never lets disagree (MarshalBinary derives one from the
// other). A value that does disagree was not written by such a binary: it is
// corrupt or crafted, and must be treated like any other undecodable frame —
// refused for every caller, invalidated, and never a source of content. In
// particular the header must not admit a reader to a body stamped under a
// BROADER authorization (finding 1: an `a`-only header in front of an
// administrator body handed the body to an `a`-only agent), and a
// future-expiry header must not admit a reader to an expired body (the
// admitted decode then evicted the entry on the gated door, work the header
// verdict never does).
func TestGetRecordsAs_FrameHeaderBodyDisagreementIsLegacy(t *testing.T) {
	now := time.Now().Round(0)
	aOnly := &Authorization{CallerKind: CallerKindAgent, Principal: "narrow", AllowedServers: []string{"a"}, Permissions: []string{"read"}}
	broad := &Authorization{CallerKind: CallerKindAgent, Principal: "broad", AllowedServers: []string{"a", "b"}, Permissions: []string{"read", "write"}}
	admin := &Authorization{CallerKind: CallerKindAdmin}
	const content = `[{"name":"SENTINEL-DISAGREE"}]`

	record := func(mutate func(r *Record)) *Record {
		r := &Record{Key: "k", ToolName: "t", FullContent: content, TotalSize: len(content),
			Timestamp: now, ExpiresAt: now.Add(time.Hour), CreatedAt: now, LastAccessed: now,
			Version: RecordVersion, Producer: aOnly}
		if mutate != nil {
			mutate(r)
		}
		return r
	}
	encode := func(t *testing.T, v interface{}) []byte {
		t.Helper()
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	const seedContent = `[{"name":"SEED"}]`

	fixtures := []struct {
		name   string
		header recordHeader
		body   *Record
		reader Authorization
	}{
		{name: "a-only header, administrator body, a-only reader",
			header: record(nil).header(), body: record(func(r *Record) { r.Producer = admin }), reader: *aOnly},
		{name: "a-only header, broader agent body, a-only reader",
			header: record(nil).header(), body: record(func(r *Record) { r.Producer = broad }), reader: *aOnly},
		{name: "a-only header, legacy (unstamped) body, a-only reader",
			header: record(nil).header(), body: record(func(r *Record) { r.Producer = nil; r.Version = 0 }), reader: *aOnly},
		{name: "future-expiry header, expired body, administrator reader",
			header: record(nil).header(), body: record(func(r *Record) { r.ExpiresAt = now.Add(-time.Hour) }), reader: *admin},
		{name: "header size differs from body size, administrator reader",
			header: record(nil).header(), body: record(func(r *Record) { r.TotalSize = len(content) + 1 }), reader: *admin},
		{name: "current-version header, unversioned body, administrator reader",
			header: record(nil).header(), body: record(func(r *Record) { r.Version = 0 }), reader: *admin},
	}
	for _, fx := range fixtures {
		t.Run(fx.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "cache.db")
			m, db := openManagerAt(t, path)
			const key = "disagree"
			// A genuine a-only entry beside the crafted one: it persists the
			// a-only snapshot the crafted header references, so what refuses
			// the crafted value is the header/body disagreement — not a
			// snapshot the bucket lacks — and it is the live neighbour whose
			// accounting the invalidation must leave exact.
			if err := m.StoreAs("seed", "t", nil, seedContent, "", 1, *aOnly); err != nil {
				t.Fatal(err)
			}
			putFramedRecord(t, db, key, fx.header.encode(), encode(t, fx.body))
			// Fold the entry into the stats the way a Store would have, so
			// the invalidation's accounting is observable.
			if err := m.update(func(tx *bbolt.Tx) error {
				m.stats.TotalEntries++
				m.stats.TotalSizeBytes += fx.header.TotalSize
				return m.saveStats(tx)
			}); err != nil {
				t.Fatal(err)
			}

			resp, err := m.GetRecordsAs(key, 0, 10, fx.reader)
			if !errors.Is(err, ErrLegacyProvenance) {
				t.Fatalf("got err=%v resp=%v, want ErrLegacyProvenance", err, resp)
			}
			if resp != nil {
				t.Fatalf("disagreeing frame returned content: %+v", resp)
			}
			if _, ok := m.Peek(key); ok {
				t.Fatal("disagreeing frame still present after the refused redemption")
			}
			if got := m.GetStats(); got.TotalEntries != 1 || got.TotalSizeBytes != len(seedContent) || got.EvictedCount != 1 {
				t.Fatalf("stats after invalidation = %+v, want the crafted entry and its header size folded out and the seed (%d bytes) untouched", *got, len(seedContent))
			}
			m.Close()
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			m2, db2 := openManagerAt(t, path)
			defer db2.Close()
			defer m2.Close()
			if got := onDiskEntryCount(t, db2); got != 1 {
				t.Fatalf("on-disk count after restart = %d, want 1 (the seed)", got)
			}
		})
	}

	// Control: a frame MarshalBinary wrote round-trips through every door.
	t.Run("control: self-written frame agrees", func(t *testing.T) {
		m, db := openManagerAt(t, filepath.Join(t.TempDir(), "cache.db"))
		defer db.Close()
		defer m.Close()
		if err := m.StoreAs("k", "t", nil, content, "", 1, *aOnly); err != nil {
			t.Fatal(err)
		}
		if _, err := m.GetRecordsAs("k", 0, 10, *aOnly); err != nil {
			t.Fatalf("same-authorization redemption: %v", err)
		}
	})
}

// Codex round 4, finding 2 (cache) / finding 1 (server): the round-3 frame
// carried the whole producer snapshot in a JSON header bounded at 1 MiB, so
// (a) a legitimate snapshot over the bound — configuration bounds neither
// the server count nor the name length — could not be cached at all, and
// (b) every live-key refusal JSON-decoded the whole header while a
// nonexistent key decoded nothing: a fleet-sized timing oracle. The frame
// header is now FIXED-SIZE (version, kind, deny-all bit, tier bits, expiry,
// size, producer DIGEST) and the snapshot is kept once, under its digest, in
// the snapshots bucket for diagnostics. This pins (a): a snapshot naming
// 5,000 servers in both its grant and its profile round-trips, is redeemed
// by its producer (FR-001: same authorization, same entry) — through the
// storing manager and after a restart, where nothing but the header on disk
// can decide (research D16) — by a reader presenting the same authorization
// with its lists in another order (the digest is canonical) and by an
// unrestricted agent covering its tier (the header's bits), is stored once
// across entries, and is refused to a narrower agent on a plain scope
// refusal. The timing half is TestGetRecordsAs_RefusalIsSnapshotSizeIndependent.
func TestStoreAs_LargeSnapshotRoundTrips(t *testing.T) {
	const content = `[{"name":"SENTINEL-LARGE"}]`
	producer := fleetSnapshot(5000, 112)
	if n := len(snapshotBytes(producer)); n <= 1<<20 {
		t.Fatalf("fixture snapshot is %d bytes; want over the former 1 MiB header bound, which refused it at write time", n)
	}
	narrow := Authorization{CallerKind: CallerKindAgent, Principal: "narrow", AllowedServers: []string{"srv-000001"}, Permissions: []string{"read"}}
	reordered := producer
	reordered.AllowedServers = append([]string(nil), producer.AllowedServers...)
	reordered.ProfileServers = append([]string(nil), producer.ProfileServers...)
	slices.Reverse(reordered.AllowedServers)
	slices.Reverse(reordered.ProfileServers)
	reordered.Profile = "fleet-renamed" // the name is not part of the digest
	// Unrestricted (wildcard grant, no pin, no profile) with the producer's
	// one tier: admitted on the header's tier bits, without the snapshot.
	star := Authorization{CallerKind: CallerKindAgent, Principal: "star", AllowedServers: []string{"*"}, Permissions: []string{"read"}}

	path := filepath.Join(t.TempDir(), "cache.db")
	m, db := openManagerAt(t, path)
	for _, key := range []string{"large-1", "large-2"} {
		if err := m.StoreAs(key, "t", nil, content, "", 1, producer); err != nil {
			t.Fatalf("store under a %d-byte snapshot: %v", len(snapshotBytes(producer)), err)
		}
	}
	if got := onDiskSnapshotCount(t, db); got != 1 {
		t.Fatalf("snapshots on disk = %d, want 1: two entries under one authorization must share one snapshot", got)
	}
	redeem := func(t *testing.T, m *Manager, key string, reader Authorization) {
		t.Helper()
		resp, err := m.GetRecordsAs(key, 0, 10, reader)
		if err != nil {
			t.Fatalf("producer's own redemption refused: %v", err)
		}
		if len(resp.Records) != 1 {
			t.Fatalf("records = %+v, want the one sentinel", resp.Records)
		}
		if resp.Producer == nil || len(resp.Producer.AllowedServers) != len(producer.AllowedServers) {
			t.Fatalf("the admitted page must carry the full producer snapshot for child stamping; got %v", resp.Producer != nil)
		}
		if _, ok := m.Peek(key); !ok {
			t.Fatal("entry deleted by its producer's redemption")
		}
	}
	redeem(t, m, "large-1", producer)
	redeem(t, m, "large-1", reordered)
	redeem(t, m, "large-1", star)
	if _, err := m.GetRecordsAs("large-1", 0, 10, narrow); !errors.Is(err, ErrUnauthorizedRead) {
		t.Fatalf("narrow agent: err = %v, want ErrUnauthorizedRead", err)
	}
	if _, ok := m.Peek("large-1"); !ok {
		t.Fatal("a scope refusal must not evict")
	}
	m.Close()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	// After a restart nothing is in memory: the header on disk decides.
	m2, db2 := openManagerAt(t, path)
	defer db2.Close()
	defer m2.Close()
	redeem(t, m2, "large-2", producer)
	redeem(t, m2, "large-2", reordered)
	redeem(t, m2, "large-2", star)
	if _, err := m2.GetRecordsAs("large-2", 0, 10, narrow); !errors.Is(err, ErrUnauthorizedRead) {
		t.Fatalf("narrow agent after restart: err = %v, want ErrUnauthorizedRead", err)
	}
}

// Research D16: the snapshots bucket is administrator diagnostics, not an
// input to the verdict. Deleting a producer's snapshot from it changes
// nothing on the gated door: the producer still redeems (digest-equal on the
// header), an unrestricted agent still redeems, a narrower agent is still
// refused without eviction, and the next store re-persists the snapshot.
func TestGetRecordsAs_SnapshotBucketIsDiagnosticsOnly(t *testing.T) {
	m, db := openManagerAt(t, filepath.Join(t.TempDir(), "cache.db"))
	defer db.Close()
	defer m.Close()
	producer := Authorization{CallerKind: CallerKindAgent, Principal: "p", AllowedServers: []string{"a", "b"}, Permissions: []string{"read"}}
	narrow := Authorization{CallerKind: CallerKindAgent, Principal: "n", AllowedServers: []string{"a"}, Permissions: []string{"read"}}
	star := Authorization{CallerKind: CallerKindAgent, Principal: "star", AllowedServers: []string{"*"}, Permissions: []string{"read"}}
	if err := m.StoreAs("k", "t", nil, `[1]`, "", 1, producer); err != nil {
		t.Fatal(err)
	}
	if got := onDiskSnapshotCount(t, db); got != 1 {
		t.Fatalf("premise: snapshots = %d, want 1", got)
	}
	digest := producer.digest()
	if err := db.Update(func(tx *bbolt.Tx) error {
		return tx.Bucket([]byte(CacheSnapshotBucket)).Delete(digest[:])
	}); err != nil {
		t.Fatal(err)
	}
	if got := onDiskSnapshotCount(t, db); got != 0 {
		t.Fatalf("premise: snapshots = %d, want 0", got)
	}
	for name, reader := range map[string]Authorization{"producer": producer, "unrestricted agent": star, "administrator": {CallerKind: CallerKindAdmin}} {
		if resp, err := m.GetRecordsAs("k", 0, 10, reader); err != nil || len(resp.Records) != 1 {
			t.Fatalf("%s: resp=%v err=%v, want the entry served without its diagnostic snapshot", name, resp, err)
		}
	}
	if _, err := m.GetRecordsAs("k", 0, 10, narrow); !errors.Is(err, ErrUnauthorizedRead) {
		t.Fatalf("narrow: err = %v, want ErrUnauthorizedRead", err)
	}
	if _, ok := m.Peek("k"); !ok {
		t.Fatal("the entry must survive")
	}
	if err := m.StoreAs("k2", "t", nil, `[1]`, "", 1, producer); err != nil {
		t.Fatal(err)
	}
	if got := onDiskSnapshotCount(t, db); got != 1 {
		t.Fatalf("snapshots after re-store = %d, want 1", got)
	}
}

// fleetSnapshot returns an agent authorization naming n servers of nameLen
// characters in both its grant and its effective profile, the way a real
// snapshot on a large fleet does.
func fleetSnapshot(n, nameLen int) Authorization {
	a := Authorization{CallerKind: CallerKindAgent, Principal: "wide", Permissions: []string{"read"},
		ProfilePin: "fleet", Profile: "fleet", ProfileScoped: true}
	pad := strings.Repeat("x", nameLen-len("srv-000000-"))
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("srv-%06d-%s", i, pad)
		a.AllowedServers = append(a.AllowedServers, name)
		a.ProfileServers = append(a.ProfileServers, name)
	}
	return a
}

// onDiskSnapshotCount counts the snapshots bucket directly.
func onDiskSnapshotCount(t *testing.T, db *bbolt.DB) int {
	t.Helper()
	n := 0
	if err := db.View(func(tx *bbolt.Tx) error {
		return tx.Bucket([]byte(CacheSnapshotBucket)).ForEach(func(_, _ []byte) error {
			n++
			return nil
		})
	}); err != nil {
		t.Fatal(err)
	}
	return n
}

// The snapshots bucket is content-addressed and shared, so the cleanup sweep
// must drop a snapshot only once NO surviving entry references it, and keep
// one that a live entry still does.
func TestCleanup_PrunesUnreferencedSnapshots(t *testing.T) {
	m, db := openManagerAt(t, filepath.Join(t.TempDir(), "cache.db"))
	defer db.Close()
	defer m.Close()
	a := Authorization{CallerKind: CallerKindAgent, Principal: "a", AllowedServers: []string{"a"}, Permissions: []string{"read"}}
	b := Authorization{CallerKind: CallerKindAgent, Principal: "b", AllowedServers: []string{"b"}, Permissions: []string{"read"}}
	for key, p := range map[string]Authorization{"a-live": a, "a-expiring": a, "b-expiring": b} {
		if err := m.StoreAs(key, "t", nil, `[1]`, "", 1, p); err != nil {
			t.Fatal(err)
		}
	}
	if got := onDiskSnapshotCount(t, db); got != 2 {
		t.Fatalf("snapshots = %d, want 2", got)
	}
	expireEntry(t, db, "a-expiring")
	expireEntry(t, db, "b-expiring")
	if err := m.cleanup(); err != nil {
		t.Fatal(err)
	}
	if got := onDiskEntryCount(t, db); got != 1 {
		t.Fatalf("entries after cleanup = %d, want 1", got)
	}
	if got := onDiskSnapshotCount(t, db); got != 1 {
		t.Fatalf("snapshots after cleanup = %d, want 1: a's is still referenced, b's is not", got)
	}
	if _, err := m.GetRecordsAs("a-live", 0, 10, a); err != nil {
		t.Fatalf("the surviving entry must still redeem after the prune: %v", err)
	}
	// A later entry under b re-persists the pruned snapshot.
	if err := m.StoreAs("b-again", "t", nil, `[1]`, "", 1, b); err != nil {
		t.Fatal(err)
	}
	if got := onDiskSnapshotCount(t, db); got != 2 {
		t.Fatalf("snapshots after re-store = %d, want 2", got)
	}
}

// Codex rounds 3, 4 and 5 on the pre-frame (bare JSON) record's size. Round 3
// folded the value's length out (an over-count of the escaped body, clamped
// against unrelated entries); round 4 decoded the value to fold out exactly
// len(FullContent) — a payload-sized decode on a refusal path, the one
// documented exception to payload independence — and round 5 pointed out
// that the exception IS the timing oracle FR-001 forbids. Research D16: the
// invalidation folds out NOTHING for a value without a decodable header (the
// entry count is exact without decoding, the size is not), and the cleanup
// sweep, which walks and decodes every record anyway, recomputes
// TotalEntries and TotalSizeBytes from the bucket. So the statistics are
// eventually consistent: over-counting by exactly the legacy payload until
// the sweep, exact after it — the live neighbour's, and nothing clamped — in
// memory and after a restart.
func TestGetRecordsAs_PreFrameInvalidationLeavesSizeToTheSweep(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.db")
	m, db := openManagerAt(t, path)
	broad := Authorization{CallerKind: CallerKindAgent, Principal: "b", AllowedServers: []string{"a"}, Permissions: []string{"read"}}
	live := `[{"v":"` + strings.Repeat("x", 100) + `"}]`
	if err := m.StoreAs("live", "t", nil, live, "", 1, broad); err != nil {
		t.Fatal(err)
	}
	// Content whose JSON encoding is far longer than the content itself, so
	// any estimate from the value's length would be visibly wrong.
	legacy := `["` + strings.Repeat(`\"`, 200) + `"]`
	if err := m.Store("legacy", "t", nil, legacy, "", 1); err != nil {
		t.Fatal(err)
	}
	if err := db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(CacheBucket))
		var rec Record
		if err := rec.UnmarshalBinary(bucket.Get([]byte("legacy"))); err != nil {
			return err
		}
		raw, err := json.Marshal(&rec)
		if err != nil {
			return err
		}
		return bucket.Put([]byte("legacy"), raw)
	}); err != nil {
		t.Fatal(err)
	}
	if got := m.GetStats().TotalSizeBytes; got != len(live)+len(legacy) {
		t.Fatalf("seed: TotalSizeBytes = %d, want %d", got, len(live)+len(legacy))
	}

	if _, err := m.GetRecordsAs("legacy", 0, 10, Authorization{CallerKind: CallerKindAdmin}); !errors.Is(err, ErrLegacyProvenance) {
		t.Fatalf("err = %v, want ErrLegacyProvenance", err)
	}
	if _, ok := m.Peek("legacy"); ok {
		t.Fatal("pre-frame record still present")
	}
	// Documented drift: the entry is gone and counted out, the size waits
	// for the sweep — nothing was read from the payload to find it.
	if got := m.GetStats(); got.TotalEntries != 1 || got.TotalSizeBytes != len(live)+len(legacy) || got.EvictedCount != 1 {
		t.Fatalf("stats after invalidation = %+v, want entries 1, evicted 1, size still %d (the legacy payload waits for the sweep)", *got, len(live)+len(legacy))
	}
	if err := m.cleanup(); err != nil {
		t.Fatal(err)
	}
	if got := m.GetStats(); got.TotalEntries != 1 || got.TotalSizeBytes != len(live) {
		t.Fatalf("stats after the sweep = %+v, want entries 1, size %d (the live neighbour's, exactly)", *got, len(live))
	}
	if _, err := m.GetRecordsAs("live", 0, 10, broad); err != nil {
		t.Fatalf("live neighbour: %v", err)
	}
	m.Close()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	m2, db2 := openManagerAt(t, path)
	defer db2.Close()
	defer m2.Close()
	if got := m2.GetStats(); got.TotalEntries != 1 || got.TotalSizeBytes != len(live) {
		t.Fatalf("stats after restart = %+v, want entries 1, size %d", *got, len(live))
	}
}

// The sweep's recomputation is from the bucket, whatever the counters held:
// a 1 MiB pre-frame entry invalidated on the gated door leaves TotalSizeBytes
// inflated by 1 MiB (round 3's original complaint) for at most one
// CleanupInterval, and drift of any other origin is reconciled the same way.
func TestCleanup_RecomputesEntriesAndSizeFromTheBucket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.db")
	m, db := openManagerAt(t, path)
	defer db.Close()
	defer m.Close()
	content := `[{"v":"` + strings.Repeat("x", 1<<20) + `"}]`
	if err := m.Store("pre-frame", "t", nil, content, "", 1); err != nil {
		t.Fatal(err)
	}
	if err := db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(CacheBucket))
		var rec Record
		if err := rec.UnmarshalBinary(bucket.Get([]byte("pre-frame"))); err != nil {
			return err
		}
		raw, err := json.Marshal(&rec)
		if err != nil {
			return err
		}
		return bucket.Put([]byte("pre-frame"), raw)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.GetRecordsAs("pre-frame", 0, 10, Authorization{CallerKind: CallerKindAdmin}); !errors.Is(err, ErrLegacyProvenance) {
		t.Fatalf("err = %v, want ErrLegacyProvenance", err)
	}
	if got := m.GetStats(); got.TotalEntries != 0 || got.TotalSizeBytes != len(content) {
		t.Fatalf("stats after invalidation = %+v, want entries 0 and the 1 MiB still counted until the sweep", *got)
	}
	// Arbitrary drift on top, as a crashed transaction or an older binary
	// could have left.
	if err := m.update(func(tx *bbolt.Tx) error {
		m.stats.TotalEntries = 7
		m.stats.TotalSizeBytes += 12345
		return m.saveStats(tx)
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.cleanup(); err != nil {
		t.Fatal(err)
	}
	if got := m.GetStats(); got.TotalEntries != 0 || got.TotalSizeBytes != 0 {
		t.Fatalf("stats after the sweep = %+v, want entries 0, size 0", *got)
	}
}
