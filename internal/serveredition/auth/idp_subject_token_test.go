//go:build server

package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/serveredition/broker"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/serveredition/users"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
	"go.uber.org/zap"
)

// testCredKey is a deterministic base64-encoded 32-byte AES-256 key for tests.
func testCredKey(t *testing.T) string {
	t.Helper()
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = byte(i + 1)
	}
	return base64.StdEncoding.EncodeToString(raw)
}

// idpMockProviderServer simulates an OAuth provider whose /token endpoint
// handles BOTH the authorization_code grant (login) and the refresh_token grant.
func idpMockProviderServer(t *testing.T, userEmail, userName, userSub string) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		_ = r.ParseForm()
		w.Header().Set("Content-Type", "application/json")
		if r.FormValue("grant_type") == "refresh_token" {
			// Refresh grant returns a brand-new access token (and rotated refresh).
			json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck
				"access_token":  "refreshed-access-token",
				"refresh_token": "rotated-refresh-token",
				"token_type":    "Bearer",
				"expires_in":    3600,
			})
			return
		}
		// Authorization-code grant (login).
		json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck
			"access_token":  "mock-access-token",
			"refresh_token": "mock-refresh-token",
			"token_type":    "Bearer",
			"expires_in":    3600,
			"scope":         "openid email profile",
		})
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck
			"sub":   userSub,
			"email": userEmail,
			"name":  userName,
		})
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

// setupIDPTestHandler builds a handler wired to a real (enabled) credential
// store on a shared BBolt DB, plus a mock provider. storeIDPTokens toggles the
// capture-at-login flag.
func setupIDPTestHandler(t *testing.T, storeIDPTokens bool) (*OAuthHandler, *users.UserStore, broker.CredentialStore, *httptest.Server) {
	t.Helper()

	tmpFile := filepath.Join(t.TempDir(), "test.db")
	db, err := bbolt.Open(tmpFile, 0o600, &bbolt.Options{Timeout: time.Second})
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	userStore := users.NewUserStore(db)
	require.NoError(t, userStore.EnsureBuckets())

	credStore, err := broker.NewBBoltAESStore(db, testCredKey(t), zap.NewNop())
	require.NoError(t, err)
	require.True(t, credStore.Enabled())

	mockServer := idpMockProviderServer(t, "user@example.com", "Test User", "sub-123")

	originalFactory := providerRegistry["google"]
	providerRegistry["google"] = func(_ string) *OAuthProvider {
		return &OAuthProvider{
			Name:         "google",
			AuthURL:      mockServer.URL + "/authorize",
			TokenURL:     mockServer.URL + "/token",
			UserInfoURL:  mockServer.URL + "/userinfo",
			Scopes:       []string{"openid", "email", "profile"},
			SupportsOIDC: false, // force userinfo path so the mock /userinfo is used
			SupportsPKCE: true,
		}
	}
	t.Cleanup(func() { providerRegistry["google"] = originalFactory })

	sessionMgr := NewSessionManager(userStore, time.Hour, false)
	teamsCfg := &config.ServerEditionConfig{
		Enabled:        true,
		AdminEmails:    []string{"admin@example.com"},
		OAuth:          &config.ServerEditionOAuthConfig{Provider: "google", ClientID: "cid", ClientSecret: "secret"},
		SessionTTL:     config.Duration(time.Hour),
		BearerTokenTTL: config.Duration(time.Hour),
		StoreIDPTokens: storeIDPTokens,
	}
	handler := NewOAuthHandler(userStore, sessionMgr, teamsCfg, []byte("test-hmac-key-for-jwt-signing-32b"), zap.NewNop().Sugar())
	handler.SetCredentialStore(credStore)

	return handler, userStore, credStore, mockServer
}

// driveCallback runs a full login callback for the mock user and returns the userID.
func driveCallback(t *testing.T, handler *OAuthHandler, userStore *users.UserStore) string {
	t.Helper()
	state := "state01state02state03state04state05state06state07state08state09"
	handler.statesMu.Lock()
	handler.pendingStates[state] = &oauthState{CodeVerifier: "verifier", RedirectURI: "/ui/", CreatedAt: time.Now()}
	handler.statesMu.Unlock()

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/v1/auth/callback?code=code&state=%s", state), nil)
	req.Host = "localhost:8080"
	w := httptest.NewRecorder()
	handler.HandleCallback(w, req)
	require.Equal(t, http.StatusFound, w.Result().StatusCode)

	user, err := userStore.GetUserByEmail("user@example.com")
	require.NoError(t, err)
	require.NotNil(t, user)
	return user.ID
}

