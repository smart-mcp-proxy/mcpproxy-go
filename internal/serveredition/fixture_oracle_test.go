//go:build server

package serveredition

import (
	"encoding/json"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/serveredition/users"
)

// Two-fixture oracle (T067, spec 107 FR-047 part 3, contracts/entitlement-predicate.md).
//
// Every group-entitlement test from T070 onward proves the same shape: the
// caller's view must be IDENTICAL whether or not a server the caller cannot
// see happens to exist. A single fixture cannot prove that — a bug that leaks
// a hidden server (or hides one that should be visible) is invisible unless
// there is something to diff against. So T067 builds two blueprints that
// differ ONLY in which shared servers exist:
//
//	fixture A: shared servers a, b, a__b
//	fixture B: shared server  a only (b and a__b absent)
//
// Every other input — users, groups, the access grant, tokens, the active
// profile — is byte-identical between A and B. A caller entitled only to `a`
// (Alice, group "eng") must see the SAME response from BOTH fixtures; a
// caller entitled to `b` too (Carol, group "ops") sees `b` in A and a
// non-disclosing "no such server" in B, parity-checked against a name that
// never existed in either fixture (assertStatusParity; see
// memory/project_entitlement_test_oracle.md — body-must-not-contain-the-name
// is the WRONG oracle, status+body PARITY with an absent resource is right).
//
// The `access` grant below (fixtureAccessBlueprint) is installed on both
// fixtures' LIVE config (wiringHarness.setAccess), which the entitlement
// predicate reads through ServerEditionConfigProvider (T074/T075,
// contracts/entitlement-predicate.md §1). Keeping the blueprint here (rather
// than duplicating it per test file) is what makes "fixture A and fixture B
// share the same access map" a structural guarantee instead of something
// every new test has to remember to copy.

// Per-server high-entropy sentinels. Each is unique enough that its
// accidental presence in a response body can only mean the corresponding
// server's data leaked into that response — never a coincidental substring
// match against ordinary fixture text (server names, emails, etc.).
const (
	sentinelServerA  = "sentinel-a-7f3c9e1b2d4a6f80c1"
	sentinelServerB  = "sentinel-b-9a1d5c7e3f2b60813c"
	sentinelServerAB = "sentinel-ab-2c6f8a1e4b9d0357aa"
)

// fixtureAccessBlueprint is the `server_edition.access` grant both fixture A
// and fixture B are built with (contracts/entitlement-predicate.md §1):
//
//	eng -> [a]
//	ops -> [a, b]
//
// with an empty default_servers (no group match -> deny-all for
// non-administrators, per the security invariant in the dispatch prompt).
// The two fixtures differ only in whether `b` (and `a__b`) exist to be
// granted — the map itself never changes, which is what makes a
// cross-fixture diff meaningful: any behavioural difference between A and B
// for the same caller must come from server EXISTENCE, never from a
// different grant.
var fixtureAccessBlueprint = struct {
	GroupServers   map[string][]string
	DefaultServers []string
}{
	GroupServers:   map[string][]string{"eng": {"a"}, "ops": {"a", "b"}},
	DefaultServers: []string{},
}

// fixtureServerSpec describes one shared server the blueprint installs, with
// the sentinel that must appear in responses that legitimately reveal it and
// must never appear in ones that don't.
type fixtureServerSpec struct {
	Name     string
	Sentinel string
}

// fixtureSharedServers is fixture A's full shared-server set. Fixture B is
// this list filtered down to the servers whose Name is "a" — i.e. b and
// a__b (the collision-shaped name exercising the `server__tool` direct-id
// separator, contracts/entitlement-predicate.md and Spec 105) are absent.
var fixtureSharedServers = []fixtureServerSpec{
	{Name: "a", Sentinel: sentinelServerA},
	{Name: "b", Sentinel: sentinelServerB},
	{Name: "a__b", Sentinel: sentinelServerAB},
}

