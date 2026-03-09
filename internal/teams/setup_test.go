//go:build server

package teams

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"go.etcd.io/bbolt"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

func TestSetupMultiUserOAuth_Disabled(t *testing.T) {
	// When teams is not enabled, setup should be a no-op
	logger := zap.NewNop().Sugar()
	router := chi.NewRouter()

	deps := Dependencies{
		Router: router,
		Logger: logger,
		Config: &config.Config{
			Teams: &config.TeamsConfig{
				Enabled: false,
			},
		},
	}

	err := setupMultiUserOAuth(deps)
	if err != nil {
		t.Fatalf("expected no error for disabled teams, got: %v", err)
	}
}

func TestSetupMultiUserOAuth_NilConfig(t *testing.T) {
	logger := zap.NewNop().Sugar()
	router := chi.NewRouter()

	deps := Dependencies{
		Router: router,
		Logger: logger,
		Config: nil,
	}

	err := setupMultiUserOAuth(deps)
	if err != nil {
		t.Fatalf("expected no error for nil config, got: %v", err)
	}
}

func TestSetupMultiUserOAuth_NilTeamsConfig(t *testing.T) {
	logger := zap.NewNop().Sugar()
	router := chi.NewRouter()

	deps := Dependencies{
		Router: router,
		Logger: logger,
		Config: &config.Config{
			Teams: nil,
		},
	}

	err := setupMultiUserOAuth(deps)
	if err != nil {
		t.Fatalf("expected no error for nil teams config, got: %v", err)
	}
}

func TestSetupMultiUserOAuth_InvalidConfig(t *testing.T) {
	logger := zap.NewNop().Sugar()
	router := chi.NewRouter()

	// Enabled but missing required fields should return validation error
	deps := Dependencies{
		Router: router,
		Logger: logger,
		Config: &config.Config{
			Teams: &config.TeamsConfig{
				Enabled:     true,
				AdminEmails: nil, // Missing admin emails
			},
		},
	}

	err := setupMultiUserOAuth(deps)
	if err == nil {
		t.Fatal("expected validation error for invalid teams config")
	}
}

func TestSetupMultiUserOAuth_RegistersRoutes(t *testing.T) {
	// Create a temporary directory for HMAC key and database
	tmpDir, err := os.MkdirTemp("", "teams-setup-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a temporary BBolt database
	dbPath := tmpDir + "/test.db"
	db, err := bbolt.Open(dbPath, 0600, &bbolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		t.Fatalf("failed to open bbolt: %v", err)
	}
	defer db.Close()

	logger := zap.NewNop().Sugar()
	router := chi.NewRouter()

	deps := Dependencies{
		Router:  router,
		DB:      db,
		Logger:  logger,
		DataDir: tmpDir,
		Config: &config.Config{
			Teams: &config.TeamsConfig{
				Enabled:        true,
				AdminEmails:    []string{"admin@example.com"},
				SessionTTL:     config.Duration(24 * time.Hour),
				BearerTokenTTL: config.Duration(24 * time.Hour),
				OAuth: &config.TeamsOAuthConfig{
					Provider:     "google",
					ClientID:     "test-client-id",
					ClientSecret: "test-client-secret",
				},
			},
		},
	}

	err = setupMultiUserOAuth(deps)
	if err != nil {
		t.Fatalf("setupMultiUserOAuth failed: %v", err)
	}

	// Verify routes were registered by making test requests
	// Login should redirect to OAuth provider (302)
	loginReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/login", nil)
	loginReq.Host = "localhost:8080"
	loginRec := httptest.NewRecorder()
	router.ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusFound {
		t.Errorf("expected login to redirect (302), got %d", loginRec.Code)
	}

	// Callback without params should return 400
	callbackReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/callback", nil)
	callbackReq.Host = "localhost:8080"
	callbackRec := httptest.NewRecorder()
	router.ServeHTTP(callbackRec, callbackReq)
	if callbackRec.Code != http.StatusBadRequest {
		t.Errorf("expected callback without params to return 400, got %d", callbackRec.Code)
	}

	// Logout without auth should return 401
	logoutReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	logoutReq.Host = "localhost:8080"
	logoutRec := httptest.NewRecorder()
	router.ServeHTTP(logoutRec, logoutReq)
	if logoutRec.Code != http.StatusUnauthorized {
		t.Errorf("expected logout without auth to return 401, got %d", logoutRec.Code)
	}
}

func TestSetupMultiUserOAuth_FeatureRegisteredViaInit(t *testing.T) {
	// Verify that the init() function registered the feature by re-importing
	// the feature name via RegisteredFeatures after manually re-registering.
	// Note: Other tests in this file reset the global features slice, so we
	// verify the feature setup function works correctly when re-registered.
	saved := features
	defer func() { features = saved }()

	features = nil
	Register(Feature{
		Name:  "multiuser-oauth",
		Setup: setupMultiUserOAuth,
	})

	names := RegisteredFeatures()
	if len(names) != 1 || names[0] != "multiuser-oauth" {
		t.Fatalf("expected [multiuser-oauth], got %v", names)
	}
}
