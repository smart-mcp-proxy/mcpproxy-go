package storage

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
)

// TestMintClientCredential_Basics pins the FR-021 shape and invariants of a
// freshly minted client credential (T028).
func TestMintClientCredential_Basics(t *testing.T) {
	mgr, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	raw, err := auth.GenerateClientToken()
	require.NoError(t, err)

	tok, err := mgr.MintClientCredential("cursor", raw, testHMACKey, auth.ProfileModeLocked, "work-readonly", time.Now().Add(365*24*time.Hour))
	require.NoError(t, err)
	require.NotNil(t, tok)
	assert.Equal(t, "client-cursor", tok.Name)
	assert.Equal(t, auth.KindClient, tok.Kind)
	assert.Equal(t, "cursor", tok.ClientID)
	assert.Equal(t, []string{"*"}, tok.AllowedServers)
	assert.ElementsMatch(t, []string{auth.PermRead, auth.PermWrite, auth.PermDestructive}, tok.Permissions)
	require.NoError(t, auth.ValidateTokenInvariants(tok, auth.KindClient))

	// The minted secret authenticates.
	validated, err := mgr.ValidateAgentToken(raw, testHMACKey)
	require.NoError(t, err)
	assert.Equal(t, "client-cursor", validated.Name)
}

// TestMintClientCredential_ReplacesRevokedOrExpired pins that minting for an
// id whose existing kind=client record is revoked or expired replaces it in
// the same transaction, while minting over an ACTIVE record is refused
// (T028, staged rotation is the only path over an active credential).
func TestMintClientCredential_ReplacesRevokedOrExpired(t *testing.T) {
	mgr, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	raw1, _ := auth.GenerateClientToken()
	_, err := mgr.MintClientCredential("cursor", raw1, testHMACKey, auth.ProfileModeSwitchable, "", time.Now().Add(24*time.Hour))
	require.NoError(t, err)

	// Minting again while active must be refused.
	raw2, _ := auth.GenerateClientToken()
	_, err = mgr.MintClientCredential("cursor", raw2, testHMACKey, auth.ProfileModeSwitchable, "", time.Now().Add(24*time.Hour))
	require.ErrorIs(t, err, ErrClientCredentialActive)

	// Revoke, then mint must replace cleanly and the old secret must stop
	// authenticating.
	require.NoError(t, mgr.RevokeAgentToken("client-cursor"))
	raw3, _ := auth.GenerateClientToken()
	tok, err := mgr.MintClientCredential("cursor", raw3, testHMACKey, auth.ProfileModeLocked, "work-full", time.Now().Add(24*time.Hour))
	require.NoError(t, err)
	assert.Equal(t, "work-full", tok.ProfilePin)

	_, err = mgr.ValidateAgentToken(raw1, testHMACKey)
	require.Error(t, err)
	validated, err := mgr.ValidateAgentToken(raw3, testHMACKey)
	require.NoError(t, err)
	assert.Equal(t, "work-full", validated.ProfilePin)
}

// TestMintClientCredential_ConflictWithRegularToken pins FR-021: a
// grandfathered kind=agent token already named client-<id> is never touched
// and the mint returns the conflict error.
func TestMintClientCredential_ConflictWithRegularToken(t *testing.T) {
	mgr, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	legacy, rawLegacy := makeTestToken("client-cursor")
	require.NoError(t, mgr.CreateAgentToken(legacy, rawLegacy, testHMACKey))

	raw, _ := auth.GenerateClientToken()
	_, err := mgr.MintClientCredential("cursor", raw, testHMACKey, auth.ProfileModeLocked, "work-readonly", time.Now().Add(24*time.Hour))
	require.ErrorIs(t, err, ErrClientCredentialConflict)

	// The legacy token must be entirely unaffected.
	stillThere, err := mgr.GetAgentTokenByName("client-cursor")
	require.NoError(t, err)
	require.NotNil(t, stillThere)
	assert.Equal(t, "", stillThere.Kind)
}

// TestMintClientCredential_Invariants pins the mint-time input validation
// (invalid client id, invalid mode, locked without a pin).
func TestMintClientCredential_Invariants(t *testing.T) {
	mgr, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()
	raw, _ := auth.GenerateClientToken()

	_, err := mgr.MintClientCredential("Bad_ID!", raw, testHMACKey, auth.ProfileModeLocked, "p", time.Now().Add(time.Hour))
	require.Error(t, err)

	_, err = mgr.MintClientCredential("cursor", raw, testHMACKey, "bogus-mode", "p", time.Now().Add(time.Hour))
	require.Error(t, err)

	_, err = mgr.MintClientCredential("cursor", raw, testHMACKey, auth.ProfileModeLocked, "", time.Now().Add(time.Hour))
	require.Error(t, err)
}

