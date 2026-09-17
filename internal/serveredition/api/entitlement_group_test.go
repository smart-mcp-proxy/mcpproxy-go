//go:build server

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	teamsauth "github.com/smart-mcp-proxy/mcpproxy-go/internal/serveredition/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/serveredition/broker"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/serveredition/users"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 107 T070 (US1, FR-004/FR-010, contracts/entitlement-predicate.md §1).
//
// The predicate contract adds a group term:
//
//	grant(u)     = ⋃ access.group_servers[g] for g ∈ u.Groups ∪ (access.default_servers if no group matches)
//	entitled(u)  = personal(u) ∪ { s ∈ shared : access == nil ∨ s ∈ grant(u) }   # tenants
//
// This file's blueprint mirrors internal/serveredition's two-fixture oracle
// (fixture_oracle_test.go, T067) — eng -> [a], ops -> [a, b], empty
// default_servers — but that oracle lives in package serveredition's _test.go
// files, which are not importable from this sibling package, so the fixture
// (users, shared servers, the two-fixture/status-parity assertions) is
// rebuilt locally, directly over the production per-user door: UserHandlers +
// UserActivityHandlers + CredentialHandlers wired through
// RegisterRoutesWithPrefix, exactly as internal/serveredition/setup.go wires
// them (the same shape user_handlers_shared_guard_test.go's
// perUserDoorRouter uses).
//
// Every test below is [behaviour-red] against HEAD: entitledServerNames
// (user_handlers.go:808) reads only `sc.Shared`, never `User.Groups` or an
// `access` grant (the config.ServerEditionConfig field itself does not exist
// until T074) — so today's per-user doors show a tenant EVERY Shared server
// regardless of group, exactly the #1166-shaped gap FR-010 forbids. T075
// (entitledServerNamesFor) is what turns these green. The one exception is
// TestEntitlementGroup_DanaAdminProjectionUnchanged, which is a PIN: an
// administrator's view must stay byte-for-byte what it is today (SC-006), so
// that assertion is expected to pass now and must keep passing after T075.

// --- Fixture blueprint (mirrors fixtureAccessBlueprint, contracts/entitlement-predicate.md §1) ---
//
//	eng -> [a]
//	ops -> [a, b]
//	default_servers: [] (no group match => deny-all for non-administrators)
//
// Fixture A carries all three shared servers (a, b, a__b — the collision-shaped
// name exercising the server__tool direct-id separator); fixture B carries
// only "a". Neither group grants a__b to anyone: it exists only to prove a
// caller's view does not depend on what THEY cannot see existing.

const (
	groupSentinelA  = "sentinel-grp-a-3f9c7e1b2d4a"
	groupSentinelB  = "sentinel-grp-b-8a2d5c40f716"
	groupSentinelAB = "sentinel-grp-ab-1e6f09378ac2"
)

// groupAbsentServerName never exists in any fixture built by this file — the
// "nonexistent" arm of every status-parity assertion.
const groupAbsentServerName = "group-absent-Zq9"

// groupSharedServer builds a Shared, brokered admin server so the same
// fixture set exercises /user/servers, /user/diagnostics AND
// /user/credentials* with one blueprint (brokerEntitled requires
// Shared && AuthBroker != nil).
func groupSharedServer(name, sentinel string) *config.ServerConfig {
	return &config.ServerConfig{
		Name:     name,
		URL:      "https://" + name + ".example.com/mcp-" + sentinel,
		Protocol: "http",
		Shared:   true,
		Enabled:  true,
		AuthBroker: &config.AuthBrokerConfig{
			Mode:                  config.AuthBrokerModeOAuthConnect,
			AuthorizationEndpoint: "https://as.example.com/authorize",
			TokenEndpoint:         "https://idp.example.com/token",
			ClientID:              "client-" + name,
			Scopes:                []string{"repo"},
		},
	}
}

func groupFixtureServersA() []*config.ServerConfig {
	return []*config.ServerConfig{
		groupSharedServer("a", groupSentinelA),
		groupSharedServer("b", groupSentinelB),
		groupSharedServer("a__b", groupSentinelAB),
	}
}

func groupFixtureServersB() []*config.ServerConfig {
	return []*config.ServerConfig{groupSharedServer("a", groupSentinelA)}
}

// --- Rig: the production per-user door over one admin server set ---

