//go:build server

package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Server fixture names. "admin-private" is in the admin configuration but NOT
// flagged Shared, so no ordinary tenant may ever see or reach it.
const (
	scopeSharedServer  = "shared-ok"
	scopePrivateServer = "admin-private"
	scopeAPersonal     = "a-personal"
	scopeBPersonal     = "b-personal"
	scopeAbsentServer  = "no-such-server-Zq7"
)

const tokenAdminUser = "01HTEST00000000000ADMINUSR"

func adminUserCtx() *auth.AuthContext {
	return auth.AdminUserContext(tokenAdminUser, "root@example.com", "Root", "google")
}

// scopeFixtureServers mirrors what setup.go hands NewUserHandlers: the WHOLE
// admin configuration, of which only the Shared entries are visible to a
// tenant through /api/v1/user/servers.
func scopeFixtureServers() []*config.ServerConfig {
	return []*config.ServerConfig{
		{Name: scopeSharedServer, URL: "https://shared.example", Protocol: "http", Enabled: true, Shared: true},
		{Name: scopePrivateServer, URL: "https://private.example", Protocol: "http", Enabled: true},
	}
}

// createToken posts a create request under the rig's current identity.
func (rig *tokenTestRig) createToken(t *testing.T, name string, allowedServers []string) *httptest.ResponseRecorder {
	t.Helper()

	body := map[string]interface{}{"name": name, "permissions": []string{auth.PermRead}}
	if allowedServers != nil {
		body["allowed_servers"] = allowedServers
	}
	raw, err := json.Marshal(body)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/user/tokens", strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	rig.router.ServeHTTP(w, req)
	return w
}

// seedPersonalServer writes a personal server through the user store, the same
// place createServer persists one.
func (rig *tokenTestRig) seedPersonalServer(t *testing.T, owner, name string) {
	t.Helper()
	require.NoError(t, rig.users.CreateUserServer(owner, &config.ServerConfig{
		Name:     name,
		URL:      "https://" + name + ".example",
		Protocol: "http",
		Enabled:  true,
	}))
}

