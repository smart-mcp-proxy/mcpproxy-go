//go:build server

package serveredition

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	teamsauth "github.com/smart-mcp-proxy/mcpproxy-go/internal/serveredition/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/serveredition/users"
)

// TestSetupMultiUserOAuth_DerivedDefaultsNeverReachTheLiveConfig pins the
// Spec 107 FR-039 split between Validate and ApplyDefaults at the point where
// it can actually be broken: setup.go.
//
// deps.Config is the runtime's live/desired *config.Config — the very pointer
// PATCH /api/v1/config marshals as its merge base and ApplyConfig writes back
// to disk. ApplyDefaults fills credential_encryption_key from MCPPROXY_CRED_KEY,
// oauth.tenant_id="common" and the 24h TTLs; every one of those fields is
// `omitempty`, so if ApplyDefaults ran on the shared pointer the env secret and
// the defaults would be emitted into the next write-back of the config file.
// Setup must therefore apply them to a clone and hand the clone to the
// handlers, leaving the operator's document exactly as loaded.
//
// Oracle discipline: the positive control proves the defaults DID apply to
// what the handlers use — the login redirect for a tenant-less Microsoft
// provider goes to the "common" tenant and the credential store came up
// enabled off the environment key — so the untouched live block is the split
// working, not ApplyDefaults never having run.
//
// BITES: call cfg.ApplyDefaults() on deps.Config.ServerEdition in setup.go.
func TestSetupMultiUserOAuth_DerivedDefaultsNeverReachTheLiveConfig(t *testing.T) {
	t.Setenv("MCPPROXY_CRED_KEY", "")

	tmpDir := t.TempDir()
	db, err := bbolt.Open(tmpDir+"/test.db", 0600, &bbolt.Options{Timeout: time.Second})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	live := &config.Config{
		ServerEdition: &config.ServerEditionConfig{
			Enabled:     true,
			AdminEmails: []string{"admin@example.com"},
			OAuth: &config.ServerEditionOAuthConfig{
				Provider:     "microsoft",
				ClientID:     "test-client-id",
				ClientSecret: "test-client-secret",
				// TenantID deliberately unset: ApplyDefaults fills "common".
			},
			// TTLs deliberately zero: ApplyDefaults fills 24h.
		},
	}
	router := chi.NewRouter()
	require.NoError(t, setupMultiUserOAuth(Dependencies{
		Router:  router,
		DB:      db,
		Logger:  zap.NewNop().Sugar(),
		DataDir: tmpDir,
		Config:  live,
	}))

	// Positive control: the handlers run with the defaults applied. A
	// tenant-less Microsoft provider redirects to the "common" tenant.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/login", nil)
	req.Host = "localhost:8080"
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusFound, rec.Code, "login must redirect (%s)", rec.Body.String())
	redirect, err := url.Parse(rec.Header().Get("Location"))
	require.NoError(t, err)
	assert.Equal(t, "/common/oauth2/v2.0/authorize", redirect.Path,
		"the OAuth handler must see the defaulted Microsoft tenant")

	// The live block the runtime will marshal on the next PATCH / write-back is
	// exactly what the operator wrote: no derived value was written into it.
	se := live.ServerEdition
	assert.Equal(t, "", se.OAuth.TenantID, "oauth.tenant_id default must not be persisted into the live config")
	assert.Equal(t, config.Duration(0), se.SessionTTL, "session_ttl default must not be persisted into the live config")
	assert.Equal(t, config.Duration(0), se.BearerTokenTTL, "bearer_token_ttl default must not be persisted into the live config")
	assert.Equal(t, "", se.CredentialEncryptionKey, "credential_encryption_key must not be persisted into the live config")
}

// TestSetupMultiUserOAuth_EnvCredentialKeyNeverReachesTheLiveConfig is the
// secret half of the property above on its own: with MCPPROXY_CRED_KEY set,
// the credential store comes up enabled (positive control through the
// production credential route, which reports "unavailable" for a disabled
// store) while the live block's credential_encryption_key stays empty, so the
// environment secret is never written into the config file.
func TestSetupMultiUserOAuth_EnvCredentialKeyNeverReachesTheLiveConfig(t *testing.T) {
	// 32 zero bytes, base64: a well-formed AES-256 key.
	t.Setenv("MCPPROXY_CRED_KEY", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")

	tmpDir := t.TempDir()
	db, err := bbolt.Open(tmpDir+"/test.db", 0600, &bbolt.Options{Timeout: time.Second})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	live := &config.Config{
		Servers: []*config.ServerConfig{{
			Name:     "brokered",
			URL:      "https://brokered.example.com/mcp",
			Protocol: "http",
			Enabled:  true,
			Shared:   true,
			AuthBroker: &config.AuthBrokerConfig{
				Mode:                  config.AuthBrokerModeOAuthConnect,
				TokenEndpoint:         "https://idp.example.com/token",
				AuthorizationEndpoint: "https://idp.example.com/authorize",
			},
		}},
		ServerEdition: &config.ServerEditionConfig{
			Enabled:     true,
			AdminEmails: []string{"admin@example.com"},
			OAuth: &config.ServerEditionOAuthConfig{
				Provider:     "google",
				ClientID:     "test-client-id",
				ClientSecret: "test-client-secret",
			},
		},
	}
	router := chi.NewRouter()
	logger := zap.NewNop().Sugar()
	require.NoError(t, setupMultiUserOAuth(Dependencies{
		Router:  router,
		DB:      db,
		Logger:  logger,
		DataDir: tmpDir,
		Config:  live,
	}))

	store := users.NewUserStore(db)
	u := users.NewUser("tenant@example.com", "tenant@example.com", "google", "sub-tenant")
	require.NoError(t, store.CreateUser(u))
	hmacKey, err := auth.GetOrCreateHMACKey(tmpDir)
	require.NoError(t, err)
	bearer, err := teamsauth.GenerateBearerToken(hmacKey, u.ID, u.Email, u.DisplayName, "user", u.Provider, time.Hour)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/user/credentials", nil)
	req.Host = "localhost:8080"
	req.Header.Set("Authorization", "Bearer "+bearer)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "positive control: the credential surface answers (%s)", rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"not_connected"`,
		"positive control: the store must have come up enabled off MCPPROXY_CRED_KEY (a disabled store reports \"unavailable\")")

	assert.Equal(t, "", live.ServerEdition.CredentialEncryptionKey,
		"MCPPROXY_CRED_KEY must not be copied into the live config block")
}
