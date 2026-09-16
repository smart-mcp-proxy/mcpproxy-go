package cache

import (
	"encoding/json"
	"errors"
	"testing"

	"go.uber.org/zap"
)

// CouldHaveProduced is the read gate: a reader may see an entry only when its
// own authorization is at least as broad as the one the entry was produced
// under, in every dimension. "Broader" is the direction that matters — an
// unrestricted admin could have produced anything, a weather-only token could
// not have produced a github listing.
func TestAuthorization_CouldHaveProduced(t *testing.T) {
	admin := Authorization{CallerKind: CallerKindAdmin}
	anonymous := Authorization{CallerKind: CallerKindAnonymous}
	broad := Authorization{CallerKind: CallerKindAgent, Principal: "broad",
		AllowedServers: []string{"github", "weather"}, Permissions: []string{"read", "write"}}
	narrow := Authorization{CallerKind: CallerKindAgent, Principal: "narrow",
		AllowedServers: []string{"weather"}, Permissions: []string{"read"}}
	wildcard := Authorization{CallerKind: CallerKindAgent, Principal: "star",
		AllowedServers: []string{"*"}, Permissions: []string{"read", "write", "destructive"}}
	pinned := Authorization{CallerKind: CallerKindAgent, Principal: "pinned",
		AllowedServers: []string{"github", "weather"}, Permissions: []string{"read", "write"},
		ProfilePin: "research", Profile: "research", ProfileScoped: true, ProfileServers: []string{"github"}}
	otherPin := pinned
	otherPin.ProfilePin, otherPin.Profile, otherPin.ProfileServers = "deploy", "deploy", []string{"weather"}
	stalePin := pinned
	stalePin.ProfileServers = []string{} // the pinned profile was deleted: deny-all scope, same name
	adminInProfile := Authorization{CallerKind: CallerKindAdmin, Profile: "research",
		ProfileScoped: true, ProfileServers: []string{"github"}}
	adminInWiderProfile := Authorization{CallerKind: CallerKindAdmin, Profile: "everything",
		ProfileScoped: true, ProfileServers: []string{"github", "weather"}}
	adminInDenyAll := Authorization{CallerKind: CallerKindAdmin, Profile: "research",
		ProfileScoped: true, ProfileServers: []string{}}
	alice := Authorization{CallerKind: CallerKindUser, Principal: "user-alice"}
	bob := Authorization{CallerKind: CallerKindUser, Principal: "user-bob"}

	cases := []struct {
		name     string
		producer Authorization
		reader   Authorization
		want     bool
	}{
		{"same agent token", broad, broad, true},
		{"narrower server scope", broad, narrow, false},
		{"narrower permission tier", broad, Authorization{CallerKind: CallerKindAgent,
			AllowedServers: []string{"github", "weather"}, Permissions: []string{"read"}}, false},
		{"broader agent may read narrower", narrow, broad, true},
		{"wildcard server scope covers everything", broad, wildcard, true},
		{"explicit list does not cover wildcard", wildcard, broad, false},
		{"admin reads agent entry", broad, admin, true},
		{"agent cannot read admin entry", admin, broad, false},
		{"agent cannot read wildcard-less anonymous entry", anonymous, wildcard, false},
		{"anonymous cannot read an authenticated admin entry", admin, anonymous, false},
		{"admin reads anonymous", anonymous, admin, true},
		{"anonymous reads anonymous", anonymous, anonymous, true},
		{"anonymous reads agent entry", broad, anonymous, true},
		{"same profile pin", pinned, pinned, true},
		{"different profile pin", pinned, otherPin, false},
		{"unpinned reader is broader than pinned producer", pinned, broad, true},
		{"pinned reader is narrower than unpinned producer", broad, pinned, false},
		// Spec 105 FR-001 (D5, task T031): superset is ordered by caller kind
		// first, so an administrator's own profile binding never narrows what
		// it may redeem. Before this feature the profile comparison ran first
		// and these four cells were false (#1226 R1-F2).
		{"admin bound to a URL profile reads an unscoped admin entry (kind first)", admin, adminInProfile, true},
		{"unscoped admin reads profile-bound admin entry", adminInProfile, admin, true},
		{"profile whose servers cover the producer's profile", adminInProfile, adminInWiderProfile, true},
		{"admin profile that does not cover the producer's profile still reads (kind first)", adminInWiderProfile, adminInProfile, true},
		{"deleted profile: same name, deny-all scope, admin still reads (kind first)", adminInProfile, adminInDenyAll, true},
		{"stale pin (profile deleted) cannot read its own earlier entry", pinned, stalePin, false},
		{"deny-all reader matches nothing, not even a deny-all-stamped entry", stalePin, stalePin, false},
		{"deny-all admin reader is not guarded: the deny-all guard is for agents only", adminInDenyAll, adminInDenyAll, true},
		{"anonymous reads a user's entry", alice, anonymous, true},
		{"anonymous cannot read an admin_user entry", Authorization{CallerKind: CallerKindAdminUser, Principal: "u9"}, anonymous, false},
		{"same user", alice, alice, true},
		{"different user", alice, bob, false},
		{"agent cannot read a user's entry", alice, wildcard, false},
		{"admin reads a user's entry", alice, admin, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.producer.CouldHaveProduced(tc.reader); got != tc.want {
				t.Fatalf("producer=%+v reader=%+v: got %v want %v", tc.producer, tc.reader, got, tc.want)
			}
		})
	}
}

