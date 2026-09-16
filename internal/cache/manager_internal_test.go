package cache

import (
	"errors"
	"testing"
	"time"

	"go.etcd.io/bbolt"
	"go.uber.org/zap"
)

// Spec 105 FR-002 (gap FR001-G2): entries the proxy writes for ITSELF — the
// registry search cache (`registry-servers:<id>:<tag>:<query>:<limit>`) and
// the repository guesser cache (`npm:<pkg>`) — are stamped with the internal
// caller kind by their writers and are non-redeemable through the gated read
// for every caller, administrators included (SC-005 names this exception).
// Unlike legacy entries they are refused WITHOUT eviction: their keys are
// guessable, and evicting on refusal would let any caller purge the entries
// the registry (Peek) and guesser (Get) readers depend on.
//
// The kind is spelled as its wire value here so this file compiles against
// the pre-feature package; the constant the writers use is asserted by the
// writer tests in internal/runtime and internal/experiments.
const internalCallerKindWire = "internal"

func TestGetRecordsAs_InternalEntryRefusedForEveryCallerWithoutEviction(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	m, err := NewManager(db, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	internal := Authorization{CallerKind: internalCallerKindWire}
	const registryKey = "registry-servers:official:::10"
	const npmKey = "npm:@acme/mcp-server"
	if err := m.StoreAs(registryKey, "registry-servers", nil, `[{"id":"srv-1","name":"SENTINEL-REGISTRY"}]`, "", 1, internal); err != nil {
		t.Fatal(err)
	}
	if err := m.StoreAs(npmKey, "repo_guess", map[string]interface{}{"package_name": "@acme/mcp-server"}, `{"package_name":"@acme/mcp-server","exists":true}`, "", 1, internal); err != nil {
		t.Fatal(err)
	}
	before := *m.GetStats()

	readers := []struct {
		name   string
		reader Authorization
	}{
		{"admin", Authorization{CallerKind: CallerKindAdmin}},
		{"admin_user", Authorization{CallerKind: CallerKindAdminUser, Principal: "u9"}},
		{"anonymous", Authorization{CallerKind: CallerKindAnonymous}},
		{"wildcard agent", Authorization{CallerKind: CallerKindAgent, Principal: "star",
			AllowedServers: []string{"*"}, Permissions: []string{"read", "write", "destructive"}}},
		{"user", Authorization{CallerKind: CallerKindUser, Principal: "u1"}},
		// Even a reader presenting the internal kind itself is refused: the
		// gated read is the agent-facing door, and no request comes through it
		// as the proxy's own writer.
		{"internal-shaped reader", internal},
	}
	for _, key := range []string{registryKey, npmKey} {
		for _, rd := range readers {
			t.Run(key+"/"+rd.name, func(t *testing.T) {
				resp, err := m.GetRecordsAs(key, 0, 10, rd.reader)
				if !errors.Is(err, ErrUnauthorizedRead) {
					t.Fatalf("%s reading internal entry %q: got err=%v resp=%v, want ErrUnauthorizedRead", rd.name, key, err, resp)
				}
				if !errors.Is(err, ErrInternalEntry) {
					t.Fatalf("%s: got %v, want the ErrInternalEntry sentinel the handler renders for administrators", rd.name, err)
				}
				if resp != nil {
					t.Fatalf("refused internal read returned content: %+v", resp)
				}
				// No eviction: the internal readers still find their entry.
				rec, ok := m.Peek(key)
				if !ok {
					t.Fatalf("internal entry %q was evicted by a refused redemption (eviction DoS on a guessable key)", key)
				}
				if rec.Producer == nil || rec.Producer.CallerKind != internalCallerKindWire {
					t.Fatalf("internal stamp lost: %+v", rec.Producer)
				}
				if got, err := m.Get(key); err != nil || got == nil {
					t.Fatalf("the ungated internal reader (Get) must still serve %q: %v", key, err)
				}
			})
		}
	}

	// Refusals are neither hits nor evictions. Each refusal counts as a MISS
	// — the same stats signal an absent key leaves, so the refusal commits
	// like a miss and shares its timing class (critique round 1, finding 1);
	// the Get control calls above are the only hits.
	refusals := len(readers) * 2
	after := *m.GetStats()
	if got, want := after.MissCount, before.MissCount+refusals; got != want {
		t.Fatalf("MissCount = %d, want %d: every refused internal read counts as a miss", got, want)
	}
	after.HitCount = before.HitCount
	after.MissCount = before.MissCount
	if before != after {
		t.Fatalf("refused internal reads changed stats beyond misses and the control Gets: before=%+v after=%+v", before, after)
	}
}

// Critique round 2, finding 4: on the gated door the expiry check ran BEFORE
// the internal-kind check, so a read_cache probe of a guessable internal key
// whose entry had passed its TTL committed the eviction — exactly the
// eviction-on-refusal FR-002 forbids for internal entries, bounded only by
// the cleanup interval. The registry reader (runtime.SearchRegistryServers)
// deliberately serves an expired entry through Peek as Stale until cleanup
// runs; the gated door must leave it there.
func TestGetRecordsAs_ExpiredInternalEntryRefusedWithoutEviction(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	m, err := NewManager(db, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	const key = "registry-servers:official:::10"
	if err := m.StoreAs(key, "registry-servers", nil, `[{"id":"srv-1"}]`, "", 1, Authorization{CallerKind: internalCallerKindWire}); err != nil {
		t.Fatal(err)
	}
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
	before := *m.GetStats()

	for _, rd := range []struct {
		name   string
		reader Authorization
	}{
		{"admin", Authorization{CallerKind: CallerKindAdmin}},
		{"wildcard agent", Authorization{CallerKind: CallerKindAgent, Principal: "star",
			AllowedServers: []string{"*"}, Permissions: []string{"read", "write", "destructive"}}},
	} {
		resp, err := m.GetRecordsAs(key, 0, 10, rd.reader)
		if !errors.Is(err, ErrInternalEntry) {
			t.Fatalf("%s reading an EXPIRED internal entry: got err=%v resp=%v, want ErrInternalEntry (the internal refusal, not the expiry eviction)", rd.name, err, resp)
		}
		rec, ok := m.Peek(key)
		if !ok {
			t.Fatalf("%s: the expired internal entry was evicted by a gated probe; the registry reader serves it as Stale until cleanup", rd.name)
		}
		if !rec.IsExpired() {
			t.Fatalf("premise lost: %+v", rec)
		}
	}
	after := *m.GetStats()
	if after.EvictedCount != before.EvictedCount || after.TotalEntries != before.TotalEntries {
		t.Fatalf("a gated probe of an expired internal key must not evict: before=%+v after=%+v", before, after)
	}
}
