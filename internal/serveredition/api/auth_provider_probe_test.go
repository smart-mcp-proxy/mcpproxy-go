//go:build server

package api_test

// Spec 107 T049 (US2, FR-030): the public edition probe `GET /api/v1/auth/provider`.
//
// contracts/rest-endpoints.md §1: no authentication, no side effects (allocates
// no pending login state), returns ONLY `{display_name}` — an operator-chosen
// label, falling back to the provider family name — and never the issuer,
// client id, tenant id, scopes, domains or provider family. It is registered
// by SetupAll only on an enabled block (T053 mounts it beside login/callback,
// outside every auth group), so a disabled block registers nothing and 404
// falls out of chi.
//
// Compile-red until T053 adds AuthEndpoints.RegisterPublicRoutesWithPrefix
// (the public sibling of RegisterRoutesWithPrefix, auth_endpoints.go:51);
// the SetupAll-driven cases are behaviour-red on the PR-B base (404 today).
//
// External test package: the wiring cases need serveredition.SetupAll, which
// imports this package.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/serveredition"
	teamsapi "github.com/smart-mcp-proxy/mcpproxy-go/internal/serveredition/api"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

const providerProbePath = "/api/v1/auth/provider"

// forbiddenProbeValues are the configured secrets and identifiers the probe
// must never disclose; every fixture below plants them so a leak is caught by
// a substring check on the raw body.
var forbiddenProbeValues = []string{
	"https://idp.example.test",  // issuer_url
	"probe-client-id",           // client_id
	"probe-client-secret",       // client_secret
	"11111111-2222-3333-4444",   // tenant_id
	"probe-scope-secret",        // scopes
	"probe-domain.example.test", // allowed_domains
	"probe-groups-claim",        // groups_claim
}

func probeOAuthBlock(provider, displayName string) *config.ServerEditionOAuthConfig {
	return &config.ServerEditionOAuthConfig{
		Provider:       provider,
		ClientID:       "probe-client-id",
		ClientSecret:   "probe-client-secret",
		TenantID:       "11111111-2222-3333-4444",
		AllowedDomains: []string{"probe-domain.example.test"},
		IssuerURL:      "https://idp.example.test",
		Scopes:         []string{"openid", "probe-scope-secret"},
		GroupsClaim:    "probe-groups-claim",
		DisplayName:    displayName,
	}
}

func probeBlock(enabled bool, oauth *config.ServerEditionOAuthConfig) *config.ServerEditionConfig {
	return &config.ServerEditionConfig{
		Enabled:        enabled,
		AdminEmails:    []string{"admin@example.com"},
		SessionTTL:     config.Duration(24 * time.Hour),
		BearerTokenTTL: config.Duration(24 * time.Hour),
		OAuth:          oauth,
	}
}

func probeGet(t *testing.T, h http.Handler) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, providerProbePath, http.NoBody)
	req.Host = "localhost:8080"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// assertProbeBody checks the exact contract shape: 200, JSON, exactly one key
// `display_name` with the expected value, no cookies, no redirect, no leak.
func assertProbeBody(t *testing.T, rec *httptest.ResponseRecorder, wantDisplayName string) {
	t.Helper()
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	assert.Contains(t, rec.Header().Get("Content-Type"), "application/json")
	assert.Empty(t, rec.Header().Values("Set-Cookie"), "the probe must not start a session")
	assert.Empty(t, rec.Header().Get("Location"), "the probe must not redirect to the IdP")

	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), "body=%s", rec.Body.String())
	require.Len(t, body, 1, "only {display_name}: %s", rec.Body.String())
	got, ok := body["display_name"].(string)
	require.True(t, ok, "display_name must be a string: %s", rec.Body.String())
	assert.True(t, strings.EqualFold(wantDisplayName, got), "display_name: want %q (case-insensitive), got %q", wantDisplayName, got)
	assert.LessOrEqual(t, len(got), 64)

	for _, secret := range forbiddenProbeValues {
		assert.NotContains(t, rec.Body.String(), secret, "the probe leaked a configured value")
	}
}

// TestAuthProviderProbe_DisplayName drives the handler directly. The
// collaborators are nil on purpose: the probe reads only the boot block, so a
// nil user store, session manager and HMAC key must not matter — which is
// also the structural guarantee that it cannot touch the OAuth handler's
// pendingStates (that map lives in package auth and has no exported reader).
func TestAuthProviderProbe_DisplayName(t *testing.T) {
	cases := []struct {
		name        string
		provider    string
		displayName string
		want        string
	}{
		{"oidc with display_name", "oidc", "Acme SSO", "Acme SSO"},
		{"oidc falls back to the family name", "oidc", "", "oidc"},
		{"google falls back to the family name", "google", "", "google"},
		{"github falls back to the family name", "github", "", "github"},
		{"microsoft falls back to the family name", "microsoft", "", "microsoft"},
		{"legacy provider honours display_name", "google", "Sign in with Corp Google", "Sign in with Corp Google"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			block := probeBlock(true, probeOAuthBlock(tc.provider, tc.displayName))
			block.ApplyDefaults()
			require.NoError(t, block.Validate())

			endpoints := teamsapi.NewAuthEndpoints(nil, nil, block, nil, zap.NewNop().Sugar())
			router := chi.NewRouter()
			endpoints.RegisterPublicRoutesWithPrefix(router, "/api/v1")

			first := probeGet(t, router)
			assertProbeBody(t, first, tc.want)

			// Side-effect free: repeated calls are identical and nothing is
			// allocated per call (a probe that delegated to HandleLogin would
			// 302 and grow pendingStates).
			for i := 0; i < 50; i++ {
				again := probeGet(t, router)
				require.Equal(t, first.Body.String(), again.Body.String())
			}
		})
	}
}

