package storage

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
)

// legacyNameIndexEntry reads the raw legacy owner-blind name->hash index so a
// test can assert what the personal edition sees on disk.
func legacyNameIndexEntry(t *testing.T, m *Manager, name string) string {
	t.Helper()
	var out string
	err := m.db.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(AgentTokenNamesBucket))
		if b == nil {
			return nil
		}
		if v := b.Get([]byte(name)); v != nil {
			out = string(v)
		}
		return nil
	})
	require.NoError(t, err)
	return out
}

// makeOwnedTestToken builds a token record with an explicit owner.
func makeOwnedTestToken(t *testing.T, name, userID string) (auth.AgentToken, string) {
	t.Helper()
	rawToken, err := auth.GenerateToken()
	require.NoError(t, err)
	return auth.AgentToken{
		Name:           name,
		UserID:         userID,
		AllowedServers: []string{"*"},
		Permissions:    []string{"read"},
	}, rawToken
}

// TestAgentToken_SameNameDifferentOwners: token names are a PER-OWNER
// namespace, so two tenants can each hold a token called "ci".
//
// BITE VERIFICATION NOTE: this test calls the new owner-scoped methods, so
// simply reverting the whole change makes the package fail to BUILD, which
// proves nothing. To see it bite, keep the new methods and make the duplicate
// check in CreateAgentToken owner-blind again (resolve with
// findAgentTokenHashLocked ignoring token.UserID); the second create then
// fails with ErrAgentTokenNameExists.
func TestAgentToken_SameNameDifferentOwners(t *testing.T) {
	manager, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	tokenA, rawA := makeOwnedTestToken(t, "ci", "userA")
	tokenB, rawB := makeOwnedTestToken(t, "ci", "userB")

	require.NoError(t, manager.CreateAgentToken(tokenA, rawA, testHMACKey))
	require.NoError(t, manager.CreateAgentToken(tokenB, rawB, testHMACKey),
		"a second tenant must be able to name their token %q", "ci")

	gotA, err := manager.GetAgentTokenByOwnerAndName("userA", "ci")
	require.NoError(t, err)
	require.NotNil(t, gotA)
	gotB, err := manager.GetAgentTokenByOwnerAndName("userB", "ci")
	require.NoError(t, err)
	require.NotNil(t, gotB)

	assert.NotEqual(t, gotA.TokenHash, gotB.TokenHash, "the two owners must resolve to different records")
	assert.Equal(t, "userA", gotA.UserID)
	assert.Equal(t, "userB", gotB.UserID)

	// A third tenant sees nothing, and so does the ownerless namespace the
	// personal edition uses.
	gotC, err := manager.GetAgentTokenByOwnerAndName("userC", "ci")
	require.NoError(t, err)
	assert.Nil(t, gotC)
	gotBare, err := manager.GetAgentTokenByName("ci")
	require.NoError(t, err)
	assert.Nil(t, gotBare, "a user-owned name must not be resolvable in the ownerless namespace")

	// Same owner, same name is still a conflict — and it is a typed one, so
	// handlers never have to echo a storage string.
	dupe, rawDupe := makeOwnedTestToken(t, "ci", "userA")
	err = manager.CreateAgentToken(dupe, rawDupe, testHMACKey)
	assert.ErrorIs(t, err, ErrAgentTokenNameExists)
}

// TestAgentToken_DeleteForOwnerLeavesOtherOwner guards the sharpest regression
// a per-owner namespace could cause: one tenant's delete nuking another
// tenant's live credential.
func TestAgentToken_DeleteForOwnerLeavesOtherOwner(t *testing.T) {
	manager, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	tokenA, rawA := makeOwnedTestToken(t, "ci", "userA")
	tokenB, rawB := makeOwnedTestToken(t, "ci", "userB")
	require.NoError(t, manager.CreateAgentToken(tokenA, rawA, testHMACKey))
	require.NoError(t, manager.CreateAgentToken(tokenB, rawB, testHMACKey))

	// Positive control: both raw tokens authenticate before the delete.
	_, err := manager.ValidateAgentToken(rawA, testHMACKey)
	require.NoError(t, err)
	_, err = manager.ValidateAgentToken(rawB, testHMACKey)
	require.NoError(t, err)

	require.NoError(t, manager.DeleteAgentTokenForOwner("userA", "ci"))

	// A's is gone...
	gotA, err := manager.GetAgentTokenByOwnerAndName("userA", "ci")
	require.NoError(t, err)
	assert.Nil(t, gotA)
	_, err = manager.ValidateAgentToken(rawA, testHMACKey)
	assert.Error(t, err)

	// ...B's is untouched and still authenticates.
	gotB, err := manager.GetAgentTokenByOwnerAndName("userB", "ci")
	require.NoError(t, err)
	require.NotNil(t, gotB)
	validatedB, err := manager.ValidateAgentToken(rawB, testHMACKey)
	require.NoError(t, err, "one tenant's delete revoked another tenant's live credential")
	assert.Equal(t, "userB", validatedB.UserID)
}

