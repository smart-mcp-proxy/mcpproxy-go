//go:build server

package serveredition

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"go.etcd.io/bbolt"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	teamsauth "github.com/smart-mcp-proxy/mcpproxy-go/internal/serveredition/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/serveredition/users"
)

// --- Issue #1169: stale admin JWTs on the PRODUCTION route table ---

// serverEditionTestDeps wires the same harness as
// TestSetupMultiUserOAuth_RegistersRoutes and returns the router, the user
// store, and the HMAC key setupMultiUserOAuth actually installed.
func serverEditionTestDeps(t *testing.T) (*chi.Mux, *users.UserStore, []byte) {
	t.Helper()

	tmpDir := t.TempDir()
	db, err := bbolt.Open(tmpDir+"/test.db", 0600, &bbolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		t.Fatalf("failed to open bbolt: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	router := chi.NewRouter()
	deps := Dependencies{
		Router:  router,
		DB:      db,
		Logger:  zap.NewNop().Sugar(),
		DataDir: tmpDir,
		Config: &config.Config{
			ServerEdition: &config.ServerEditionConfig{
				Enabled:        true,
				AdminEmails:    []string{"admin@example.com"},
				SessionTTL:     config.Duration(24 * time.Hour),
				BearerTokenTTL: config.Duration(24 * time.Hour),
				OAuth: &config.ServerEditionOAuthConfig{
					Provider:     "google",
					ClientID:     "test-client-id",
					ClientSecret: "test-client-secret",
				},
			},
		},
	}

	if err := setupMultiUserOAuth(deps); err != nil {
		t.Fatalf("setupMultiUserOAuth failed: %v", err)
	}

	// The same key setupMultiUserOAuth loaded: GetOrCreateHMACKey is
	// idempotent and the key file now exists under tmpDir.
	hmacKey, err := auth.GetOrCreateHMACKey(tmpDir)
	if err != nil {
		t.Fatalf("failed to load HMAC key: %v", err)
	}

	return router, users.NewUserStore(db), hmacKey
}

// TestSetupMultiUserOAuth_DemotedAdminJWTIsIndistinguishableFromPlainUser
// drives the real registration (setupMultiUserOAuth -> chi -> requireAdmin).
//
// ORACLE: status parity between a stale role="admin" JWT held by a demoted
// user and a role="user" JWT held by a plain user, ANCHORED on the exact
// expected status (403). The anchor is load-bearing: a mis-minted token or a
// mismatched HMAC key would make both requests 401, and a bare parity assert
// would then pass vacuously. A POSITIVE CONTROL runs first on the SAME router
// and SAME route, proving the route matched (chi's NotFoundHandler would give
// 404), the middleware accepted the token, and the handler really ran.
//
// BITES: unfixed, demoted=200 (requireAdmin passes on the frozen claim) while
// plain=403.
func TestSetupMultiUserOAuth_DemotedAdminJWTIsIndistinguishableFromPlainUser(t *testing.T) {
	router, userStore, hmacKey := serverEditionTestDeps(t)

	mkUser := func(email, name string) *users.User {
		u := users.NewUser(email, name, "google", "sub-"+email)
		if err := userStore.CreateUser(u); err != nil {
			t.Fatalf("failed to create user %s: %v", email, err)
		}
		return u
	}
	adminUser := mkUser("admin@example.com", "Real Admin") // IS in admin_emails
	demoted := mkUser("demoted@example.com", "Demoted")    // NOT in admin_emails
	plain := mkUser("plain@example.com", "Plain")          // NOT in admin_emails

	call := func(u *users.User, role string) *httptest.ResponseRecorder {
		token, err := teamsauth.GenerateBearerToken(hmacKey, u.ID, u.Email, u.DisplayName, role, u.Provider, time.Hour)
		if err != nil {
			t.Fatalf("failed to mint token for %s: %v", u.Email, err)
		}
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/users", nil)
		req.Host = "localhost:8080"
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	if rec := call(adminUser, "admin"); rec.Code != http.StatusOK {
		t.Fatalf("positive control failed: a real admin got %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	demotedRec := call(demoted, "admin") // stale, pre-demotion claim
	plainRec := call(plain, "user")

	if demotedRec.Code != plainRec.Code {
		t.Errorf("stale admin JWT is distinguishable from a plain user: demoted=%d plain=%d", demotedRec.Code, plainRec.Code)
	}
	if demotedRec.Code != http.StatusForbidden {
		t.Errorf("expected the demoted admin to get %d, got %d (body: %s)", http.StatusForbidden, demotedRec.Code, demotedRec.Body.String())
	}
	if plainRec.Code != http.StatusForbidden {
		t.Errorf("expected the plain user to get %d, got %d", http.StatusForbidden, plainRec.Code)
	}
}

// TestSetupMultiUserOAuth_DemotedAdminCannotMintFreshAdminToken closes the
// renewal loop #1169 misses: generateToken mints a NEW JWT from ac.Role, so on
// unfixed code a demoted admin renews admin indefinitely and the bug never
// self-heals at token expiry.
//
// BITES: unfixed, the reissued token carries role="admin".
func TestSetupMultiUserOAuth_DemotedAdminCannotMintFreshAdminToken(t *testing.T) {
	router, userStore, hmacKey := serverEditionTestDeps(t)

	demoted := users.NewUser("demoted@example.com", "Demoted", "google", "sub-demoted")
	if err := userStore.CreateUser(demoted); err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	stale, err := teamsauth.GenerateBearerToken(hmacKey, demoted.ID, demoted.Email, demoted.DisplayName, "admin", demoted.Provider, time.Hour)
	if err != nil {
		t.Fatalf("failed to mint the stale token: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/token", nil)
	req.Host = "localhost:8080"
	req.Header.Set("Authorization", "Bearer "+stale)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	// Anchor: renewal itself must succeed, otherwise the role assertion below
	// would be satisfied vacuously by an empty/absent token.
	if rec.Code != http.StatusOK {
		t.Fatalf("expected token renewal to succeed with 200, got %d (body: %s)", rec.Code, rec.Body.String())
	}

	var resp struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode the token response: %v (body: %s)", err, rec.Body.String())
	}
	if resp.Token == "" {
		t.Fatalf("token renewal returned an empty token (body: %s)", rec.Body.String())
	}

	claims, err := teamsauth.ParseBearerTokenUnverified(resp.Token)
	if err != nil {
		t.Fatalf("failed to parse the reissued token: %v", err)
	}
	if claims.Role != "user" {
		t.Errorf("demoted admin renewed an admin token: reissued role=%q, want %q", claims.Role, "user")
	}
}
