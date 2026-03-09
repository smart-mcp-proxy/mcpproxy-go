//go:build server

package auth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/teams/users"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
	"go.uber.org/zap"
)

// setupTestOAuthHandler creates an OAuthHandler with a real BBolt store
// and a mock OAuth provider server.
func setupTestOAuthHandler(t *testing.T, oauthCfg *config.TeamsOAuthConfig) (*OAuthHandler, *users.UserStore) {
	t.Helper()

	tmpFile := filepath.Join(t.TempDir(), "test.db")
	db, err := bbolt.Open(tmpFile, 0600, &bbolt.Options{Timeout: 1 * time.Second})
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	store := users.NewUserStore(db)
	require.NoError(t, store.EnsureBuckets())

	sessionMgr := NewSessionManager(store, time.Hour, false)

	teamsCfg := &config.TeamsConfig{
		Enabled:        true,
		AdminEmails:    []string{"admin@example.com"},
		OAuth:          oauthCfg,
		SessionTTL:     config.Duration(time.Hour),
		BearerTokenTTL: config.Duration(time.Hour),
	}

	logger := zap.NewNop().Sugar()
	hmacKey := []byte("test-hmac-key-for-jwt-signing-32b")

	handler := NewOAuthHandler(store, sessionMgr, teamsCfg, hmacKey, logger)
	return handler, store
}

// mockOAuthProviderServer creates a httptest.Server that simulates an OAuth provider.
// It handles /token and /userinfo endpoints.
func mockOAuthProviderServer(t *testing.T, userEmail, userName, userSub string) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()

	// Token endpoint
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "mock-access-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	})

	// UserInfo endpoint
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer mock-access-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"sub":     userSub,
			"email":   userEmail,
			"name":    userName,
			"picture": "https://example.com/avatar.png",
		})
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

// registerMockProvider temporarily replaces the "google" provider factory
// with one that points to the mock server. The original is restored on cleanup.
func registerMockProvider(t *testing.T, mockServer *httptest.Server) {
	t.Helper()

	originalFactory := providerRegistry["google"]

	providerRegistry["google"] = func(_ string) *OAuthProvider {
		return &OAuthProvider{
			Name:         "google",
			AuthURL:      mockServer.URL + "/authorize",
			TokenURL:     mockServer.URL + "/token",
			UserInfoURL:  mockServer.URL + "/userinfo",
			Scopes:       []string{"openid", "email", "profile"},
			SupportsOIDC: true,
			SupportsPKCE: true,
		}
	}

	t.Cleanup(func() {
		providerRegistry["google"] = originalFactory
	})
}

