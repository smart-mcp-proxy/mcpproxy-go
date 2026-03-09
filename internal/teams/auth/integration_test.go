//go:build server

package auth

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
	"go.uber.org/zap"

	coreauth "github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/teams/users"
)

// ---------- mock OAuth provider ----------

// mockOAuthProvider is a configurable mock OAuth identity provider that serves
// authorization, token, and userinfo endpoints via httptest.Server.
type mockOAuthProvider struct {
	server    *httptest.Server
	userEmail string
	userName  string
	userID    string
}

func newMockOAuthProvider(t *testing.T, email, name, id string) *mockOAuthProvider {
	t.Helper()

	m := &mockOAuthProvider{userEmail: email, userName: name, userID: id}

	mux := http.NewServeMux()

	// Authorization endpoint: redirects back with code, forwarding the state.
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		state := r.URL.Query().Get("state")
		redirectURI := r.URL.Query().Get("redirect_uri")
		http.Redirect(w, r, redirectURI+"?code=mock_auth_code&state="+state, http.StatusFound)
	})

	// Token endpoint: returns an access_token (no id_token to force userinfo path).
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "mock_access_token",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	})

	// UserInfo endpoint: returns the configured user profile.
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer mock_access_token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"sub":   m.userID,
			"email": m.userEmail,
			"name":  m.userName,
		})
	})

	m.server = httptest.NewServer(mux)
	t.Cleanup(m.server.Close)
	return m
}

// ---------- integration test scaffold ----------

type integrationSetup struct {
	appServer      *httptest.Server
	oauthHandler   *OAuthHandler
	sessionManager *SessionManager
	userStore      *users.UserStore
	teamsConfig    *config.TeamsConfig
	hmacKey        []byte
}

// setupIntegration creates the full stack:
//   - temp BBolt DB + user store
//   - mock OAuth provider
//   - OAuthHandler + SessionManager + TeamsAuthMiddleware
//   - chi router wired up with login / callback / logout / me / token endpoints
//   - httptest.Server serving the chi router
func setupIntegration(t *testing.T, email, name, userSub string, oauthCfg *config.TeamsOAuthConfig, adminEmails []string) *integrationSetup {
	t.Helper()

	// -- Database --
	dbPath := filepath.Join(t.TempDir(), "integration.db")
	db, err := bbolt.Open(dbPath, 0600, &bbolt.Options{Timeout: 1 * time.Second})
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	store := users.NewUserStore(db)
	require.NoError(t, store.EnsureBuckets())

	// -- Mock OAuth provider --
	mockProvider := newMockOAuthProvider(t, email, name, userSub)

	// Register the mock provider in the package-level registry so that
	// GetProvider("google", ...) returns endpoints pointing at the mock.
	origFactory := providerRegistry["google"]
	providerRegistry["google"] = func(_ string) *OAuthProvider {
		return &OAuthProvider{
			Name:         "google",
			AuthURL:      mockProvider.server.URL + "/authorize",
			TokenURL:     mockProvider.server.URL + "/token",
			UserInfoURL:  mockProvider.server.URL + "/userinfo",
			Scopes:       []string{"openid", "email", "profile"},
			SupportsOIDC: true,
			SupportsPKCE: true,
		}
	}
	t.Cleanup(func() { providerRegistry["google"] = origFactory })

	// -- Config --
	if adminEmails == nil {
		adminEmails = []string{"admin@test.com"}
	}
	sessionTTL := 24 * time.Hour
	bearerTTL := 24 * time.Hour
	teamsCfg := &config.TeamsConfig{
		Enabled:        true,
		AdminEmails:    adminEmails,
		OAuth:          oauthCfg,
		SessionTTL:     config.Duration(sessionTTL),
		BearerTokenTTL: config.Duration(bearerTTL),
	}

	hmacKey := []byte("integration-test-hmac-key-32bytes")
	logger := zap.NewNop().Sugar()

	// -- Components --
	sessionMgr := NewSessionManager(store, sessionTTL, false)
	oauthH := NewOAuthHandler(store, sessionMgr, teamsCfg, hmacKey, logger)
	authMW := NewTeamsAuthMiddleware(sessionMgr, store, teamsCfg, hmacKey, logger)

	// -- Router --
	r := chi.NewRouter()

	// Public auth routes (no middleware).
	r.Get("/api/v1/auth/login", oauthH.HandleLogin)
	r.Get("/api/v1/auth/callback", oauthH.HandleCallback)

	// Protected routes (require auth middleware).
	r.Group(func(r chi.Router) {
		r.Use(authMW.Middleware())
		r.Post("/api/v1/auth/logout", oauthH.HandleLogout)
		r.Get("/api/v1/auth/me", func(w http.ResponseWriter, req *http.Request) {
			ac := coreauth.AuthContextFromContext(req.Context())
			if ac == nil || ac.GetUserID() == "" {
				writeJSONError(w, http.StatusUnauthorized, "Authentication required")
				return
			}
			user, err := store.GetUser(ac.GetUserID())
			if err != nil || user == nil {
				writeJSONError(w, http.StatusInternalServerError, "User not found")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":           user.ID,
				"email":        user.Email,
				"display_name": user.DisplayName,
				"role":         ac.Role,
				"provider":     user.Provider,
			})
		})
		r.Post("/api/v1/auth/token", func(w http.ResponseWriter, req *http.Request) {
			ac := coreauth.AuthContextFromContext(req.Context())
			if ac == nil || ac.GetUserID() == "" {
				writeJSONError(w, http.StatusUnauthorized, "Authentication required")
				return
			}
			user, err := store.GetUser(ac.GetUserID())
			if err != nil || user == nil {
				writeJSONError(w, http.StatusInternalServerError, "User not found")
				return
			}
			ttl := teamsCfg.BearerTokenTTL.Duration()
			token, err := GenerateBearerToken(hmacKey, user.ID, user.Email, user.DisplayName, ac.Role, user.Provider, ttl)
			if err != nil {
				writeJSONError(w, http.StatusInternalServerError, "Failed to generate token")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"token":      token,
				"expires_at": time.Now().UTC().Add(ttl).Format(time.RFC3339),
			})
		})
	})

	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	return &integrationSetup{
		appServer:      srv,
		oauthHandler:   oauthH,
		sessionManager: sessionMgr,
		userStore:      store,
		teamsConfig:    teamsCfg,
		hmacKey:        hmacKey,
	}
}