// Entries persisted before producer stamping existed carry no authorization.
// Spec 105 FR-002 (task T031 inversion): they are legacy provenance — refused
// for EVERY caller kind, administrators and the anonymous /mcp caller
// included, and invalidated by the first refused redemption. Before this
// feature they were treated as produced by an unrestricted caller with no
// identity and any unrestricted reader could page them; the full matrix and
// the durability proof live in manager_legacy_test.go.
func TestGetRecordsAs_LegacyEntryWithoutProducer(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	m, err := NewManager(db, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	seed := func() {
		t.Helper()
		if err := m.Store("legacy", "github:list", nil, `{"items":[1,2,3]}`, "items", 3); err != nil {
			t.Fatal(err)
		}
	}
	seed()
	agent := Authorization{CallerKind: CallerKindAgent, AllowedServers: []string{"*"}, Permissions: []string{"read"}}
	if _, err := m.GetRecordsAs("legacy", 0, 10, agent); !errors.Is(err, ErrUnauthorizedRead) {
		t.Fatalf("agent reading a legacy entry: got %v, want ErrUnauthorizedRead", err)
	}
	if _, ok := m.Peek("legacy"); ok {
		t.Fatal("the refused redemption must invalidate the legacy entry")
	}
	seed()
	if _, err := m.GetRecordsAs("legacy", 0, 10, Authorization{CallerKind: CallerKindAdmin}); !errors.Is(err, ErrUnauthorizedRead) {
		t.Fatalf("admin reading a legacy entry: got %v, want ErrUnauthorizedRead (FR-002: every caller)", err)
	}
	seed()
	if _, err := m.GetRecordsAs("legacy", 0, 10, Authorization{CallerKind: CallerKindAnonymous}); !errors.Is(err, ErrUnauthorizedRead) {
		t.Fatalf("anonymous reading a legacy entry: got %v, want ErrUnauthorizedRead (FR-002: every caller)", err)
	}
	seed()
	user := Authorization{CallerKind: CallerKindUser, Principal: "u1"}
	if _, err := m.GetRecordsAs("legacy", 0, 10, user); !errors.Is(err, ErrUnauthorizedRead) {
		t.Fatalf("user reading a legacy entry: got %v, want ErrUnauthorizedRead", err)
	}
	// Once invalidated the key is a plain miss for every caller.
	if _, err := m.GetRecordsAs("legacy", 0, 10, Authorization{CallerKind: CallerKindAdmin}); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("after invalidation: got %v, want ErrKeyNotFound", err)
	}
}