// groupFixtureRig wires UserHandlers, UserActivityHandlers and
// CredentialHandlers over a REAL *users.UserStore and *storage.Manager,
// registered through RegisterRoutesWithPrefix (the production form). The
// active caller is switched per-call via actAs, the same pattern
// tokenTestRig (user_token_isolation_test.go) uses.
type groupFixtureRig struct {
	router *chi.Mux
	users  *users.UserStore
	store  *storage.Manager
	as     *auth.AuthContext

	alice, bob, carol, dana *users.User
}

func (rig *groupFixtureRig) actAs(ac *auth.AuthContext) { rig.as = ac }

func (rig *groupFixtureRig) get(target string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	w := httptest.NewRecorder()
	rig.router.ServeHTTP(w, req)
	return w
}

func (rig *groupFixtureRig) post(target, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	rig.router.ServeHTTP(w, req)
	return w
}

func (rig *groupFixtureRig) delete(target string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodDelete, target, nil)
	w := httptest.NewRecorder()
	rig.router.ServeHTTP(w, req)
	return w
}

// setUserGroups changes a user's stored Groups claim, standing in for an
// administrator regrouping a tenant (or their next `oidc` login replacing the
// claim, FR-008) between the two calls a test makes.
func (rig *groupFixtureRig) setUserGroups(t *testing.T, u *users.User, groups []string) {
	t.Helper()
	u.Groups = groups
	require.NoError(t, rig.users.UpdateUser(u))
}

// groupUserCtx/groupAdminCtx build the AuthContext the per-user door reads
// getUserID from; these carry no Groups (Groups lives on the stored
// users.User record, read by entitledServerNamesFor). They are session-cookie
// principals, the only kind the minting doors admit (T078).
func groupUserCtx(u *users.User) *auth.AuthContext {
	return withCookieKind(auth.UserContext(u.ID, u.Email, u.DisplayName, u.Provider))
}

func groupAdminCtx(u *users.User) *auth.AuthContext {
	return withCookieKind(auth.AdminUserContext(u.ID, u.Email, u.DisplayName, u.Provider))
}

// groupFixtureAccess is the live `server_edition.access` block of the
// blueprint (eng -> [a], ops -> [a, b], empty default_servers), installed on
// the predicate exactly as setup.go installs the live block through
// ServerEditionConfigProvider.
func groupFixtureAccess() *config.ServerEditionConfig {
	return &config.ServerEditionConfig{
		Enabled:     true,
		AdminEmails: []string{"dana@example.com"},
		Access: &config.ServerEditionAccessConfig{
			GroupServers:   map[string][]string{"eng": {"a"}, "ops": {"a", "b"}},
			DefaultServers: []string{},
		},
	}
}

func newGroupFixtureRig(t *testing.T, admin []*config.ServerConfig) *groupFixtureRig {
	t.Helper()
	logger := zap.NewNop().Sugar()

	dbPath := t.TempDir()
	db, err := bbolt.Open(filepath.Join(dbPath, "users.db"), 0600, &bbolt.Options{Timeout: time.Second})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	userStore := users.NewUserStore(db)
	require.NoError(t, userStore.EnsureBuckets())

	mgr, err := storage.NewManager(t.TempDir(), logger)
	require.NoError(t, err)
	t.Cleanup(func() { mgr.Close() })

	rig := &groupFixtureRig{users: userStore, store: mgr}

	rig.alice = users.NewUser("alice@example.com", "Alice", "google", "sub-alice")
	rig.alice.Groups = []string{"eng"}
	require.NoError(t, userStore.CreateUser(rig.alice))

	rig.bob = users.NewUser("bob@example.com", "Bob", "google", "sub-bob")
	// Bob's Groups stays empty: with an empty access.default_servers this is
	// the deny-all tenant the security invariant requires.
	require.NoError(t, userStore.CreateUser(rig.bob))

	rig.carol = users.NewUser("carol@example.com", "Carol", "google", "sub-carol")
	rig.carol.Groups = []string{"ops"}
	require.NoError(t, userStore.CreateUser(rig.carol))

	rig.dana = users.NewUser("dana@example.com", "Dana", "google", "sub-dana")
	require.NoError(t, userStore.CreateUser(rig.dana))

	userHandlers := NewUserHandlers(userStore, StaticAdminServers(admin), mgr, tokenTestHMACKey, logger)
	userHandlers.SetServerEditionConfigProvider(teamsauth.StaticServerEditionConfig(groupFixtureAccess()))
	activityHandlers := NewUserActivityHandlers(nil, userStore, admin, logger)
	activityHandlers.SetEntitlement(userHandlers)
	credHandlers := NewCredentialHandlers(credTestStore(t), admin, nil, logger)
	credHandlers.SetEntitlement(userHandlers)

	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := auth.WithAuthContext(req.Context(), rig.as)
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	userHandlers.RegisterRoutesWithPrefix(r, "/api/v1")
	activityHandlers.RegisterRoutesWithPrefix(r, "/api/v1")
	credHandlers.RegisterRoutesWithPrefix(r, "/api/v1")
	rig.router = r

	return rig
}

