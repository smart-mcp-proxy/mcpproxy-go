//go:build server

package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Issue #1161, follow-up sweep.
//
// The fix for #1161 masked the SHARED servers on the per-user door. It relied
// throughout on one entitlement predicate — `sc.Shared` — because
// internal/serveredition/setup.go hands EVERY handler `deps.Config.Servers`,
// the admin's whole server list, under the name `sharedServers`. A handler that
// forgets the predicate does not merely leak a masked shared server: it
// discloses admin upstreams the admin deliberately did NOT share.
//
// user_handlers.go applies the predicate at all six of its loops. Two sibling
// handlers on the SAME per-user door did not:
//
//   - UserActivityHandlers.getDiagnostics iterated the list unfiltered and
//     labelled every entry `ownership:"shared"`;
//   - CredentialHandlers selected purely on `AuthBroker != nil`, so a
//     non-shared brokered upstream was enumerable AND its per-user OAuth
//     connect flow was drivable by any authenticated user.
//
// These tests pin the predicate at every per-user door, not just the three the
// original issue named.

// unsharedAdminServer is an admin-configured upstream that is explicitly NOT
// shared. No per-user surface may name it.
func unsharedAdminServer() *config.ServerConfig {
	return &config.ServerConfig{
		Name:     "prod-payments-admin",
		URL:      "https://payments.internal/mcp",
		Protocol: "http",
		Enabled:  true,
		Shared:   false,
	}
}

// unsharedBrokeredAdminServer is a non-shared admin upstream that carries an
// auth_broker block in the connect-flow mode — the shape the credential
// surfaces select on.
func unsharedBrokeredAdminServer() *config.ServerConfig {
	srv := brokerHTTPServer("internal-hr", config.AuthBrokerModeOAuthConnect)
	srv.Shared = false
	return srv
}

func TestGetDiagnostics_OmitsUnsharedAdminServers(t *testing.T) {
	admin := []*config.ServerConfig{
		defaultSharedServers()[0], // shared: must be listed
		unsharedAdminServer(),     // not shared: must not be
	}
	handlers, _ := activityTestSetup(t, nil, admin)
	router := activityTestRouter(handlers, defaultAuthContext())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/user/diagnostics", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.NotContains(t, w.Body.String(), "prod-payments-admin",
		"diagnostics disclosed an admin upstream that was never shared")

	var resp DiagnosticsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Servers, 1, "only the shared server belongs on a per-user surface")
	assert.Equal(t, "shared-github", resp.Servers[0].Name)
}

func TestCredentialsList_OmitsUnsharedAdminServers(t *testing.T) {
	shared := brokerHTTPServer("shared-gh", config.AuthBrokerModeOAuthConnect)
	handlers := NewCredentialHandlers(
		credTestStore(t),
		[]*config.ServerConfig{shared, unsharedBrokeredAdminServer()},
		nil, nil,
	)
	router := credRouter(handlers, defaultAuthContext())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/user/credentials", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.NotContains(t, w.Body.String(), "internal-hr",
		"the credential list disclosed a brokered upstream that was never shared")

	var resp CredentialListResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Credentials, 1)
	assert.Equal(t, "shared-gh", resp.Credentials[0].Server)
}

// The connect flow is the ACTIONABLE half of the same omission: reaching it for
// an unentitled upstream drives the gateway's OAuth flow against the admin's
// registered client and persists a per-user credential for a server the caller
// was never granted. Disconnect and callback select through the same lookup.
func TestCredentialRoutes_RejectUnsharedAdminServer(t *testing.T) {
	handlers := NewCredentialHandlers(
		credTestStore(t),
		[]*config.ServerConfig{unsharedBrokeredAdminServer()},
		nil, nil,
	)
	router := credRouter(handlers, defaultAuthContext())

	for _, tc := range []struct {
		method string
		target string
	}{
		{http.MethodGet, "/api/v1/user/credentials/internal-hr/connect"},
		{http.MethodGet, "/api/v1/user/credentials/internal-hr/callback"},
		{http.MethodDelete, "/api/v1/user/credentials/internal-hr"},
	} {
		req := httptest.NewRequest(tc.method, tc.target, nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code,
			"%s %s reached an admin upstream that was never shared", tc.method, tc.target)
	}
}