// A refused read must not count as a hit or bump the entry's access stats —
// otherwise the refusal is visible as "someone read this" in the stats. It
// DOES count as a miss (critique round 1, finding 1 — inverted from "stats
// byte-identical"): a miss is the one signal an absent key leaves, and taking
// the same committing branch is what puts the refusal in the miss's timing
// class; a refusal that committed nothing was a ~3000x timing oracle.
func TestGetRecordsAs_RefusedReadLeavesStatsUntouched(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	m, err := NewManager(db, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	admin := Authorization{CallerKind: CallerKindAdmin}
	if err := m.StoreAs("k", "github:list", nil, `{"items":[1,2,3]}`, "items", 3, admin); err != nil {
		t.Fatal(err)
	}
	before := *m.GetStats()
	agent := Authorization{CallerKind: CallerKindAgent, AllowedServers: []string{"github"}, Permissions: []string{"read"}}
	if _, err := m.GetRecordsAs("k", 0, 10, agent); !errors.Is(err, ErrUnauthorizedRead) {
		t.Fatalf("got %v, want ErrUnauthorizedRead", err)
	}
	after := *m.GetStats()
	if after.MissCount != before.MissCount+1 {
		t.Fatalf("a refused read must count as exactly one miss: before=%+v after=%+v", before, after)
	}
	after.MissCount = before.MissCount
	if before != after {
		t.Fatalf("refused read changed stats beyond the miss: before=%+v after=%+v", before, after)
	}
	rec, ok := m.Peek("k")
	if !ok || rec.AccessCount != 0 {
		t.Fatalf("refused read bumped access count: %+v", rec)
	}
	if rec.Producer == nil || rec.Producer.CallerKind != CallerKindAdmin {
		t.Fatalf("producer authorization not persisted with the entry: %+v", rec.Producer)
	}
}

// The gate must not change what an authorized reader gets back: the
// gated page is byte-identical to the ungated one.
func TestGetRecordsAs_SameAuthorizationIsByteIdentical(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	m, err := NewManager(db, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	agent := Authorization{CallerKind: CallerKindAgent, Principal: "bot",
		AllowedServers: []string{"github"}, Permissions: []string{"read"}}
	content := `{"tools":[{"name":"a","x":1},{"name":"b","x":2},{"name":"c","x":3}]}`
	if err := m.StoreAs("k", "retrieve_tools", map[string]interface{}{"query": "q"}, content, "tools", 3, agent); err != nil {
		t.Fatal(err)
	}
	for _, offset := range []int{0, 1, 2, 3} {
		ungated, err := m.GetRecords("k", offset, 2)
		if err != nil {
			t.Fatal(err)
		}
		gated, err := m.GetRecordsAs("k", offset, 2, agent)
		if err != nil {
			t.Fatalf("offset %d: %v", offset, err)
		}
		want, _ := json.Marshal(ungated)
		got, _ := json.Marshal(gated)
		if string(want) != string(got) {
			t.Fatalf("offset %d: gated page differs from ungated\n want %s\n got  %s", offset, want, got)
		}
	}
}

// Spec 105 FR-001 (`spec.md:124`, research D5, gap FR001-G6): superset is
// ordered by CALLER KIND FIRST. An administrator reader qualifies for any
// snapshot regardless of its own profile binding — unscoped, narrower, wider,
// empty, or a profile deleted since — where before this feature the profile
// comparison ran first and refused a profile-bound administrator every
// unscoped or wider entry (#1226 R1-F2). An agent reader never qualifies for
// an administrator-produced snapshot, however broad the agent. Between agent
// snapshots nothing changes: the deny-all guard (an empty effective profile
// reads nothing, not even its own deny-all-stamped entry) applies to agent
// readers only, and pin equality, server set and permission set must each
// contain the snapshot's.
//
// The pre-D5 table above (TestAuthorization_CouldHaveProduced) pins the
// profile-first order for administrators; task T031 inverts those cells.
func TestAuthorization_CallerKindFirst(t *testing.T) {
	admin := Authorization{CallerKind: CallerKindAdmin}
	adminUser := Authorization{CallerKind: CallerKindAdminUser, Principal: "u9"}
	adminInProfile := Authorization{CallerKind: CallerKindAdmin, Profile: "research",
		ProfileScoped: true, ProfileServers: []string{"github"}}
	adminInWiderProfile := Authorization{CallerKind: CallerKindAdmin, Profile: "everything",
		ProfileScoped: true, ProfileServers: []string{"github", "weather"}}
	adminInEmptyProfile := Authorization{CallerKind: CallerKindAdmin, Profile: "empty",
		ProfileScoped: true, ProfileServers: []string{}}
	adminInDeletedProfile := Authorization{CallerKind: CallerKindAdmin, Profile: "research",
		ProfileScoped: true, ProfileServers: nil} // the URL/session profile vanished: same name, deny-all scope
	adminUserInProfile := Authorization{CallerKind: CallerKindAdminUser, Principal: "u9", Profile: "research",
		ProfileScoped: true, ProfileServers: []string{"github"}}
	broad := Authorization{CallerKind: CallerKindAgent, Principal: "broad",
		AllowedServers: []string{"github", "weather"}, Permissions: []string{"read", "write"}}
	wildcard := Authorization{CallerKind: CallerKindAgent, Principal: "star",
		AllowedServers: []string{"*"}, Permissions: []string{"read", "write", "destructive"}}
	pinnedWildcard := Authorization{CallerKind: CallerKindAgent, Principal: "star-pinned",
		AllowedServers: []string{"*"}, Permissions: []string{"read", "write", "destructive"},
		ProfilePin: "research", Profile: "research", ProfileScoped: true, ProfileServers: []string{"github"}}
	agentInSessionProfile := Authorization{CallerKind: CallerKindAgent, Principal: "broad",
		AllowedServers: []string{"github", "weather"}, Permissions: []string{"read", "write"},
		Profile: "research", ProfileScoped: true, ProfileServers: []string{"github"}}
	agentInEmptyProfile := Authorization{CallerKind: CallerKindAgent, Principal: "star",
		AllowedServers: []string{"*"}, Permissions: []string{"read", "write", "destructive"},
		Profile: "empty", ProfileScoped: true, ProfileServers: []string{}}
	stalePin := pinnedWildcard
	stalePin.ProfileServers = []string{} // pinned profile deleted: deny-all, same name
	// An agent whose token carries NO server grant. Token creation normalises
	// an empty list to ["*"], but a server-edition rotation that narrows the
	// grant persists nil (intersectAllowedServers), and every dispatch gate
	// (auth.CanAccessServer, serverInScope) treats that as deny-all.
	emptyGrant := Authorization{CallerKind: CallerKindAgent, Principal: "empty",
		AllowedServers: nil, Permissions: []string{"read"}}
	emptyGrantList := emptyGrant
	emptyGrantList.Principal, emptyGrantList.AllowedServers = "empty-list", []string{}
	emptyGrantWider := Authorization{CallerKind: CallerKindAgent, Principal: "empty-wider",
		AllowedServers: nil, Permissions: []string{"read", "write", "destructive"}}

	cases := []struct {
		name     string
		producer Authorization
		reader   Authorization
		want     bool
	}{
		// Administrator reader qualifies for ANY snapshot, whatever its profile.
		{"profile-bound admin reads an unscoped admin entry", admin, adminInProfile, true},
		{"narrower-profile admin reads a wider-profile admin entry", adminInWiderProfile, adminInProfile, true},
		{"wider-profile admin reads a narrower-profile admin entry", adminInProfile, adminInWiderProfile, true},
		{"empty-profile admin reads an unscoped admin entry", admin, adminInEmptyProfile, true},
		{"empty-profile admin reads a profile-bound admin entry", adminInProfile, adminInEmptyProfile, true},
		{"deleted-profile admin reads an unscoped admin entry", admin, adminInDeletedProfile, true},
		{"deleted-profile admin reads its own earlier profile-bound entry", adminInProfile, adminInDeletedProfile, true},
		{"empty-profile admin reads its own deny-all-stamped entry", adminInEmptyProfile, adminInEmptyProfile, true},
		{"profile-bound admin reads an unscoped agent entry", broad, adminInProfile, true},
		{"profile-bound admin reads a wildcard agent entry", wildcard, adminInProfile, true},
		{"empty-profile admin reads a pinned agent entry", pinnedWildcard, adminInEmptyProfile, true},
		{"profile-bound admin_user reads an unscoped admin entry", admin, adminUserInProfile, true},
		{"profile-bound admin_user reads an unscoped admin_user entry", adminUser, adminUserInProfile, true},
		{"unscoped admin reads a profile-bound admin entry (unchanged)", adminInProfile, admin, true},

		// Agent reader never qualifies for an administrator snapshot.
		{"wildcard full-permission agent cannot read an unscoped admin entry", admin, wildcard, false},
		{"wildcard agent cannot read a profile-bound admin entry", adminInProfile, wildcard, false},
		{"pinned wildcard agent cannot read an admin entry bound to the same profile", adminInProfile, pinnedWildcard, false},
		{"wildcard agent cannot read an admin_user entry", adminUser, wildcard, false},
		{"wildcard agent cannot read an empty-profile admin entry", adminInEmptyProfile, wildcard, false},

		// Between agent snapshots the dimension checks are unchanged.
		{"pinned wildcard agent cannot read an unpinned agent entry", broad, pinnedWildcard, false},
		{"pinned wildcard agent cannot read an unpinned wildcard entry", wildcard, pinnedWildcard, false},
		{"session-profiled agent cannot read an unscoped agent entry", broad, agentInSessionProfile, false},
		{"unscoped agent reads its session-profiled entry", agentInSessionProfile, broad, true},
		{"wildcard agent reads a narrower agent entry", broad, wildcard, true},

		// Deny-all guard applies to AGENT readers only.
		{"empty-profile agent reads nothing: unscoped agent entry", broad, agentInEmptyProfile, false},
		{"empty-profile agent reads nothing: its own deny-all-stamped entry", agentInEmptyProfile, agentInEmptyProfile, false},
		{"stale pin reads nothing: its own earlier entry", pinnedWildcard, stalePin, false},
		{"stale pin reads nothing: its own deny-all-stamped entry", stalePin, stalePin, false},

		// Codex round 1: an empty server grant is deny-all everywhere else
		// (CanAccessServer, serverInScope), so an empty-grant agent could not
		// have produced ANY entry — coversServers([], []) must not let it
		// redeem an identically-stamped one.
		{"empty-grant agent reads nothing: its own identically-stamped entry (nil)", emptyGrant, emptyGrant, false},
		{"empty-grant agent reads nothing: its own identically-stamped entry ([])", emptyGrantList, emptyGrantList, false},
		{"empty-grant agent reads nothing: nil vs [] are the same deny-all", emptyGrantList, emptyGrant, false},
		{"empty-grant agent reads nothing: another empty-grant agent's entry", emptyGrant, emptyGrantWider, false},
		{"empty-grant agent reads nothing: unscoped agent entry", broad, emptyGrant, false},
		{"empty-grant agent reads nothing: admin entry", admin, emptyGrant, false},
		{"broad agent reads an empty-grant agent's entry (it covers the empty set)", emptyGrant, broad, true},
		{"admin reads an empty-grant agent's entry (kind first)", emptyGrant, admin, true},
		{"profile-bound admin reads an empty-grant agent's entry (kind first)", emptyGrant, adminInProfile, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.producer.CouldHaveProduced(tc.reader); got != tc.want {
				t.Fatalf("producer=%+v reader=%+v: got %v want %v", tc.producer, tc.reader, got, tc.want)
			}
		})
	}
}
