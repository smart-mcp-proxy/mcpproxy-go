package cache

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
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
// header is now FIXED-SIZE (version, kind, deny-all bit, expiry, size,
// snapshot hash) and each distinct snapshot is stored once, by content hash,
// in the snapshots bucket. This pins (a): a snapshot naming 5,000 servers in
// both its grant and its profile round-trips, is redeemed by its producer
// (FR-001: same authorization, same entry) — warm and after a restart (cold
// in-memory cache, bucket read) — is stored once across entries, and is
// refused to a narrower agent on a plain scope refusal. The timing half is
// TestGetRecordsAs_RefusalIsSnapshotSizeIndependent.
func TestStoreAs_LargeSnapshotRoundTrips(t *testing.T) {
	const content = `[{"name":"SENTINEL-LARGE"}]`
	producer := fleetSnapshot(5000, 112)
	if n := len(snapshotBytes(producer)); n <= 1<<20 {
		t.Fatalf("fixture snapshot is %d bytes; want over the former 1 MiB header bound, which refused it at write time", n)
	}
	narrow := Authorization{CallerKind: CallerKindAgent, Principal: "narrow", AllowedServers: []string{"srv-000001"}, Permissions: []string{"read"}}

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
	redeem := func(t *testing.T, m *Manager, key string) {
		t.Helper()
		resp, err := m.GetRecordsAs(key, 0, 10, producer)
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
	redeem(t, m, "large-1")
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

	// Cold: a fresh manager has an empty snapshot cache, so the first
	// redemption resolves the hash through the bucket.
	m2, db2 := openManagerAt(t, path)
	defer db2.Close()
	defer m2.Close()
	if _, ok := m2.snapshots.get(snapshotHash(snapshotBytes(producer))); ok {
		t.Fatal("premise: the snapshot cache must be cold after a restart")
	}
	redeem(t, m2, "large-2")
	if _, ok := m2.snapshots.get(snapshotHash(snapshotBytes(producer))); !ok {
		t.Fatal("the cold load must warm the snapshot cache")
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

// Codex round 3, finding 3: invalidating a pre-frame (bare JSON) record on
// the gated door folded a size of 0 into TotalSizeBytes because the header
// could not be decoded, so a 5 MiB pre-upgrade entry left the cache size
// inflated by 5 MiB for good. The value's length IS known without decoding
// and bounds the content from above (the bare JSON body carries the escaped
// content), so the invalidation folds that in instead, clamped at zero.
func TestGetRecordsAs_PreFrameLegacyInvalidationFoldsValueSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.db")
	m, db := openManagerAt(t, path)
	content := `[{"v":"` + strings.Repeat("x", 1<<20) + `"}]`
	const key = "pre-frame"
	// A pre-upgrade binary stored the entry (stats folded in) as bare JSON.
	if err := m.Store(key, "t", nil, content, "", 1); err != nil {
		t.Fatal(err)
	}
	if err := db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(CacheBucket))
		var rec Record
		if err := rec.UnmarshalBinary(bucket.Get([]byte(key))); err != nil {
			return err
		}
		raw, err := json.Marshal(&rec)
		if err != nil {
			return err
		}
		return bucket.Put([]byte(key), raw)
	}); err != nil {
		t.Fatal(err)
	}
	if got := m.GetStats().TotalSizeBytes; got != len(content) {
		t.Fatalf("seed: TotalSizeBytes = %d, want %d", got, len(content))
	}

	if _, err := m.GetRecordsAs(key, 0, 10, Authorization{CallerKind: CallerKindAdmin}); !errors.Is(err, ErrLegacyProvenance) {
		t.Fatalf("err = %v, want ErrLegacyProvenance", err)
	}
	if _, ok := m.Peek(key); ok {
		t.Fatal("pre-frame record still present")
	}
	if got := m.GetStats(); got.TotalEntries != 0 || got.TotalSizeBytes != 0 {
		t.Fatalf("stats after invalidation = %+v, want the payload folded out (entries 0, size 0)", *got)
	}
	m.Close()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	m2, db2 := openManagerAt(t, path)
	defer db2.Close()
	defer m2.Close()
	if got := m2.GetStats(); got.TotalEntries != 0 || got.TotalSizeBytes != 0 {
		t.Fatalf("stats after restart = %+v, want entries 0, size 0", *got)
	}
}

// Codex round 4, finding 3: round 3 folded the pre-frame value's whole
// length out of TotalSizeBytes — the escaped JSON body, not the payload the
// store folded in — and clamped the aggregate at zero, so invalidating one
// legacy entry whose content escapes heavily could zero the accounting of
// unrelated live entries. The invalidation now folds out exactly
// len(FullContent), read by decoding the value on that one-shot path, and
// nothing is clamped: the live neighbour keeps its exact size, in memory and
// after a restart.
func TestGetRecordsAs_PreFrameInvalidationSubtractsOnlyItsPayload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.db")
	m, db := openManagerAt(t, path)
	broad := Authorization{CallerKind: CallerKindAgent, Principal: "b", AllowedServers: []string{"a"}, Permissions: []string{"read"}}
	live := `[{"v":"` + strings.Repeat("x", 100) + `"}]`
	if err := m.StoreAs("live", "t", nil, live, "", 1, broad); err != nil {
		t.Fatal(err)
	}
	// Content whose JSON encoding is far longer than the content itself.
	legacy := `["` + strings.Repeat(`\"`, 200) + `"]`
	if err := m.Store("legacy", "t", nil, legacy, "", 1); err != nil {
		t.Fatal(err)
	}
	var rawLen int
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
		rawLen = len(raw)
		return bucket.Put([]byte("legacy"), raw)
	}); err != nil {
		t.Fatal(err)
	}
	if rawLen <= len(legacy)+len(live) {
		t.Fatalf("fixture: the raw record (%d bytes) must exceed both payloads together (%d) for the overshoot to be observable", rawLen, len(legacy)+len(live))
	}
	if got := m.GetStats().TotalSizeBytes; got != len(live)+len(legacy) {
		t.Fatalf("seed: TotalSizeBytes = %d, want %d", got, len(live)+len(legacy))
	}

	if _, err := m.GetRecordsAs("legacy", 0, 10, Authorization{CallerKind: CallerKindAdmin}); !errors.Is(err, ErrLegacyProvenance) {
		t.Fatalf("err = %v, want ErrLegacyProvenance", err)
	}
	if got := m.GetStats(); got.TotalEntries != 1 || got.TotalSizeBytes != len(live) {
		t.Fatalf("stats after invalidation = %+v, want entries 1, size %d (the live neighbour's, exactly)", *got, len(live))
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
