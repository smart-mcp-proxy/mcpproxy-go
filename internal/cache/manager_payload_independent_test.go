package cache

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"go.etcd.io/bbolt"
)

// Codex round 2 (cache finding 3, server finding 1): spec Definitions make a
// non-disclosing refusal indistinguishable in status, body AND timing CLASS
// from a miss. Round 1 pinned the commit shape (every refusal commits a stats
// write like a miss); what it did not pin is the work done BEFORE the
// refusal. A miss went straight to the stats write, while every existing key
// was fully JSON-decoded — its multi-megabyte FullContent included — before
// the provenance class, expiry or the guard were looked at. A narrow token
// alternating a known key against a nonexistent one therefore measured an
// O(payload) decode on one and not the other: same body, different class.
//
// The gate now decides on a fixed-size frame header (version, producer,
// expiry, size) that MarshalBinary writes in front of the record; the record
// is decoded only after admission. This test pins that structurally, through
// the heap: the bytes a refused read allocates must not grow with the entry's
// payload, for every refusing path — the non-evicting ones (scope, admin
// snapshot, internal, expired) and the invalidating ones (legacy provenance,
// undecodable). A positive control proves the meter sees a decode: an
// ADMITTED read of the same big entry allocates at least the payload.
func TestGetRecordsAs_RefusalIsPayloadSizeIndependent(t *testing.T) {
	broad := Authorization{CallerKind: CallerKindAgent, Principal: "broad",
		AllowedServers: []string{"github", "weather"}, Permissions: []string{"read"}}
	narrow := Authorization{CallerKind: CallerKindAgent, Principal: "narrow",
		AllowedServers: []string{"weather"}, Permissions: []string{"read"}}
	admin := Authorization{CallerKind: CallerKindAdmin}

	const bigPayload = 4 << 20
	payload := func(n int) string { return `[{"v":"` + strings.Repeat("x", n) + `"}]` }
	sizes := []struct {
		name string
		n    int
	}{{"1KB", 1 << 10}, {"4MB", bigPayload}}

	// Every refusing path, seeded into a fresh database so the eviction of
	// one entry never rewrites the payload of a sibling.
	variants := []struct {
		name string
		seed func(t *testing.T, m *Manager, db *bbolt.DB, key, body string)
		want error
	}{
		{"scope refusal (broader agent snapshot)", func(t *testing.T, m *Manager, _ *bbolt.DB, key, body string) {
			if err := m.StoreAs(key, "t", nil, body, "", 1, broad); err != nil {
				t.Fatal(err)
			}
		}, ErrUnauthorizedRead},
		{"administrator snapshot", func(t *testing.T, m *Manager, _ *bbolt.DB, key, body string) {
			if err := m.StoreAs(key, "t", nil, body, "", 1, admin); err != nil {
				t.Fatal(err)
			}
		}, ErrUnauthorizedRead},
		{"internal entry", func(t *testing.T, m *Manager, _ *bbolt.DB, key, body string) {
			if err := m.StoreAs(key, "t", nil, body, "", 1, Authorization{CallerKind: CallerKindInternal}); err != nil {
				t.Fatal(err)
			}
		}, ErrInternalEntry},
		{"legacy provenance (framed, unstamped), evicted", func(t *testing.T, _ *Manager, db *bbolt.DB, key, body string) {
			now := time.Now()
			rec := &Record{Key: key, ToolName: "t", Timestamp: now, FullContent: body, TotalSize: len(body),
				ExpiresAt: now.Add(time.Hour), CreatedAt: now, LastAccessed: now}
			data, err := rec.MarshalBinary()
			if err != nil {
				t.Fatal(err)
			}
			if err := db.Update(func(tx *bbolt.Tx) error {
				return tx.Bucket([]byte(CacheBucket)).Put([]byte(key), data)
			}); err != nil {
				t.Fatal(err)
			}
		}, ErrLegacyProvenance},
		{"undecodable record, evicted", func(t *testing.T, _ *Manager, db *bbolt.DB, key, body string) {
			if err := db.Update(func(tx *bbolt.Tx) error {
				return tx.Bucket([]byte(CacheBucket)).Put([]byte(key), []byte("\xff not a record "+body))
			}); err != nil {
				t.Fatal(err)
			}
		}, ErrLegacyProvenance},
		{"expired entry, left for the sweep", func(t *testing.T, m *Manager, db *bbolt.DB, key, body string) {
			if err := m.StoreAs(key, "t", nil, body, "", 1, broad); err != nil {
				t.Fatal(err)
			}
			expireEntry(t, db, key)
		}, ErrKeyExpired},
		// Codex round 5: the two refusals that still did work proportional
		// to hidden state. A same-kind scope refusal against a producer
		// naming 5,000 servers, probed COLD (the manager reopened, so
		// nothing decoded at store time can help), and a pre-frame legacy
		// entry, which used to be decoded to recover its size. Both must
		// sit in the miss's class on the first probe — there is no
		// warm-up and no one-shot exception (research D16).
		{"scope refusal against a 5,000-server producer, cold manager", func(t *testing.T, m *Manager, _ *bbolt.DB, key, body string) {
			if err := m.StoreAs(key, "t", nil, body, "", 1, fleetSnapshot(5000, 112)); err != nil {
				t.Fatal(err)
			}
		}, ErrUnauthorizedRead},
		{"legacy provenance (pre-frame bare JSON), evicted", func(t *testing.T, _ *Manager, db *bbolt.DB, key, body string) {
			now := time.Now()
			putRawRecord(t, db, key, map[string]interface{}{
				"key": key, "tool_name": "t", "timestamp": now, "full_content": body,
				"total_size": len(body), "expires_at": now.Add(time.Hour), "created_at": now, "last_accessed": now,
			})
		}, ErrLegacyProvenance},
	}

	// The refusal's allocation budget: room for the frame header, the stats
	// round-trip and bbolt's own bookkeeping for a commit (an eviction hands
	// the payload's pages to the freelist by id, never by content), and an
	// order of magnitude below the smallest payload that would betray a
	// decode. The 1 KB and 4 MB legs must both fit — a refusal that scaled
	// with the payload fails on the 4 MB leg alone.
	const refusalAllocBudget = 256 << 10

	for _, v := range variants {
		for _, sz := range sizes {
			t.Run(v.name+"/"+sz.name, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "cache.db")
				m, db := openManagerAt(t, path)
				// A small live neighbour so the key is never alone in its leaf.
				if err := m.StoreAs("neighbour", "t", nil, payload(64), "", 1, broad); err != nil {
					t.Fatal(err)
				}
				v.seed(t, m, db, "target", payload(sz.n))
				// Every variant is probed through a REOPENED manager: the
				// verdict must come from the fixed header on disk, never
				// from anything the store path left in memory.
				m.Close()
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
				m, db = openManagerAt(t, path)
				defer db.Close()
				defer m.Close()

				allocated, resp, err := allocatedBy(func() (*ReadCacheResponse, error) {
					return m.GetRecordsAs("target", 0, 10, narrow)
				})
				if !errors.Is(err, v.want) || resp != nil {
					t.Fatalf("premise: expected refusal %v, got resp=%v err=%v", v.want, resp != nil, err)
				}
				if allocated > refusalAllocBudget {
					t.Fatalf("refusing a %s entry allocated %d bytes (budget %d): the payload was decoded before the refusal — a timing class a nonexistent key does not share", sz.name, allocated, refusalAllocBudget)
				}
			})
		}
	}

	t.Run("miss", func(t *testing.T) {
		m, db := openManagerAt(t, filepath.Join(t.TempDir(), "cache.db"))
		defer db.Close()
		defer m.Close()
		if err := m.StoreAs("neighbour", "t", nil, payload(bigPayload), "", 1, broad); err != nil {
			t.Fatal(err)
		}
		allocated, _, err := allocatedBy(func() (*ReadCacheResponse, error) {
			return m.GetRecordsAs("absent", 0, 10, narrow)
		})
		if !errors.Is(err, ErrKeyNotFound) {
			t.Fatalf("premise: %v", err)
		}
		if allocated > refusalAllocBudget {
			t.Fatalf("a miss next to a 4 MB neighbour allocated %d bytes (budget %d)", allocated, refusalAllocBudget)
		}
	})

	t.Run("positive control: an admitted read decodes the payload", func(t *testing.T) {
		m, db := openManagerAt(t, filepath.Join(t.TempDir(), "cache.db"))
		defer db.Close()
		defer m.Close()
		if err := m.StoreAs("target", "t", nil, payload(bigPayload), "", 1, broad); err != nil {
			t.Fatal(err)
		}
		allocated, resp, err := allocatedBy(func() (*ReadCacheResponse, error) {
			return m.GetRecordsAs("target", 0, 10, broad)
		})
		if err != nil || resp == nil || len(resp.Records) != 1 {
			t.Fatalf("premise: the producer reads its own entry: resp=%+v err=%v", resp, err)
		}
		if allocated < bigPayload {
			t.Fatalf("meter blind: an admitted read of a 4 MB entry allocated only %d bytes", allocated)
		}
	})
}