// --- Two-fixture oracle (local to this package; see file comment) ---

type groupTwoFixture struct {
	A, B *groupFixtureRig
}

func newGroupTwoFixture(t *testing.T) *groupTwoFixture {
	t.Helper()
	return &groupTwoFixture{
		A: newGroupFixtureRig(t, groupFixtureServersA()),
		B: newGroupFixtureRig(t, groupFixtureServersB()),
	}
}

type groupTwoFixtureOp func(rig *groupFixtureRig) (status int, body []byte)

var groupNondeterministicKeyPattern = regexp.MustCompile(`(?i)^(id|.*_id|created|updated|.*_at|expires_in|token|.*_token|request_id)$`)

func groupNormalize(t *testing.T, body []byte) []byte {
	t.Helper()
	if !json.Valid(body) {
		return body
	}
	var v interface{}
	require.NoError(t, json.Unmarshal(body, &v))
	groupNormalizeValue(v)
	out, err := json.Marshal(v)
	require.NoError(t, err)
	return out
}

func groupNormalizeValue(v interface{}) {
	switch tv := v.(type) {
	case map[string]interface{}:
		for k, child := range tv {
			if groupNondeterministicKeyPattern.MatchString(k) {
				tv[k] = "<normalized>"
				continue
			}
			groupNormalizeValue(child)
		}
	case []interface{}:
		for _, child := range tv {
			groupNormalizeValue(child)
		}
	}
}

// assertGroupTwoFixture proves FR-010: the same caller's view must not vary
// with what OTHER, out-of-scope servers happen to exist.
func assertGroupTwoFixture(t *testing.T, tf *groupTwoFixture, op groupTwoFixtureOp, forbiddenSubstrings ...string) {
	t.Helper()

	statusA, bodyA := op(tf.A)
	statusB, bodyB := op(tf.B)

	require.Equalf(t, statusA, statusB,
		"status must be identical across fixtures for the same caller (FR-010)\nA (%d): %s\nB (%d): %s",
		statusA, bodyA, statusB, bodyB)

	normA := groupNormalize(t, bodyA)
	normB := groupNormalize(t, bodyB)
	if json.Valid(normA) && json.Valid(normB) {
		require.JSONEqf(t, string(normB), string(normA),
			"fixture A and fixture B bodies must be identical once normalised (FR-010)\nA: %s\nB: %s", bodyA, bodyB)
	} else {
		require.Equalf(t, string(normB), string(normA),
			"fixture A and fixture B bodies must be identical (FR-010)\nA: %s\nB: %s", bodyA, bodyB)
	}

	for _, s := range forbiddenSubstrings {
		assert.NotContainsf(t, string(bodyA), s, "fixture A response leaked %q", s)
		assert.NotContainsf(t, string(bodyB), s, "fixture B response leaked %q", s)
	}
}