// ---------- Full OAuth Login Flow ----------

func TestIntegration_FullOAuthFlow(t *testing.T) {
	s := setupIntegration(t,
		"alice@example.com", "Alice Test", "google-sub-alice",
		&config.TeamsOAuthConfig{
			Provider:     "google",
			ClientID:     "mock-client-id",
			ClientSecret: "mock-client-secret",
		},
		[]string{"admin@test.com"}, // alice is a regular user
	)

	// We need an HTTP client that does NOT follow redirects automatically so
	// we can inspect the intermediate 302 responses.
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// -------- (a) Login initiation --------
	loginResp, err := client.Get(s.appServer.URL + "/api/v1/auth/login")
	require.NoError(t, err)
	defer loginResp.Body.Close()

	assert.Equal(t, http.StatusFound, loginResp.StatusCode, "login should redirect")

	loginLocation := loginResp.Header.Get("Location")
	require.NotEmpty(t, loginLocation)

	loginRedirect, err := url.Parse(loginLocation)
	require.NoError(t, err)

	// The redirect should point to the mock provider's /authorize endpoint.
	assert.Contains(t, loginRedirect.Path, "/authorize")
	assert.Equal(t, "mock-client-id", loginRedirect.Query().Get("client_id"))

	state := loginRedirect.Query().Get("state")
	assert.NotEmpty(t, state, "state parameter must be present")
	assert.Len(t, state, 64, "state should be 32 bytes hex-encoded")

	// -------- (b) OAuth callback --------
	// The mock provider's /authorize would redirect to
	//   <callback>?code=mock_auth_code&state=<state>
	// We simulate calling the callback URL directly with the extracted state.
	callbackURL := s.appServer.URL + "/api/v1/auth/callback?code=mock_auth_code&state=" + state
	callbackResp, err := client.Get(callbackURL)
	require.NoError(t, err)
	defer callbackResp.Body.Close()

	assert.Equal(t, http.StatusFound, callbackResp.StatusCode, "callback should redirect to dashboard")
	assert.Equal(t, "/ui/", callbackResp.Header.Get("Location"))

	// Extract session cookie.
	var sessionCookie *http.Cookie
	for _, c := range callbackResp.Cookies() {
		if c.Name == SessionCookieName {
			sessionCookie = c
			break
		}
	}
	require.NotNil(t, sessionCookie, "session cookie must be set after callback")
	assert.NotEmpty(t, sessionCookie.Value)

	// -------- (c) Authenticated request: /auth/me via session --------
	meReq, _ := http.NewRequest(http.MethodGet, s.appServer.URL+"/api/v1/auth/me", nil)
	meReq.AddCookie(sessionCookie)
	meResp, err := client.Do(meReq)
	require.NoError(t, err)
	defer meResp.Body.Close()

	assert.Equal(t, http.StatusOK, meResp.StatusCode)

	var meBody map[string]interface{}
	require.NoError(t, json.NewDecoder(meResp.Body).Decode(&meBody))
	assert.Equal(t, "alice@example.com", meBody["email"])
	assert.Equal(t, "Alice Test", meBody["display_name"])
	assert.Equal(t, "user", meBody["role"])
	assert.Equal(t, "google", meBody["provider"])
	assert.NotEmpty(t, meBody["id"])

	// -------- (d) Generate bearer token --------
	tokenReq, _ := http.NewRequest(http.MethodPost, s.appServer.URL+"/api/v1/auth/token", nil)
	tokenReq.AddCookie(sessionCookie)
	tokenResp, err := client.Do(tokenReq)
	require.NoError(t, err)
	defer tokenResp.Body.Close()

	assert.Equal(t, http.StatusOK, tokenResp.StatusCode)

	var tokenBody map[string]interface{}
	require.NoError(t, json.NewDecoder(tokenResp.Body).Decode(&tokenBody))
	bearerToken, ok := tokenBody["token"].(string)
	require.True(t, ok, "token field must be a string")
	assert.NotEmpty(t, bearerToken)
	assert.NotEmpty(t, tokenBody["expires_at"])

	// Validate JWT claims.
	claims, err := ValidateBearerToken(bearerToken, s.hmacKey)
	require.NoError(t, err)
	assert.Equal(t, "alice@example.com", claims.Email)
	assert.Equal(t, "user", claims.Role)
	assert.Equal(t, "google", claims.Provider)

	// -------- (e) Bearer token access: /auth/me --------
	meBearerReq, _ := http.NewRequest(http.MethodGet, s.appServer.URL+"/api/v1/auth/me", nil)
	meBearerReq.Header.Set("Authorization", "Bearer "+bearerToken)
	meBearerResp, err := client.Do(meBearerReq)
	require.NoError(t, err)
	defer meBearerResp.Body.Close()

	assert.Equal(t, http.StatusOK, meBearerResp.StatusCode)

	var meBearerBody map[string]interface{}
	require.NoError(t, json.NewDecoder(meBearerResp.Body).Decode(&meBearerBody))
	assert.Equal(t, "alice@example.com", meBearerBody["email"])
	assert.Equal(t, "Alice Test", meBearerBody["display_name"])

	// -------- (f) Logout --------
	logoutReq, _ := http.NewRequest(http.MethodPost, s.appServer.URL+"/api/v1/auth/logout", nil)
	logoutReq.AddCookie(sessionCookie)
	logoutResp, err := client.Do(logoutReq)
	require.NoError(t, err)
	defer logoutResp.Body.Close()

	assert.Equal(t, http.StatusOK, logoutResp.StatusCode)

	// Verify session cookie is cleared.
	var clearedCookie *http.Cookie
	for _, c := range logoutResp.Cookies() {
		if c.Name == SessionCookieName {
			clearedCookie = c
			break
		}
	}
	require.NotNil(t, clearedCookie, "session cookie should be in logout response")
	assert.Equal(t, -1, clearedCookie.MaxAge, "cookie MaxAge should be -1 to clear")

	var logoutBody map[string]interface{}
	require.NoError(t, json.NewDecoder(logoutResp.Body).Decode(&logoutBody))
	assert.Equal(t, "logged_out", logoutBody["status"])

	// -------- (g) Post-logout: session cookie rejected --------
	postLogoutReq, _ := http.NewRequest(http.MethodGet, s.appServer.URL+"/api/v1/auth/me", nil)
	postLogoutReq.AddCookie(sessionCookie) // same cookie, session revoked
	postLogoutResp, err := client.Do(postLogoutReq)
	require.NoError(t, err)
	defer postLogoutResp.Body.Close()

	assert.Equal(t, http.StatusUnauthorized, postLogoutResp.StatusCode,
		"old session cookie should be rejected after logout")
}

