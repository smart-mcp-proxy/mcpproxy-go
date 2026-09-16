//go:build server

package server

// Spec 107 T049 (US2, FR-029, FR-043(a)): forced MCP authentication under the
// server edition.
//
// With `server_edition.enabled: true` the /mcp* auth middleware MUST behave as
// if `require_mcp_auth` were true regardless of the configured value: no
// credential → 401, an unrecognised bearer (a session cookie, a user JWT) →
// 401; agent tokens, the global API key and the socket are unchanged. The
// decision is the build-tagged accessor config.EffectiveRequireMCPAuth (T052:
// server build `cfg.RequireMCPAuth || server_edition.enabled`, personal build
// `cfg.RequireMCPAuth`), so internal/server stays edition-neutral. An explicit
// `require_mcp_auth: false` is not a validation error; boot logs one notice
// with the contracts/config-keys.md text.
//
// Compile-red until T052 lands config.EffectiveRequireMCPAuth; the middleware
// assertions are then behaviour-red until mcpAuthMiddleware (server.go:353,450)
// reads the accessor and the boot notice is emitted. The personal-build twin
// is mcp_auth_forced_personal_test.go.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	teamsauth "github.com/smart-mcp-proxy/mcpproxy-go/internal/serveredition/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/transport"
)

// forcedAuthBootNotice is the exact boot-notice text of contracts/config-keys.md
// (`require_mcp_auth` row).
const forcedAuthBootNotice = "require_mcp_auth: false is overridden to true because server_edition.enabled is true"

// forcedAuthConfig returns a config with the server-edition block enabled and a
// valid legacy OAuth block (no network I/O: discovery is lazy, and NewServer
// does not run SetupAll — wireServerEditionOAuth runs in startCustomHTTPServer).
func forcedAuthConfig(t *testing.T, apiKey string, requireMCPAuth, editionEnabled bool) *config.Config {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.Listen = "127.0.0.1:0"
	cfg.APIKey = apiKey
	cfg.RequireMCPAuth = requireMCPAuth
	cfg.ServerEdition = &config.ServerEditionConfig{
		Enabled:     editionEnabled,
		AdminEmails: []string{"admin@example.com"},
		OAuth: &config.ServerEditionOAuthConfig{
			Provider:     "google",
			ClientID:     "test-client-id",
			ClientSecret: "test-client-secret",
		},
	}
	return cfg
}

func newForcedAuthTestServer(t *testing.T, cfg *config.Config, logger *zap.Logger) *Server {
	t.Helper()
	srv, err := NewServer(cfg, logger)
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Shutdown() })
	return srv
}

// mintAgentToken creates a real mcp_agt_ token against the server's HMAC key
// and storage, so the agent branch of the middleware validates end-to-end.
func mintAgentToken(t *testing.T, srv *Server, name string) string {
	t.Helper()
	cfg := srv.runtime.Config()
	hmacKey, err := auth.GetOrCreateHMACKey(cfg.DataDir)
	require.NoError(t, err)
	raw, err := auth.GenerateToken()
	require.NoError(t, err)
	require.NoError(t, srv.runtime.StorageManager().CreateAgentToken(auth.AgentToken{
		Name:           name,
		AllowedServers: []string{"*"},
		Permissions:    []string{"read"},
		ExpiresAt:      time.Now().Add(time.Hour),
	}, raw, hmacKey))
	return raw
}

// mintUserJWT signs a server-edition user bearer JWT with the same HMAC key the
// server derives from its data dir — a genuine SSO credential, not a random
// string, so the "unrecognised bearer" branch is exercised with the real thing.
func mintUserJWT(t *testing.T, srv *Server) string {
	t.Helper()
	cfg := srv.runtime.Config()
	hmacKey, err := auth.GetOrCreateHMACKey(cfg.DataDir)
	require.NoError(t, err)
	token, err := teamsauth.GenerateBearerToken(hmacKey, "01HZUSERAAAAAAAAAAAAAAAAA", "alice@example.com", "Alice", "user", "google", time.Hour)
	require.NoError(t, err)
	return token
}