// getServer is one of the two handlers issue #1161 names. Its shared branch
// rendered the caller's preference as unset, so the detail read disagreed with
// the list read and with the enable response — a user who had disabled a shared
// server saw the admin's `enabled` and no `user_enabled` at all.
func TestGetServer_ReportsCallersSharedServerPreference(t *testing.T) {
	shared := defaultSharedServers()[:1]
	handlers, store := testSetup(t, shared)
	require.NoError(t, store.SetSharedServerPref(testUserID, shared[0].Name, false))

	router := testRouter(handlers, defaultAuthContext())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/user/servers/"+shared[0].Name, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var resp ServerResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "shared", resp.Ownership)
	require.NotNil(t, resp.UserEnabled, "the detail read dropped the caller's own preference")
	assert.False(t, *resp.UserEnabled)
}

// unsharedBrokerSecret is planted on the NON-shared admin upstream below, so
// the whole-door net fails distinctly on an entitlement gap (a server that must
// not be named at all) versus a redaction gap (a shared server's secret).
const unsharedBrokerSecret = "unsharedsecret_MDk4NzY1NDMyMWFi"

// perUserDoorRouter builds the router PRODUCTION builds: all three per-user
// handler sets, registered through RegisterRoutesWithPrefix — the form
// internal/serveredition/setup.go actually calls — over one admin server list.
func perUserDoorRouter(t *testing.T, admin []*config.ServerConfig) *chi.Mux {
	t.Helper()

	userHandlers, _ := testSetup(t, admin)
	activityHandlers, _ := activityTestSetup(t, nil, admin)
	credHandlers := NewCredentialHandlers(credTestStore(t), admin, nil, nil)

	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := auth.WithAuthContext(req.Context(), defaultAuthContext())
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	userHandlers.RegisterRoutesWithPrefix(r, "/api/v1")
	activityHandlers.RegisterRoutesWithPrefix(r, "/api/v1")
	credHandlers.RegisterRoutesWithPrefix(r, "/api/v1")
	return r
}

// The fail-closed net for the WHOLE per-user door.
//
// The #1161 fix shipped a net like this scoped to `/user/servers` and driven by
// a router assembled only from UserHandlers. Both limits mattered: the two
// routes that still leaked — /user/diagnostics and /user/credentials — are
// siblings on the same door, mounted by the same Group in setup.go, and neither
// was reachable by that walk. A hand-drawn boundary around the handlers that
// were known to leak is the shape this defect keeps taking, so this walk takes
// the whole `/user/` prefix off the PRODUCTION registration and asserts two
// things at once: no shared server's credential is echoed, and no upstream the
// admin did not share is so much as named.
func TestPerUserDoor_LeaksNoSecretAndNamesNoUnsharedServer(t *testing.T) {
	unshared := unsharedBrokeredAdminServer()
	unshared.Headers = map[string]string{"Authorization": "Bearer " + unsharedBrokerSecret}
	admin := []*config.ServerConfig{secretBearingSharedServer(), unshared}

	router := perUserDoorRouter(t, admin)

	// A body that decodes cleanly as every request type this door accepts, so a
	// route is exercised for real rather than bouncing off a 400.
	body := []byte(`{"enabled":false}`)

	// A route that echoes the name the CALLER just put in the path discloses
	// nothing — the caller already knew it. What must not differ is the ANSWER:
	// an unshared upstream has to look exactly like one that does not exist, or
	// the status code is an existence oracle over the admin's whole config.
	const absentName = "no-such-server-Zq7"

	call := func(method, route, name string) *httptest.ResponseRecorder {
		target := strings.ReplaceAll(route, "{name}", name)
		target = strings.ReplaceAll(target, "{server}", name)
		// chi renders a trailing-slash subroute as "/*"; the bare path is the
		// one clients call.
		target = strings.TrimSuffix(target, "/*")

		req := httptest.NewRequest(method, target, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}

	walked := 0
	err := chi.Walk(router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if !strings.Contains(route, "/user/") {
			return nil
		}
		// Aim every parameterised route at BOTH fixtures: the shared server
		// tests redaction, the unshared one tests entitlement.
		for _, name := range []string{"shared-secrets", unshared.Name} {
			walked++
			w := call(method, route, name)

			// A redirect carries its payload in Location, not the body.
			haystack := w.Body.String() + "\n" + w.Header().Get("Location")

			for _, secret := range append(sharedSecrets(), unsharedBrokerSecret) {
				assert.NotContains(t, haystack, secret,
					"%s %s echoed an admin credential (status %d)", method, route, w.Code)
			}

			if name != unshared.Name {
				continue
			}
			if !strings.Contains(route, "{name}") && !strings.Contains(route, "{server}") {
				// A COLLECTION route takes no name; substituting one is a
				// no-op, so its 200 is expected. What it must not do is
				// enumerate the unentitled upstream.
				assert.NotContains(t, haystack, unshared.Name,
					"%s %s enumerated an admin upstream that was never shared", method, route)
				continue
			}
			// On a route that ADDRESSES a server by name, no successful answer
			// may exist for an unentitled upstream, and the failure must be the
			// one a nonexistent name gets.
			assert.GreaterOrEqual(t, w.Code, 400,
				"%s %s answered for an admin upstream that was never shared", method, route)
			assert.Equal(t, call(method, route, absentName).Code, w.Code,
				"%s %s answers differently for an unshared upstream than for an absent one — an existence oracle over the admin's config",
				method, route)
		}
		return nil
	})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, walked, 26, "the walk must reach every /user/ route on the door")
}