// ---------- Admin user role ----------

func TestIntegration_AdminUser(t *testing.T) {
	s := setupIntegration(t,
		"admin@test.com", "Admin Boss", "google-sub-admin",
		&config.TeamsOAuthConfig{
			Provider:     "google",
			ClientID:     "mock-client-id",
			ClientSecret: "mock-client-secret",
		},
		[]string{"admin@test.com"},
	)

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// Login
	loginResp, err := client.Get(s.appServer.URL + "/api/v1/auth/login")
	require.NoError(t, err)
	defer loginResp.Body.Close()
	require.Equal(t, http.StatusFound, loginResp.StatusCode)

	location := loginResp.Header.Get("Location")
	redirectURL, err := url.Parse(location)
	require.NoError(t, err)
	state := redirectURL.Query().Get("state")

	// Callback
	cbResp, err := client.Get(s.appServer.URL + "/api/v1/auth/callback?code=mock_auth_code&state=" + state)
	require.NoError(t, err)
	defer cbResp.Body.Close()
	require.Equal(t, http.StatusFound, cbResp.StatusCode)

	var sessionCookie *http.Cookie
	for _, c := range cbResp.Cookies() {
		if c.Name == SessionCookieName {
			sessionCookie = c
			break
		}
	}
	require.NotNil(t, sessionCookie)

	// Check /me => role should be "admin"
	meReq, _ := http.NewRequest(http.MethodGet, s.appServer.URL+"/api/v1/auth/me", nil)
	meReq.AddCookie(sessionCookie)
	meResp, err := client.Do(meReq)
	require.NoError(t, err)
	defer meResp.Body.Close()

	assert.Equal(t, http.StatusOK, meResp.StatusCode)

	var meBody map[string]interface{}
	require.NoError(t, json.NewDecoder(meResp.Body).Decode(&meBody))
	assert.Equal(t, "admin@test.com", meBody["email"])
	assert.Equal(t, "Admin Boss", meBody["display_name"])
	assert.Equal(t, "admin", meBody["role"], "admin email should produce admin role")
}