// assertGroupStatusParity proves a by-name door's refusal for a name that
// EXISTS but is out of the caller's scope is byte-identical (status + body,
// normalised) to its refusal for a name that never existed anywhere — the
// non-disclosing-refusal oracle (memory/project_entitlement_test_oracle.md):
// never "body must not contain the name" (a 404 legitimately echoes the
// caller's own path param), always parity between the two refusals. The
// echoed request names (hiddenName, absentName — the caller's OWN input) are
// the one legitimate difference between the two bodies, so they are
// normalised to a placeholder before the comparison; everything else must be
// byte-identical.
func assertGroupStatusParity(t *testing.T, hidden, absent *httptest.ResponseRecorder, hiddenName, absentName string) {
	t.Helper()

	require.Equalf(t, absent.Code, hidden.Code,
		"an out-of-scope EXISTING name and a nonexistent name must refuse with the same status\nhidden (%d): %s\nabsent (%d): %s",
		hidden.Code, hidden.Body.String(), absent.Code, absent.Body.String())

	normHidden := groupNormalize(t, groupNormalizeEchoedName(hidden.Body.Bytes(), hiddenName))
	normAbsent := groupNormalize(t, groupNormalizeEchoedName(absent.Body.Bytes(), absentName))
	if json.Valid(normHidden) && json.Valid(normAbsent) {
		require.JSONEqf(t, string(normAbsent), string(normHidden),
			"an out-of-scope existing name and a nonexistent name must refuse with the same body (non-disclosing refusal)\nhidden: %s\nabsent: %s",
			hidden.Body.String(), absent.Body.String())
	} else {
		require.Equalf(t, string(normAbsent), string(normHidden),
			"an out-of-scope existing name and a nonexistent name must refuse with the same body (non-disclosing refusal)\nhidden: %s\nabsent: %s",
			hidden.Body.String(), absent.Body.String())
	}
}

// groupNormalizeEchoedName replaces the quoted request name a refusal echoes
// (`Server "b" not found`, JSON-escaped as \"b\") with a fixed placeholder.
func groupNormalizeEchoedName(body []byte, name string) []byte {
	if name == "" {
		return body
	}
	out := strings.ReplaceAll(string(body), `\"`+name+`\"`, `\"<name>\"`)
	return []byte(out)
}

func sharedNames(list []*ServerResponse) []string {
	out := make([]string, 0, len(list))
	for _, s := range list {
		if s != nil && s.ServerConfig != nil {
			out = append(out, s.Name)
		}
	}
	return out
}

func diagnosticNames(list []*ServerDiagnostic) []string {
	out := make([]string, 0, len(list))
	for _, d := range list {
		out = append(out, d.Name)
	}
	return out
}

// --- /user/servers ---

// TestEntitlementGroup_AliceServersListParityAcrossFixtures is the FR-010
// oracle for the list door: Alice (group "eng", entitled to [a]) must see the
// SAME /user/servers response whether or not "b"/"a__b" exist to be hidden
// from her.
//
// [behaviour-red]: HEAD's entitledServerNames has no group term, so fixture
// A's response additionally lists "b" and "a__b" (both Shared) — the two
// fixtures diverge and both sentinels leak.
func TestEntitlementGroup_AliceServersListParityAcrossFixtures(t *testing.T) {
	tf := newGroupTwoFixture(t)
	op := func(rig *groupFixtureRig) (int, []byte) {
		rig.actAs(groupUserCtx(rig.alice))
		w := rig.get("/api/v1/user/servers")
		return w.Code, w.Body.Bytes()
	}
	assertGroupTwoFixture(t, tf, op, groupSentinelB, groupSentinelAB)
}

// TestEntitlementGroup_AliceServersListNarrowedToGroup is the direct
// (single-fixture) form of the same requirement: Alice's Shared list must be
// exactly her group's grant, [a].
//
// [behaviour-red]: HEAD returns all three Shared servers.
func TestEntitlementGroup_AliceServersListNarrowedToGroup(t *testing.T) {
	rig := newGroupFixtureRig(t, groupFixtureServersA())
	rig.actAs(groupUserCtx(rig.alice))

	w := rig.get("/api/v1/user/servers")
	require.Equal(t, http.StatusOK, w.Code)

	var resp ServerListResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.ElementsMatch(t, []string{"a"}, sharedNames(resp.Shared),
		"Alice's group \"eng\" grants only [a] (contracts/entitlement-predicate.md §1); "+
			"entitledServerNames ignores access.group_servers on HEAD and returns every Shared server instead")
}

// TestEntitlementGroup_AliceByNameServerParityWithAbsent proves the by-name
// door for a server Alice cannot see ("b", "a__b" — Shared but outside her
// group's grant) refuses exactly like a name that never existed.
//
// [behaviour-red]: HEAD's getServer finds any Shared server by name
// regardless of group, so both hidden names 200 today instead of 404.
func TestEntitlementGroup_AliceByNameServerParityWithAbsent(t *testing.T) {
	rig := newGroupFixtureRig(t, groupFixtureServersA())
	rig.actAs(groupUserCtx(rig.alice))

	for _, hidden := range []string{"b", "a__b"} {
		t.Run(hidden, func(t *testing.T) {
			wHidden := rig.get("/api/v1/user/servers/" + hidden)
			wAbsent := rig.get("/api/v1/user/servers/" + groupAbsentServerName)
			assertGroupStatusParity(t, wHidden, wAbsent, hidden, groupAbsentServerName)
		})
	}
}

