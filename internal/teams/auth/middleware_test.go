//go:build server

package auth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"go.etcd.io/bbolt"
	"go.uber.org/zap"

	coreauth "github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/teams/users"
)

// testMiddlewareSetup holds all components needed for middleware tests.
type testMiddlewareSetup struct {
	db             *bbolt.DB
	userStore      *users.UserStore
	sessionManager *SessionManager
	middleware     *TeamsAuthMiddleware
	teamsConfig    *config.TeamsConfig
	hmacKey        []byte
	testUser       *users.User
	adminUser      *users.User
	disabledUser   *users.User
}

// setupMiddlewareTest creates a temporary BBolt DB, user store, session manager,
// test users, and a TeamsAuthMiddleware instance.
func setupMiddlewareTest(t *testing.T) *testMiddlewareSetup {
	t.Helper()

	// Create temp DB
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := bbolt.Open(dbPath, 0600, &bbolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		t.Fatalf("failed to open bbolt db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	// Create user store and ensure buckets
	userStore := users.NewUserStore(db)
	if err := userStore.EnsureBuckets(); err != nil {
		t.Fatalf("failed to ensure buckets: %v", err)
	}

	// Create test users
	testUser := users.NewUser("user@example.com", "Test User", "google", "google-sub-123")
	if err := userStore.CreateUser(testUser); err != nil {
		t.Fatalf("failed to create test user: %v", err)
	}

	adminUser := users.NewUser("admin@example.com", "Admin User", "google", "google-sub-admin")
	if err := userStore.CreateUser(adminUser); err != nil {
		t.Fatalf("failed to create admin user: %v", err)
	}

	disabledUser := users.NewUser("disabled@example.com", "Disabled User", "google", "google-sub-disabled")
	disabledUser.Disabled = true
	if err := userStore.CreateUser(disabledUser); err != nil {
		t.Fatalf("failed to create disabled user: %v", err)
	}

	// Teams config with admin@example.com as admin
	teamsConfig := &config.TeamsConfig{
		Enabled:     true,
		AdminEmails: []string{"admin@example.com"},
	}

	// Session manager
	sessionTTL := 1 * time.Hour
	sessionManager := NewSessionManager(userStore, sessionTTL, false)

	// HMAC key for JWT tokens
	hmacKey := []byte("test-hmac-key-for-middleware-testing-32b")

	// Logger (discard output in tests)
	logger := zap.NewNop().Sugar()

	mw := NewTeamsAuthMiddleware(sessionManager, userStore, teamsConfig, hmacKey, logger)

	return &testMiddlewareSetup{
		db:             db,
		userStore:      userStore,
		sessionManager: sessionManager,
		middleware:     mw,
		teamsConfig:    teamsConfig,
		hmacKey:        hmacKey,
		testUser:       testUser,
		adminUser:      adminUser,
		disabledUser:   disabledUser,
	}
}

// createSessionForUser creates a session for the given user and returns the session.
func (s *testMiddlewareSetup) createSessionForUser(t *testing.T, userID string) *users.Session {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	session, err := s.sessionManager.CreateSession(userID, r)
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}
	return session
}

// generateJWT generates a JWT for the given user parameters.
func (s *testMiddlewareSetup) generateJWT(t *testing.T, userID, email, displayName, role, provider string, ttl time.Duration) string {
	t.Helper()
	token, err := GenerateBearerToken(s.hmacKey, userID, email, displayName, role, provider, ttl)
	if err != nil {
		t.Fatalf("failed to generate bearer token: %v", err)
	}
	return token
}

// generateExpiredJWT creates a JWT token with backdated claims so it is already expired.
func generateExpiredJWT(t *testing.T, hmacKey []byte, userID, email, displayName, role, provider string) string {
	t.Helper()

	past := time.Now().UTC().Add(-2 * time.Hour)
	claims := UserClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(past),
			ExpiresAt: jwt.NewNumericDate(past.Add(1 * time.Hour)), // Expired 1 hour ago
			ID:        "expired-test-jti",
		},
		Email:       email,
		DisplayName: displayName,
		Role:        role,
		Provider:    provider,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(hmacKey)
	if err != nil {
		t.Fatalf("failed to sign expired JWT: %v", err)
	}
	return signed
}

