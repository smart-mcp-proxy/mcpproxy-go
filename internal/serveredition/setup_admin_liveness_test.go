//go:build server

package serveredition

import (
	"net/http"
	"net/http/httptest"
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

// setLiveAdminEmails replaces the admin list the CURRENT configuration carries,
// which is what editing `admin_emails` and letting the file watcher hot-reload
// does. Copy-on-write down to the server-edition block, matching the runtime's
// snapshot publishing.
func (h *wiringHarness) setLiveAdminEmails(emails []string) {
	h.configMu.Lock()
	defer h.configMu.Unlock()
	nextSE := *h.config.ServerEdition
	nextSE.AdminEmails = emails
	next := *h.config
	next.ServerEdition = &nextSE
	h.config = &next
}

// getAdminUsers calls the production admin route as u, over the production
// route table and middleware chain. It answers 200 for an admin and 403 for a
// plain user, so its status IS the role decision.
func (h *wiringHarness) getAdminUsers(t *testing.T, u *users.User) *httptest.ResponseRecorder {
	t.Helper()
	// The `role` claim is minted "admin" on purpose in every call below: it is
	// informational, and a test that varied it could pass by trusting the claim
	// instead of the config.
	token, err := teamsauth.GenerateBearerToken(h.hmacKey, u.ID, u.Email, u.DisplayName, "admin", u.Provider, time.Hour)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/users", nil)
	req.Host = "localhost:8080"
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, req)
	return rec
}

// TestSetupMultiUserOAuth_DemotionTakesEffectWithoutRestart is the whole of
// issue #1169 on the production wiring.
//
// The role is derived from `admin_emails`, and the middleware used to hold the
// *config.ServerEditionConfig captured when setup ran. The configuration is
// hot-reloadable — config.LoadFromFile unmarshals `server_edition` in full, and
// it is not among the restart-pinned fields — so an operator who removed an
// admin from the list changed nothing at all until the process restarted. That
// is a narrower gap than "until the JWT expires", but it is the same gap: the
// documented remediation does not take effect when it is applied.
//
// Oracle discipline: the SAME request, by the SAME user, with the SAME token,
// succeeds first. Only the configuration the provider returns changes between
// the two calls, so a 403 afterwards can only be the live read.
//
// BITES: pass `cfg` instead of `serverEditionConfig` to
// NewServerEditionAuthMiddleware in setup.go and the second call returns 200.
func TestSetupMultiUserOAuth_DemotionTakesEffectWithoutRestart(t *testing.T) {
	h := newWiringHarness(t)

	admin := h.mkUser(t, "admin@example.com")

	control := h.getAdminUsers(t, admin)
	require.Equal(t, http.StatusOK, control.Code,
		"positive control: the boot admin must reach the admin route (%s)", control.Body.String())

	// The operator edits admin_emails and the file watcher republishes.
	h.setLiveAdminEmails([]string{"someone-else@example.com"})

	demoted := h.getAdminUsers(t, admin)
	assert.Equal(t, http.StatusForbidden, demoted.Code,
		"a user removed from admin_emails must lose admin on their NEXT request, not at the next restart (%s)",
		demoted.Body.String())
}

// TestSetupMultiUserOAuth_PromotionTakesEffectWithoutRestart is the other
// direction, and pins the docs claim that adding someone promotes them on their
// next request.
//
// BITES: same neuter as above — the second call stays 403.
func TestSetupMultiUserOAuth_PromotionTakesEffectWithoutRestart(t *testing.T) {
	h := newWiringHarness(t)

	tenant := h.mkUser(t, "tenant@example.com")

	control := h.getAdminUsers(t, tenant)
	require.Equal(t, http.StatusForbidden, control.Code,
		"positive control: a plain user must be refused before the reload (%s)", control.Body.String())

	h.setLiveAdminEmails([]string{"admin@example.com", "tenant@example.com"})

	promoted := h.getAdminUsers(t, tenant)
	assert.Equal(t, http.StatusOK, promoted.Code,
		"a user added to admin_emails must gain admin on their next request (%s)", promoted.Body.String())
}