// --- /user/diagnostics ---

// TestEntitlementGroup_AliceDiagnosticsParityAcrossFixtures is the FR-010
// oracle for the diagnostics door.
//
// [behaviour-red]: fixture A additionally reports "b" and "a__b".
func TestEntitlementGroup_AliceDiagnosticsParityAcrossFixtures(t *testing.T) {
	tf := newGroupTwoFixture(t)
	op := func(rig *groupFixtureRig) (int, []byte) {
		rig.actAs(groupUserCtx(rig.alice))
		w := rig.get("/api/v1/user/diagnostics")
		return w.Code, w.Body.Bytes()
	}
	assertGroupTwoFixture(t, tf, op)
}

// TestEntitlementGroup_AliceDiagnosticsNarrowedToGroup is the direct form:
// Alice's diagnostics must list only her group's grant.
//
// [behaviour-red]: HEAD reports every Shared server.
func TestEntitlementGroup_AliceDiagnosticsNarrowedToGroup(t *testing.T) {
	rig := newGroupFixtureRig(t, groupFixtureServersA())
	rig.actAs(groupUserCtx(rig.alice))

	w := rig.get("/api/v1/user/diagnostics")
	require.Equal(t, http.StatusOK, w.Code)

	var resp DiagnosticsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.ElementsMatch(t, []string{"a"}, diagnosticNames(resp.Servers),
		"Alice's diagnostics must list only her entitled Shared server, not every Shared server")
}

// --- /user/credentials* ---

// TestEntitlementGroup_AliceCredentialsParityAcrossFixtures is the FR-010
// oracle for the credential-status list.
//
// [behaviour-red]: fixture A additionally reports "b" and "a__b" (3 entries
// vs fixture B's 1).
func TestEntitlementGroup_AliceCredentialsParityAcrossFixtures(t *testing.T) {
	tf := newGroupTwoFixture(t)
	op := func(rig *groupFixtureRig) (int, []byte) {
		rig.actAs(groupUserCtx(rig.alice))
		w := rig.get("/api/v1/user/credentials")
		return w.Code, w.Body.Bytes()
	}
	assertGroupTwoFixture(t, tf, op)
}

// TestEntitlementGroup_AliceCredentialByNameDoorsParityWithAbsent covers the
// by-name credential doors (connect, callback, delete) named in the dispatch
// prompt as "/user/credentials*".
//
// [behaviour-red]: brokerServerByName/brokerEntitled select on Shared alone,
// so "b" and "a__b" resolve like any entitled server today.
func TestEntitlementGroup_AliceCredentialByNameDoorsParityWithAbsent(t *testing.T) {
	rig := newGroupFixtureRig(t, groupFixtureServersA())
	rig.actAs(groupUserCtx(rig.alice))

	for _, hidden := range []string{"b", "a__b"} {
		t.Run(hidden+"/connect", func(t *testing.T) {
			wHidden := rig.get("/api/v1/user/credentials/" + hidden + "/connect")
			wAbsent := rig.get("/api/v1/user/credentials/" + groupAbsentServerName + "/connect")
			assertGroupStatusParity(t, wHidden, wAbsent, hidden, groupAbsentServerName)
		})
		t.Run(hidden+"/delete", func(t *testing.T) {
			wHidden := rig.delete("/api/v1/user/credentials/" + hidden)
			wAbsent := rig.delete("/api/v1/user/credentials/" + groupAbsentServerName)
			assertGroupStatusParity(t, wHidden, wAbsent, hidden, groupAbsentServerName)
		})
	}
}

// countingCredentialStore wraps a real broker.CredentialStore and counts
// Get calls, so a test can assert the credential-store timing class from the
// dispatch prompt: a by-name door must resolve entitlement BEFORE touching
// the store, so a caller's out-of-scope (but existing) server performs no
// MORE store reads than an absent one.
type countingCredentialStore struct {
	broker.CredentialStore
	getCalls int32
}