// authContextHandler is a test handler that writes the AuthContext as JSON.
func authContextHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ac := coreauth.AuthContextFromContext(r.Context())
		if ac == nil {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, `{"error":"no auth context"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"type":         ac.Type,
			"user_id":      ac.UserID,
			"email":        ac.Email,
			"display_name": ac.DisplayName,
			"role":         ac.Role,
			"provider":     ac.Provider,
		})
	})
}

// --- Middleware Tests ---

func TestMiddleware_ValidSession(t *testing.T) {
	s := setupMiddlewareTest(t)

	session := s.createSessionForUser(t, s.testUser.ID)

	handler := s.middleware.Middleware()(authContextHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/servers", nil)
	req.AddCookie(&http.Cookie{
		Name:  SessionCookieName,
		Value: session.ID,
	})
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var body map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if body["type"] != coreauth.AuthTypeUser {
		t.Errorf("expected type %q, got %q", coreauth.AuthTypeUser, body["type"])
	}
	if body["user_id"] != s.testUser.ID {
		t.Errorf("expected user_id %q, got %q", s.testUser.ID, body["user_id"])
	}
	if body["email"] != s.testUser.Email {
		t.Errorf("expected email %q, got %q", s.testUser.Email, body["email"])
	}
	if body["display_name"] != s.testUser.DisplayName {
		t.Errorf("expected display_name %q, got %q", s.testUser.DisplayName, body["display_name"])
	}
	if body["provider"] != s.testUser.Provider {
		t.Errorf("expected provider %q, got %q", s.testUser.Provider, body["provider"])
	}
}

func TestMiddleware_ValidJWT(t *testing.T) {
	s := setupMiddlewareTest(t)

	token := s.generateJWT(t, s.testUser.ID, s.testUser.Email, s.testUser.DisplayName, "user", s.testUser.Provider, 1*time.Hour)

	handler := s.middleware.Middleware()(authContextHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/servers", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var body map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if body["type"] != coreauth.AuthTypeUser {
		t.Errorf("expected type %q, got %q", coreauth.AuthTypeUser, body["type"])
	}
	if body["user_id"] != s.testUser.ID {
		t.Errorf("expected user_id %q, got %q", s.testUser.ID, body["user_id"])
	}
	if body["email"] != s.testUser.Email {
		t.Errorf("expected email %q, got %q", s.testUser.Email, body["email"])
	}
}

func TestMiddleware_NoAuth(t *testing.T) {
	s := setupMiddlewareTest(t)

	handler := s.middleware.Middleware()(authContextHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/servers", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rr.Code, rr.Body.String())
	}

	var body map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if body["error"] != "Unauthorized" {
		t.Errorf("expected error %q, got %q", "Unauthorized", body["error"])
	}
}

func TestMiddleware_ExpiredSession(t *testing.T) {
	s := setupMiddlewareTest(t)

	// Create a session with a past expiry by directly storing it
	session := &users.Session{
		ID:        "expired-session-id",
		UserID:    s.testUser.ID,
		CreatedAt: time.Now().UTC().Add(-2 * time.Hour),
		ExpiresAt: time.Now().UTC().Add(-1 * time.Hour), // Already expired
	}
	if err := s.userStore.CreateSession(session); err != nil {
		t.Fatalf("failed to create expired session: %v", err)
	}

	handler := s.middleware.Middleware()(authContextHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/servers", nil)
	req.AddCookie(&http.Cookie{
		Name:  SessionCookieName,
		Value: session.ID,
	})
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestMiddleware_ExpiredJWT(t *testing.T) {
	s := setupMiddlewareTest(t)

	token := generateExpiredJWT(t, s.hmacKey, s.testUser.ID, s.testUser.Email, s.testUser.DisplayName, "user", s.testUser.Provider)

	handler := s.middleware.Middleware()(authContextHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/servers", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestMiddleware_DisabledUser_Session(t *testing.T) {
	s := setupMiddlewareTest(t)

	session := s.createSessionForUser(t, s.disabledUser.ID)

	handler := s.middleware.Middleware()(authContextHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/servers", nil)
	req.AddCookie(&http.Cookie{
		Name:  SessionCookieName,
		Value: session.ID,
	})
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestMiddleware_DisabledUser_JWT(t *testing.T) {
	s := setupMiddlewareTest(t)

	token := s.generateJWT(t, s.disabledUser.ID, s.disabledUser.Email, s.disabledUser.DisplayName, "user", s.disabledUser.Provider, 1*time.Hour)

	handler := s.middleware.Middleware()(authContextHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/servers", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestMiddleware_AdminRole_Session(t *testing.T) {
	s := setupMiddlewareTest(t)

	session := s.createSessionForUser(t, s.adminUser.ID)

	handler := s.middleware.Middleware()(authContextHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/servers", nil)
	req.AddCookie(&http.Cookie{
		Name:  SessionCookieName,
		Value: session.ID,
	})
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var body map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if body["type"] != coreauth.AuthTypeAdminUser {
		t.Errorf("expected type %q, got %q", coreauth.AuthTypeAdminUser, body["type"])
	}
	if body["role"] != "admin" {
		t.Errorf("expected role %q, got %q", "admin", body["role"])
	}
	if body["user_id"] != s.adminUser.ID {
		t.Errorf("expected user_id %q, got %q", s.adminUser.ID, body["user_id"])
	}
	if body["email"] != s.adminUser.Email {
		t.Errorf("expected email %q, got %q", s.adminUser.Email, body["email"])
	}
}

func TestMiddleware_UserRole_Session(t *testing.T) {
	s := setupMiddlewareTest(t)

	session := s.createSessionForUser(t, s.testUser.ID)

	handler := s.middleware.Middleware()(authContextHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/servers", nil)
	req.AddCookie(&http.Cookie{
		Name:  SessionCookieName,
		Value: session.ID,
	})
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var body map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if body["type"] != coreauth.AuthTypeUser {
		t.Errorf("expected type %q, got %q", coreauth.AuthTypeUser, body["type"])
	}
	if body["role"] != "user" {
		t.Errorf("expected role %q, got %q", "user", body["role"])
	}
}

func TestMiddleware_AdminRole_JWT(t *testing.T) {
	s := setupMiddlewareTest(t)

	token := s.generateJWT(t, s.adminUser.ID, s.adminUser.Email, s.adminUser.DisplayName, "admin", s.adminUser.Provider, 1*time.Hour)

	handler := s.middleware.Middleware()(authContextHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/servers", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var body map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if body["type"] != coreauth.AuthTypeAdminUser {
		t.Errorf("expected type %q, got %q", coreauth.AuthTypeAdminUser, body["type"])
	}
	if body["role"] != "admin" {
		t.Errorf("expected role %q, got %q", "admin", body["role"])
	}
}

func TestMiddleware_AgentTokenIgnored(t *testing.T) {
	s := setupMiddlewareTest(t)

	handler := s.middleware.Middleware()(authContextHandler())

	// Agent tokens with mcp_agt_ prefix should not be treated as JWT
	req := httptest.NewRequest(http.MethodGet, "/api/v1/servers", nil)
	req.Header.Set("Authorization", "Bearer mcp_agt_some_agent_token_value")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestMiddleware_SessionTakesPrecedenceOverBearer(t *testing.T) {
	s := setupMiddlewareTest(t)

	// Create session for regular user
	session := s.createSessionForUser(t, s.testUser.ID)

	// Generate JWT for admin user
	token := s.generateJWT(t, s.adminUser.ID, s.adminUser.Email, s.adminUser.DisplayName, "admin", s.adminUser.Provider, 1*time.Hour)

	handler := s.middleware.Middleware()(authContextHandler())

	// Send request with both session cookie and Bearer token
	req := httptest.NewRequest(http.MethodGet, "/api/v1/servers", nil)
	req.AddCookie(&http.Cookie{
		Name:  SessionCookieName,
		Value: session.ID,
	})
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var body map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	// Session (regular user) should take precedence over Bearer (admin)
	if body["type"] != coreauth.AuthTypeUser {
		t.Errorf("expected session user type %q, got %q (session should take precedence)", coreauth.AuthTypeUser, body["type"])
	}
	if body["user_id"] != s.testUser.ID {
		t.Errorf("expected session user_id %q, got %q", s.testUser.ID, body["user_id"])
	}
}

func TestMiddleware_InvalidJWTSignature(t *testing.T) {
	s := setupMiddlewareTest(t)

	// Generate a JWT with a different HMAC key
	wrongKey := []byte("wrong-hmac-key-for-signature-test-32b")
	token, err := GenerateBearerToken(wrongKey, s.testUser.ID, s.testUser.Email, s.testUser.DisplayName, "user", s.testUser.Provider, 1*time.Hour)
	if err != nil {
		t.Fatalf("failed to generate token: %v", err)
	}

	handler := s.middleware.Middleware()(authContextHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/servers", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rr.Code, rr.Body.String())
	}
}

// --- AdminOnly Tests ---

func TestAdminOnly_Admin(t *testing.T) {
	s := setupMiddlewareTest(t)

	ac := coreauth.AdminUserContext(s.adminUser.ID, s.adminUser.Email, s.adminUser.DisplayName, s.adminUser.Provider)

	adminHandler := s.middleware.AdminOnly()(authContextHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/users", nil)
	req = req.WithContext(coreauth.WithAuthContext(req.Context(), ac))
	rr := httptest.NewRecorder()

	adminHandler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestAdminOnly_User(t *testing.T) {
	s := setupMiddlewareTest(t)

	ac := coreauth.UserContext(s.testUser.ID, s.testUser.Email, s.testUser.DisplayName, s.testUser.Provider)

	adminHandler := s.middleware.AdminOnly()(authContextHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/users", nil)
	req = req.WithContext(coreauth.WithAuthContext(req.Context(), ac))
	rr := httptest.NewRecorder()

	adminHandler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rr.Code, rr.Body.String())
	}

	var body map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if body["error"] != "Forbidden" {
		t.Errorf("expected error %q, got %q", "Forbidden", body["error"])
	}
}

func TestAdminOnly_NoContext(t *testing.T) {
	s := setupMiddlewareTest(t)

	adminHandler := s.middleware.AdminOnly()(authContextHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/users", nil)
	rr := httptest.NewRecorder()

	adminHandler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rr.Code, rr.Body.String())
	}
}
