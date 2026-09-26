package storage

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
)

// createGrandfatheredRegularToken writes tok directly to the agent_tokens
// bucket, bypassing CreateAgentToken's FR-021 "client-" name guard — the
// only way to construct the pre-108 scenario the guard exists to prevent
// going forward (a REGULAR token that already holds a client-<id> name).
func createGrandfatheredRegularToken(t *testing.T, mgr *Manager, tok auth.AgentToken, rawToken string) {
	t.Helper()
	hash := auth.HashToken(rawToken, testHMACKey)
	tok.TokenHash = hash
	tok.TokenPrefix = auth.TokenPrefix(rawToken)
	if tok.CreatedAt.IsZero() {
		tok.CreatedAt = time.Now().UTC()
	}
	data, err := json.Marshal(tok)
	require.NoError(t, err)
	require.NoError(t, mgr.db.db.Update(func(tx *bbolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(AgentTokensBucket))
		if err != nil {
			return err
		}
		return b.Put([]byte(hash), data)
	}))
}

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
	createGrandfatheredRegularToken(t, mgr, legacy, rawLegacy)

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

// TestCreateAgentToken_RejectsClientPrefixForRegularToken pins FR-021:
// "regular tokens MUST NOT be created with the client- prefix" (T028) — a
// grandfathered pre-108 token that already holds such a name is untouched
// (MintClientCredential's own conflict check, tested above); this guards the
// NEW-create door.
func TestCreateAgentToken_RejectsClientPrefixForRegularToken(t *testing.T) {
	mgr, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	tok, raw := makeTestToken("client-cursor")
	err := mgr.CreateAgentToken(tok, raw, testHMACKey)
	require.Error(t, err)

	// A genuine kind=client record is unaffected by this guard (it goes
	// through MintClientCredential, not CreateAgentToken, but the guard
	// itself must not misfire on a Kind=client value).
	tok2, raw2 := makeTestToken("client-cursor")
	tok2.Kind = auth.KindClient
	tok2.ClientID = "cursor"
	tok2.ProfileMode = auth.ProfileModeSwitchable
	tok2.AllowedServers = []string{"*"}
	tok2.Permissions = []string{auth.PermRead, auth.PermWrite, auth.PermDestructive}
	require.NoError(t, mgr.CreateAgentToken(tok2, raw2, testHMACKey))
}

// TestValidateAgentToken_MalformedClientRecord pins the storage half of
// T027a: a hand-written malformed kind=client bbolt record is refused on
// every authentication with the exact FR-021 text, never a downgrade to a
// regular wildcard agent token.
func TestValidateAgentToken_MalformedClientRecord(t *testing.T) {
	mgr, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	raw, err := auth.GenerateClientToken()
	require.NoError(t, err)

	// kind=client without client_id.
	malformed := auth.AgentToken{
		Name:           "client-cursor",
		Kind:           auth.KindClient,
		ProfileMode:    auth.ProfileModeSwitchable,
		AllowedServers: []string{"*"},
		Permissions:    []string{auth.PermRead, auth.PermWrite, auth.PermDestructive},
	}
	hash := auth.HashToken(raw, testHMACKey)
	malformed.TokenHash = hash
	malformed.TokenPrefix = auth.TokenPrefix(raw)
	data, err := json.Marshal(malformed)
	require.NoError(t, err)
	require.NoError(t, mgr.db.db.Update(func(tx *bbolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(AgentTokensBucket))
		if err != nil {
			return err
		}
		return b.Put([]byte(hash), data)
	}))

	_, err = mgr.ValidateAgentToken(raw, testHMACKey)
	require.Error(t, err)
	assert.Equal(t, "malformed credential record", err.Error())
}

func TestValidateAgentToken_RejectsUnrecognisedPrefix(t *testing.T) {
	mgr, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	_, err := mgr.ValidateAgentToken("mcp_xyz_"+"0000000000000000000000000000000000000000000000000000000000000000", testHMACKey)
	require.Error(t, err)
}