// fixtureHarness is one fixture's live wiringHarness plus the identities and
// raw tokens its blueprint mints, so a test can say "Alice's wildcard token
// against fixture B" without re-deriving either side.
type fixtureHarness struct {
	h *wiringHarness

	alice, bob, carol, dana *users.User

	// aliceTokenStar/aliceTokenA are raw agent-token secrets minted directly
	// against storage (bypassing the mint HTTP door, which does not yet
	// narrow against the access grant — that is T075/T076's job). They exist
	// so T071/T072 can drive storage.ValidateAgentToken and the MCP door
	// directly with a token whose REQUESTED scope is known and fixed across
	// both fixtures.
	aliceTokenStar string // requested AllowedServers: ["*"]
	aliceTokenA    string // requested AllowedServers: ["a"]
}

// twoFixture holds the fixture-A and fixture-B harnesses built from the
// shared blueprint above. assertTwoFixture and assertStatusParity are the
// two oracles every T070+/T072 test drives through it.
type twoFixture struct {
	A *fixtureHarness
	B *fixtureHarness
}

// newTwoFixture builds fixture A (a, b, a__b) and fixture B (a only) from
// the shared blueprint: same OAuth config, same admin emails (Dana), same
// users and groups, same access grant, same "ops-only" profile pinned to
// [b]. The ONLY difference is which servers exist to be shared.
//
// This is [tooling], not a test: it makes no assertion of its own. T070,
// T070a and T072 call it and then drive assertTwoFixture/assertStatusParity
// against the result.
func newTwoFixture(t *testing.T) *twoFixture {
	t.Helper()
	return &twoFixture{
		A: buildFixtureHarness(t, fixtureSharedServers),
		B: buildFixtureHarness(t, filterFixtureServers(fixtureSharedServers, "a")),
	}
}

// filterFixtureServers keeps only the named servers, preserving order — used
// to derive fixture B's server set from fixture A's without hand-duplicating
// the sentinel list (and risking the two drifting apart).
func filterFixtureServers(all []fixtureServerSpec, keep ...string) []fixtureServerSpec {
	keepSet := make(map[string]struct{}, len(keep))
	for _, name := range keep {
		keepSet[name] = struct{}{}
	}
	out := make([]fixtureServerSpec, 0, len(keep))
	for _, spec := range all {
		if _, ok := keepSet[spec.Name]; ok {
			out = append(out, spec)
		}
	}
	return out
}

// buildFixtureHarness builds one side of the two-fixture oracle: a live
// wiringHarness carrying exactly the given shared servers, an administrator
// (Dana), and three tenants —
//
//	Alice: group "eng"  -> entitled to [a]        (once T075 wires the grant)
//	Bob:   group []      -> entitled to nothing (empty access.default_servers)
//	Carol: group "ops"  -> entitled to [a, b]     (b only where it exists)
//
// plus the "ops-only" profile pinned to [b] (present in both fixtures; in B
// it warn-skips per config.ValidateProfiles' unknown-server rule since b
// does not exist there — the profile itself is still identical config
// between the two fixtures, only its EffectiveServers differs).
//
// Every server's URL carries its sentinel so any door that echoes a
// server's connection details (not just its name) can be checked for leakage
// too.
func buildFixtureHarness(t *testing.T, servers []fixtureServerSpec) *fixtureHarness {
	t.Helper()

	// `oidc`: the one provider that yields groups, and the only one under
	// which a non-empty access.group_servers validates (FR-007).
	h := newWiringHarnessWith(t, &config.ServerEditionOAuthConfig{
		Provider:     "oidc",
		ClientID:     "test-client-id",
		ClientSecret: "test-client-secret",
		IssuerURL:    "https://idp.example.com",
	})

	// Dana is the fixture's administrator; her email must be live in
	// AdminEmails for teamsauth role derivation to grant her the admin role.
	h.setAdminEmails([]string{"admin@example.com", "dana@example.com"})

	live := make([]*config.ServerConfig, 0, len(servers))
	for _, spec := range servers {
		live = append(live, &config.ServerConfig{
			Name:     spec.Name,
			URL:      "http://127.0.0.1:9/" + spec.Name + "-" + spec.Sentinel,
			Protocol: "http",
			Shared:   true,
			Enabled:  true,
		})
	}
	h.setLiveServers(live)
	h.setLiveProfiles([]config.ProfileConfig{{Name: "ops-only", Servers: []string{"b"}}})
	h.setAccess(&config.ServerEditionAccessConfig{
		GroupServers:   fixtureAccessBlueprint.GroupServers,
		DefaultServers: fixtureAccessBlueprint.DefaultServers,
	})

	fx := &fixtureHarness{h: h}

	fx.alice = h.mkUser(t, "alice@example.com")
	fx.alice.Groups = []string{"eng"}
	require.NoError(t, h.users.UpdateUser(fx.alice))

	fx.bob = h.mkUser(t, "bob@example.com")
	// Bob's groups stay empty: with an empty access.default_servers this is
	// the deny-all tenant the security invariant requires (empty/absent
	// access = deny-all for non-administrators).

	fx.carol = h.mkUser(t, "carol@example.com")
	fx.carol.Groups = []string{"ops"}
	require.NoError(t, h.users.UpdateUser(fx.carol))

	fx.dana = h.mkUser(t, "dana@example.com")

	fx.aliceTokenStar = mintFixtureToken(t, h, fx.alice, "alice-star", []string{"*"})
	fx.aliceTokenA = mintFixtureToken(t, h, fx.alice, "alice-a", []string{"a"})

	return fx
}