// TestAgentToken_RevokeAndRegenerateAreOwnerScoped: the mutators must refuse a
// name they do not own, and must not touch the other owner's record.
func TestAgentToken_RevokeAndRegenerateAreOwnerScoped(t *testing.T) {
	manager, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	tokenA, rawA := makeOwnedTestToken(t, "ci", "userA")
	tokenB, rawB := makeOwnedTestToken(t, "ci", "userB")
	require.NoError(t, manager.CreateAgentToken(tokenA, rawA, testHMACKey))
	require.NoError(t, manager.CreateAgentToken(tokenB, rawB, testHMACKey))

	// userC owns nothing: both mutators report the same "not found" as they
	// would for a name nobody has.
	assert.ErrorIs(t, manager.RevokeAgentTokenForOwner("userC", "ci"), ErrAgentTokenNotFound)
	assert.ErrorIs(t, manager.RevokeAgentTokenForOwner("userC", "nope"), ErrAgentTokenNotFound)
	_, err := manager.RegenerateAgentTokenForOwner("userC", "ci", "mcp_agt_whatever", testHMACKey)
	assert.ErrorIs(t, err, ErrAgentTokenNotFound)

	// Positive control: the owner CAN revoke, and only their own record moves.
	require.NoError(t, manager.RevokeAgentTokenForOwner("userA", "ci"))
	gotA, err := manager.GetAgentTokenByOwnerAndName("userA", "ci")
	require.NoError(t, err)
	require.NotNil(t, gotA)
	assert.True(t, gotA.Revoked)

	gotB, err := manager.GetAgentTokenByOwnerAndName("userB", "ci")
	require.NoError(t, err)
	require.NotNil(t, gotB)
	assert.False(t, gotB.Revoked, "revoking one tenant's token revoked another's")
	_, err = manager.ValidateAgentToken(rawB, testHMACKey)
	assert.NoError(t, err)

	// Regenerate is likewise confined to the owner's record.
	newRawB, err := auth.GenerateToken()
	require.NoError(t, err)
	updatedB, err := manager.RegenerateAgentTokenForOwner("userB", "ci", newRawB, testHMACKey)
	require.NoError(t, err)
	assert.Equal(t, "userB", updatedB.UserID)
	_, err = manager.ValidateAgentToken(newRawB, testHMACKey)
	assert.NoError(t, err)
	// A's record is still the revoked one, not B's regenerated hash.
	gotA, err = manager.GetAgentTokenByOwnerAndName("userA", "ci")
	require.NoError(t, err)
	require.NotNil(t, gotA)
	assert.True(t, gotA.Revoked)
	assert.NotEqual(t, updatedB.TokenHash, gotA.TokenHash)
}

// TestAgentToken_LastUsedByHashStampsOnlyThatRecord: the auth hot path stamps
// by hash, which is unique across owners, so it can never touch the other
// tenant's token.
func TestAgentToken_LastUsedByHashStampsOnlyThatRecord(t *testing.T) {
	manager, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	tokenA, rawA := makeOwnedTestToken(t, "ci", "userA")
	tokenB, rawB := makeOwnedTestToken(t, "ci", "userB")
	require.NoError(t, manager.CreateAgentToken(tokenA, rawA, testHMACKey))
	require.NoError(t, manager.CreateAgentToken(tokenB, rawB, testHMACKey))

	validatedB, err := manager.ValidateAgentToken(rawB, testHMACKey)
	require.NoError(t, err)
	require.NoError(t, manager.UpdateAgentTokenLastUsedByHash(validatedB.TokenHash))

	gotB, err := manager.GetAgentTokenByOwnerAndName("userB", "ci")
	require.NoError(t, err)
	require.NotNil(t, gotB)
	require.NotNil(t, gotB.LastUsedAt, "the owner's own token must be stamped")

	gotA, err := manager.GetAgentTokenByOwnerAndName("userA", "ci")
	require.NoError(t, err)
	require.NotNil(t, gotA)
	assert.Nil(t, gotA.LastUsedAt, "using one tenant's token stamped another tenant's record")
}

// TestAgentToken_PersonalEditionNamespaceUnchanged: ownerless tokens keep the
// exact behaviour the personal edition has always had, including the legacy
// bare-name index entry, so a rollback reads back what it wrote.
func TestAgentToken_PersonalEditionNamespaceUnchanged(t *testing.T) {
	manager, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	token, raw := makeTestToken("ci")
	require.NoError(t, manager.CreateAgentToken(token, raw, testHMACKey))

	got, err := manager.GetAgentTokenByName("ci")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Empty(t, got.UserID)

	// Global uniqueness still holds within the ownerless namespace.
	dupe, rawDupe := makeTestToken("ci")
	assert.ErrorIs(t, manager.CreateAgentToken(dupe, rawDupe, testHMACKey), ErrAgentTokenNameExists)

	// The legacy index entry is still written for ownerless tokens.
	assert.Equal(t, got.TokenHash, legacyNameIndexEntry(t, manager, "ci"),
		"the personal edition's legacy name index must be unchanged")

	// ...and it is NOT written for owned tokens.
	owned, rawOwned := makeOwnedTestToken(t, "owned-only", "userA")
	require.NoError(t, manager.CreateAgentToken(owned, rawOwned, testHMACKey))
	assert.Empty(t, legacyNameIndexEntry(t, manager, "owned-only"),
		"a user-owned name must not claim the global index slot")

	require.NoError(t, manager.DeleteAgentToken("ci"))
	assert.Empty(t, legacyNameIndexEntry(t, manager, "ci"),
		"deleting an ownerless token must free its legacy index entry")
}