func TestHandleCallback_StoresIDPSubjectToken_WhenEnabled(t *testing.T) {
	handler, userStore, credStore, _ := setupIDPTestHandler(t, true)
	userID := driveCallback(t, handler, userStore)

	cred, err := credStore.Get(userID, "")
	require.NoError(t, err, "idp subject token should be persisted at login")
	assert.Equal(t, "idp_subject_token", cred.Type)
	assert.Equal(t, "mock-access-token", cred.AccessToken)
	assert.Equal(t, "mock-refresh-token", cred.RefreshToken)
	assert.False(t, cred.ExpiresAt.IsZero(), "expiry should be derived from expires_in")
	assert.True(t, cred.ExpiresAt.After(time.Now()), "stored token should not already be expired")
}

func TestHandleCallback_NoStorage_WhenDisabled(t *testing.T) {
	handler, userStore, credStore, _ := setupIDPTestHandler(t, false)
	userID := driveCallback(t, handler, userStore)

	_, err := credStore.Get(userID, "")
	assert.ErrorIs(t, err, broker.ErrNotFound, "disabled flag must not persist any IdP token")
}

func TestGetValidIDPSubjectToken_ValidReturnsAsIs(t *testing.T) {
	handler, _, credStore, _ := setupIDPTestHandler(t, true)
	userID := "user-valid"
	require.NoError(t, credStore.Put(userID, "", &broker.UpstreamCredential{
		Type:         "idp_subject_token",
		AccessToken:  "still-good",
		RefreshToken: "rt",
		ExpiresAt:    time.Now().Add(time.Hour),
	}))

	cred, err := handler.GetValidIDPSubjectToken(context.Background(), userID)
	require.NoError(t, err)
	assert.Equal(t, "still-good", cred.AccessToken, "a valid token is returned unchanged")
}

func TestGetValidIDPSubjectToken_RefreshesNearExpiry(t *testing.T) {
	handler, _, credStore, _ := setupIDPTestHandler(t, true)
	userID := "user-refresh"
	require.NoError(t, credStore.Put(userID, "", &broker.UpstreamCredential{
		Type:         "idp_subject_token",
		AccessToken:  "expired-access",
		RefreshToken: "mock-refresh-token",
		ExpiresAt:    time.Now().Add(-time.Minute), // already expired
	}))

	cred, err := handler.GetValidIDPSubjectToken(context.Background(), userID)
	require.NoError(t, err)
	assert.Equal(t, "refreshed-access-token", cred.AccessToken, "expired token must be refreshed")
	assert.True(t, cred.ExpiresAt.After(time.Now()), "refreshed token has a fresh expiry")

	// Refreshed token must be re-persisted.
	stored, err := credStore.Get(userID, "")
	require.NoError(t, err)
	assert.Equal(t, "refreshed-access-token", stored.AccessToken)
	assert.Equal(t, "rotated-refresh-token", stored.RefreshToken, "rotated refresh token is persisted")
}

func TestGetValidIDPSubjectToken_ExpiredNotRefreshable_ReauthSignal(t *testing.T) {
	handler, _, credStore, _ := setupIDPTestHandler(t, true)
	userID := "user-noreauth"
	require.NoError(t, credStore.Put(userID, "", &broker.UpstreamCredential{
		Type:        "idp_subject_token",
		AccessToken: "expired-access",
		// no refresh token
		ExpiresAt: time.Now().Add(-time.Minute),
	}))

	_, err := handler.GetValidIDPSubjectToken(context.Background(), userID)
	assert.ErrorIs(t, err, ErrReauthRequired, "expired + not refreshable must signal re-auth")
}

func TestGetValidIDPSubjectToken_NotStored_ReauthSignal(t *testing.T) {
	handler, _, _, _ := setupIDPTestHandler(t, true)

	_, err := handler.GetValidIDPSubjectToken(context.Background(), "unknown-user")
	assert.ErrorIs(t, err, ErrReauthRequired, "absent token must signal re-auth, never a stale token")
}

func TestGetValidIDPSubjectToken_StoreDisabled_ReauthSignal(t *testing.T) {
	handler, _, _, _ := setupIDPTestHandler(t, true)
	handler.SetCredentialStore(nil) // simulate broker disabled

	_, err := handler.GetValidIDPSubjectToken(context.Background(), "any")
	assert.ErrorIs(t, err, ErrReauthRequired)
}
