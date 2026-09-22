//go:build server

package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// T081 (Spec 107 PR-C, US4): chi.Walk over the PRODUCTION route table,
// asserting the FR-002/FR-045 verdict for a tenant (`user`-typed) session
// principal on EVERY method+route this binary actually serves — not a
// hand-maintained list, so a route added later without an allowlist entry
// fails this test the moment it is registered (contracts/rest-endpoints.md
// §8, spec.md US4 Independent Test).
//
// [compile-red until T083]: SetSessionPrincipalResolver does not exist on
// *Server yet (it is T083's SessionPrincipalResolver hook,
// contracts/entitlement-predicate.md §4) — this file does not compile at
// HEAD. That is the intended "red": the walk cannot even be driven through a
// session-cookie principal until the hook lands, and the assertions below
// pin what T083/T084 must make true once it does.

// tenantWalkController is the minimal ServerController double this walk
// needs: a real *config.Config so apiKeyAuthMiddleware does not take the
// "no config / testing scenario" bypass (which would let every route through
// unauthenticated and make the whole walk vacuous — see
// project_entitlement_test_oracle and project_verify_test_bites_build_failure
// in memory), plus just enough server/tool/profile data that the allowlisted
// GET routes return something instead of panicking.
type tenantWalkController struct {
	baseController
}

func (m *tenantWalkController) GetCurrentConfig() *config.Config {
	return &config.Config{APIKey: "admin-test-key"}
}

func (m *tenantWalkController) GetConfig() (*config.Config, error) {
	return &config.Config{APIKey: "admin-test-key"}, nil
}

// tenantEntitledServer is the one server name the tenant session principal
// below is entitled to (US4 Independent Test: "for a server she *can* see").
// tenantHiddenServer stands in for every {id}/{name}/{client}/{tool} path
// param on a route this tenant is NOT entitled to — used to prove 404 parity
// on the scoped subtree, and fed into every allowlist substitution that is
// not the entitled name so the walk never accidentally proves a route "safe"
// only because it happened to pick a visible id.
const (
	tenantEntitledServer = "everything"
	tenantHiddenServer   = "does-not-exist"
)

// tenantSessionCookieValue is the opaque cookie value the fake resolver below
// keys off; its content is never inspected by anything but the test's own
// resolver stub; SessionPrincipalResolver in T083 wires the real one against
// the session store.
const tenantSessionCookieValue = "alice-session"

func newTenantWalkServer(t *testing.T) *Server {
	t.Helper()
	srv := NewServer(&tenantWalkController{}, zap.NewNop().Sugar(), nil)

	// [T083 dependency] SessionPrincipalResolver (contracts/entitlement-predicate.md
	// §4): SetSessionPrincipalResolver(f) where f(r, kind, value) returns the
	// AuthContext for a cookie/bearer credential. A tenant with exactly one
	// entitled server, session-cookie-only (CredentialKindCookie, so
	// IsSessionPrincipal() is true and CanRevealSecrets() stays false).
	srv.SetSessionPrincipalResolver(func(_ *http.Request, kind auth.CredentialKind, value string) (*auth.AuthContext, error) {
		if kind != auth.CredentialKindCookie || value != tenantSessionCookieValue {
			return nil, nil
		}
		return &auth.AuthContext{
			Type:           auth.AuthTypeUser,
			UserID:         "alice",
			Email:          "alice@example.com",
			Role:           "user",
			Provider:       "keycloak",
			AllowedServers: []string{tenantEntitledServer},
			CredentialKind: auth.CredentialKindCookie,
		}, nil
	})

	return srv
}

// tenantAllowlistedRoute is one row of the FR-002 table
// (contracts/rest-endpoints.md §8): a method+pattern that a tenant session
// principal may reach at all, filtered downstream by the existing scope
// predicates rather than refused outright.
type tenantAllowlistedRoute struct {
	method  string
	pattern string
}

