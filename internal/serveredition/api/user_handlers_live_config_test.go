//go:build server

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// lateAdminServer is a name that is NOT in the admin configuration when the
// handlers are built, and is added to it afterwards — which is what a config
// hot reload does.
const lateAdminServer = "late-admin-db"

// TestTokenScope_ReadsTheLiveAdminConfiguration closes the reopening of the
// name-collision escalation.
//
// The control that stops a personal server from lending its name to a token
// scope (collidesWithAdminConfig, used by entitledServerNames) compared against
// a slice CAPTURED WHEN THE HANDLERS WERE CONSTRUCTED, at process start. The
// configuration is hot-reloadable, so any admin server added afterwards was
// invisible to it — and a tenant who had already created a personal server of
// that name walked through the entitlement check exactly as they did before the
// control existed, minting a token whose bare name
// auth.AuthContext.CanAccessServer resolves to the ADMIN's server.
//
// Oracle discipline:
//
//  1. the personal server is created, and a token minted over it, BEFORE the
//     admin server appears — a positive control on the same router proving the
//     route, the store and the entitlement path all work, and proving the name
//     was genuinely free at that moment;
//  2. the only thing that changes between the control and the probe is the
//     configuration the provider returns;
//  3. statuses are PINNED (201 then 400), never asserted as bare parity; and
//  4. the escalation is checked at its END state — what allowed_servers any
//     stored token carries — not merely at the refusal.
//
// BITES: build the handlers with StaticAdminServers(scopeFixtureServers()) in
// newTokenTestRigWithServers — the boot-time snapshot this replaced — and the
// probe mints 201 carrying the admin's server name.
func TestTokenScope_ReadsTheLiveAdminConfiguration(t *testing.T) {
	rig := newTokenTestRigWithServers(t, scopeFixtureServers())
	rig.actAs(userACtx())

	// (1) The name is free right now, so both of these must succeed.
	created := rig.createServer(t, lateAdminServer)
	require.Equal(t, http.StatusCreated, created.Code,
		"positive control: an uncontested personal server must be creatable (%s)", created.Body.String())

	control := rig.createToken(t, "before-reload", []string{lateAdminServer})
	require.Equal(t, http.StatusCreated, control.Code,
		"positive control: a token over an entitled personal server must mint (%s)", control.Body.String())

	// (2) The admin now adds a PRIVATE server with that very name — a hot
	// reload, which is the only thing that changes here.
	rig.addAdminServer(&config.ServerConfig{
		Name: lateAdminServer, URL: "https://late.example", Protocol: "http", Enabled: true,
	})

	// (3) The same request that just succeeded must now be refused.
	probe := rig.createToken(t, "after-reload", []string{lateAdminServer})
	require.Equal(t, http.StatusBadRequest, probe.Code,
		"a name the admin has since taken must stop entitling a token (%s)", probe.Body.String())

	// The wildcard must not smuggle it in either.
	star := rig.createToken(t, "after-reload-star", []string{"*"})
	require.Equal(t, http.StatusCreated, star.Code,
		"the wildcard must still resolve over the remaining entitlement (%s)", star.Body.String())
	var starResp AgentTokenResponse
	require.NoError(t, json.Unmarshal(star.Body.Bytes(), &starResp))
	assert.NotContains(t, starResp.AllowedServers, lateAdminServer,
		`materialising "*" must not include a name the admin has since taken`)
	assert.Contains(t, starResp.AllowedServers, scopeSharedServer,
		"the wildcard must still cover what the tenant may genuinely reach")

	// (4) End state: no token minted AFTER the reload carries the name. The
	// pre-reload control token does, which is the honest limit of this fix —
	// a token is a capability frozen at mint time — and is what issue #1179's
	// admin revocation surface exists to clean up.
	all, err := rig.store.ListAgentTokens()
	require.NoError(t, err)
	require.NotEmpty(t, all, "the control tokens must be in storage, or this sweep proves nothing")
	for _, tk := range all {
		if tk.Name == "before-reload" {
			continue
		}
		assert.NotContains(t, tk.AllowedServers, lateAdminServer,
			"token %q was minted after the reload and must not carry the admin's name", tk.Name)
	}
	after, err := rig.store.GetAgentTokenByOwnerAndName(tokenUserA, "after-reload")
	require.NoError(t, err)
	assert.Nil(t, after, "the refused mint must not have persisted a token")
}