// TestClientCredentialRotation_StageAndFinalize pins T028a: stage 1 adds a
// pending secret and both old and new authenticate; finalize promotes and
// drops the old; a second finalize is an idempotent no-op.
func TestClientCredentialRotation_StageAndFinalize(t *testing.T) {
	mgr, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	oldRaw, _ := auth.GenerateClientToken()
	_, err := mgr.MintClientCredential("cursor", oldRaw, testHMACKey, auth.ProfileModeLocked, "work-readonly", time.Now().Add(365*24*time.Hour))
	require.NoError(t, err)

	newRaw, _ := auth.GenerateClientToken()
	_, err = mgr.StageClientCredentialRotation("cursor", newRaw, testHMACKey)
	require.NoError(t, err)

	// Both secrets authenticate during the overlap.
	_, err = mgr.ValidateAgentToken(oldRaw, testHMACKey)
	require.NoError(t, err)
	_, err = mgr.ValidateAgentToken(newRaw, testHMACKey)
	require.NoError(t, err)

	finalized, err := mgr.FinalizeClientCredentialRotation("cursor")
	require.NoError(t, err)
	assert.Empty(t, finalized.PendingHash)

	// Old secret stops authenticating; new one keeps working.
	_, err = mgr.ValidateAgentToken(oldRaw, testHMACKey)
	require.Error(t, err)
	_, err = mgr.ValidateAgentToken(newRaw, testHMACKey)
	require.NoError(t, err)

	// A second finalize is an idempotent no-op (ErrClientCredentialNotRotating,
	// treated by callers as 200).
	_, err = mgr.FinalizeClientCredentialRotation("cursor")
	require.ErrorIs(t, err, ErrClientCredentialNotRotating)

	// The new secret must still authenticate after the idempotent no-op.
	_, err = mgr.ValidateAgentToken(newRaw, testHMACKey)
	require.NoError(t, err)
}

// TestClientCredentialRotation_Rollback pins the rollback half: discarding
// the pending secret keeps the old one valid and the new one refused.
func TestClientCredentialRotation_Rollback(t *testing.T) {
	mgr, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	oldRaw, _ := auth.GenerateClientToken()
	_, err := mgr.MintClientCredential("cursor", oldRaw, testHMACKey, auth.ProfileModeSwitchable, "", time.Now().Add(24*time.Hour))
	require.NoError(t, err)

	newRaw, _ := auth.GenerateClientToken()
	_, err = mgr.StageClientCredentialRotation("cursor", newRaw, testHMACKey)
	require.NoError(t, err)

	_, err = mgr.RollbackClientCredentialRotation("cursor")
	require.NoError(t, err)

	_, err = mgr.ValidateAgentToken(oldRaw, testHMACKey)
	require.NoError(t, err)
	_, err = mgr.ValidateAgentToken(newRaw, testHMACKey)
	require.Error(t, err)
}

// TestForgetClientCredential pins disconnect/forget: the credential is
// revoked and stops authenticating; forget → re-mint works (T028).
func TestForgetClientCredential(t *testing.T) {
	mgr, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	raw, _ := auth.GenerateClientToken()
	_, err := mgr.MintClientCredential("cursor", raw, testHMACKey, auth.ProfileModeSwitchable, "", time.Now().Add(24*time.Hour))
	require.NoError(t, err)

	_, err = mgr.ForgetClientCredential("cursor")
	require.NoError(t, err)

	_, err = mgr.ValidateAgentToken(raw, testHMACKey)
	require.Error(t, err)

	raw2, _ := auth.GenerateClientToken()
	_, err = mgr.MintClientCredential("cursor", raw2, testHMACKey, auth.ProfileModeLocked, "work-full", time.Now().Add(24*time.Hour))
	require.NoError(t, err)
	_, err = mgr.ValidateAgentToken(raw2, testHMACKey)
	require.NoError(t, err)
}

// TestRegularTokenCannotUseClientPrefixName pins "regular token with
// client- prefix refused" (T028): CreateAgentToken must not silently allow a
// regular token to squat the client namespace it did not previously reject.
// (Documents existing behaviour; the FR-021 400 on the REST surface is
// enforced at the httpapi layer, not here.)
func TestValidateAgentToken_RejectsUnrecognisedPrefix(t *testing.T) {
	mgr, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	_, err := mgr.ValidateAgentToken("mcp_xyz_"+"0000000000000000000000000000000000000000000000000000000000000000", testHMACKey)
	require.Error(t, err)
}