// tenantAllowlist mirrors rest-endpoints.md §8 verbatim. Every entry here is
// a route the tenant reaches (then gets filtered by scope); everything else
// under /api/v1, plus every other method on these same patterns, is the fixed
// 403. `/events` is listed separately below (SSE, GET+HEAD, not under
// /api/v1).
var tenantAllowlist = []tenantAllowlistedRoute{
	{http.MethodGet, "/api/v1/status"},
	{http.MethodGet, "/api/v1/servers"},
	// scopedServerSubtree: every GET under /servers/{id}/** EXCEPT
	// /servers/{id}/tool-calls (named must-refuse) and the static
	// /servers/import/paths (host filesystem paths, not a server subtree —
	// the matcher must deny it explicitly ahead of the {id} rule, see below).
	{http.MethodGet, "/api/v1/servers/{id}"},
	{http.MethodGet, "/api/v1/servers/{id}/tools"},
	{http.MethodGet, "/api/v1/servers/{id}/logs"},
	{http.MethodGet, "/api/v1/servers/{id}/diagnostics"},
	{http.MethodGet, "/api/v1/servers/{id}/tools/{tool}/diff"},
	{http.MethodGet, "/api/v1/servers/{id}/tools/export"},
	{http.MethodGet, "/api/v1/servers/{id}/scan/status"},
	{http.MethodGet, "/api/v1/servers/{id}/scan/report"},
	{http.MethodGet, "/api/v1/servers/{id}/scan/files"},
	{http.MethodGet, "/api/v1/servers/{id}/integrity"},
	{http.MethodGet, "/api/v1/tools"},
	{http.MethodGet, "/api/v1/index/search"},
	{http.MethodPost, "/api/v1/preflight"},
	{http.MethodGet, "/api/v1/profiles"},
	{http.MethodGet, "/api/v1/profiles/active"},
}

// tenantNamedMustRefuse is the explicit "must return the fixed 403" list from
// spec.md's US4 Independent Test — named so a regression in the generic
// "everything else is 403" sweep below is not the only thing pinning these
// (a reviewer, or a future refactor of the allowlist matcher, reading this
// file should see the exact routes the spec calls out by name without having
// to diff the generic walk).
var tenantNamedMustRefuse = []tenantAllowlistedRoute{
	{http.MethodGet, "/api/v1/config"},
	{http.MethodGet, "/api/v1/activity"},
	{http.MethodGet, "/api/v1/activity/summary"},
	{http.MethodGet, "/api/v1/tool-calls"},
	{http.MethodGet, "/api/v1/servers/{id}/tool-calls"},
	{http.MethodGet, "/api/v1/info"},
	{http.MethodGet, "/api/v1/routing"},
	{http.MethodGet, "/api/v1/docker/status"},
	{http.MethodGet, "/api/v1/stats/tokens"},
	{http.MethodGet, "/api/v1/security/overview"},
	{http.MethodPost, "/api/v1/tools/call"},
	{http.MethodPost, "/api/v1/code/exec"},
	{http.MethodPost, "/api/v1/tool-calls/{id}/replay"},
	{http.MethodPost, "/api/v1/registries/{id}/refresh"},
	{http.MethodPost, "/api/v1/telemetry/update-failure"},
	{http.MethodPost, "/api/v1/onboarding/mark"},
	{http.MethodPost, "/api/v1/feedback"},
	{http.MethodGet, "/api/v1/code/scripts"},
	{http.MethodGet, "/api/v1/diagnostics"},
	{http.MethodGet, "/api/v1/doctor"},
	{http.MethodGet, "/api/v1/telemetry/payload"},
	{http.MethodGet, "/api/v1/connect"},
	{http.MethodGet, "/api/v1/annotations/coverage"},
	{http.MethodPatch, "/api/v1/config"},
	{http.MethodPost, "/api/v1/servers"},
	{http.MethodDelete, "/api/v1/servers/{id}"},
	{http.MethodPost, "/api/v1/security/scanners/{id}/enable"},
	{http.MethodPost, "/api/v1/tokens"},
	// The two static routes a naive raw-path "/servers/{id}/**" matcher would
	// wrongly admit (rest-endpoints.md §8 explicit carve-outs):
	{http.MethodGet, "/api/v1/servers/import/paths"},
}

func isTenantAllowlisted(method, pattern string) bool {
	for _, row := range tenantAllowlist {
		if row.method == method && row.pattern == pattern {
			return true
		}
	}
	return false
}

