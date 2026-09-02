//go:build server

package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/serveredition/users"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

const (
	tokenUserA = "01HTEST0000000000000USERA"
	tokenUserB = "01HTEST0000000000000USERB"
)

var tokenTestHMACKey = []byte("test-hmac-key-32-bytes-long!!!!!")

// tokenTestRig wires UserHandlers over a REAL *storage.Manager and registers
// the routes through RegisterRoutesWithPrefix — the function
// internal/serveredition/setup.go actually calls. RegisterRoutes (the nested
// chi.Route form the older testRouter uses) is never called in production, and
// a prefix mismatch would silently turn every assertion below into a chi 404.
//
// The auth context is chosen per request so one router can serve both tenants.
type tokenTestRig struct {
	router *chi.Mux
	store  *storage.Manager
	users  *users.UserStore
	as     *auth.AuthContext
}

func newTokenTestRig(t *testing.T) *tokenTestRig {
	t.Helper()
	return newTokenTestRigWithServers(t, nil)
}

// newTokenTestRigWithServers is newTokenTestRig with an admin server
// configuration, so a test can exercise the entitlement predicate (personal +
// Shared) that createUserToken constrains a token's allowed_servers to. The
// slice is what setup.go passes: the WHOLE config, shared and unshared alike.
func newTokenTestRigWithServers(t *testing.T, sharedServers []*config.ServerConfig) *tokenTestRig {
	t.Helper()

	logger := zap.NewNop().Sugar()

	dbPath := t.TempDir()
	db, err := bbolt.Open(dbPath+"/users.db", 0600, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	userStore := users.NewUserStore(db)
	require.NoError(t, userStore.EnsureBuckets())

	mgr, err := storage.NewManager(t.TempDir(), logger)
	require.NoError(t, err)
	t.Cleanup(func() { mgr.Close() })

	rig := &tokenTestRig{store: mgr, users: userStore}
	handlers := NewUserHandlers(userStore, sharedServers, mgr, tokenTestHMACKey, logger)

	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := auth.WithAuthContext(req.Context(), rig.as)
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	handlers.RegisterRoutesWithPrefix(r, "/api/v1")
	rig.router = r

	return rig
}

// as switches the identity subsequent calls are made under.
func (rig *tokenTestRig) actAs(ac *auth.AuthContext) { rig.as = ac }

func (rig *tokenTestRig) call(t *testing.T, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	rig.router.ServeHTTP(w, req)
	return w
}

// seedToken writes a token through the STORAGE layer with an explicit owner.
// Seeding through the HTTP create route would be fragile: createUserToken 400s
// unless the body carries permissions including "read", and a fixture that
// silently fails to land is exactly what makes a parity oracle vacuous.
func (rig *tokenTestRig) seedToken(t *testing.T, owner, name string) {
	t.Helper()
	raw, err := auth.GenerateToken()
	require.NoError(t, err)
	require.NoError(t, rig.store.CreateAgentToken(auth.AgentToken{
		Name:        name,
		UserID:      owner,
		Permissions: []string{auth.PermRead},
	}, raw, tokenTestHMACKey))
}

func userACtx() *auth.AuthContext {
	return auth.UserContext(tokenUserA, "a@example.com", "User A", "google")
}

func userBCtx() *auth.AuthContext {
	return auth.UserContext(tokenUserB, "b@example.com", "User B", "google")
}

// assertHandlerNotFound checks that the response is the HANDLER's 404 envelope
// and not chi's NotFoundHandler. chi answers an unregistered path with the
// plain text "404 page not found\n", which does not decode as the envelope —
// so decoding it here is what rules out "the route never matched", the most
// likely way a status-parity assertion passes while exercising nothing.
func assertHandlerNotFound(t *testing.T, w *httptest.ResponseRecorder, what string) {
	t.Helper()
	require.Equal(t, http.StatusNotFound, w.Code, "%s: expected exactly 404", what)

	var envelope struct {
		Error      string `json:"error"`
		Message    string `json:"message"`
		StatusCode int    `json:"status_code"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope),
		"%s: body did not decode as the handler's error envelope — chi's own 404 would look like this (%q)", what, w.Body.String())
	assert.Equal(t, http.StatusNotFound, envelope.StatusCode, "%s: envelope status_code", what)
	assert.Equal(t, http.StatusText(http.StatusNotFound), envelope.Error, "%s: envelope error", what)
}

// TestUserTokenRoutes_UnownedIsIndistinguishableFromAbsent is the security
// property the 403->404 collapse exists to deliver: a caller who owns neither
// an existing-but-unowned token nor a nonexistent one must not be able to tell
// the two apart.
//
// Bare status parity between "unowned" and "absent" is NOT a sufficient oracle
// — it passes when the fixture never lands (404/404), when the route never
// matches (chi 404/404), and when the token store is nil (500/500). All three
// are ruled out here:
//
//  1. a POSITIVE CONTROL runs FIRST, on the SAME router and the SAME name, as
//     the owner, and must succeed — proving the fixture landed, the route
//     matched and the store is wired;
//  2. an independent existence proof: the owner's own GET /user/tokens lists
//     the name, through the production route table;
//  3. the expected status is PINNED to 404, not merely equal between the two
//     probes; and
//  4. the body must decode as the HANDLER's error envelope, which chi's own
//     plain-text 404 does not.
//
// BITES: on unfixed code the unowned probe is 403 ("Cannot revoke another
// user's token") while the absent probe is 404.
func TestUserTokenRoutes_UnownedIsIndistinguishableFromAbsent(t *testing.T) {
	const absentName = "no-such-token-Zq7"

	cases := []struct {
		label  string
		method string
		path   string // %s is the token name
		// consumesFixture is true when the owner's positive control destroys
		// the record, so it must be re-seeded before user B probes.
		consumesFixture bool
	}{
		{"revoke", http.MethodDelete, "/api/v1/user/tokens/%s", false},
		{"delete", http.MethodDelete, "/api/v1/user/tokens/%s/permanent", true},
		{"regenerate", http.MethodPost, "/api/v1/user/tokens/%s/regenerate", false},
	}

	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			rig := newTokenTestRig(t)
			name := "ci-" + tc.label

			rig.seedToken(t, tokenUserA, name)

			// (2) Independent existence proof through the production routes.
			rig.actAs(userACtx())
			listed := rig.call(t, http.MethodGet, "/api/v1/user/tokens")
			require.Equal(t, http.StatusOK, listed.Code, "owner's token list must be readable")
			require.Contains(t, listed.Body.String(), name,
				"the fixture did not land: the owner's own token list does not show %q", name)

			// (1) Positive control, first, same router, same name, as the owner.
			ctl := rig.call(t, tc.method, fmt.Sprintf(tc.path, name))
			require.Equal(t, http.StatusOK, ctl.Code,
				"positive control failed: the owner got %d for %s %s (body: %s)",
				ctl.Code, tc.method, tc.path, ctl.Body.String())

			if tc.consumesFixture {
				rig.seedToken(t, tokenUserA, name)
			}

			// Snapshot A's record AFTER the control, so the final check
			// attributes any change to user B's probe rather than to the
			// control (revoke and regenerate both mutate it legitimately).
			before, err := rig.store.GetAgentTokenByOwnerAndName(tokenUserA, name)
			require.NoError(t, err)
			require.NotNil(t, before, "the fixture must exist before user B probes it")

			// The probes: user B owns neither name.
			rig.actAs(userBCtx())
			unowned := rig.call(t, tc.method, fmt.Sprintf(tc.path, name))
			absent := rig.call(t, tc.method, fmt.Sprintf(tc.path, absentName))

			assert.Equal(t, absent.Code, unowned.Code,
				"%s: an unowned token answers %d while an absent one answers %d — a name-existence oracle",
				tc.label, unowned.Code, absent.Code)

			// (3) + (4): pin the value, and prove the handler answered.
			assertHandlerNotFound(t, unowned, tc.label+" (unowned)")
			assertHandlerNotFound(t, absent, tc.label+" (absent)")

			// The unowned probe must not have mutated A's token.
			after, err := rig.store.GetAgentTokenByOwnerAndName(tokenUserA, name)
			require.NoError(t, err)
			require.NotNil(t, after, "user B's probe deleted user A's token")
			assert.Equal(t, before.Revoked, after.Revoked, "user B's probe changed the revoked state of user A's token")
			assert.Equal(t, before.TokenHash, after.TokenHash, "user B's probe regenerated user A's token")
		})
	}
}

// TestCreateUserToken_SameNameAsAnotherUserSucceeds closes the create-path half
// of the oracle. Collapsing only the revoke/delete/regenerate 403s would move
// the oracle rather than close it: create would still answer 409 for a name
// another tenant owns.
//
// BITES: on unfixed code the second create returns 409 and echoes the storage
// string `agent token with name "ci" already exists` verbatim.
func TestCreateUserToken_SameNameAsAnotherUserSucceeds(t *testing.T) {
	rig := newTokenTestRig(t)

	rig.seedToken(t, tokenUserA, "ci")

	rig.actAs(userBCtx())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/user/tokens",
		strings.NewReader(`{"name":"ci","permissions":["read"]}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	rig.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusCreated, w.Code,
		"user B could not create a token named %q because another tenant owns that name (body: %s)", "ci", w.Body.String())
	assert.NotContains(t, w.Body.String(), "already exists",
		"the create path echoed a storage conflict, disclosing that someone owns this name")

	// Both records exist independently and neither clobbered the other.
	gotA, err := rig.store.GetAgentTokenByOwnerAndName(tokenUserA, "ci")
	require.NoError(t, err)
	require.NotNil(t, gotA)
	gotB, err := rig.store.GetAgentTokenByOwnerAndName(tokenUserB, "ci")
	require.NoError(t, err)
	require.NotNil(t, gotB)
	assert.NotEqual(t, gotA.TokenHash, gotB.TokenHash)

	// A duplicate for the SAME owner is still refused — and without echoing
	// storage internals.
	dupe := httptest.NewRequest(http.MethodPost, "/api/v1/user/tokens",
		strings.NewReader(`{"name":"ci","permissions":["read"]}`))
	dupe.Header.Set("Content-Type", "application/json")
	dupeRec := httptest.NewRecorder()
	rig.router.ServeHTTP(dupeRec, dupe)
	assert.Equal(t, http.StatusConflict, dupeRec.Code)
	assert.NotContains(t, dupeRec.Body.String(), "agent token with")
}

// TestUserTokenRoutes_RejectAgentTierContext: now that an agent token carries
// its owner's UserID, a non-empty UserID is no longer proof of the user tier.
// Every /api/v1/user/tokens route must gate on IsUser().
//
// BITES: with the UserID field added and getUserID still testing only
// GetUserID() != "", the agent context is accepted AS the tenant and these
// routes answer 200/404 instead of 401.
func TestUserTokenRoutes_RejectAgentTierContext(t *testing.T) {
	rig := newTokenTestRig(t)
	rig.seedToken(t, tokenUserA, "ci")

	// Positive control: as the owning USER, the list route works.
	rig.actAs(userACtx())
	ok := rig.call(t, http.MethodGet, "/api/v1/user/tokens")
	require.Equal(t, http.StatusOK, ok.Code)
	require.Contains(t, ok.Body.String(), "ci")

	// The agent tier: same UserID, agent Type.
	agent := (&auth.AgentToken{
		Name:        "ci",
		TokenPrefix: "mcp_agt_abcd",
		Permissions: []string{auth.PermRead},
		UserID:      tokenUserA,
	}).AuthContext()
	require.Equal(t, tokenUserA, agent.GetUserID(), "the fixture must actually carry the owner's id")
	rig.actAs(agent)

	for _, tc := range []struct {
		method, target string
	}{
		{http.MethodGet, "/api/v1/user/tokens"},
		{http.MethodPost, "/api/v1/user/tokens"},
		{http.MethodDelete, "/api/v1/user/tokens/ci"},
		{http.MethodDelete, "/api/v1/user/tokens/ci/permanent"},
		{http.MethodPost, "/api/v1/user/tokens/ci/regenerate"},
	} {
		w := rig.call(t, tc.method, tc.target)
		assert.Equal(t, http.StatusUnauthorized, w.Code,
			"%s %s accepted an agent-tier context as its owner (body: %s)", tc.method, tc.target, w.Body.String())
	}

	// And the token itself is untouched.
	got, err := rig.store.GetAgentTokenByOwnerAndName(tokenUserA, "ci")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.False(t, got.Revoked)
}
