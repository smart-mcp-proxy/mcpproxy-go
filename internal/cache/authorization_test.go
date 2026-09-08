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
		{"admin bound to a URL profile cannot read an unscoped admin entry", admin, adminInProfile, false},
		{"unscoped admin reads profile-bound admin entry", adminInProfile, admin, true},
		{"profile whose servers cover the producer's profile", adminInProfile, adminInWiderProfile, true},
		{"profile whose servers do not cover the producer's profile", adminInWiderProfile, adminInProfile, false},
		{"deleted profile: same name, deny-all scope, cannot read", adminInProfile, adminInDenyAll, false},
		{"stale pin (profile deleted) cannot read its own earlier entry", pinned, stalePin, false},
		{"deny-all reader matches nothing, not even a deny-all-stamped entry", stalePin, stalePin, false},
		{"deny-all admin reader matches nothing", adminInDenyAll, adminInDenyAll, false},
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
// They are treated as produced by an unrestricted caller with no identity:
// any unrestricted reader (anonymous /mcp included) may page them, no agent
// or user may.
func TestGetRecordsAs_LegacyEntryWithoutProducer(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	m, err := NewManager(db, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	if err := m.Store("legacy", "github:list", nil, `{"items":[1,2,3]}`, "items", 3); err != nil {
		t.Fatal(err)
	}
	agent := Authorization{CallerKind: CallerKindAgent, AllowedServers: []string{"*"}, Permissions: []string{"read"}}
	if _, err := m.GetRecordsAs("legacy", 0, 10, agent); !errors.Is(err, ErrUnauthorizedRead) {
		t.Fatalf("agent reading a legacy entry: got %v, want ErrUnauthorizedRead", err)
	}
	if _, err := m.GetRecordsAs("legacy", 0, 10, Authorization{CallerKind: CallerKindAdmin}); err != nil {
		t.Fatalf("admin reading a legacy entry: %v", err)
	}
	if _, err := m.GetRecordsAs("legacy", 0, 10, Authorization{CallerKind: CallerKindAnonymous}); err != nil {
		t.Fatalf("anonymous reading a legacy entry: %v", err)
	}
	user := Authorization{CallerKind: CallerKindUser, Principal: "u1"}
	if _, err := m.GetRecordsAs("legacy", 0, 10, user); !errors.Is(err, ErrUnauthorizedRead) {
		t.Fatalf("user reading a legacy entry: got %v, want ErrUnauthorizedRead", err)
	}
}

// A refused read must not count as a hit or bump the entry's access stats —
// otherwise the refusal is visible as "someone read this" in the stats.
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
	if before != after {
		t.Fatalf("refused read changed stats: before=%+v after=%+v", before, after)
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