func (s *countingCredentialStore) Get(userID, serverKey string) (*broker.UpstreamCredential, error) {
	atomic.AddInt32(&s.getCalls, 1)
	return s.CredentialStore.Get(userID, serverKey)
}

// TestEntitlementGroup_CredentialsListReadsStoreOnlyForEntitledServer is the
// timing-class pin for the credentials LIST door (contracts/
// entitlement-predicate.md's "resolve entitlement before any store lookup",
// applied here as: no credential-store read for a server outside the
// caller's grant).
//
// [behaviour-red]: brokerServerList() (credential_handlers.go:354) selects on
// Shared alone, so listCredentials reads the store once per Shared+brokered
// server — 3 Get calls for Alice today (a, b, a__b) instead of 1 (a).
func TestEntitlementGroup_CredentialsListReadsStoreOnlyForEntitledServer(t *testing.T) {
	admin := groupFixtureServersA()
	counting := &countingCredentialStore{CredentialStore: credTestStore(t)}

	// Alice's record (with her groups) must be in the store the predicate
	// reads, and the predicate must see the blueprint's access block.
	userStore := newFixtureUserStore(t)
	alice := users.NewUser("alice@example.com", "Alice", "google", "sub-alice")
	alice.Groups = []string{"eng"}
	require.NoError(t, userStore.CreateUser(alice))

	handlers := NewCredentialHandlers(counting, admin, nil, zap.NewNop().Sugar())
	predicate := NewUserHandlers(userStore, StaticAdminServers(admin), nil, nil, zap.NewNop().Sugar())
	predicate.SetServerEditionConfigProvider(teamsauth.StaticServerEditionConfig(groupFixtureAccess()))
	handlers.SetEntitlement(predicate)
	router := credRouter(t, handlers, groupUserCtx(alice))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/user/credentials", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	assert.Equal(t, int32(1), atomic.LoadInt32(&counting.getCalls),
		"the credential store must be read only for Alice's entitled server (\"a\"); "+
			"brokerServerList selects on Shared alone on HEAD and reads the store for every "+
			"Shared+brokered server regardless of group, including ones outside her grant")
}

// --- Bob: empty Groups, empty default_servers => deny-all ---

// TestEntitlementGroup_BobEmptyGroupsSeesNoSharedServers pins the security
// invariant: Bob's Groups is empty and access.default_servers is empty, so he
// is entitled to nothing shared, in EITHER fixture.
//
// [behaviour-red]: HEAD grants every Shared server regardless of group, so
// Bob sees "a" in both fixtures.
func TestEntitlementGroup_BobEmptyGroupsSeesNoSharedServers(t *testing.T) {
	for _, admin := range [][]*config.ServerConfig{groupFixtureServersA(), groupFixtureServersB()} {
		rig := newGroupFixtureRig(t, admin)
		rig.actAs(groupUserCtx(rig.bob))

		w := rig.get("/api/v1/user/servers")
		require.Equal(t, http.StatusOK, w.Code)

		var resp ServerListResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Empty(t, sharedNames(resp.Shared),
			"Bob's empty Groups matches no access.group_servers key and access.default_servers "+
				"is empty — deny-all for non-administrators (security invariant)")
	}
}

// --- Carol: group "ops" -> [a, b], never a__b ---

// TestEntitlementGroup_CarolOpsGroupSeesAAndBNotCollision proves the grant is
// narrow-only: ops names [a, b] explicitly and nothing grants a__b to anyone.
//
// [behaviour-red]: HEAD also returns "a__b" for Carol.
func TestEntitlementGroup_CarolOpsGroupSeesAAndBNotCollision(t *testing.T) {
	rig := newGroupFixtureRig(t, groupFixtureServersA())
	rig.actAs(groupUserCtx(rig.carol))

	w := rig.get("/api/v1/user/servers")
	require.Equal(t, http.StatusOK, w.Code)

	var resp ServerListResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.ElementsMatch(t, []string{"a", "b"}, sharedNames(resp.Shared),
		"Carol's group \"ops\" grants [a, b] (contracts/entitlement-predicate.md §1's "+
			"access.group_servers); a__b is granted to no group and must stay hidden from her too")
}

// --- Dana: administrator projection unchanged (SC-006 parity pin) ---

