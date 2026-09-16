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
			putFramedRecord(t, db, key, encode(t, fx.header), encode(t, fx.body))
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
			if got := m.GetStats(); got.TotalEntries != 0 || got.TotalSizeBytes != 0 || got.EvictedCount != 1 {
				t.Fatalf("stats after invalidation = %+v, want the entry and its header size folded out", *got)
			}
			m.Close()
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			m2, db2 := openManagerAt(t, path)
			defer db2.Close()
			defer m2.Close()
			if got := onDiskEntryCount(t, db2); got != 0 {
				t.Fatalf("on-disk count after restart = %d, want 0", got)
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

// Codex round 3, finding 2: the reader bounds the frame header it will decode
// (maxRecordHeaderLen); the writer must never persist a frame the reader will
// classify as corrupt, or a legitimate entry produced under a large
// authorization snapshot is stored, then refused and deleted on its
// producer's own first redemption (FR-001's same-authorization guarantee).
// Both sides of the bound: a snapshot that fits is stored and redeemed by the
// producing authorization; one that does not is refused AT WRITE TIME with an
// error the producer can act on (the truncator logs it and serves the
// truncated payload without a cache entry) and leaves nothing behind.
func TestStoreAs_HeaderBoundEnforcedAtWriteTime(t *testing.T) {
	const content = `[{"name":"SENTINEL-LARGE"}]`
	// snapshotAround returns an agent authorization whose frame header is at
	// least target bytes long (thousands of 64-character server names, split
	// across the grant and the effective profile the way a real snapshot is).
	snapshotAround := func(t *testing.T, target int) Authorization {
		t.Helper()
		a := Authorization{CallerKind: CallerKindAgent, Principal: "wide", Permissions: []string{"read"},
			ProfilePin: "fleet", Profile: "fleet", ProfileScoped: true}
		pad := strings.Repeat("x", 48)
		for i := 0; ; i++ {
			name := fmt.Sprintf("srv-%06d-%s", i, pad) // 64 characters
			if i%2 == 0 {
				a.AllowedServers = append(a.AllowedServers, name)
			}
			a.ProfileServers = append(a.ProfileServers, name)
			if i%256 == 0 {
				h, err := json.Marshal((&Record{Version: RecordVersion, Producer: &a, TotalSize: len(content)}).header())
				if err != nil {
					t.Fatal(err)
				}
				if len(h) >= target {
					return a
				}
			}
		}
	}

	t.Run("fits: stored and redeemed by its producer", func(t *testing.T) {
		producer := snapshotAround(t, maxRecordHeaderLen/2)
		h, err := json.Marshal((&Record{Version: RecordVersion, Producer: &producer, TotalSize: len(content)}).header())
		if err != nil {
			t.Fatal(err)
		}
		if len(h) > maxRecordHeaderLen {
			t.Fatalf("fixture header is %d bytes, over the %d bound", len(h), maxRecordHeaderLen)
		}
		m, db := openManagerAt(t, filepath.Join(t.TempDir(), "cache.db"))
		defer db.Close()
		defer m.Close()
		if err := m.StoreAs("large", "t", nil, content, "", 1, producer); err != nil {
			t.Fatalf("store under a %d-byte header: %v", len(h), err)
		}
		resp, err := m.GetRecordsAs("large", 0, 10, producer)
		if err != nil {
			t.Fatalf("producer's own redemption refused: %v", err)
		}
		if len(resp.Records) != 1 {
			t.Fatalf("records = %+v, want the one sentinel", resp.Records)
		}
		if _, ok := m.Peek("large"); !ok {
			t.Fatal("entry deleted by its producer's redemption")
		}
	})

	t.Run("does not fit: refused at write time, nothing persisted", func(t *testing.T) {
		producer := snapshotAround(t, maxRecordHeaderLen+1)
		m, db := openManagerAt(t, filepath.Join(t.TempDir(), "cache.db"))
		defer db.Close()
		defer m.Close()
		before := *m.GetStats()
		err := m.StoreAs("huge", "t", nil, content, "", 1, producer)
		if !errors.Is(err, errRecordHeaderOversize) {
			t.Fatalf("StoreAs err = %v, want errRecordHeaderOversize", err)
		}
		if got := onDiskEntryCount(t, db); got != 0 {
			t.Fatalf("on-disk count = %d, want 0: an oversize frame was persisted", got)
		}
		if got := *m.GetStats(); got != before {
			t.Fatalf("stats moved on a refused store: %+v -> %+v", before, got)
		}
		if _, err := m.GetRecordsAs("huge", 0, 10, producer); !errors.Is(err, ErrKeyNotFound) {
			t.Fatalf("read after refused store: err = %v, want ErrKeyNotFound", err)
		}
	})
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