// mintFixtureToken creates an agent token directly against storage (not
// through the mint HTTP door — see fixtureHarness.aliceTokenStar) with the
// given REQUESTED AllowedServers, and returns the raw secret.
func mintFixtureToken(t *testing.T, h *wiringHarness, owner *users.User, name string, requested []string) string {
	t.Helper()
	raw, err := auth.GenerateToken()
	require.NoError(t, err)
	require.NoError(t, h.tokens.CreateAgentToken(auth.AgentToken{
		Name:           name,
		UserID:         owner.ID,
		AllowedServers: requested,
		Permissions:    []string{auth.PermRead},
	}, raw, h.hmacKey))
	return raw
}

// doBearer issues one request against this fixture's live router,
// authenticated as the given bearer credential (a JWT from
// teamsauth.GenerateBearerToken or a raw agent-token secret), and returns
// the status and body. It never inspects or asserts anything itself — every
// oracle in this file is built on top of it.
func (fx *fixtureHarness) doBearer(method, path, bearer string) (status int, body []byte) {
	req := httptest.NewRequest(method, path, nil)
	req.Host = "localhost:8080"
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	fx.h.router.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}

// twoFixtureOp is one request, issued once per fixture by assertTwoFixture.
// Implementations close over the identity/credential to use and call
// fx.doBearer (or drive an in-process MCP/index call) — assertTwoFixture
// only needs the resulting status and body back.
type twoFixtureOp func(fx *fixtureHarness) (status int, body []byte)

// nondeterministicKeyPattern matches JSON object keys whose values are
// expected to differ between two otherwise-identical fixtures/runs — ids,
// timestamps and token secrets minted fresh per harness — so
// normalizeNondeterministic can blank them before a structural diff.
var nondeterministicKeyPattern = regexp.MustCompile(`(?i)^(id|.*_id|created|updated|.*_at|expires_in|token|.*_token|request_id)$`)

// normalizeNondeterministic parses body as JSON and recursively replaces the
// value of any key matching nondeterministicKeyPattern with a fixed
// placeholder, returning the re-marshalled bytes. A non-JSON body (a plain
// 403/404 page, for instance) is returned unchanged — there is nothing
// structural to normalise, and the caller compares such bodies verbatim.
func normalizeNondeterministic(t *testing.T, body []byte) []byte {
	t.Helper()
	if !json.Valid(body) {
		return body
	}
	var v interface{}
	require.NoError(t, json.Unmarshal(body, &v))
	normalizeValue(v)
	out, err := json.Marshal(v)
	require.NoError(t, err)
	return out
}

func normalizeValue(v interface{}) {
	switch tv := v.(type) {
	case map[string]interface{}:
		for k, child := range tv {
			if nondeterministicKeyPattern.MatchString(k) {
				tv[k] = "<normalized>"
				continue
			}
			normalizeValue(child)
		}
	case []interface{}:
		for _, child := range tv {
			normalizeValue(child)
		}
	}
}