// tenantWalkSubstitutions fills chi path params with a value on a route the
// tenant IS entitled to see, so a false "not 403" can never be explained by
// having picked a server/tool/client name that happens to not exist at all
// (which would be 404-for-a-different-reason, not scope enforcement).
var tenantWalkSubstitutions = map[string]string{
	"{id}":       tenantEntitledServer,
	"{name}":     tenantEntitledServer,
	"{tool}":     "echo",
	"{client}":   "claude-desktop",
	"{serverId}": tenantEntitledServer,
}

func fillRoutePattern(pattern string) string {
	out := pattern
	for placeholder, value := range tenantWalkSubstitutions {
		out = strings.ReplaceAll(out, placeholder, value)
	}
	return out
}

// tenantForbiddenBody is the fixed 403 body every refused route must return,
// byte-identical regardless of the route or of whether the body the caller
// sent was well-formed (rest-endpoints.md §8: "before any body parse; a
// malformed body is 403, not 400").
const tenantForbiddenErrorField = `"error":"forbidden"`

func doTenantRequest(t *testing.T, srv *Server, method, path string, malformedBody bool) *httptest.ResponseRecorder {
	t.Helper()
	var body *strings.Reader
	if malformedBody {
		body = strings.NewReader("{not valid json")
	} else {
		body = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, body)
	if malformedBody {
		req.Header.Set("Content-Type", "application/json")
	}
	req.AddCookie(&http.Cookie{Name: "mcpproxy_session", Value: tenantSessionCookieValue})
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w
}

// TestTenantSessionAllowlistWalk walks the production /api/v1 route table
// (plus /events) and asserts the FR-045 verdict for a tenant session
// principal on every method+route it finds: an allowlisted row is filtered
// (never the fixed 403), everything else is the fixed 403 — emitted before
// the handler runs, so a malformed JSON body on a refused route still comes
// back 403, never 400 (rest-endpoints.md §8).
func TestTenantSessionAllowlistWalk(t *testing.T) {
	srv := newTenantWalkServer(t)

	seen := map[string]bool{}
	err := chi.Walk(srv.Router(), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if !strings.HasPrefix(route, "/api/v1") {
			return nil // /healthz, /readyz, /metrics etc. are not tenant-gated doors
		}
		if method == http.MethodOptions {
			return nil // CORS preflight, not a resource
		}
		seen[method+" "+route] = true
		return nil
	})
	require.NoError(t, err)
	require.NotEmpty(t, seen, "chi.Walk found no /api/v1 routes — the route table changed shape")

	for key := range seen {
		parts := strings.SplitN(key, " ", 2)
		method, pattern := parts[0], parts[1]
		path := fillRoutePattern(pattern)

		t.Run(key, func(t *testing.T) {
			w := doTenantRequest(t, srv, method, path, false)

			if isTenantAllowlisted(method, pattern) {
				require.NotEqualf(t, http.StatusForbidden, w.Code,
					"allowlisted route %s %s must be filtered by the scope predicates, never the fixed 403", method, pattern)
				return
			}

			require.Equalf(t, http.StatusForbidden, w.Code,
				"non-allowlisted route %s %s must answer the fixed 403 for a tenant session", method, pattern)
			require.Containsf(t, w.Body.String(), tenantForbiddenErrorField,
				"refused route %s %s must carry the fixed scoped-caller 403 body", method, pattern)
		})
	}
}

// TestTenantSessionNamedMustRefuseList pins the exact routes spec.md's US4
// Independent Test calls out by name, independent of whatever the generic
// route-table sweep above finds — so a change to the allowlist matcher that
// accidentally widens it cannot silently drop one of these without a diff
// here too.
func TestTenantSessionNamedMustRefuseList(t *testing.T) {
	srv := newTenantWalkServer(t)

	for _, row := range tenantNamedMustRefuse {
		row := row
		t.Run(row.method+" "+row.pattern, func(t *testing.T) {
			path := fillRoutePattern(row.pattern)
			w := doTenantRequest(t, srv, row.method, path, false)
			require.Equalf(t, http.StatusForbidden, w.Code,
				"named must-refuse route %s %s must be the fixed 403 for a tenant session", row.method, row.pattern)
			require.Containsf(t, w.Body.String(), tenantForbiddenErrorField,
				"named must-refuse route %s %s must carry the fixed scoped-caller 403 body", row.method, row.pattern)
		})
	}
}