// TestEntitlementGroup_DanaAdminProjectionUnchanged is a PIN, not a red test:
// entitled(admin) is the whole configuration, unchanged by the group term
// (contracts/entitlement-predicate.md §1, FR-004 "administrator projection
// unchanged", SC-006). This assertion is expected to hold on HEAD and after
// T075 alike.
func TestEntitlementGroup_DanaAdminProjectionUnchanged(t *testing.T) {
	rig := newGroupFixtureRig(t, groupFixtureServersA())
	rig.actAs(groupAdminCtx(rig.dana))

	w := rig.get("/api/v1/user/servers")
	require.Equal(t, http.StatusOK, w.Code)

	var resp ServerListResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.ElementsMatch(t, []string{"a", "b", "a__b"}, sharedNames(resp.Shared),
		"an administrator's per-user door view must keep seeing the whole configuration")
}

// --- Mint scope (/user/tokens) ---

// TestEntitlementGroup_MintStarNarrowsToGroupEntitlement: minting with the
// wildcard must materialise into the CALLER's group grant, not the whole
// Shared set.
//
// [behaviour-red]: HEAD's resolveTokenServerScope materialises "*" into
// entitledServerNames' ungrouped result — [a, b, a__b] for Alice, not [a].
func TestEntitlementGroup_MintStarNarrowsToGroupEntitlement(t *testing.T) {
	rig := newGroupFixtureRig(t, groupFixtureServersA())
	rig.actAs(groupUserCtx(rig.alice))

	w := rig.post("/api/v1/user/tokens", `{"name":"alice-star","permissions":["read"],"allowed_servers":["*"]}`)
	require.Equal(t, http.StatusCreated, w.Code, "positive control: minting must succeed at all")

	var resp AgentTokenResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, []string{"a"}, resp.AllowedServers,
		"Alice's \"*\" must materialise into her group grant [a], not every Shared server")
}