// TestSetupMultiUserOAuth_DroppedServerEditionBlockKeepsBootAdmins pins the
// fallback: a reload that loses the `server_edition` block entirely must not
// silently strip every admin (which would lock the deployment's operators out
// of their own admin surface). It falls back to the boot block — the behaviour
// before the live read existed — and never WIDENS the list.
func TestSetupMultiUserOAuth_DroppedServerEditionBlockKeepsBootAdmins(t *testing.T) {
	h := newWiringHarness(t)

	admin := h.mkUser(t, "admin@example.com")
	tenant := h.mkUser(t, "tenant@example.com")

	h.configMu.Lock()
	next := *h.config
	next.ServerEdition = nil
	h.config = &next
	h.configMu.Unlock()

	assert.Equal(t, http.StatusOK, h.getAdminUsers(t, admin).Code,
		"a config with no server_edition block must fall back to the boot admin list")
	assert.Equal(t, http.StatusForbidden, h.getAdminUsers(t, tenant).Code,
		"the fallback must not widen the admin list")
}

// TestSetupMultiUserOAuth_OwnerGateSurvivesAFailedSetup is finding N3.
//
// wireServerEditionOAuth only LOGS SetupAll's error — the process comes up
// serving traffic either way — so anything setupMultiUserOAuth installs after a
// fallible step is simply absent from a running server. The agent-token owner
// gate used to be installed after cfg.Validate(), userStore.EnsureBuckets(),
// auth.GetOrCreateHMACKey() and broker.NewBBoltAESStore(). A failure in any of
// them therefore removed the one control that stops a DISABLED user's agent
// tokens from authenticating: fail-open on exactly the wrong axis.
//
// Here setup is made to fail at its first fallible step (an enabled
// server_edition block with no admin_emails fails Validate), and the gate must
// still be in force.
//
// BITES: move the SetAgentTokenOwnerGate block in setup.go back below
// `cfg.Validate()` and the assertions below fail — the disabled owner's token
// validates.
func TestSetupMultiUserOAuth_OwnerGateSurvivesAFailedSetup(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := bbolt.Open(tmpDir+"/test.db", 0600, &bbolt.Options{Timeout: time.Second})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	logger := zap.NewNop().Sugar()
	tokens, err := storage.NewManager(t.TempDir(), logger)
	require.NoError(t, err)
	t.Cleanup(func() { tokens.Close() })

	userStore := users.NewUserStore(db)
	require.NoError(t, userStore.EnsureBuckets())

	hmacKey, err := auth.GetOrCreateHMACKey(tmpDir)
	require.NoError(t, err)

	user := users.NewUser("tenant@example.com", "tenant@example.com", "google", "sub-tenant")
	require.NoError(t, userStore.CreateUser(user))

	mkToken := func(owner, name string) string {
		raw, gerr := auth.GenerateToken()
		require.NoError(t, gerr)
		require.NoError(t, tokens.CreateAgentToken(auth.AgentToken{
			Name:        name,
			UserID:      owner,
			Permissions: []string{auth.PermRead},
		}, raw, hmacKey))
		return raw
	}
	owned := mkToken(user.ID, "ci")
	ownerless := mkToken("", "personal")

	// Enabled, but invalid: no admin_emails. cfg.Validate() is the FIRST
	// fallible step in setupMultiUserOAuth.
	broken := &config.Config{
		ServerEdition: &config.ServerEditionConfig{
			Enabled: true,
			OAuth: &config.ServerEditionOAuthConfig{
				Provider:     "google",
				ClientID:     "test-client-id",
				ClientSecret: "test-client-secret",
			},
		},
	}

	err = setupMultiUserOAuth(Dependencies{
		Router:         chi.NewRouter(),
		DB:             db,
		Logger:         logger,
		DataDir:        tmpDir,
		Config:         broken,
		ConfigProvider: func() *config.Config { return broken },
		StorageManager: tokens,
	})
	require.Error(t, err, "the harness must actually reproduce a failed setup, or it proves nothing")

	// Positive control: with an ACTIVE owner the token still validates, so the
	// refusal below is the Disabled flag and not a gate that denies everything.
	ok, err := tokens.ValidateAgentToken(owned, hmacKey)
	require.NoError(t, err, "positive control: an active owner's token must validate after a failed setup")
	require.NotNil(t, ok)

	user.Disabled = true
	require.NoError(t, userStore.UpdateUser(user))

	denied, err := tokens.ValidateAgentToken(owned, hmacKey)
	assert.Nil(t, denied, "a disabled owner's token must not authenticate even when setup failed")
	require.Error(t, err)
	assert.ErrorIs(t, err, storage.ErrAgentTokenOwnerInactive)

	// The personal edition's ownerless tokens stay unaffected.
	personal, err := tokens.ValidateAgentToken(ownerless, hmacKey)
	require.NoError(t, err, "an ownerless token must be unaffected by the owner gate")
	require.NotNil(t, personal)
}
