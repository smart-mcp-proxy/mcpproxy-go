package cache

import (
	"errors"
	"testing"

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

	// Refusals are neither hits nor evictions; only the two Get calls per
	// reader above count as hits — subtract them to compare the rest.
	after := *m.GetStats()
	after.HitCount = before.HitCount
	if before != after {
		t.Fatalf("refused internal reads changed stats beyond the control Gets: before=%+v after=%+v", before, after)
	}
}
