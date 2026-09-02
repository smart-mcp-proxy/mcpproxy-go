//go:build server

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createServer posts a personal-server create under the rig's current identity.
func (rig *tokenTestRig) createServer(t *testing.T, name string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(CreateServerRequest{
		Name:     name,
		URL:      "http://127.0.0.1:9/" + name,
		Protocol: "http",
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/user/servers", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	rig.router.ServeHTTP(w, req)
	return w
}

// TestCreateUserToken_PersonalServerCannotShadowAdminPrivateServer closes the
// bypass in the allowed_servers entitlement check.
//
// The check asks "is this name in the caller's entitled set?", and that set is
// "my personal servers, plus the admin servers flagged Shared". createServer only
// refused a name colliding with a SHARED server, so a tenant could create a
// personal server named exactly like one of the admin's PRIVATE upstreams; the
// name then entered their entitled set on the strength of the personal server,
// and the token they minted named the admin's server as far as
// auth.AuthContext.CanAccessServer is concerned — it compares bare strings and
// has no notion of an owner.
//
// Oracle discipline:
//
//  1. a POSITIVE CONTROL first, on the SAME router: the tenant creates an
//     ordinary personal server and mints a token scoped to it (201/201), so a
//     later 409/400 cannot be a broken route, an unwired store or a bad body;
//  2. an independent proof through the production per-user door that the admin's
//     private server is INVISIBLE to this tenant — the property the escalation
//     would have defeated;
//  3. statuses are PINNED (201, 409, 400), never asserted as bare parity;
//  4. the escalation is checked at its END state — what allowed_servers any
//     stored token actually carries — not merely at the refusal.
//
// BITES: on unfixed code the shadowing personal server is CREATED (201) and the
// token naming the admin's private server is MINTED (201) carrying that name.
func TestCreateUserToken_PersonalServerCannotShadowAdminPrivateServer(t *testing.T) {
	rig := newTokenTestRigWithServers(t, scopeFixtureServers())
	rig.actAs(userACtx())

	// (1) Positive control: an ordinary personal server, and a token over it.
	ctrl := rig.createServer(t, scopeAPersonal)
	require.Equal(t, http.StatusCreated, ctrl.Code,
		"positive control: creating an uncontested personal server must succeed (%s)", ctrl.Body.String())

	ctrlTok := rig.createToken(t, "ctrl-token", []string{scopeAPersonal})
	require.Equal(t, http.StatusCreated, ctrlTok.Code,
		"positive control: minting a token over an entitled server must succeed (%s)", ctrlTok.Body.String())

	// (2) The admin's private server is invisible through the production door.
	listed := rig.call(t, http.MethodGet, "/api/v1/user/servers")
	require.Equal(t, http.StatusOK, listed.Code)
	var doorway ServerListResponse
	require.NoError(t, json.Unmarshal(listed.Body.Bytes(), &doorway))
	for _, s := range doorway.Personal {
		require.NotEqual(t, scopePrivateServer, s.Name, "the admin's private server must not be listed as personal")
	}
	for _, s := range doorway.Shared {
		require.NotEqual(t, scopePrivateServer, s.Name, "the admin's private server must not be listed as shared")
	}

	// (3) The collision itself is refused, and really did not land.
	collide := rig.createServer(t, scopePrivateServer)
	require.Equal(t, http.StatusConflict, collide.Code,
		"a personal server may not take an admin-configured name (%s)", collide.Body.String())

	stored, err := rig.users.GetUserServer(tokenUserA, scopePrivateServer)
	require.NoError(t, err)
	assert.Nil(t, stored, "the refused personal server must not have been persisted")

	// (4) And the escalation's end state is unreachable: no token may name it.
	tok := rig.createToken(t, "escalation", []string{scopePrivateServer})
	require.Equal(t, http.StatusBadRequest, tok.Code,
		"a token must not be mintable over the admin's private server (%s)", tok.Body.String())

	all, err := rig.store.ListAgentTokens()
	require.NoError(t, err)
	require.NotEmpty(t, all, "the positive control's token must be in storage, or this sweep proves nothing")
	for _, tk := range all {
		assert.NotContains(t, tk.AllowedServers, scopePrivateServer,
			"no stored token may carry the admin's private server in its scope (token %q)", tk.Name)
	}
}

// TestEntitledServerNames_ExcludesPreExistingCollision covers the collision
// createServer's refusal cannot reach: a personal row written BEFORE the refusal
// existed, or one whose name the admin only later added to the configuration.
// The entitled set must drop it on its own rather than trusting the create path.
//
// BITES: without the exclusion in entitledServerNames, both the explicit request
// and the materialised "*" carry the admin's private server.
func TestEntitledServerNames_ExcludesPreExistingCollision(t *testing.T) {
	rig := newTokenTestRigWithServers(t, scopeFixtureServers())
	rig.actAs(userACtx())

	// Written straight through the user store, exactly as a pre-fix createServer
	// would have left it — the HTTP route now refuses this name.
	rig.seedPersonalServer(t, tokenUserA, scopePrivateServer)

	// Positive control on the same rig: an uncontested personal server still
	// mints, so a 400 below is the collision and not a broken fixture.
	rig.seedPersonalServer(t, tokenUserA, scopeAPersonal)
	ctrl := rig.createToken(t, "ctrl-token", []string{scopeAPersonal})
	require.Equal(t, http.StatusCreated, ctrl.Code,
		"positive control: an uncontested personal server must remain scopable (%s)", ctrl.Body.String())

	tok := rig.createToken(t, "legacy-collision", []string{scopePrivateServer})
	require.Equal(t, http.StatusBadRequest, tok.Code,
		"a pre-existing shadowing personal server must not entitle the admin's name (%s)", tok.Body.String())

	// "*" must not smuggle it in through the materialised set either.
	star := rig.createToken(t, "legacy-star", []string{"*"})
	require.Equal(t, http.StatusCreated, star.Code, "the star must still resolve (%s)", star.Body.String())
	var resp AgentTokenResponse
	require.NoError(t, json.Unmarshal(star.Body.Bytes(), &resp))
	assert.NotContains(t, resp.AllowedServers, scopePrivateServer,
		`materialising "*" must not include an admin-config collision`)
	assert.Contains(t, resp.AllowedServers, scopeAPersonal,
		"the star must still cover genuinely owned servers")
}

// TestCreateServer_CollisionMessageIsUniform pins the anti-oracle property of
// the refusal: the body a tenant gets for colliding with the admin's PRIVATE
// server must be identical to the one for a server they can already see, up to
// the name they themselves sent. A message that distinguished them would let a
// tenant enumerate the admin's private inventory one name at a time — the same
// defect class this branch closed on token names.
//
// BITES: on unfixed code the private-name request returns 201, so there is no
// second body to compare and the require below fails on the status.
func TestCreateServer_CollisionMessageIsUniform(t *testing.T) {
	rig := newTokenTestRigWithServers(t, scopeFixtureServers())
	rig.actAs(userACtx())

	sharedCollision := rig.createServer(t, scopeSharedServer)
	privateCollision := rig.createServer(t, scopePrivateServer)

	require.Equal(t, http.StatusConflict, sharedCollision.Code,
		"colliding with a shared server must be a conflict (%s)", sharedCollision.Body.String())
	require.Equal(t, http.StatusConflict, privateCollision.Code,
		"colliding with an admin-private server must be the SAME conflict (%s)", privateCollision.Body.String())

	// Same body, once the caller's own input is normalised away. Echoing back
	// what the caller sent is not a disclosure; anything else that differs is.
	assert.Equal(t,
		normaliseCollisionBody(t, sharedCollision, scopeSharedServer),
		normaliseCollisionBody(t, privateCollision, scopePrivateServer),
		"the refusal must not reveal whether the colliding server is shared or private")

	// And the private refusal leaks nothing about the server behind the name.
	assert.NotContains(t, privateCollision.Body.String(), "shared",
		"the private collision must not be described as a shared-server conflict")
	assert.NotContains(t, privateCollision.Body.String(), "private.example",
		"the refusal must not echo the admin server's configuration")
}

// normaliseCollisionBody replaces the caller's own server name with a fixed
// placeholder so two refusals can be compared for everything EXCEPT that name.
func normaliseCollisionBody(t *testing.T, w *httptest.ResponseRecorder, name string) string {
	t.Helper()
	var envelope map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope),
		"body did not decode as the handler's error envelope (%q)", w.Body.String())
	msg, _ := envelope["message"].(string)
	require.NotEmpty(t, msg, "refusal must carry a message")
	envelope["message"] = strings.ReplaceAll(msg, name, "<NAME>")
	out, err := json.Marshal(envelope)
	require.NoError(t, err)
	return string(out)
}