// TestTenantSessionRefusalIsBeforeBodyParse: rest-endpoints.md §8 — the fixed
// 403 is emitted "before any body parse; a malformed body is 403, not 400".
// A refused mutating route with an unparseable JSON body must still answer
// 403, never a 400 from a handler that got far enough to try json.Decode.
func TestTenantSessionRefusalIsBeforeBodyParse(t *testing.T) {
	srv := newTenantWalkServer(t)

	w := doTenantRequest(t, srv, http.MethodPost, "/api/v1/tools/call", true)
	require.Equal(t, http.StatusForbidden, w.Code,
		"a malformed body on a refused route must not leak a 400 from the handler's own body parsing")
	require.Contains(t, w.Body.String(), tenantForbiddenErrorField)
}

// TestTenantSessionScopedSubtreeParity: for the /servers/{id}/** allowlisted
// GET routes, an entitled server and an absent one must be indistinguishable
// (404 parity, project_entitlement_test_oracle) — never "body must not
// contain the name", which a caller's own echoed path param can defeat.
func TestTenantSessionScopedSubtreeParity(t *testing.T) {
	srv := newTenantWalkServer(t)

	entitled := doTenantRequest(t, srv, http.MethodGet, "/api/v1/servers/"+tenantEntitledServer+"/tools", false)
	require.NotEqual(t, http.StatusForbidden, entitled.Code,
		"the tenant's own entitled server must not be scope-refused")

	hidden := doTenantRequest(t, srv, http.MethodGet, "/api/v1/servers/"+tenantHiddenServer+"/tools", false)
	require.NotEqual(t, http.StatusForbidden, hidden.Code,
		"an unentitled server on an allowlisted route is a 404, not the fixed 403 (it is filtered, not refused)")
	require.Equal(t, http.StatusNotFound, hidden.Code,
		"an unentitled server must read exactly as a nonexistent one (status parity)")
}

// TestTenantSessionNamedMustRefuseSurvivesEncodedServerName pins cross-review
// round 1's P1 finding: nothing in config validation forbids a server name
// containing a literal "/" (only ":" is refused,
// internal/config/server_edition_config.go validateAccessServerName), and
// such a name is addressed on the wire as a single percent-encoded path
// segment. Chi routes on r.URL.RawPath when it is set (server.go:888), so
// "/api/v1/servers/io.github.owner%2Frepo/tool-calls" still dispatches to
// handleGetServerToolCalls with id="io.github.owner/repo" — the allowlist
// gate ahead of it MUST see the same raw form, or it Cuts the DECODED path
// on the literal "/" the escape produced, misreads the trailing segment as
// "repo/tool-calls" instead of "tool-calls", and lets the named must-refuse
// route through.
//
// BITES: reverting tenantSessionRoutePath to always return r.URL.Path (as
// tryInstallSessionPrincipal did before this round) makes this fail with a
// 200/404 from the real handler instead of the fixed 403.
func TestTenantSessionNamedMustRefuseSurvivesEncodedServerName(t *testing.T) {
	srv := newTenantWalkServer(t)

	path := "/api/v1/servers/io.github.owner%2Frepo/tool-calls"
	w := doTenantRequest(t, srv, http.MethodGet, path, false)
	require.Equalf(t, http.StatusForbidden, w.Code,
		"an encoded slash in the server name must not defeat the /servers/{id}/tool-calls must-refuse: got %d body=%s",
		w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), tenantForbiddenErrorField)
}

// TestTenantSessionCanRevealSecretsFalse: session principals never satisfy
// CanRevealSecrets (constraint 11) — pinned directly against the AuthContext
// the resolver hands back, independent of any specific route's behaviour.
func TestTenantSessionCanRevealSecretsFalse(t *testing.T) {
	ac := &auth.AuthContext{
		Type:           auth.AuthTypeUser,
		UserID:         "alice",
		AllowedServers: []string{tenantEntitledServer},
		CredentialKind: auth.CredentialKindCookie,
	}
	require.True(t, ac.IsSessionPrincipal())
	require.False(t, ac.CanRevealSecrets(), "a session principal must never be able to reveal raw secrets")
}
