//go:build server

package serveredition

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// wiringHarness drives the PRODUCTION setup function, with the two dependencies
// the fixes in this change hang off: a live configuration provider and a real
// agent-token store.
type wiringHarness struct {
	router   *chi.Mux
	users    *users.UserStore
	tokens   *storage.Manager
	hmacKey  []byte
	configMu sync.RWMutex
	config   *config.Config
}

// setLiveServers replaces the servers the CURRENT configuration carries, which
// is what a hot reload does. Copy-on-write, matching the runtime's snapshot
// publishing, so a concurrent reader never sees a half-built slice.
func (h *wiringHarness) setLiveServers(servers []*config.ServerConfig) {
	h.configMu.Lock()
	defer h.configMu.Unlock()
	next := *h.config
	next.Servers = servers
	h.config = &next
}

func (h *wiringHarness) currentConfig() *config.Config {
	h.configMu.RLock()
	defer h.configMu.RUnlock()
	return h.config
}

func newWiringHarness(t *testing.T) *wiringHarness {
	t.Helper()

	tmpDir := t.TempDir()
	db, err := bbolt.Open(tmpDir+"/test.db", 0600, &bbolt.Options{Timeout: time.Second})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	logger := zap.NewNop().Sugar()
	tokens, err := storage.NewManager(t.TempDir(), logger)
	require.NoError(t, err)
	t.Cleanup(func() { tokens.Close() })

	h := &wiringHarness{
		router: chi.NewRouter(),
		tokens: tokens,
		config: &config.Config{
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

	require.NoError(t, setupMultiUserOAuth(Dependencies{
		Router:         h.router,
		DB:             db,
		Logger:         logger,
		DataDir:        tmpDir,
		Config:         h.currentConfig(),
		ConfigProvider: h.currentConfig,
		StorageManager: tokens,
	}))

	h.users = users.NewUserStore(db)
	h.hmacKey, err = auth.GetOrCreateHMACKey(tmpDir)
	require.NoError(t, err)

	return h
}

func (h *wiringHarness) mkUser(t *testing.T, email string) *users.User {
	t.Helper()
	u := users.NewUser(email, email, "google", "sub-"+email)
	require.NoError(t, h.users.CreateUser(u))
	return u
}

// createPersonalServer posts to the production per-user create route as u.
func (h *wiringHarness) createPersonalServer(t *testing.T, u *users.User, name string) *httptest.ResponseRecorder {
	t.Helper()
	token, err := teamsauth.GenerateBearerToken(h.hmacKey, u.ID, u.Email, u.DisplayName, "user", u.Provider, time.Hour)
	require.NoError(t, err)

	body := `{"name":"` + name + `","url":"http://127.0.0.1:9/` + name + `","protocol":"http"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/user/servers", strings.NewReader(body))
	req.Host = "localhost:8080"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, req)
	return rec
}

// TestSetupMultiUserOAuth_UserHandlersSeeConfigReloads proves the LIVE
// configuration is actually threaded through the production wiring, not just
// available to be threaded.
//
// setup.go used to hand api.NewUserHandlers `deps.Config.Servers` — the slice
// as it stood at process start. The configuration is hot-reloadable, so every
// name-collision and entitlement decision afterwards was made against a
// configuration that no longer existed. This drives the real setup function and
// the real chi route table, so a wiring mistake (provider not passed, fallback
// taken unconditionally) fails here even though the handler-level tests pass.
//
// Oracle discipline: the same request succeeds first, for a different tenant,
// on the same router — proving the route, the auth middleware and the store all
// work, and that the name was genuinely free at that moment. The only thing
// that changes between the two calls is the configuration the provider returns.
//
// BITES: pass `sharedServers` instead of `adminServers` to
// teamsapi.NewUserHandlers in setup.go and the second call returns 201.
func TestSetupMultiUserOAuth_UserHandlersSeeConfigReloads(t *testing.T) {
	h := newWiringHarness(t)

	first := h.mkUser(t, "first@example.com")
	second := h.mkUser(t, "second@example.com")

	control := h.createPersonalServer(t, first, "late-admin-db")
	require.Equal(t, http.StatusCreated, control.Code,
		"positive control: the name must be free before the reload (%s)", control.Body.String())

	// A hot reload adds an admin server with that name.
	h.setLiveServers([]*config.ServerConfig{
		{Name: "late-admin-db", URL: "https://late.example", Protocol: "http", Enabled: true},
	})

	probe := h.createPersonalServer(t, second, "late-admin-db")
	require.Equal(t, http.StatusConflict, probe.Code,
		"a name the admin took after boot must no longer be available (%s)", probe.Body.String())

	stored, err := h.users.GetUserServer(second.ID, "late-admin-db")
	require.NoError(t, err)
	assert.Nil(t, stored, "the refused server must not have been persisted")
}

// TestSetupMultiUserOAuth_InstallsAgentTokenOwnerGate proves the owner gate is
// wired to the real user store by the real setup function.
//
// storage.Manager exposes the gate, but a gate nobody installs is inert — and
// the whole disabled-owner fix would then be a no-op in production while every
// storage-level test passed.
//
// Oracle discipline: the token validates first, on the same manager after setup
// has run, so the later refusal is the user's Disabled flag and not a mis-hashed
// token; an ownerless token is checked throughout as the personal-edition
// control; and a token whose owner does not exist at all is checked too, since
// "unknown user" and "disabled user" must both deny.
//
// BITES: remove the SetAgentTokenOwnerGate block from setupMultiUserOAuth and
// every "must not authenticate" assertion below fails.
func TestSetupMultiUserOAuth_InstallsAgentTokenOwnerGate(t *testing.T) {
	h := newWiringHarness(t)

	user := h.mkUser(t, "tenant@example.com")

	mkToken := func(t *testing.T, owner, name string) string {
		t.Helper()
		raw, err := auth.GenerateToken()
		require.NoError(t, err)
		require.NoError(t, h.tokens.CreateAgentToken(auth.AgentToken{
			Name:        name,
			UserID:      owner,
			Permissions: []string{auth.PermRead},
		}, raw, h.hmacKey))
		return raw
	}

	owned := mkToken(t, user.ID, "ci")
	ownerless := mkToken(t, "", "personal")
	orphan := mkToken(t, "01HTEST00000000000GHOSTUSR", "ghost")

	// Positive control: an active owner's token authenticates through the gate
	// the setup function installed.
	got, err := h.tokens.ValidateAgentToken(owned, h.hmacKey)
	require.NoError(t, err, "positive control: an active owner's token must validate")
	require.NotNil(t, got)

	// A token whose owner is not in the store at all must never authenticate.
	ghost, err := h.tokens.ValidateAgentToken(orphan, h.hmacKey)
	assert.Nil(t, ghost, "a token for an identity that does not exist must not authenticate")
	assert.Error(t, err)

	// Disable the owner through the store the gate reads.
	user.Disabled = true
	require.NoError(t, h.users.UpdateUser(user))

	denied, err := h.tokens.ValidateAgentToken(owned, h.hmacKey)
	assert.Nil(t, denied, "a disabled owner's token must not authenticate")
	require.Error(t, err)
	assert.ErrorIs(t, err, storage.ErrAgentTokenOwnerInactive)

	// The personal edition's ownerless tokens are never gated.
	personal, err := h.tokens.ValidateAgentToken(ownerless, h.hmacKey)
	require.NoError(t, err, "an ownerless token must be unaffected by the owner gate")
	require.NotNil(t, personal)
}
