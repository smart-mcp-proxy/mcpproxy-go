//go:build server

package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRegenerateUserToken_RevokedTokenIsRefused is finding N4 on the HTTP
// surface.
//
// Disabling a user revokes every token they minted, and `enableUser` documents
// that re-enabling must not resurrect them. Regenerate cleared `Revoked`, so the
// owner could name a burned token and get a working secret back for that very
// record — undoing an admin's remediation from a tenant route.
//
// Oracle discipline: the SAME route, on the SAME token, succeeds first; only the
// revocation happens in between. And the refusal is checked to be a real one —
// the response carries no usable secret, and the record stays revoked.
//
// BITES: restore `token.Revoked = false` (and drop the ErrAgentTokenRevoked
// guard) in RegenerateAgentTokenForOwner; the second call returns 200 with a
// fresh secret.
func TestRegenerateUserToken_RevokedTokenIsRefused(t *testing.T) {
	rig := newTokenTestRig(t)
	rig.actAs(userACtx())
	rig.seedToken(t, tokenUserA, "ci")

	control := rig.call(t, http.MethodPost, "/api/v1/user/tokens/ci/regenerate")
	require.Equal(t, http.StatusOK, control.Code,
		"positive control: a live token must be rotatable (%s)", control.Body.String())

	// An admin disables the owner, which burns their tokens.
	burned, err := rig.store.RevokeAgentTokensForOwner(tokenUserA)
	require.NoError(t, err)
	require.Equal(t, 1, burned, "fixture: the token must actually have been revoked")

	refused := rig.call(t, http.MethodPost, "/api/v1/user/tokens/ci/regenerate")
	assert.Equal(t, http.StatusConflict, refused.Code,
		"rotating a revoked token must be refused, not silently un-revoke it (%s)", refused.Body.String())

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(refused.Body.Bytes(), &body))
	assert.NotContains(t, body, "token", "a refusal must not hand back a usable secret")
	// The storage sentinel's own text must not be echoed into the response,
	// which is the leak writeTokenMutationError exists to prevent.
	assert.NotContains(t, refused.Body.String(), "agent token is revoked")

	stored, err := rig.store.GetAgentTokenByOwnerAndName(tokenUserA, "ci")
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.True(t, stored.Revoked, "the record must still be revoked after the refusal")
}

// TestRegenerateUserToken_AnotherTenantsRevokedTokenStays404 keeps the #1168
// indistinguishability intact: the new 409 must be reachable only by the
// record's own owner, or it becomes an oracle for another tenant's token names.
func TestRegenerateUserToken_AnotherTenantsRevokedTokenStays404(t *testing.T) {
	rig := newTokenTestRig(t)
	rig.actAs(userACtx())
	rig.seedToken(t, tokenUserA, "ci")
	require.NoError(t, rig.store.RevokeAgentTokenForOwner(tokenUserA, "ci"))

	rig.actAs(userBCtx())
	probe := rig.call(t, http.MethodPost, "/api/v1/user/tokens/ci/regenerate")
	require.Equal(t, http.StatusNotFound, probe.Code,
		"another tenant must not learn the name exists, let alone that it is revoked (%s)", probe.Body.String())

	// Byte-parity with a name that exists nowhere at all: the two must be
	// indistinguishable, status AND body.
	absent := rig.call(t, http.MethodPost, "/api/v1/user/tokens/ci-absent/regenerate")
	require.Equal(t, http.StatusNotFound, absent.Code)
	assert.Equal(t,
		bytesReplaceTokenName(absent.Body.String(), "ci-absent", "ci"),
		probe.Body.String(),
		"a revoked token owned by someone else must be byte-identical to an absent one")
}

// bytesReplaceTokenName normalises the token name a 404 body echoes back, so the
// parity assertion above compares everything EXCEPT the caller's own path
// parameter (which a 404 legitimately quotes back).
func bytesReplaceTokenName(body, from, to string) string {
	out := make([]byte, 0, len(body))
	for i := 0; i < len(body); {
		if i+len(from) <= len(body) && body[i:i+len(from)] == from {
			out = append(out, to...)
			i += len(from)
			continue
		}
		out = append(out, body[i])
		i++
	}
	return string(out)
}