// ---------- Domain restriction ----------

func TestIntegration_DomainRestriction(t *testing.T) {
	s := setupIntegration(t,
		"user@forbidden.org", "Forbidden User", "google-sub-forbidden",
		&config.TeamsOAuthConfig{
			Provider:       "google",
			ClientID:       "mock-client-id",
			ClientSecret:   "mock-client-secret",
			AllowedDomains: []string{"acme.com", "example.com"},
		},
		[]string{"admin@acme.com"},
	)

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// Login to get a valid state.
	loginResp, err := client.Get(s.appServer.URL + "/api/v1/auth/login")
	require.NoError(t, err)
	defer loginResp.Body.Close()
	require.Equal(t, http.StatusFound, loginResp.StatusCode)

	location := loginResp.Header.Get("Location")
	redirectURL, err := url.Parse(location)
	require.NoError(t, err)
	state := redirectURL.Query().Get("state")

	// Callback: should get 403 because the email domain is not allowed.
	cbResp, err := client.Get(s.appServer.URL + "/api/v1/auth/callback?code=mock_auth_code&state=" + state)
	require.NoError(t, err)
	defer cbResp.Body.Close()

	assert.Equal(t, http.StatusForbidden, cbResp.StatusCode,
		"login from disallowed domain should be rejected with 403")

	body, _ := io.ReadAll(cbResp.Body)
	assert.Contains(t, string(body), "domain not allowed")
}

// ---------- Expired session ----------

func TestIntegration_ExpiredSession(t *testing.T) {
	// Use a very short session TTL. We create the session manually with a
	// past expiry so the test does not need to sleep.
	s := setupIntegration(t,
		"alice@example.com", "Alice", "google-sub-alice",
		&config.TeamsOAuthConfig{
			Provider:     "google",
			ClientID:     "mock-client-id",
			ClientSecret: "mock-client-secret",
		},
		nil,
	)

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// First, log in normally to create a user in the store.
	loginResp, err := client.Get(s.appServer.URL + "/api/v1/auth/login")
	require.NoError(t, err)
	defer loginResp.Body.Close()
	require.Equal(t, http.StatusFound, loginResp.StatusCode)

	location := loginResp.Header.Get("Location")
	redirectURL, err := url.Parse(location)
	require.NoError(t, err)
	state := redirectURL.Query().Get("state")

	cbResp, err := client.Get(s.appServer.URL + "/api/v1/auth/callback?code=mock_auth_code&state=" + state)
	require.NoError(t, err)
	defer cbResp.Body.Close()
	require.Equal(t, http.StatusFound, cbResp.StatusCode)

	// Get the user ID from the store.
	user, err := s.userStore.GetUserByEmail("alice@example.com")
	require.NoError(t, err)
	require.NotNil(t, user)

	// Create a session that is already expired (past ExpiresAt).
	expiredSession := &users.Session{
		ID:        "expired-integration-session-id",
		UserID:    user.ID,
		CreatedAt: time.Now().UTC().Add(-2 * time.Hour),
		ExpiresAt: time.Now().UTC().Add(-1 * time.Hour), // already expired
	}
	require.NoError(t, s.userStore.CreateSession(expiredSession))

	// Try to access /auth/me with the expired session.
	meReq, _ := http.NewRequest(http.MethodGet, s.appServer.URL+"/api/v1/auth/me", nil)
	meReq.AddCookie(&http.Cookie{
		Name:  SessionCookieName,
		Value: expiredSession.ID,
	})
	meResp, err := client.Do(meReq)
	require.NoError(t, err)
	defer meResp.Body.Close()

	assert.Equal(t, http.StatusUnauthorized, meResp.StatusCode,
		"expired session should be rejected with 401")
}