// The two registration tables on each handler set are hand-maintained
// duplicates, and only RegisterRoutesWithPrefix is wired in production
// (setup.go). A route added to one and not the other either ships unreachable
// or — worse for a security net — ships live while every test that walks the
// other table stays green. Assert the two tables describe the same door.
func TestRouteTables_AgreeBetweenPrefixedAndNestedForms(t *testing.T) {
	admin := defaultSharedServers()
	userHandlers, _ := testSetup(t, admin)
	activityHandlers, _ := activityTestSetup(t, nil, admin)
	credHandlers := NewCredentialHandlers(credTestStore(t), admin, nil, nil)

	collect := func(register func(chi.Router)) []string {
		r := chi.NewRouter()
		register(r)
		var routes []string
		require.NoError(t, chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
			// The nested form renders a collection root as "/user/servers/";
			// the prefixed form as "/user/servers". Normalise so the comparison
			// is about the DOOR, not chi's rendering of it.
			route = strings.TrimSuffix(route, "/*")
			if len(route) > 1 {
				route = strings.TrimSuffix(route, "/")
			}
			routes = append(routes, method+" "+route)
			return nil
		}))
		sort.Strings(routes)
		return routes
	}

	nested := collect(func(r chi.Router) {
		r.Route("/api/v1", func(r chi.Router) {
			userHandlers.RegisterRoutes(r)
			activityHandlers.RegisterRoutes(r)
			credHandlers.RegisterRoutes(r)
		})
	})
	prefixed := collect(func(r chi.Router) {
		userHandlers.RegisterRoutesWithPrefix(r, "/api/v1")
		activityHandlers.RegisterRoutesWithPrefix(r, "/api/v1")
		credHandlers.RegisterRoutesWithPrefix(r, "/api/v1")
	})

	assert.Equal(t, nested, prefixed,
		"the nested and prefixed route tables disagree; production wires only the prefixed one")
}

// A shared server is masked for a DIFFERENT tenant than the one who configured
// it, so it must be masked under the AUDIT policy, not the LIVE one.
//
// MaskValue's `••••<last2> (<N> chars)` rendering is an affordance for an
// operator editing their own credential: it says which token is configured and
// is re-read within seconds. A shared server is read-only to the user seeing it
// (updateServer and deleteServer answer 403, enableServer stores only a
// per-user preference), so the affordance buys that reader nothing — while
// handing every tenant of the deployment a durable fingerprint of the admin's
// credential: its exact length and last two bytes, a correlation handle across
// users and a materially smaller search space for a low-entropy secret. That is
// precisely the reasoning AuditMaskValue was written for.
func TestSharedServer_MaskDisclosesNeitherLengthNorTail(t *testing.T) {
	shared := []*config.ServerConfig{secretBearingSharedServer()}
	handlers, _ := testSetup(t, shared)
	router := testRouter(handlers, defaultAuthContext())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/user/servers", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	body := w.Body.String()
	assert.NotRegexp(t, `\(\d+ chars\)`, body,
		"the response published the exact length of an admin credential")
	for _, secret := range sharedSecrets() {
		assert.NotContains(t, body, secret[len(secret)-2:]+" (",
			"the response published the trailing bytes of an admin credential")
	}
}