// allocatedBy returns the heap bytes fn allocated (runtime.MemStats.TotalAlloc
// is cumulative and never decremented by GC, so the delta is exactly the
// allocation volume of the call; nothing else allocates in this package's
// tests, which do not run in parallel).
func allocatedBy(fn func() (*ReadCacheResponse, error)) (uint64, *ReadCacheResponse, error) {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	resp, err := fn()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc, resp, err
}

// expireEntry rewrites the entry's expiry into the past in place, the way the
// package's expiry tests and the server's read_cache scope tests do.
func expireEntry(t *testing.T, db *bbolt.DB, key string) {
	t.Helper()
	if err := db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(CacheBucket))
		var rec Record
		if err := rec.UnmarshalBinary(bucket.Get([]byte(key))); err != nil {
			return err
		}
		rec.ExpiresAt = time.Now().Add(-time.Hour)
		data, err := rec.MarshalBinary()
		if err != nil {
			return err
		}
		return bucket.Put([]byte(key), data)
	}); err != nil {
		t.Fatal(err)
	}
}

// The same finding, on the commit shape: TestGetRecordsAs_RefusalCommitsLikeAMiss
// pins the non-evicting refusals (scope, admin snapshot, internal, and — now
// that the gated door leaves expired entries to the cleanup sweep — expired)
// to a miss's bbolt write count. The two invalidating refusals (legacy
// provenance, undecodable) cannot write exactly what a miss writes: FR-002
// requires the legacy entry durably invalidated by the refusal itself, not
// by a later sweep. Their extra work is pinned as BOUNDED instead: the
// delete rewrites the leaf node minus the entry and hands the value's pages
// to the freelist by id range, so the page-write count is the same for a
// 1 KB and a 4 MB payload and exceeds a miss's by a constant that does not
// depend on the entry (the cache leaf, and the freelist page it dirties).
// It is also one-shot: the entry is gone, so a second probe is a plain miss.
func TestGetRecordsAs_EvictingRefusalWritesArePayloadIndependent(t *testing.T) {
	broad := Authorization{CallerKind: CallerKindAgent, Principal: "broad",
		AllowedServers: []string{"github"}, Permissions: []string{"read"}}
	narrow := Authorization{CallerKind: CallerKindAgent, Principal: "narrow",
		AllowedServers: []string{"weather"}, Permissions: []string{"read"}}
	payload := func(n int) string { return `[{"v":"` + strings.Repeat("x", n) + `"}]` }

	seeds := map[string]func(t *testing.T, m *Manager, db *bbolt.DB, key, body string){
		"legacy": func(t *testing.T, _ *Manager, db *bbolt.DB, key, body string) {
			now := time.Now()
			putRawRecord(t, db, key, map[string]interface{}{"key": key, "tool_name": "t", "timestamp": now,
				"full_content": body, "total_size": len(body), "expires_at": now.Add(time.Hour), "created_at": now, "last_accessed": now})
		},
		"undecodable": func(t *testing.T, _ *Manager, db *bbolt.DB, key, body string) {
			if err := db.Update(func(tx *bbolt.Tx) error {
				return tx.Bucket([]byte(CacheBucket)).Put([]byte(key), []byte("\xff"+body))
			}); err != nil {
				t.Fatal(err)
			}
		},
	}

	// writesFor seeds one fresh database and returns the bbolt page writes
	// the refused read performed, and those of a miss on the same database.
	writesFor := func(t *testing.T, seed func(*testing.T, *Manager, *bbolt.DB, string, string), n int) (refusal, miss int64) {
		t.Helper()
		m, db := openManagerAt(t, filepath.Join(t.TempDir(), "cache.db"))
		defer db.Close()
		defer m.Close()
		if err := m.StoreAs("neighbour", "t", nil, payload(64), "", 1, broad); err != nil {
			t.Fatal(err)
		}
		seed(t, m, db, "target", payload(n))
		before := db.Stats().TxStats.Write
		if _, err := m.GetRecordsAs("absent", 0, 10, narrow); !errors.Is(err, ErrKeyNotFound) {
			t.Fatalf("premise: %v", err)
		}
		miss = db.Stats().TxStats.Write - before
		before = db.Stats().TxStats.Write
		if resp, err := m.GetRecordsAs("target", 0, 10, narrow); err == nil || resp != nil {
			t.Fatalf("premise: expected a refusal, got resp=%+v err=%v", resp, err)
		}
		refusal = db.Stats().TxStats.Write - before
		if _, ok := m.Peek("target"); ok {
			t.Fatal("premise: an evicting refusal removes the entry")
		}
		return refusal, miss
	}

	// One committed delete of a single-page leaf: the cache leaf page and
	// the meta/freelist pages a miss already dirties. Anything beyond that
	// is a rebalance or a payload-sized rewrite, neither of which a refusal
	// may perform.
	const evictionWriteBudget = 2
	for name, seed := range seeds {
		t.Run(name, func(t *testing.T) {
			smallRefusal, smallMiss := writesFor(t, seed, 1<<10)
			bigRefusal, bigMiss := writesFor(t, seed, 4<<20)
			if smallMiss != bigMiss {
				t.Fatalf("premise: a miss writes the same pages whatever its neighbours hold: %d vs %d", smallMiss, bigMiss)
			}
			if smallRefusal != bigRefusal {
				t.Fatalf("evicting a 1 KB entry wrote %d pages, a 4 MB entry %d: the eviction is payload-proportional", smallRefusal, bigRefusal)
			}
			if extra := bigRefusal - bigMiss; extra > evictionWriteBudget {
				t.Fatalf("evicting refusal wrote %d pages, a miss %d: %d extra exceeds the one-leaf budget of %d (%s)", bigRefusal, bigMiss, extra, evictionWriteBudget, fmt.Sprint(name))
			}
		})
	}
}