// errorMessage decodes the handler's error envelope. chi's own 404 is plain
// text and does not decode, so this also rules out "the route never matched".
func errorMessage(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var envelope struct {
		Error      string `json:"error"`
		Message    string `json:"message"`
		StatusCode int    `json:"status_code"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope),
		"body did not decode as the handler's error envelope (%q)", w.Body.String())
	require.Equal(t, w.Code, envelope.StatusCode, "envelope status_code disagrees with the HTTP status")
	return envelope.Message
}

// TestCreateUserToken_ScopeConstrainedToEntitledServers is the property that
// makes the #1166 REST-enumeration scope filter hold in the server edition:
// a tenant must not be able to WIDEN their own token past what they may see.
//
// createUserToken used to persist the requested allowed_servers verbatim, and
// AllowedServers is the sole input to auth.AuthContext.CanAccessServer — so
// asking for the admin's unshared server, or for "*", minted a credential that
// walked straight through the scope filter into the admin's whole inventory.
//
// Oracle discipline, matching the rest of this branch:
//
//  1. a POSITIVE CONTROL runs FIRST, on the SAME router, minting a token over
//     the caller's genuinely entitled servers — so a 400 below cannot be the
//     route failing to match, the store being unwired, or the body being
//     malformed;
//  2. an independent entitlement proof through the PRODUCTION per-user door
//     (GET /api/v1/user/servers), showing exactly which names user A may see;
//  3. statuses are PINNED (201 / 400), never asserted as bare parity; and
//  4. every rejection is confirmed at the STORAGE layer — no token record.
//
// BITES: on unfixed code every "must be rejected" case returns 201 and the
// requested scope is persisted verbatim.
func TestCreateUserToken_ScopeConstrainedToEntitledServers(t *testing.T) {
	rig := newTokenTestRigWithServers(t, scopeFixtureServers())
	rig.seedPersonalServer(t, tokenUserA, scopeAPersonal)
	rig.seedPersonalServer(t, tokenUserB, scopeBPersonal)

	rig.actAs(userACtx())

	// (2) Entitlement proof through the production route table.
	listed := rig.call(t, http.MethodGet, "/api/v1/user/servers")
	require.Equal(t, http.StatusOK, listed.Code, "the per-user door must be readable")
	var doorway ServerListResponse
	require.NoError(t, json.Unmarshal(listed.Body.Bytes(), &doorway))

	visible := map[string]bool{}
	for _, s := range doorway.Personal {
		visible[s.Name] = true
	}
	for _, s := range doorway.Shared {
		visible[s.Name] = true
	}
	require.True(t, visible[scopeAPersonal], "fixture did not land: user A cannot see their own %q", scopeAPersonal)
	require.True(t, visible[scopeSharedServer], "fixture did not land: user A cannot see shared %q", scopeSharedServer)
	require.False(t, visible[scopePrivateServer], "fixture is wrong: %q must NOT be visible to a tenant", scopePrivateServer)
	require.False(t, visible[scopeBPersonal], "fixture is wrong: %q must NOT be visible to user A", scopeBPersonal)

	// (1) Positive control: the entitled scope mints, and persists exactly.
	ctl := rig.createToken(t, "ctl", []string{scopeSharedServer, scopeAPersonal})
	require.Equal(t, http.StatusCreated, ctl.Code,
		"positive control failed: an entitled scope was rejected (%s)", ctl.Body.String())
	stored, err := rig.store.GetAgentTokenByOwnerAndName(tokenUserA, "ctl")
	require.NoError(t, err)
	require.NotNil(t, stored, "positive control did not persist a token")
	require.Equal(t, []string{scopeSharedServer, scopeAPersonal}, stored.AllowedServers,
		"an entitled scope must be persisted unchanged")

	// The probes: every one of these names is outside user A's entitlement.
	rejected := []struct {
		label   string
		servers []string
	}{
		{"admin's unshared server", []string{scopePrivateServer}},
		{"another tenant's personal server", []string{scopeBPersonal}},
		{"a server that exists nowhere", []string{scopeAbsentServer}},
		{"an entitled name smuggling an unentitled one", []string{scopeAPersonal, scopePrivateServer}},
		{"a glob, which the enforcement layer does not expand", []string{"shared-*"}},
	}

	for i, tc := range rejected {
		name := fmt.Sprintf("probe-%d", i)
		w := rig.createToken(t, name, tc.servers)

		assert.Equal(t, http.StatusBadRequest, w.Code,
			"%s: expected exactly 400, got %d (%s)", tc.label, w.Code, w.Body.String())

		persisted, err := rig.store.GetAgentTokenByOwnerAndName(tokenUserA, name)
		require.NoError(t, err)
		assert.Nil(t, persisted, "%s: a rejected request still minted a token", tc.label)
	}
}

// TestCreateUserToken_ScopeRejectionIsNotAnExistenceOracle guards the shape of
// the refusal. This branch has just collapsed the 403/404 oracle on token
// names; a message that said "that server is not shared with you" for a real
// server and "unknown server" for an imaginary one would re-open the same
// oracle one field over, letting a tenant enumerate the admin's private
// inventory by minting tokens.
//
// BITES: on unfixed code both probes answer 201, so there is no message to
// compare and the status pin fails first.
func TestCreateUserToken_ScopeRejectionIsNotAnExistenceOracle(t *testing.T) {
	rig := newTokenTestRigWithServers(t, scopeFixtureServers())
	rig.seedPersonalServer(t, tokenUserA, scopeAPersonal)
	rig.actAs(userACtx())

	// Positive control first: rejections below are the check, not a broken route.
	ctl := rig.createToken(t, "oracle-ctl", []string{scopeAPersonal})
	require.Equal(t, http.StatusCreated, ctl.Code,
		"positive control failed: an entitled scope was rejected (%s)", ctl.Body.String())

	real := rig.createToken(t, "probe-real", []string{scopePrivateServer})
	fake := rig.createToken(t, "probe-fake", []string{scopeAbsentServer})

	require.Equal(t, http.StatusBadRequest, real.Code, "existing-but-unentitled server: expected 400 (%s)", real.Body.String())
	require.Equal(t, http.StatusBadRequest, fake.Code, "nonexistent server: expected 400 (%s)", fake.Body.String())

	realMsg := errorMessage(t, real)
	fakeMsg := errorMessage(t, fake)

	// The only permitted difference is the name the caller itself supplied.
	normalise := func(msg, name string) string { return strings.ReplaceAll(msg, name, "<NAME>") }
	assert.Equal(t,
		normalise(fakeMsg, scopeAbsentServer),
		normalise(realMsg, scopePrivateServer),
		"the refusal distinguishes an existing server from an imaginary one — a server-name existence oracle")
	assert.NotContains(t, realMsg, "shared",
		"the refusal leaks WHY the server is unreachable, which classifies it for the caller")
}

// TestCreateUserToken_WildcardMeansOnlyWhatTheCallerMaySee pins the wildcard
// decision. "*" is the only wildcard auth.AuthContext.CanAccessServer honours,
// and it matches every server unconditionally — so persisting a tenant's "*"
// leaves a standing grant over the whole deployment. For a tenant it is
// materialised into their entitled set; for an admin, who does administer the
// deployment, it keeps its literal meaning.
//
// BITES: on unfixed code the tenant's token stores the literal ["*"].
func TestCreateUserToken_WildcardMeansOnlyWhatTheCallerMaySee(t *testing.T) {
	t.Run("tenant wildcard expands to the entitled set", func(t *testing.T) {
		rig := newTokenTestRigWithServers(t, scopeFixtureServers())
		rig.seedPersonalServer(t, tokenUserA, scopeAPersonal)
		rig.seedPersonalServer(t, tokenUserB, scopeBPersonal)
		rig.actAs(userACtx())

		w := rig.createToken(t, "star", []string{"*"})
		require.Equal(t, http.StatusCreated, w.Code, "a tenant wildcard should mint, narrowed (%s)", w.Body.String())

		stored, err := rig.store.GetAgentTokenByOwnerAndName(tokenUserA, "star")
		require.NoError(t, err)
		require.NotNil(t, stored, "the wildcard request did not persist a token")

		assert.NotContains(t, stored.AllowedServers, "*",
			"a tenant's token kept the literal wildcard: it can reach every server in the deployment")
		assert.ElementsMatch(t, []string{scopeAPersonal, scopeSharedServer}, stored.AllowedServers,
			"the wildcard must materialise to exactly the servers the per-user door shows this tenant")

		// And the caller is told what they actually got.
		var resp AgentTokenResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.ElementsMatch(t, stored.AllowedServers, resp.AllowedServers,
			"the response must echo the effective scope, so the narrowing is not silent")
	})

	t.Run("admin wildcard stays literal", func(t *testing.T) {
		rig := newTokenTestRigWithServers(t, scopeFixtureServers())
		rig.actAs(adminUserCtx())

		w := rig.createToken(t, "star", []string{"*"})
		require.Equal(t, http.StatusCreated, w.Code, "an admin wildcard should mint (%s)", w.Body.String())

		stored, err := rig.store.GetAgentTokenByOwnerAndName(tokenAdminUser, "star")
		require.NoError(t, err)
		require.NotNil(t, stored)
		assert.Equal(t, []string{"*"}, stored.AllowedServers,
			"an admin administers every server; freezing their wildcard would silently drop servers added later")
	})

	t.Run("admin may scope to an unshared configured server", func(t *testing.T) {
		rig := newTokenTestRigWithServers(t, scopeFixtureServers())
		rig.actAs(adminUserCtx())

		w := rig.createToken(t, "priv", []string{scopePrivateServer})
		require.Equal(t, http.StatusCreated, w.Code,
			"an admin's own unshared server must remain scopable (%s)", w.Body.String())

		stored, err := rig.store.GetAgentTokenByOwnerAndName(tokenAdminUser, "priv")
		require.NoError(t, err)
		require.NotNil(t, stored)
		assert.Equal(t, []string{scopePrivateServer}, stored.AllowedServers)
	})

	t.Run("admin is still held to configured names", func(t *testing.T) {
		rig := newTokenTestRigWithServers(t, scopeFixtureServers())
		rig.actAs(adminUserCtx())

		w := rig.createToken(t, "ghost", []string{scopeAbsentServer})
		assert.Equal(t, http.StatusBadRequest, w.Code,
			"an unknown server name should be rejected for an admin too (%s)", w.Body.String())
	})

	t.Run("wildcard with nothing entitled is refused, not silently deny-all", func(t *testing.T) {
		// No shared servers configured and no personal servers for this user.
		rig := newTokenTestRigWithServers(t, nil)
		rig.actAs(userACtx())

		w := rig.createToken(t, "star", []string{"*"})
		require.Equal(t, http.StatusBadRequest, w.Code,
			"a wildcard over an empty entitlement set must not mint a mystery token (%s)", w.Body.String())

		stored, err := rig.store.GetAgentTokenByOwnerAndName(tokenUserA, "star")
		require.NoError(t, err)
		assert.Nil(t, stored)
	})
}

// TestCreateUserToken_OmittedScopeIsUnchanged pins the behaviour the new check
// deliberately does NOT alter: an omitted allowed_servers stays empty, which at
// the agent tier already denies every server. Rewriting it to ["*"] the way the
// personal edition does (where the only caller IS the operator) would be a
// widening dressed up as a default.
func TestCreateUserToken_OmittedScopeIsUnchanged(t *testing.T) {
	rig := newTokenTestRigWithServers(t, scopeFixtureServers())
	rig.seedPersonalServer(t, tokenUserA, scopeAPersonal)
	rig.actAs(userACtx())

	w := rig.createToken(t, "bare", nil)
	require.Equal(t, http.StatusCreated, w.Code, "omitting allowed_servers must still mint (%s)", w.Body.String())

	stored, err := rig.store.GetAgentTokenByOwnerAndName(tokenUserA, "bare")
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Empty(t, stored.AllowedServers, "an omitted scope must not be widened into a wildcard")

	// And an empty scope really is deny-all at the agent tier, so the default
	// needs no widening to be safe.
	assert.False(t, stored.AuthContext().CanAccessServer(scopeSharedServer),
		"an empty AllowedServers must deny, not allow — otherwise the default above is a hole")
}