// assertTwoFixture runs op once against fixture A and once against fixture
// B, normalises nondeterministic fields in both response bodies, and asserts
// the two are identical in status and (normalised) body — the FR-010/US4.2
// requirement that an entitled caller's view never varies with what OTHER,
// out-of-scope servers happen to exist. It then asserts NEITHER response
// contains any of forbiddenSentinels — the servers this op's caller must
// never see evidence of, in either fixture.
//
// A body that fails to parse as JSON (a 401/403 plain page) is compared
// verbatim instead of structurally; normalizeNondeterministic already
// returns such bodies unchanged.
func assertTwoFixture(t *testing.T, tf *twoFixture, op twoFixtureOp, forbiddenSentinels ...string) {
	t.Helper()

	statusA, bodyA := op(tf.A)
	statusB, bodyB := op(tf.B)

	require.Equalf(t, statusA, statusB,
		"status must be identical across fixtures for the same caller (FR-010)\nA (%d): %s\nB (%d): %s",
		statusA, bodyA, statusB, bodyB)

	normA := normalizeNondeterministic(t, bodyA)
	normB := normalizeNondeterministic(t, bodyB)
	if json.Valid(normA) && json.Valid(normB) {
		require.JSONEqf(t, string(normB), string(normA),
			"fixture A and fixture B bodies must be identical once normalised (FR-010)\nA: %s\nB: %s", bodyA, bodyB)
	} else {
		require.Equalf(t, string(normB), string(normA),
			"fixture A and fixture B bodies must be identical (FR-010)\nA: %s\nB: %s", bodyA, bodyB)
	}

	for _, sentinel := range forbiddenSentinels {
		require.NotContainsf(t, string(bodyA), sentinel, "fixture A response leaked sentinel %q", sentinel)
		require.NotContainsf(t, string(bodyB), sentinel, "fixture B response leaked sentinel %q", sentinel)
	}
}

// assertStatusParity proves a by-name door's refusal for a name that EXISTS
// but is out of the caller's scope is byte-identical (status and body, after
// normalisation) to its refusal for a name that never existed anywhere in
// either fixture.
//
// This is deliberately NOT a "body must not contain the name" check: a 404
// legitimately echoes the caller's own path parameter back (the requested
// name), so a hidden server's refusal correctly contains that name too —
// the same way an absent server's refusal does. The right oracle, per
// memory/project_entitlement_test_oracle.md, is parity between the two
// refusals, not absence of the name from one of them.
func assertStatusParity(t *testing.T, doorHidden, doorAbsent func() (status int, body []byte)) {
	t.Helper()

	statusHidden, bodyHidden := doorHidden()
	statusAbsent, bodyAbsent := doorAbsent()

	require.Equalf(t, statusAbsent, statusHidden,
		"an out-of-scope existing name and a nonexistent name must refuse with the same status\nhidden (%d): %s\nabsent (%d): %s",
		statusHidden, bodyHidden, statusAbsent, bodyAbsent)

	normHidden := normalizeNondeterministic(t, bodyHidden)
	normAbsent := normalizeNondeterministic(t, bodyAbsent)
	if json.Valid(normHidden) && json.Valid(normAbsent) {
		require.JSONEqf(t, string(normAbsent), string(normHidden),
			"an out-of-scope existing name and a nonexistent name must refuse with the same body (non-disclosing refusal)\nhidden: %s\nabsent: %s", bodyHidden, bodyAbsent)
	} else {
		require.Equalf(t, string(normAbsent), string(normHidden),
			"an out-of-scope existing name and a nonexistent name must refuse with the same body (non-disclosing refusal)\nhidden: %s\nabsent: %s", bodyHidden, bodyAbsent)
	}
}

// bearerFor mints a fresh session-cookie-equivalent bearer JWT for u
// (role "admin" for Dana, "user" otherwise) against fx's harness — the
// credential the /user/* and /admin/* doors accept.
func (fx *fixtureHarness) bearerFor(t *testing.T, u *users.User) string {
	t.Helper()
	role := "user"
	if u.ID == fx.dana.ID {
		role = "admin"
	}
	token, err := fx.h.generateBearerToken(u, role)
	require.NoError(t, err)
	return token
}