// putRawRecord's JSON-only fixture and MarshalBinary's framed output must
// both decode to the same Record: the frame is invisible to every reader
// that goes through UnmarshalBinary (Peek, Get, cleanup, Invalidate), and a
// record a pre-feature binary wrote — raw JSON, no frame — still decodes.
func TestRecord_BinaryRoundTripAcceptsFramedAndRawJSON(t *testing.T) {
	now := time.Now().Round(0)
	rec := &Record{Key: "k", ToolName: "t", FullContent: `[1]`, TotalSize: 3, ExpiresAt: now.Add(time.Hour),
		CreatedAt: now, LastAccessed: now, Timestamp: now, Version: RecordVersion,
		Producer: &Authorization{CallerKind: CallerKindAgent, Principal: "p", AllowedServers: []string{"a"}, Permissions: []string{"read"}}}
	framed, err := rec.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if string(framed) == string(raw) {
		t.Fatal("MarshalBinary must frame the record so the gate can read its header without the payload")
	}
	for name, data := range map[string][]byte{"framed": framed, "raw": raw} {
		var got Record
		if err := got.UnmarshalBinary(data); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got.Key != "k" || got.FullContent != `[1]` || !got.HasCurrentProvenance() || got.Producer.Principal != "p" {
			t.Fatalf("%s: round trip lost fields: %+v", name, got)
		}
	}
}