// TestCreateServer_ReadsTheLiveAdminConfiguration is the same staleness on the
// create door: a name the admin takes after boot must stop being available.
//
// Oracle discipline: user B creates the name successfully BEFORE the reload
// (positive control on the same router, and proof the name was free), then the
// same request from a tenant who does not hold it is refused after.
//
// BITES: with a boot-time snapshot the post-reload create returns 201.
func TestCreateServer_ReadsTheLiveAdminConfiguration(t *testing.T) {
	rig := newTokenTestRigWithServers(t, scopeFixtureServers())

	rig.actAs(userBCtx())
	control := rig.createServer(t, lateAdminServer)
	require.Equal(t, http.StatusCreated, control.Code,
		"positive control: the name must be free before the reload (%s)", control.Body.String())

	rig.addAdminServer(&config.ServerConfig{
		Name: lateAdminServer, URL: "https://late.example", Protocol: "http", Enabled: true,
	})

	rig.actAs(userACtx())
	probe := rig.createServer(t, lateAdminServer)
	require.Equal(t, http.StatusConflict, probe.Code,
		"a name the admin has since taken must no longer be available (%s)", probe.Body.String())

	stored, err := rig.users.GetUserServer(tokenUserA, lateAdminServer)
	require.NoError(t, err)
	assert.Nil(t, stored, "the refused personal server must not have been persisted")
}

// TestCreateServer_UnavailableNameHasOneAnswer closes the existence oracle in a
// new shape.
//
// createServer refused with two different bodies: "Server name %q is not
// available" when the name was in the ADMIN configuration, and "Server %q
// already exists" when the caller already owned it. A tenant could therefore
// ask about any name and read, from the wording alone, whether the admin holds
// it — enumerating a private inventory they are not allowed to list, one
// request at a time. Both refusals now answer identically.
//
// Oracle discipline: a positive control mints a free name first (proving the
// route works and that a 409 is a refusal rather than a broken door), the three
// refusals are compared to each other with only the caller's own input
// normalised away, and every status is pinned to exactly 409.
//
// What this test does NOT claim: that the refusal leaks nothing at all. A
// tenant who knows their own inventory can still subtract it. Closing that last
// bit requires enforcement to resolve a server name against an OWNER instead of
// as a bare string, which is a change well outside this door.
//
// BITES: restore `fmt.Sprintf("Server %q already exists", req.Name)` on the
// own-duplicate branch and the own-versus-admin comparison fails.
func TestCreateServer_UnavailableNameHasOneAnswer(t *testing.T) {
	rig := newTokenTestRigWithServers(t, scopeFixtureServers())
	rig.actAs(userACtx())

	const mine = "a-server-of-my-own"
	ctrl := rig.createServer(t, mine)
	require.Equal(t, http.StatusCreated, ctrl.Code,
		"positive control: a free name must be creatable (%s)", ctrl.Body.String())

	own := rig.createServer(t, mine)
	private := rig.createServer(t, scopePrivateServer)
	shared := rig.createServer(t, scopeSharedServer)

	for _, probe := range []struct {
		label string
		w     *httptest.ResponseRecorder
	}{
		{"own duplicate", own},
		{"admin private", private},
		{"admin shared", shared},
	} {
		require.Equal(t, http.StatusConflict, probe.w.Code,
			"%s: expected exactly 409 (%s)", probe.label, probe.w.Body.String())
	}

	ownBody := normaliseCollisionBody(t, own, mine)
	privateBody := normaliseCollisionBody(t, private, scopePrivateServer)
	sharedBody := normaliseCollisionBody(t, shared, scopeSharedServer)

	assert.Equal(t, ownBody, privateBody,
		"a name the ADMIN holds privately must be indistinguishable from one the caller holds")
	assert.Equal(t, ownBody, sharedBody,
		"a name the admin shares must be indistinguishable from one the caller holds")

	// And the caller's own server is intact: the second 409 was a refusal, not
	// a clobber.
	survivor, err := rig.users.GetUserServer(tokenUserA, mine)
	require.NoError(t, err)
	require.NotNil(t, survivor, "the caller's own server must survive the duplicate refusal")
}