// TestEffectiveRequireMCPAuth_ServerBuild pins the accessor's truth table on
// the server build (research D11): the edition flag forces true; otherwise the
// configured value is returned unchanged.
func TestEffectiveRequireMCPAuth_ServerBuild(t *testing.T) {
	cases := []struct {
		name       string
		require    bool
		block      *config.ServerEditionConfig
		wantForced bool
	}{
		{"enabled block forces true over an explicit false", false, &config.ServerEditionConfig{Enabled: true}, true},
		{"enabled block keeps an explicit true", true, &config.ServerEditionConfig{Enabled: true}, true},
		{"disabled block returns the configured false", false, &config.ServerEditionConfig{Enabled: false}, false},
		{"disabled block returns the configured true", true, &config.ServerEditionConfig{Enabled: false}, true},
		{"absent block returns the configured false", false, nil, false},
		{"absent block returns the configured true", true, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{RequireMCPAuth: tc.require, ServerEdition: tc.block}
			assert.Equal(t, tc.wantForced, config.EffectiveRequireMCPAuth(cfg))
		})
	}
}

// TestMCPAuthMiddleware_ForcedUnderServerEdition is FR-029 / FR-043(a) at the
// middleware seam: `require_mcp_auth: false` is configured, the server-edition
// block is enabled, and every unauthenticated or SSO-credentialed request is
// refused while the three real MCP credentials keep working.
func TestMCPAuthMiddleware_ForcedUnderServerEdition(t *testing.T) {
	const apiKey = "test-api-key-fr029"
	srv := newForcedAuthTestServer(t, forcedAuthConfig(t, apiKey, false, true), zap.NewNop())
	require.False(t, srv.runtime.Config().RequireMCPAuth, "fixture: the configured value stays false; only the effective value is forced")

	t.Run("no credential is refused with the existing 401 body", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/mcp", http.NoBody)
		seen, code := captureAuthContext(t, srv, req)
		require.Equal(t, http.StatusUnauthorized, code)
		require.Nil(t, seen, "the downstream handler must not run for an unauthenticated caller")
	})

	t.Run("no credential 401 body is unchanged and carries no WWW-Authenticate", func(t *testing.T) {
		rec := httptest.NewRecorder()
		srv.mcpAuthMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Fatal("downstream handler reached without a credential")
		})).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/mcp", http.NoBody))
		require.Equal(t, http.StatusUnauthorized, rec.Code)
		assert.Contains(t, rec.Body.String(), "Authentication required", "contracts/rest-endpoints.md §9: body unchanged")
		assert.Empty(t, rec.Header().Get("WWW-Authenticate"), "Spec 089 scope: no challenge header here")
	})

	t.Run("session cookie alone is refused", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/mcp", http.NoBody)
		req.AddCookie(&http.Cookie{Name: teamsauth.SessionCookieName, Value: "sess-0123456789abcdef"})
		seen, code := captureAuthContext(t, srv, req)
		require.Equal(t, http.StatusUnauthorized, code)
		require.Nil(t, seen)
	})

	t.Run("user JWT as Bearer is refused", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/mcp", http.NoBody)
		req.Header.Set("Authorization", "Bearer "+mintUserJWT(t, srv))
		rec := httptest.NewRecorder()
		var seen *auth.AuthContext
		srv.mcpAuthMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			seen = auth.AuthContextFromContext(r.Context())
		})).ServeHTTP(rec, req)
		require.Equal(t, http.StatusUnauthorized, rec.Code)
		require.Nil(t, seen, "a JWT is not an MCP credential (research: no second /mcp credential type)")
		assert.Contains(t, rec.Body.String(), "Invalid authentication token")
	})

	t.Run("user JWT as X-API-Key is refused", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/mcp", http.NoBody)
		req.Header.Set("X-API-Key", mintUserJWT(t, srv))
		seen, code := captureAuthContext(t, srv, req)
		require.Equal(t, http.StatusUnauthorized, code)
		require.Nil(t, seen)
	})

	t.Run("user JWT plus session cookie is refused", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/mcp", http.NoBody)
		req.Header.Set("Authorization", "Bearer "+mintUserJWT(t, srv))
		req.AddCookie(&http.Cookie{Name: teamsauth.SessionCookieName, Value: "sess-0123456789abcdef"})
		seen, code := captureAuthContext(t, srv, req)
		require.Equal(t, http.StatusUnauthorized, code)
		require.Nil(t, seen)
	})

	t.Run("every /mcp route is refused without a credential", func(t *testing.T) {
		for _, path := range []string{"/mcp", "/mcp/all", "/mcp/code", "/mcp/call", "/mcp/p/research"} {
			seen, code := captureAuthContext(t, srv, httptest.NewRequest(http.MethodPost, path, http.NoBody))
			assert.Equal(t, http.StatusUnauthorized, code, path)
			assert.Nil(t, seen, path)
		}
	})

	t.Run("agent token is unchanged", func(t *testing.T) {
		raw := mintAgentToken(t, srv, "fr029-agent")
		req := httptest.NewRequest(http.MethodPost, "/mcp", http.NoBody)
		req.Header.Set("Authorization", "Bearer "+raw)
		seen, code := captureAuthContext(t, srv, req)
		require.Equal(t, http.StatusOK, code)
		require.NotNil(t, seen)
		assert.Equal(t, auth.AuthTypeAgent, seen.Type)
		assert.Equal(t, "fr029-agent", seen.AgentName)
		assert.False(t, seen.Anonymous)
	})

	t.Run("global API key is unchanged", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/mcp", http.NoBody)
		req.Header.Set("X-API-Key", apiKey)
		seen, code := captureAuthContext(t, srv, req)
		require.Equal(t, http.StatusOK, code)
		require.NotNil(t, seen)
		assert.True(t, seen.IsAdmin())
		assert.False(t, seen.Anonymous)
		assert.True(t, seen.CanRevealSecrets())
	})

	t.Run("socket connection without a credential is unchanged", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/mcp", http.NoBody)
		req = req.WithContext(transport.TagConnectionContext(req.Context(), transport.ConnectionSourceTray))
		seen, code := captureAuthContext(t, srv, req)
		require.Equal(t, http.StatusOK, code)
		require.NotNil(t, seen)
		assert.True(t, seen.IsAdmin())
		assert.False(t, seen.Anonymous, "OS-level socket auth is a real identity")
	})

	t.Run("socket connection with an unrecognised token is unchanged", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/mcp", http.NoBody)
		req.Header.Set("X-API-Key", "not-the-key")
		req = req.WithContext(transport.TagConnectionContext(req.Context(), transport.ConnectionSourceTray))
		seen, code := captureAuthContext(t, srv, req)
		require.Equal(t, http.StatusOK, code)
		require.NotNil(t, seen)
		assert.False(t, seen.Anonymous)
	})

	t.Run("no AnonymousContext is ever handed out", func(t *testing.T) {
		// FR-029 invariant: in the published container no unauthenticated
		// caller receives an admin-typed AnonymousContext (server.go:381-384,
		// :455-458). Sweep the credential shapes that used to reach it.
		shapes := map[string]func(*http.Request){
			"nothing":        func(*http.Request) {},
			"unknown bearer": func(r *http.Request) { r.Header.Set("Authorization", "Bearer not-a-credential") },
			"unknown apikey": func(r *http.Request) { r.Header.Set("X-API-Key", "not-a-credential") },
			"query apikey":   func(r *http.Request) { r.URL.RawQuery = "apikey=not-a-credential" },
			"cookie":         func(r *http.Request) { r.AddCookie(&http.Cookie{Name: teamsauth.SessionCookieName, Value: "x"}) },
		}
		for name, shape := range shapes {
			req := httptest.NewRequest(http.MethodPost, "/mcp", http.NoBody)
			shape(req)
			seen, code := captureAuthContext(t, srv, req)
			assert.Equal(t, http.StatusUnauthorized, code, name)
			if seen != nil {
				assert.False(t, seen.Anonymous, "%s: anonymous admin context reached the handler", name)
			}
		}
	})
}

