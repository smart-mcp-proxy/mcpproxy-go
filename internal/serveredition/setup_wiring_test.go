//go:build server

package serveredition

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
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

// setLiveProfiles replaces the current configuration's Profiles, copy-on-write
// like setLiveServers, so a fixture can pin a profile (e.g. "ops-only") without
// racing a concurrent reader of the live config.
func (h *wiringHarness) setLiveProfiles(profiles []config.ProfileConfig) {
	h.configMu.Lock()
	defer h.configMu.Unlock()
	next := *h.config
	next.Profiles = profiles
	h.config = &next
}

// setAdminEmails replaces the live ServerEdition.AdminEmails, copy-on-write
// like setLiveServers, so a fixture that needs a named administrator (the
// two-fixture oracle's Dana, fixture_oracle_test.go) does not have to
// rebuild the whole harness with a bespoke OAuth block.
func (h *wiringHarness) setAdminEmails(emails []string) {
	h.configMu.Lock()
	defer h.configMu.Unlock()
	next := *h.config
	nextSE := *next.ServerEdition
	nextSE.AdminEmails = emails
	next.ServerEdition = &nextSE
	h.config = &next
}

// setAccess replaces the live ServerEdition.Access block, copy-on-write like
// setAdminEmails, so a fixture can install (or hot-reload) the group map the
// entitlement predicate reads through ServerEditionConfigProvider (T074/T075).
func (h *wiringHarness) setAccess(access *config.ServerEditionAccessConfig) {
	h.configMu.Lock()
	defer h.configMu.Unlock()
	next := *h.config
	nextSE := *next.ServerEdition
	nextSE.Access = access
	next.ServerEdition = &nextSE
	h.config = &next
}

// generateBearerToken mints a session-cookie-equivalent bearer JWT for u,
// with the given role ("user" or "admin"), using this harness's HMAC key —
// the credential every /user/* and /admin/* door accepts via
// apiKeyAuthMiddleware. Factored out of the per-test call() closures
// (TestSetupAdminTokenRevocationUsesProductionAuth et al.) so the two-fixture
// oracle (fixture_oracle_test.go) can mint one without duplicating the
// teamsauth import under a second alias.
func (h *wiringHarness) generateBearerToken(u *users.User, role string) (string, error) {
	return teamsauth.GenerateBearerToken(h.hmacKey, u.ID, u.Email, u.DisplayName, role, u.Provider, time.Hour)
}

func newWiringHarness(t *testing.T) *wiringHarness {
	t.Helper()
	return newWiringHarnessWith(t, &config.ServerEditionOAuthConfig{
		Provider:     "google",
		ClientID:     "test-client-id",
		ClientSecret: "test-client-secret",
	})
}