// ---------- Unauthenticated access ----------

func TestIntegration_UnauthenticatedAccess(t *testing.T) {
	s := setupIntegration(t,
		"alice@example.com", "Alice", "google-sub-alice",
		&config.TeamsOAuthConfig{
			Provider:     "google",
			ClientID:     "mock-client-id",
			ClientSecret: "mock-client-secret",
		},
		nil,
	)

	client := &http.Client{}

	// /auth/me without any cookie or bearer token.
	resp, err := client.Get(s.appServer.URL + "/api/v1/auth/me")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	var body map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Contains(t, body["message"], "Authentication required")
}

// ---------- Bearer token survives logout ----------

func TestIntegration_BearerTokenSurvivesSessionLogout(t *testing.T) {
	// After a user logs out (session revoked), a previously-issued JWT bearer
	// token should still work because JWTs are stateless and validated by
	// signature + user existence, not by session.
	s := setupIntegration(t,
		"bob@example.com", "Bob Builder", "google-sub-bob",
		&config.TeamsOAuthConfig{
			Provider:     "google",
			ClientID:     "mock-client-id",
			ClientSecret: "mock-client-secret",
		},
		nil,
	)

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// Login flow
	loginResp, err := client.Get(s.appServer.URL + "/api/v1/auth/login")
	require.NoError(t, err)
	defer loginResp.Body.Close()
	require.Equal(t, http.StatusFound, loginResp.StatusCode)

	location := loginResp.Header.Get("Location")
	redirectURL, err := url.Parse(location)
	require.NoError(t, err)
	state := redirectURL.Query().Get("state")

	cbResp, err := client.Get(s.appServer.URL + "/api/v1/auth/callback?code=mock_auth_code&state=" + state)
	require.NoError(t, err)
	defer cbResp.Body.Close()
	require.Equal(t, http.StatusFound, cbResp.StatusCode)

	var sessionCookie *http.Cookie
	for _, c := range cbResp.Cookies() {
		if c.Name == SessionCookieName {
			sessionCookie = c
			break
		}
	}
	require.NotNil(t, sessionCookie)

	// Generate a bearer token while still logged in.
	tokenReq, _ := http.NewRequest(http.MethodPost, s.appServer.URL+"/api/v1/auth/token", nil)
	tokenReq.AddCookie(sessionCookie)
	tokenResp, err := client.Do(tokenReq)
	require.NoError(t, err)
	defer tokenResp.Body.Close()
	require.Equal(t, http.StatusOK, tokenResp.StatusCode)

	var tokenBody map[string]interface{}
	require.NoError(t, json.NewDecoder(tokenResp.Body).Decode(&tokenBody))
	bearerToken := tokenBody["token"].(string)

	// Logout (revoke session).
	logoutReq, _ := http.NewRequest(http.MethodPost, s.appServer.URL+"/api/v1/auth/logout", nil)
	logoutReq.AddCookie(sessionCookie)
	logoutResp, err := client.Do(logoutReq)
	require.NoError(t, err)
	defer logoutResp.Body.Close()
	require.Equal(t, http.StatusOK, logoutResp.StatusCode)

	// Bearer token should still work (JWT validated by signature, not session).
	meReq, _ := http.NewRequest(http.MethodGet, s.appServer.URL+"/api/v1/auth/me", nil)
	meReq.Header.Set("Authorization", "Bearer "+bearerToken)
	meResp, err := client.Do(meReq)
	require.NoError(t, err)
	defer meResp.Body.Close()

	assert.Equal(t, http.StatusOK, meResp.StatusCode,
		"bearer token should remain valid after session logout")

	var meBody map[string]interface{}
	require.NoError(t, json.NewDecoder(meResp.Body).Decode(&meBody))
	assert.Equal(t, "bob@example.com", meBody["email"])
}