// TestMCPAuthMiddleware_ServerBuildDisabledBlockKeepsBackCompat proves the
// override keys on `server_edition.enabled`, not on the build tag: a server
// build with the block disabled behaves exactly like the personal build.
func TestMCPAuthMiddleware_ServerBuildDisabledBlockKeepsBackCompat(t *testing.T) {
	srv := newForcedAuthTestServer(t, forcedAuthConfig(t, "test-api-key", false, false), zap.NewNop())

	seen, code := captureAuthContext(t, srv, httptest.NewRequest(http.MethodPost, "/mcp", http.NoBody))
	require.Equal(t, http.StatusOK, code)
	require.NotNil(t, seen)
	assert.True(t, seen.IsAdmin())
	assert.True(t, seen.Anonymous, "back-compat anonymous admin stays for a disabled block (#1148)")
}

func loggedMessages(logs *observer.ObservedLogs) []string {
	var out []string
	for _, e := range logs.All() {
		out = append(out, e.Message)
	}
	return out
}

// TestServerEdition_ForcedMCPAuthBootNotice: an explicit `require_mcp_auth:
// false` under an enabled block is not a validation error (no boot failure on
// upgrade for the installs that run with it off) but boot MUST log exactly one
// notice with the contracts/config-keys.md text. Nothing is logged when the
// value is already true or the block is disabled/absent.
func TestServerEdition_ForcedMCPAuthBootNotice(t *testing.T) {
	notices := func(logs *observer.ObservedLogs) []observer.LoggedEntry {
		var out []observer.LoggedEntry
		for _, e := range logs.All() {
			if strings.Contains(e.Message, "require_mcp_auth") && strings.Contains(e.Message, "overridden") {
				out = append(out, e)
			}
		}
		return out
	}

	t.Run("explicit false under an enabled block logs one notice", func(t *testing.T) {
		core, logs := observer.New(zapcore.InfoLevel)
		newForcedAuthTestServer(t, forcedAuthConfig(t, "k", false, true), zap.New(core))

		got := notices(logs)
		require.Len(t, got, 1, "exactly one boot notice; messages seen: %v", loggedMessages(logs))
		assert.Contains(t, got[0].Message, forcedAuthBootNotice)
		assert.GreaterOrEqual(t, got[0].Level, zapcore.InfoLevel)
		assert.LessOrEqual(t, got[0].Level, zapcore.WarnLevel, "a notice, never an error: the value is overridden, not refused")
	})

	t.Run("explicit true under an enabled block logs nothing", func(t *testing.T) {
		core, logs := observer.New(zapcore.InfoLevel)
		newForcedAuthTestServer(t, forcedAuthConfig(t, "k", true, true), zap.New(core))
		assert.Empty(t, notices(logs))
	})

	t.Run("false under a disabled block logs nothing", func(t *testing.T) {
		core, logs := observer.New(zapcore.InfoLevel)
		newForcedAuthTestServer(t, forcedAuthConfig(t, "k", false, false), zap.New(core))
		assert.Empty(t, notices(logs))
	})

	t.Run("false with no block logs nothing", func(t *testing.T) {
		core, logs := observer.New(zapcore.InfoLevel)
		cfg := forcedAuthConfig(t, "k", false, false)
		cfg.ServerEdition = nil
		newForcedAuthTestServer(t, cfg, zap.New(core))
		assert.Empty(t, notices(logs))
	})
}