// TestAuthProviderProbe_NoAuthenticationRequired: the route is public — an
// unrelated bearer, a wrong API key or a stale session cookie change nothing.
func TestAuthProviderProbe_NoAuthenticationRequired(t *testing.T) {
	block := probeBlock(true, probeOAuthBlock("oidc", "Acme SSO"))
	block.ApplyDefaults()
	endpoints := teamsapi.NewAuthEndpoints(nil, nil, block, nil, zap.NewNop().Sugar())
	router := chi.NewRouter()
	endpoints.RegisterPublicRoutesWithPrefix(router, "/api/v1")

	shapes := map[string]func(*http.Request){
		"stale cookie":   func(r *http.Request) { r.AddCookie(&http.Cookie{Name: "mcpproxy_session", Value: "stale"}) },
		"unknown bearer": func(r *http.Request) { r.Header.Set("Authorization", "Bearer not-a-credential") },
		"wrong api key":  func(r *http.Request) { r.Header.Set("X-API-Key", "wrong") },
	}
	for name, shape := range shapes {
		req := httptest.NewRequest(http.MethodGet, providerProbePath, http.NoBody)
		shape(req)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code, name)
		assert.JSONEq(t, `{"display_name":"Acme SSO"}`, rec.Body.String(), name)
	}
}

// probeSetup drives the production wiring (serveredition.SetupAll) with the
// given block on a bare chi router and returns the router plus SetupAll's
// error.
func probeSetup(t *testing.T, block *config.ServerEditionConfig) (chi.Router, error) {
	t.Helper()
	tmpDir := t.TempDir()
	db, err := bbolt.Open(tmpDir+"/probe.db", 0o600, &bbolt.Options{Timeout: time.Second})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	logger := zap.NewNop().Sugar()
	tokens, err := storage.NewManager(t.TempDir(), logger)
	require.NoError(t, err)
	t.Cleanup(func() { tokens.Close() })

	cfg := &config.Config{DataDir: tmpDir, ServerEdition: block}
	router := chi.NewRouter()
	setupErr := serveredition.SetupAll(serveredition.Dependencies{
		Router:         router,
		DB:             db,
		Logger:         logger,
		DataDir:        tmpDir,
		Config:         cfg,
		ConfigProvider: func() *config.Config { return cfg },
		StorageManager: tokens,
	})
	return router, setupErr
}

// TestAuthProviderProbe_WiredBySetupAll: the production setup mounts the probe
// beside login/callback, outside the session/JWT auth group.
func TestAuthProviderProbe_WiredBySetupAll(t *testing.T) {
	router, err := probeSetup(t, probeBlock(true, probeOAuthBlock("oidc", "Acme SSO")))
	require.NoError(t, err)

	assertProbeBody(t, probeGet(t, router), "Acme SSO")

	// The authenticated sibling on the same router still refuses an
	// unauthenticated caller — the probe is public by its own registration,
	// not because the auth group was weakened.
	me := httptest.NewRecorder()
	router.ServeHTTP(me, httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", http.NoBody))
	assert.Equal(t, http.StatusUnauthorized, me.Code)
}

// TestAuthProviderProbe_404WhenDisabledOrOAuthAbsent: a disabled block registers
// nothing (SetupAll returns early), and an enabled block without `oauth` fails
// validation before any route is mounted, so both answer like the personal
// build. NOTE: on a bare chi router the answer is 404. In the production
// router the probe path also sits under the `/api/v1` mount whose
// apiKeyAuthMiddleware runs before that sub-mux's 404
// (internal/httpapi/server.go:751-756), so an unauthenticated caller of an
// unregistered probe receives 401 there — see
// internal/httpapi/auth_provider_probe_personal_test.go.
func TestAuthProviderProbe_404WhenDisabledOrOAuthAbsent(t *testing.T) {
	t.Run("server_edition.enabled false", func(t *testing.T) {
		router, err := probeSetup(t, probeBlock(false, probeOAuthBlock("oidc", "Acme SSO")))
		require.NoError(t, err)
		rec := probeGet(t, router)
		assert.Equal(t, http.StatusNotFound, rec.Code)
		assert.NotContains(t, rec.Body.String(), "display_name")
	})

	t.Run("oauth block absent", func(t *testing.T) {
		router, err := probeSetup(t, probeBlock(true, nil))
		require.Error(t, err, "an enabled block without oauth must not set up")
		rec := probeGet(t, router)
		assert.Equal(t, http.StatusNotFound, rec.Code)
		assert.NotContains(t, rec.Body.String(), "display_name")
	})

	t.Run("server_edition block absent", func(t *testing.T) {
		router, err := probeSetup(t, nil)
		require.NoError(t, err)
		assert.Equal(t, http.StatusNotFound, probeGet(t, router).Code)
	})
}