// newWiringHarnessWith drives the production setup with the given OAuth
// block. newOIDCWiringHarness (harness_oidc_test.go) points it at an
// in-process tests/oauthserver OpenID Provider so a test can log in for real.
func newWiringHarnessWith(t *testing.T, oauthCfg *config.ServerEditionOAuthConfig) *wiringHarness {
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
				OAuth:          oauthCfg,
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

func TestSetupRevalidatesTokenScopeAfterUnsharing(t *testing.T) {
	h := newWiringHarness(t)
	user := h.mkUser(t, "tenant@example.com")
	h.setLiveServers([]*config.ServerConfig{{Name: "shared", Shared: true}, {Name: "private"}})
	for _, scope := range [][]string{{"shared"}, {"*"}} {
		raw, err := auth.GenerateToken()
		require.NoError(t, err)
		require.NoError(t, h.tokens.CreateAgentToken(auth.AgentToken{Name: scope[0], UserID: user.ID, AllowedServers: scope, Permissions: []string{auth.PermRead}}, raw, h.hmacKey))
		before, err := h.tokens.ValidateAgentToken(raw, h.hmacKey)
		require.NoError(t, err)
		require.Equal(t, []string{"shared"}, before.AllowedServers)
		h.setLiveServers([]*config.ServerConfig{{Name: "shared", Shared: false}, {Name: "private"}})
		after, err := h.tokens.ValidateAgentToken(raw, h.hmacKey)
		require.NoError(t, err)
		require.Empty(t, after.AllowedServers)
		h.setLiveServers([]*config.ServerConfig{{Name: "shared", Shared: true}, {Name: "private"}})
	}
}

func TestSetupAuxiliarySurfacesFollowLiveSharing(t *testing.T) {
	h := newWiringHarness(t)
	user := h.mkUser(t, "tenant@example.com")
	token, err := teamsauth.GenerateBearerToken(h.hmacKey, user.ID, user.Email, user.DisplayName, "user", user.Provider, time.Hour)
	require.NoError(t, err)
	for _, shared := range []bool{true, false, true} {
		h.setLiveServers([]*config.ServerConfig{{Name: "sharing-sentinel", Shared: shared, AuthBroker: &config.AuthBrokerConfig{Mode: config.AuthBrokerModeOAuthConnect}}})
		for _, path := range []string{"/api/v1/user/credentials", "/api/v1/user/diagnostics"} {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Host = "localhost:8080"
			req.Header.Set("Authorization", "Bearer "+token)
			rec := httptest.NewRecorder()
			h.router.ServeHTTP(rec, req)
			require.Equal(t, 200, rec.Code, rec.Body.String())
			require.Equal(t, shared, strings.Contains(rec.Body.String(), "sharing-sentinel"), path+": "+rec.Body.String())
		}
	}
}

func TestSetupAdminTokenRevocationUsesProductionAuth(t *testing.T) {
	h := newWiringHarness(t)
	admin := h.mkUser(t, "admin@example.com")
	tenant := h.mkUser(t, "tenant@example.com")
	raw, err := auth.GenerateToken()
	require.NoError(t, err)
	const tokenName = "release/保安 %25"
	require.NoError(t, h.tokens.CreateAgentToken(auth.AgentToken{
		Name: tokenName, UserID: tenant.ID, Permissions: []string{auth.PermRead},
	}, raw, h.hmacKey))

	call := func(identity *users.User) *httptest.ResponseRecorder {
		t.Helper()
		role := "user"
		if identity.ID == admin.ID {
			role = "admin"
		}
		bearer, tokenErr := teamsauth.GenerateBearerToken(
			h.hmacKey, identity.ID, identity.Email, identity.DisplayName, role, identity.Provider, time.Hour,
		)
		require.NoError(t, tokenErr)
		body, marshalErr := json.Marshal(map[string]string{"name": tokenName})
		require.NoError(t, marshalErr)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/users/"+tenant.ID+"/tokens/revoke", bytes.NewReader(body))
		req.Host = "localhost:8080"
		req.Header.Set("Authorization", "Bearer "+bearer)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.router.ServeHTTP(rec, req)
		return rec
	}

	require.Equal(t, http.StatusForbidden, call(tenant).Code)
	for attempt := 0; attempt < 2; attempt++ {
		rec := call(admin)
		require.Equal(t, http.StatusOK, rec.Code, "attempt %d: %s", attempt, rec.Body.String())
	}
	_, err = h.tokens.ValidateAgentToken(raw, h.hmacKey)
	require.Error(t, err)
}

// TestSetupMultiUserOAuth_GroupGrantWiredThroughProductionSetup drives the
// two-fixture oracle (fixture_oracle_test.go) through the PRODUCTION wiring
// (setupMultiUserOAuth): the access block, the group term of the entitlement
// predicate and the single owner resolution are installed by setup.go, not by
// a hand-built rig (Spec 107 T074/T075/T076 — US1.1, US1.3, US1.6, US1.8).
func TestSetupMultiUserOAuth_GroupGrantWiredThroughProductionSetup(t *testing.T) {
	tf := newTwoFixture(t)

	listAs := func(u func(fx *fixtureHarness) *users.User) twoFixtureOp {
		return func(fx *fixtureHarness) (int, []byte) {
			return fx.doBearer(http.MethodGet, "/api/v1/user/servers", fx.bearerFor(t, u(fx)))
		}
	}
	sharedNamesOf := func(body []byte) []string {
		var resp struct {
			Shared []struct {
				Name string `json:"name"`
			} `json:"shared"`
		}
		require.NoError(t, json.Unmarshal(body, &resp))
		out := make([]string, 0, len(resp.Shared))
		for _, s := range resp.Shared {
			out = append(out, s.Name)
		}
		return out
	}

	// US1.1: Alice (eng -> [a]) sees the same list on both fixtures, and no
	// sentinel of b / a__b.
	assertTwoFixture(t, tf, listAs(func(fx *fixtureHarness) *users.User { return fx.alice }), sentinelServerB, sentinelServerAB)
	status, body := listAs(func(fx *fixtureHarness) *users.User { return fx.alice })(tf.A)
	require.Equal(t, http.StatusOK, status, string(body))
	assert.Equal(t, []string{"a"}, sharedNamesOf(body))

	// Bob (no group, no default grant) is an unentitled tenant on both.
	assertTwoFixture(t, tf, listAs(func(fx *fixtureHarness) *users.User { return fx.bob }), sentinelServerA, sentinelServerB, sentinelServerAB)

	// Carol (ops -> [a, b]) sees b where it exists.
	_, carolA := listAs(func(fx *fixtureHarness) *users.User { return fx.carol })(tf.A)
	assert.ElementsMatch(t, []string{"a", "b"}, sharedNamesOf(carolA))

	// US1.8: Dana (admin_emails) keeps the whole shared projection.
	_, danaA := listAs(func(fx *fixtureHarness) *users.User { return fx.dana })(tf.A)
	assert.ElementsMatch(t, []string{"a", "b", "a__b"}, sharedNamesOf(danaA))

	// By-name door: hidden b == absent name (status parity, own name echoed).
	assertStatusParity(t,
		func() (int, []byte) {
			return tf.A.doBearer(http.MethodGet, "/api/v1/user/servers/b", tf.A.bearerFor(t, tf.A.alice))
		},
		func() (int, []byte) {
			return tf.B.doBearer(http.MethodGet, "/api/v1/user/servers/b", tf.B.bearerFor(t, tf.B.alice))
		},
	)

	// The agent-token path (US1.3 / US1.5): Alice's "*" token narrows to [a]
	// on both fixtures through the single owner resolution; Bob's to nothing.
	for _, fx := range []*fixtureHarness{tf.A, tf.B} {
		tok, err := fx.h.tokens.ValidateAgentToken(fx.aliceTokenStar, fx.h.hmacKey)
		require.NoError(t, err)
		assert.Equal(t, []string{"a"}, tok.AllowedServers)
		assert.Equal(t, fx.alice.Email, tok.OwnerEmail, "the owner resolution stamps the live identity")
		assert.Equal(t, "user", tok.OwnerRole)
		bobStar := mintFixtureToken(t, fx.h, fx.bob, "bob-star", []string{"*"})
		bob, err := fx.h.tokens.ValidateAgentToken(bobStar, fx.h.hmacKey)
		require.NoError(t, err)
		require.NotNil(t, bob.AllowedServers, "an unentitled token carries a NON-NIL empty grant (FR-006)")
		assert.Empty(t, bob.AllowedServers)
	}

	// FR-009: Dana's literal "*" survives the owner resolution (never frozen
	// into a snapshot of the configuration), and her live role is stamped.
	danaStar := mintFixtureToken(t, tf.A.h, tf.A.dana, "dana-star", []string{"*"})
	danaTok, err := tf.A.h.tokens.ValidateAgentToken(danaStar, tf.A.h.hmacKey)
	require.NoError(t, err)
	assert.Equal(t, []string{"*"}, danaTok.AllowedServers, "an administrator's literal star must survive validation")
	assert.Equal(t, "admin", danaTok.OwnerRole)

	// US1.6: hot reload — removing the eng mapping narrows Alice's NEXT
	// request and her existing token's next authentication; re-adding widens.
	tf.A.h.setAccess(&config.ServerEditionAccessConfig{GroupServers: map[string][]string{"ops": {"a", "b"}}, DefaultServers: []string{}})
	_, narrowed := listAs(func(fx *fixtureHarness) *users.User { return fx.alice })(tf.A)
	assert.Empty(t, sharedNamesOf(narrowed), "un-mapping eng must narrow the next REST request without a restart")
	tok, err := tf.A.h.tokens.ValidateAgentToken(tf.A.aliceTokenStar, tf.A.h.hmacKey)
	require.NoError(t, err)
	assert.Empty(t, tok.AllowedServers, "the next token authentication must see the narrowed map")

	tf.A.h.setAccess(&config.ServerEditionAccessConfig{GroupServers: map[string][]string{"eng": {"a"}, "ops": {"a", "b"}}, DefaultServers: []string{}})
	_, widened := listAs(func(fx *fixtureHarness) *users.User { return fx.alice })(tf.A)
	assert.Equal(t, []string{"a"}, sharedNamesOf(widened), "re-adding the mapping widens the next request")

	// Un-sharing a narrows too, whatever the map says.
	tf.A.h.setLiveServers([]*config.ServerConfig{{Name: "a", Protocol: "http", Shared: false, Enabled: true}})
	_, unshared := listAs(func(fx *fixtureHarness) *users.User { return fx.alice })(tf.A)
	assert.Empty(t, sharedNamesOf(unshared))
}

// TestSetupMultiUserOAuth_WiresEntitlementSnapshotProvider proves production
// setup installs teamsapi.UserHandlers' combined EntitlementSnapshotProvider
// (see TestEntitledServerNamesFor_ServersAndAccessFromOneSnapshot in
// internal/serveredition/api/entitlement_group_test.go for the split-read
// race it closes) rather than leaving the entitlement predicate on the two
// independent providers, which read the live configuration separately.
//
// Oracle: wrap Dependencies.ConfigProvider in a counter and drive one
// GET /api/v1/user/servers request through it. Two components read the live
// configuration on this request: the auth middleware (role derivation via
// its own ServerEditionConfigProvider call) and the entitlement predicate.
// With the combined provider, the predicate makes exactly ONE call for
// both its servers and access values, for a total of 2; without it (the two
// separate providers, each read independently), the predicate makes 2 calls
// on its own, for a total of 3.
//
// BITES: removing the SetEntitlementSnapshotProvider call from
// setupMultiUserOAuth makes this test observe 3 calls instead of 2.
func TestSetupMultiUserOAuth_WiresEntitlementSnapshotProvider(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := bbolt.Open(tmpDir+"/test.db", 0600, &bbolt.Options{Timeout: time.Second})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	logger := zap.NewNop().Sugar()
	tokens, err := storage.NewManager(t.TempDir(), logger)
	require.NoError(t, err)
	t.Cleanup(func() { tokens.Close() })

	baseConfig := &config.Config{
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

	var calls int32
	countingConfigProvider := func() *config.Config {
		atomic.AddInt32(&calls, 1)
		return baseConfig
	}

	router := chi.NewRouter()
	require.NoError(t, setupMultiUserOAuth(Dependencies{
		Router:         router,
		DB:             db,
		Logger:         logger,
		DataDir:        tmpDir,
		Config:         baseConfig,
		ConfigProvider: countingConfigProvider,
		StorageManager: tokens,
	}))

	userStore := users.NewUserStore(db)
	hmacKey, err := auth.GetOrCreateHMACKey(tmpDir)
	require.NoError(t, err)

	alice := users.NewUser("alice@example.com", "Alice", "google", "sub-alice")
	require.NoError(t, userStore.CreateUser(alice))

	bearer, err := teamsauth.GenerateBearerToken(hmacKey, alice.ID, alice.Email, alice.DisplayName, "user", alice.Provider, time.Hour)
	require.NoError(t, err)

	atomic.StoreInt32(&calls, 0)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/user/servers", nil)
	req.Header.Set("Authorization", "Bearer "+bearer)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	assert.Equal(t, int32(2), atomic.LoadInt32(&calls),
		"GET /user/servers must read the live configuration once for auth-middleware role "+
			"derivation and exactly once (not twice) for its entitlement decision through the "+
			"combined EntitlementSnapshotProvider")
}