// TestConnectServiceWiring_UsesEffectiveRequireMCPAuth pins the connect
// service wiring in server.go to the build-tagged accessor. FR-029 forces
// /mcp authentication under an enabled server_edition regardless of the
// configured require_mcp_auth; connect.Service decides whether to embed a
// credential in generated client configs from exactly the value it is
// threaded. Wiring it from the raw config.RequireMCPAuth field (as it stood
// before cross-review round 3) reintroduces credential-less client configs
// that /mcp then answers 401 to on their first real call — the mirror image
// of the Spec 078 leak this field exists to prevent.
//
// A source guard, not a behavioural one: exercising the real defect needs a
// listening HTTP server (startCustomHTTPServer, not NewServer alone — see the
// file comment above), out of proportion for a P2 wiring regression with an
// accessor already proven correct at every other call site (server.go:378,
// :475, TestEffectiveRequireMCPAuth_ServerBuild above).
func TestConnectServiceWiring_UsesEffectiveRequireMCPAuth(t *testing.T) {
	src, err := os.ReadFile("server.go")
	require.NoError(t, err)
	text := string(src)

	require.True(t, strings.Contains(text, "connect.NewService("), "fixture: the connect wiring block must still exist")
	assert.NotContains(t, text, "WithRequireMCPAuth(cfg.RequireMCPAuth)",
		"connect must be wired from config.EffectiveRequireMCPAuth(cfg), not the raw field (FR-029)")
	assert.NotContains(t, text, "c.Listen, c.APIKey, c.RequireMCPAuth",
		"the live config-provider closure must return config.EffectiveRequireMCPAuth(c), not the raw field (FR-029)")
	assert.Contains(t, text, "WithRequireMCPAuth(config.EffectiveRequireMCPAuth(cfg))")
	assert.Contains(t, text, "c.Listen, c.APIKey, config.EffectiveRequireMCPAuth(c)")
}