func TestHandleLogin_Redirects(t *testing.T) {
	mockServer := mockOAuthProviderServer(t, "user@example.com", "Test User", "sub-123")
	registerMockProvider(t, mockServer)

	handler, _ := setupTestOAuthHandler(t, &config.TeamsOAuthConfig{
		Provider:     "google",
		ClientID:     "test-client-id",
		ClientSecret: "test-client-secret",
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/login", nil)
	w := httptest.NewRecorder()

	handler.HandleLogin(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	assert.Equal(t, http.StatusFound, resp.StatusCode)

	location := resp.Header.Get("Location")
	require.NotEmpty(t, location, "should have Location header")

	// Parse the redirect URL and verify it points to the mock provider
	redirectURL, err := url.Parse(location)
	require.NoError(t, err)
	assert.Equal(t, mockServer.URL+"/authorize", redirectURL.Scheme+"://"+redirectURL.Host+redirectURL.Path)

	// Verify required OAuth parameters
	params := redirectURL.Query()
	assert.Equal(t, "test-client-id", params.Get("client_id"))
	assert.Equal(t, "code", params.Get("response_type"))
	assert.Contains(t, params.Get("scope"), "openid")
}

func TestHandleLogin_StateInURL(t *testing.T) {
	mockServer := mockOAuthProviderServer(t, "user@example.com", "Test User", "sub-123")
	registerMockProvider(t, mockServer)

	handler, _ := setupTestOAuthHandler(t, &config.TeamsOAuthConfig{
		Provider:     "google",
		ClientID:     "test-client-id",
		ClientSecret: "test-client-secret",
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/login", nil)
	w := httptest.NewRecorder()

	handler.HandleLogin(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	location := resp.Header.Get("Location")
	redirectURL, err := url.Parse(location)
	require.NoError(t, err)

	state := redirectURL.Query().Get("state")
	assert.NotEmpty(t, state, "state parameter must be present in redirect URL")
	// State should be 64 hex characters (32 bytes)
	assert.Len(t, state, 64, "state should be 32 bytes hex-encoded")

	// Verify PKCE code_challenge is present (provider supports PKCE)
	codeChallenge := redirectURL.Query().Get("code_challenge")
	assert.NotEmpty(t, codeChallenge, "code_challenge should be present")
	assert.Equal(t, "S256", redirectURL.Query().Get("code_challenge_method"))

	// Verify the state is stored in pendingStates
	handler.statesMu.Lock()
	pending, exists := handler.pendingStates[state]
	handler.statesMu.Unlock()
	assert.True(t, exists, "state should be stored in pendingStates")
	assert.NotEmpty(t, pending.CodeVerifier, "code verifier should be stored")
}

func TestHandleCallback_Success(t *testing.T) {
	mockServer := mockOAuthProviderServer(t, "user@example.com", "Test User", "sub-123")
	registerMockProvider(t, mockServer)

	handler, store := setupTestOAuthHandler(t, &config.TeamsOAuthConfig{
		Provider:     "google",
		ClientID:     "test-client-id",
		ClientSecret: "test-client-secret",
	})

	// Pre-populate a pending state (simulating HandleLogin)
	testState := "abc123def456abc123def456abc123def456abc123def456abc123def456abcd"
	handler.statesMu.Lock()
	handler.pendingStates[testState] = &oauthState{
		CodeVerifier: "test-code-verifier",
		RedirectURI:  "/dashboard",
		CreatedAt:    time.Now(),
	}
	handler.statesMu.Unlock()

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/v1/auth/callback?code=test-auth-code&state=%s", testState), nil)
	req.Host = "localhost:8080"
	w := httptest.NewRecorder()

	handler.HandleCallback(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	// Should redirect to the dashboard
	assert.Equal(t, http.StatusFound, resp.StatusCode)
	assert.Equal(t, "/dashboard", resp.Header.Get("Location"))

	// Verify session cookie was set
	cookies := resp.Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == SessionCookieName {
			sessionCookie = c
			break
		}
	}
	require.NotNil(t, sessionCookie, "session cookie should be set")
	assert.NotEmpty(t, sessionCookie.Value)

	// Verify user was created in the store
	user, err := store.GetUserByEmail("user@example.com")
	require.NoError(t, err)
	require.NotNil(t, user, "user should be created in store")
	assert.Equal(t, "user@example.com", user.Email)
	assert.Equal(t, "Test User", user.DisplayName)
	assert.Equal(t, "google", user.Provider)
	assert.Equal(t, "sub-123", user.ProviderSubjectID)

	// Verify the state was consumed (removed from pendingStates)
	handler.statesMu.Lock()
	_, exists := handler.pendingStates[testState]
	handler.statesMu.Unlock()
	assert.False(t, exists, "state should be consumed after callback")
}

func TestHandleCallback_InvalidState(t *testing.T) {
	mockServer := mockOAuthProviderServer(t, "user@example.com", "Test User", "sub-123")
	registerMockProvider(t, mockServer)

	handler, _ := setupTestOAuthHandler(t, &config.TeamsOAuthConfig{
		Provider:     "google",
		ClientID:     "test-client-id",
		ClientSecret: "test-client-secret",
	})

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/auth/callback?code=test-auth-code&state=invalid-state-value", nil)
	w := httptest.NewRecorder()

	handler.HandleCallback(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	var errResp map[string]interface{}
	err := json.NewDecoder(resp.Body).Decode(&errResp)
	require.NoError(t, err)
	assert.Contains(t, errResp["message"], "invalid or expired state")
}

func TestHandleCallback_MissingCode(t *testing.T) {
	mockServer := mockOAuthProviderServer(t, "user@example.com", "Test User", "sub-123")
	registerMockProvider(t, mockServer)

	handler, _ := setupTestOAuthHandler(t, &config.TeamsOAuthConfig{
		Provider:     "google",
		ClientID:     "test-client-id",
		ClientSecret: "test-client-secret",
	})

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/auth/callback?state=some-state", nil)
	w := httptest.NewRecorder()

	handler.HandleCallback(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	var errResp map[string]interface{}
	err := json.NewDecoder(resp.Body).Decode(&errResp)
	require.NoError(t, err)
	assert.Contains(t, errResp["message"], "missing code")
}

func TestHandleCallback_DomainNotAllowed(t *testing.T) {
	mockServer := mockOAuthProviderServer(t, "user@unauthorized.com", "Test User", "sub-123")
	registerMockProvider(t, mockServer)

	handler, _ := setupTestOAuthHandler(t, &config.TeamsOAuthConfig{
		Provider:       "google",
		ClientID:       "test-client-id",
		ClientSecret:   "test-client-secret",
		AllowedDomains: []string{"example.com", "acme.org"},
	})

	// Pre-populate a pending state
	testState := "domain-check-state-0123456789abcdef0123456789abcdef01234567890a"
	handler.statesMu.Lock()
	handler.pendingStates[testState] = &oauthState{
		CodeVerifier: "test-code-verifier",
		RedirectURI:  "/dashboard",
		CreatedAt:    time.Now(),
	}
	handler.statesMu.Unlock()

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/v1/auth/callback?code=test-auth-code&state=%s", testState), nil)
	req.Host = "localhost:8080"
	w := httptest.NewRecorder()

	handler.HandleCallback(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	assert.Equal(t, http.StatusForbidden, resp.StatusCode)

	var errResp map[string]interface{}
	err := json.NewDecoder(resp.Body).Decode(&errResp)
	require.NoError(t, err)
	assert.Contains(t, errResp["message"], "domain not allowed")
}

func TestHandleCallback_ExistingUser(t *testing.T) {
	mockServer := mockOAuthProviderServer(t, "existing@example.com", "Updated Name", "sub-existing")
	registerMockProvider(t, mockServer)

	handler, store := setupTestOAuthHandler(t, &config.TeamsOAuthConfig{
		Provider:     "google",
		ClientID:     "test-client-id",
		ClientSecret: "test-client-secret",
	})

	// Pre-create the user
	existingUser := users.NewUser("existing@example.com", "Original Name", "google", "sub-existing")
	originalLoginTime := existingUser.LastLoginAt
	require.NoError(t, store.CreateUser(existingUser))

	// Wait a moment so LastLoginAt will be different
	time.Sleep(10 * time.Millisecond)

	// Pre-populate a pending state
	testState := "existing-user-state-0123456789abcdef0123456789abcdef012345678a"
	handler.statesMu.Lock()
	handler.pendingStates[testState] = &oauthState{
		CodeVerifier: "test-code-verifier",
		RedirectURI:  "/dashboard",
		CreatedAt:    time.Now(),
	}
	handler.statesMu.Unlock()

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/v1/auth/callback?code=test-auth-code&state=%s", testState), nil)
	req.Host = "localhost:8080"
	w := httptest.NewRecorder()

	handler.HandleCallback(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	assert.Equal(t, http.StatusFound, resp.StatusCode)

	// Verify the user was updated, not duplicated
	allUsers, err := store.ListUsers()
	require.NoError(t, err)

	// Count users with this email
	count := 0
	for _, u := range allUsers {
		if u.Email == "existing@example.com" {
			count++
		}
	}
	assert.Equal(t, 1, count, "should not create duplicate user")

	// Verify LastLoginAt was updated
	updatedUser, err := store.GetUserByEmail("existing@example.com")
	require.NoError(t, err)
	require.NotNil(t, updatedUser)
	assert.True(t, updatedUser.LastLoginAt.After(originalLoginTime),
		"LastLoginAt should be updated (was %v, now %v)", originalLoginTime, updatedUser.LastLoginAt)

	// Verify display name was updated
	assert.Equal(t, "Updated Name", updatedUser.DisplayName)
}

func TestHandleLogout_ClearsCookie(t *testing.T) {
	handler, store := setupTestOAuthHandler(t, &config.TeamsOAuthConfig{
		Provider:     "google",
		ClientID:     "test-client-id",
		ClientSecret: "test-client-secret",
	})

	// Create a user and session
	user := users.NewUser("logout@example.com", "Logout User", "google", "sub-logout")
	require.NoError(t, store.CreateUser(user))

	session := users.NewSession(user.ID, time.Hour)
	require.NoError(t, store.CreateSession(session))

	// Create logout request with session cookie
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	req.AddCookie(&http.Cookie{
		Name:  SessionCookieName,
		Value: session.ID,
	})
	w := httptest.NewRecorder()

	handler.HandleLogout(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Verify cookie is cleared (MaxAge=-1)
	cookies := resp.Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == SessionCookieName {
			sessionCookie = c
			break
		}
	}
	require.NotNil(t, sessionCookie, "session cookie should be present in response")
	assert.Equal(t, -1, sessionCookie.MaxAge, "session cookie MaxAge should be -1 to clear it")
	assert.Empty(t, sessionCookie.Value, "session cookie value should be empty")

	// Verify session was deleted from store
	deletedSession, err := store.GetSession(session.ID)
	require.NoError(t, err)
	assert.Nil(t, deletedSession, "session should be deleted from store")

	// Verify response body
	var body map[string]string
	err = json.NewDecoder(resp.Body).Decode(&body)
	require.NoError(t, err)
	assert.Equal(t, "logged_out", body["status"])
}

func TestHandleLogout_NoSession(t *testing.T) {
	handler, _ := setupTestOAuthHandler(t, &config.TeamsOAuthConfig{
		Provider:     "google",
		ClientID:     "test-client-id",
		ClientSecret: "test-client-secret",
	})

	// Logout request without session cookie
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	w := httptest.NewRecorder()

	handler.HandleLogout(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	var errResp map[string]interface{}
	err := json.NewDecoder(resp.Body).Decode(&errResp)
	require.NoError(t, err)
	assert.Contains(t, errResp["message"], "not authenticated")
}

func TestCleanupStaleStates(t *testing.T) {
	handler, _ := setupTestOAuthHandler(t, &config.TeamsOAuthConfig{
		Provider:     "google",
		ClientID:     "test-client-id",
		ClientSecret: "test-client-secret",
	})

	// Add some states with different ages
	handler.statesMu.Lock()
	handler.pendingStates["fresh"] = &oauthState{
		CodeVerifier: "v1",
		CreatedAt:    time.Now(),
	}
	handler.pendingStates["stale"] = &oauthState{
		CodeVerifier: "v2",
		CreatedAt:    time.Now().Add(-15 * time.Minute), // 15 minutes old
	}
	handler.pendingStates["very-stale"] = &oauthState{
		CodeVerifier: "v3",
		CreatedAt:    time.Now().Add(-30 * time.Minute), // 30 minutes old
	}
	handler.statesMu.Unlock()

	handler.cleanupStaleStates()

	handler.statesMu.Lock()
	defer handler.statesMu.Unlock()

	assert.Contains(t, handler.pendingStates, "fresh", "fresh state should remain")
	assert.NotContains(t, handler.pendingStates, "stale", "stale state should be cleaned up")
	assert.NotContains(t, handler.pendingStates, "very-stale", "very stale state should be cleaned up")
}

func TestIsDomainAllowed(t *testing.T) {
	tests := []struct {
		name           string
		allowedDomains []string
		email          string
		expected       bool
	}{
		{
			name:           "no restrictions",
			allowedDomains: nil,
			email:          "user@any.com",
			expected:       true,
		},
		{
			name:           "empty restrictions",
			allowedDomains: []string{},
			email:          "user@any.com",
			expected:       true,
		},
		{
			name:           "allowed domain",
			allowedDomains: []string{"example.com"},
			email:          "user@example.com",
			expected:       true,
		},
		{
			name:           "allowed domain case insensitive",
			allowedDomains: []string{"Example.COM"},
			email:          "user@example.com",
			expected:       true,
		},
		{
			name:           "domain not allowed",
			allowedDomains: []string{"example.com"},
			email:          "user@evil.com",
			expected:       false,
		},
		{
			name:           "multiple allowed domains",
			allowedDomains: []string{"example.com", "acme.org"},
			email:          "user@acme.org",
			expected:       true,
		},
		{
			name:           "invalid email",
			allowedDomains: []string{"example.com"},
			email:          "no-at-sign",
			expected:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, _ := setupTestOAuthHandler(t, &config.TeamsOAuthConfig{
				Provider:       "google",
				ClientID:       "test-client-id",
				ClientSecret:   "test-client-secret",
				AllowedDomains: tt.allowedDomains,
			})

			result := handler.isDomainAllowed(tt.email)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildCallbackURL(t *testing.T) {
	tests := []struct {
		name     string
		host     string
		tls      bool
		xProto   string
		expected string
	}{
		{
			name:     "http localhost",
			host:     "localhost:8080",
			expected: "http://localhost:8080/api/v1/auth/callback",
		},
		{
			name:     "with X-Forwarded-Proto",
			host:     "app.example.com",
			xProto:   "https",
			expected: "https://app.example.com/api/v1/auth/callback",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/callback", nil)
			req.Host = tt.host
			if tt.xProto != "" {
				req.Header.Set("X-Forwarded-Proto", tt.xProto)
			}

			result := buildCallbackURL(req)
			assert.Equal(t, tt.expected, result)
		})
	}
}