// Codex round 4 (cache finding 2, server finding 1) put the producer snapshot
// behind a content hash and an in-memory LRU, so a refusal was O(1) once the
// snapshot was WARM — and codex round 5 pointed at the word "warm": after a
// restart, or once 128 other snapshots had evicted the target, the first
// same-kind refusal of a key stamped with a 5,000-server snapshot read and
// JSON-decoded that snapshot from the bucket, while a nonexistent key
// decoded nothing. Snapshot size is unbounded, so the difference could be
// made arbitrarily large. The door now decides on the fixed header alone —
// the reader's effective-authorization digest against the producer's, plus
// the kind, deny-all and tier bits (research D16) — and never loads a
// snapshot, so the FIRST probe on a cold manager must already sit in the
// miss's allocation class, and stay there: no warm-up, no positive control
// for a cold load.
func TestGetRecordsAs_RefusalIsSnapshotSizeIndependent(t *testing.T) {
	wide := fleetSnapshot(5000, 112)
	small := Authorization{CallerKind: CallerKindAgent, Principal: "small", AllowedServers: []string{"a", "b"}, Permissions: []string{"read"}}
	// Same kind, not deny-all, not unrestricted and not the producer: the
	// refusal is the digest comparison, the shape that used to load.
	narrow := Authorization{CallerKind: CallerKindAgent, Principal: "narrow", AllowedServers: []string{"zzz"}, Permissions: []string{"read"}}

	path := filepath.Join(t.TempDir(), "cache.db")
	m, db := openManagerAt(t, path)
	for key, p := range map[string]Authorization{"wide": wide, "small": small} {
		if err := m.StoreAs(key, "t", nil, `[{"v":1}]`, "", 1, p); err != nil {
			t.Fatal(err)
		}
	}
	m.Close()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	// Reopen: whatever the store path held in memory is gone.
	m, db = openManagerAt(t, path)
	defer db.Close()
	defer m.Close()

	probe := func(key string, want error) uint64 {
		t.Helper()
		allocated, resp, err := allocatedBy(func() (*ReadCacheResponse, error) {
			return m.GetRecordsAs(key, 0, 10, narrow)
		})
		if !errors.Is(err, want) || resp != nil {
			t.Fatalf("%s: resp=%v err=%v, want %v", key, resp != nil, err, want)
		}
		return allocated
	}
	// Room for the reader's own digest, the guard, the error path and
	// bbolt's commit noise (a freelist rewrite shows up as a ~16 KiB step);
	// still ~18x below the snapshot a load would betray.
	const constant = 64 << 10
	coldMiss := probe("absent", ErrKeyNotFound)
	if cold := probe("wide", ErrUnauthorizedRead); cold > coldMiss+constant {
		t.Fatalf("the COLD first refusal against a %d-byte snapshot allocated %d bytes, a miss %d: the snapshot was loaded before the refusal — a timing class a nonexistent key does not share", len(snapshotBytes(wide)), cold, coldMiss)
	}

	// Interleaved rounds, minimum per key: bbolt's commit-time allocations
	// vary by a few pages between commits and under the race detector, and
	// the interleaving spreads that noise over all three keys alike.
	const rounds = 12
	wideMin, smallMin, miss := ^uint64(0), ^uint64(0), ^uint64(0)
	for i := 0; i < rounds; i++ {
		wideMin = min(wideMin, probe("wide", ErrUnauthorizedRead))
		smallMin = min(smallMin, probe("small", ErrUnauthorizedRead))
		miss = min(miss, probe("absent", ErrKeyNotFound))
	}
	t.Logf("refusal: wide=%d small=%d; miss=%d bytes", wideMin, smallMin, miss)
	if wideMin > smallMin+constant {
		t.Fatalf("a refusal against a 5,000-server snapshot allocated %d bytes, against a 2-server one %d: the refusal still scales with the snapshot", wideMin, smallMin)
	}
	if wideMin > miss+constant {
		t.Fatalf("a refusal allocated %d bytes, a miss %d: not the same class", wideMin, miss)
	}
}