// TestEntitlementGroup_MintRejectsOutOfGroupServer: naming a Shared server
// outside the caller's group grant must be refused with the SAME uniform
// message resolveTokenServerScope already uses for "not yours"/"does not
// exist" (never distinguishing "exists but ungranted" from "absent").
//
// [behaviour-red]: HEAD accepts "b" for Alice (it is Shared) and mints 201.
func TestEntitlementGroup_MintRejectsOutOfGroupServer(t *testing.T) {
	rig := newGroupFixtureRig(t, groupFixtureServersA())
	rig.actAs(groupUserCtx(rig.alice))

	w := rig.post("/api/v1/user/tokens", `{"name":"alice-a-b","permissions":["read"],"allowed_servers":["a","b"]}`)
	require.Equal(t, http.StatusBadRequest, w.Code,
		"\"b\" is Shared but outside Alice's \"eng\" group grant and must be refused like any unentitled name")

	var envelope struct {
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
	assert.Equal(t, `server "b" is not available to you`, envelope.Message)
}

// TestEntitlementGroup_MintRejectionParityHiddenVsAbsentServer: minting
// against "a__b" (Shared, but granted to no group) must refuse exactly like
// minting against a name that never existed.
//
// [behaviour-red]: HEAD accepts "a__b" (it is Shared) and mints 201, while
// the absent name 400s — the two refusals are not even the same status.
func TestEntitlementGroup_MintRejectionParityHiddenVsAbsentServer(t *testing.T) {
	rig := newGroupFixtureRig(t, groupFixtureServersA())
	rig.actAs(groupUserCtx(rig.alice))

	wHidden := rig.post("/api/v1/user/tokens", `{"name":"alice-hidden","permissions":["read"],"allowed_servers":["a__b"]}`)
	wAbsent := rig.post("/api/v1/user/tokens", `{"name":"alice-absent","permissions":["read"],"allowed_servers":["`+groupAbsentServerName+`"]}`)
	assertGroupStatusParity(t, wHidden, wAbsent, "a__b", groupAbsentServerName)
}

// TestEntitlementGroup_RotateRenarrowsAfterGroupChange: rotation re-checks
// against the CURRENT entitlement (narrowScopeToEntitled), which after T075
// must mean the current GROUP grant, not just the current Shared set.
//
// [behaviour-red]: Carol mints ["a","b"] while in "ops" ([a,b]); her group is
// then changed to "eng" ([a]). HEAD's entitledServerNames never reads Groups,
// so the regenerate call still resolves her entitlement as every Shared
// server and "b" survives the rotation instead of being trimmed.
func TestEntitlementGroup_RotateRenarrowsAfterGroupChange(t *testing.T) {
	rig := newGroupFixtureRig(t, groupFixtureServersA())
	rig.actAs(groupUserCtx(rig.carol))

	wMint := rig.post("/api/v1/user/tokens", `{"name":"carol-ops","permissions":["read"],"allowed_servers":["a","b"]}`)
	require.Equal(t, http.StatusCreated, wMint.Code, "positive control: carol's \"ops\" group grants [a,b] at mint time")

	rig.setUserGroups(t, rig.carol, []string{"eng"}) // narrowed: "eng" only grants [a]

	wRotate := rig.post("/api/v1/user/tokens/carol-ops/regenerate", `{}`)
	require.Equal(t, http.StatusOK, wRotate.Code)

	var resp AgentTokenResponse
	require.NoError(t, json.Unmarshal(wRotate.Body.Bytes(), &resp))
	assert.Equal(t, []string{"a"}, resp.AllowedServers,
		"regenerate must re-check against the group grant in effect NOW "+
			"(entitledServerNamesFor(user, isAdmin) reads User.Groups live); HEAD's "+
			"entitledServerNames never reads Groups, so a standing grant to \"b\" survives "+
			"a group downgrade until this door reads Groups")
}

// TestVisibleSharedServerDoor_ReadsAdminServersExactlyOnce pins cross-review
// round 1's P1 finding: visibleSharedServers/visibleSharedServer used to call
// h.adminConfigServers() TWICE per request — once inside
// entitledServerNamesFor to compute the entitled name set, and again,
// independently, to fetch the ServerConfig objects for disclosure. A
// server-edition config hot-reload landing between those two live reads
// (FR-039 part 3 makes server_edition.access, and any other config write,
// apply without a restart) could authorize a name against one snapshot and
// disclose a *different* server's data under that name from the next one —
// e.g. a server that was Shared when the entitlement set was computed but
// has since been unshared (or replaced) still gets returned, because the
// by-name/by-set lookup never re-derives entitlement against the snapshot it
// actually reads from.
//
// The fix (tenantEntitledSnapshot) fetches the admin-config servers ONCE per
// request and threads that single snapshot through both the entitlement
// computation and the disclosure lookup, mirroring the "ONE resolver call per
// authentication" invariant this spec already holds for owner resolution.
//
// BITES: reverting visibleSharedServers/visibleSharedServer to call
// h.tenantEntitled (which does not return a snapshot) and then
// h.adminConfigServers() again makes the provider's call count 2 instead of
// 1 for a single GET /api/v1/user/servers/{name} request.
func TestVisibleSharedServerDoor_ReadsAdminServersExactlyOnce(t *testing.T) {
	logger := zap.NewNop().Sugar()

	dbPath := t.TempDir()
	db, err := bbolt.Open(filepath.Join(dbPath, "users.db"), 0600, &bbolt.Options{Timeout: time.Second})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	userStore := users.NewUserStore(db)
	require.NoError(t, userStore.EnsureBuckets())

	alice := users.NewUser("alice@example.com", "Alice", "google", "sub-alice")
	require.NoError(t, userStore.CreateUser(alice))

	admin := groupFixtureServersB() // one shared server, "a"
	var calls int32
	countingProvider := AdminServersProvider(func() []*config.ServerConfig {
		atomic.AddInt32(&calls, 1)
		return admin
	})

	userHandlers := NewUserHandlers(userStore, countingProvider, nil, nil, logger)

	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := auth.WithAuthContext(req.Context(), groupUserCtx(alice))
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	userHandlers.RegisterRoutesWithPrefix(r, "/api/v1")

	atomic.StoreInt32(&calls, 0)
	w := httptest.NewRequest(http.MethodGet, "/api/v1/user/servers/a", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, w)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, int32(1), atomic.LoadInt32(&calls),
		"GET /user/servers/{name} must read the live admin server snapshot exactly once, "+
			"not once for entitlement and once for disclosure")

	atomic.StoreInt32(&calls, 0)
	wList := httptest.NewRequest(http.MethodGet, "/api/v1/user/servers", nil)
	recList := httptest.NewRecorder()
	r.ServeHTTP(recList, wList)
	require.Equal(t, http.StatusOK, recList.Code)
	assert.Equal(t, int32(1), atomic.LoadInt32(&calls),
		"GET /user/servers must read the live admin server snapshot exactly once")
}
