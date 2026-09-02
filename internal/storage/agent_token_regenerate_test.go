package storage

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
)

// TestAgentToken_RegenerateDoesNotResurrectARevokedToken is finding N4.
//
// Disabling a user REVOKES every agent token they minted, and enableUser
// documents why: "re-enabling an account cannot resurrect a credential that may
// be why the account was disabled". RegenerateAgentTokenForOwner set
// `token.Revoked = false`, so the owner could walk straight back through that
// guarantee — name the burned token, get a fresh working secret for the very
// record the admin burned. Rotation refreshes a live credential's secret; it is
// not an un-revoke, and the two operations sit on opposite sides of a privilege
// boundary (an admin revokes, the tenant rotates).
//
// Oracle discipline: the SAME rotation succeeds first, on the same token,
// before it is revoked — so the later refusal is the Revoked flag and not a
// broken rotation path.
//
// BITES: restore `token.Revoked = false` (and drop the ErrAgentTokenRevoked
// guard) in RegenerateAgentTokenForOwner; the rotation succeeds and the token
// authenticates again.
func TestAgentToken_RegenerateDoesNotResurrectARevokedToken(t *testing.T) {
	manager, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	token, raw := makeOwnedTestToken(t, "ci", "userA")
	require.NoError(t, manager.CreateAgentToken(token, raw, testHMACKey))

	// Positive control: rotation works on a LIVE token.
	firstRaw, err := auth.GenerateToken()
	require.NoError(t, err)
	rotated, err := manager.RegenerateAgentTokenForOwner("userA", "ci", firstRaw, testHMACKey, nil)
	require.NoError(t, err, "positive control: a live token must be rotatable")
	require.NotNil(t, rotated)
	assert.False(t, rotated.Revoked)

	// An admin disables the owner, which burns their tokens.
	burned, err := manager.RevokeAgentTokensForOwner("userA")
	require.NoError(t, err)
	require.Equal(t, 1, burned, "fixture: the token must actually have been revoked")

	secondRaw, err := auth.GenerateToken()
	require.NoError(t, err)
	resurrected, err := manager.RegenerateAgentTokenForOwner("userA", "ci", secondRaw, testHMACKey, nil)
	require.Error(t, err, "a revoked token must not be rotatable")
	assert.ErrorIs(t, err, ErrAgentTokenRevoked)
	assert.Nil(t, resurrected)

	// The refusal must be a real refusal, not a cosmetic one: the new secret
	// must not authenticate, and the record must still be revoked.
	got, err := manager.ValidateAgentToken(secondRaw, testHMACKey)
	assert.Nil(t, got, "the would-be rotated secret must not authenticate")
	assert.Error(t, err)

	stored, err := manager.GetAgentTokenByOwnerAndName("userA", "ci")
	require.NoError(t, err)
	require.NotNil(t, stored, "revoke is a soft delete: the record stays visible to the operator")
	assert.True(t, stored.Revoked, "the record must still be revoked")
	assert.Equal(t, auth.HashToken(firstRaw, testHMACKey), stored.TokenHash,
		"a refused rotation must not have rotated the hash either")
}

// TestAgentToken_RegenerateRefusalIsOwnerScoped keeps the #1168 fix intact: the
// revoked branch must be reachable ONLY by the record's own owner, so it can
// never become an oracle for another tenant's token names. A different tenant
// asking about the same name gets the ordinary not-found.
func TestAgentToken_RegenerateRefusalIsOwnerScoped(t *testing.T) {
	manager, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	token, raw := makeOwnedTestToken(t, "ci", "userA")
	require.NoError(t, manager.CreateAgentToken(token, raw, testHMACKey))
	require.NoError(t, manager.RevokeAgentTokenForOwner("userA", "ci"))

	newRaw, err := auth.GenerateToken()
	require.NoError(t, err)
	_, err = manager.RegenerateAgentTokenForOwner("userB", "ci", newRaw, testHMACKey, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAgentTokenNotFound,
		"another tenant must not learn that the name exists, let alone that it is revoked")
	assert.NotErrorIs(t, err, ErrAgentTokenRevoked)
}

// TestAgentToken_RegenerateDoesNotStompForeignLegacyNameEntry is finding N5.
//
// The repoint rule was `userID == "" || indexed == oldHash`. The first disjunct
// claimed the legacy owner-blind slot for ANY ownerless rotation, regardless of
// what the entry held — including the pre-upgrade server-edition entry the
// AgentTokenNamesBucket comment promises to preserve so a rollback is not
// stranded. That is the same stomp CreateAgentToken was fixed for on this
// branch, left behind on the third maintenance path, and it contradicts the
// branch's own no-migration rollback rationale.
//
// BITES: restore the `userID == "" ||` disjunct and the index below points at
// the rotated ownerless token instead of the pre-upgrade tenant's hash.
func TestAgentToken_RegenerateDoesNotStompForeignLegacyNameEntry(t *testing.T) {
	manager, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	// Pre-upgrade deployment: a tenant's owned token plus the owner-blind index
	// entry the OLD code wrote for it.
	tenantToken, tenantRaw := makeOwnedTestToken(t, "ci", "userA")
	require.NoError(t, manager.CreateAgentToken(tenantToken, tenantRaw, testHMACKey))
	tenantHash := auth.HashToken(tenantRaw, testHMACKey)
	require.NoError(t, manager.db.db.Update(func(tx *bbolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(AgentTokenNamesBucket))
		if err != nil {
			return err
		}
		return b.Put([]byte("ci"), []byte(tenantHash))
	}))

	// An ownerless (personal-edition) token of the same name. Create already
	// declines to claim the occupied slot, which is the fixture this test needs.
	ownerless, ownerlessRaw := makeOwnedTestToken(t, "ci", "")
	require.NoError(t, manager.CreateAgentToken(ownerless, ownerlessRaw, testHMACKey))
	require.Equal(t, tenantHash, legacyNameIndexEntry(t, manager, "ci"),
		"fixture: the pre-upgrade entry must still hold the slot before the rotation under test")

	newRaw, err := auth.GenerateToken()
	require.NoError(t, err)
	rotated, err := manager.RegenerateAgentToken("ci", newRaw, testHMACKey)
	require.NoError(t, err, "the ownerless token must still be rotatable")
	require.NotNil(t, rotated)

	assert.Equal(t, tenantHash, legacyNameIndexEntry(t, manager, "ci"),
		"the pre-upgrade tenant's index entry must survive a rotation it has nothing to do with")

	// Declining the index costs the rotated token nothing: resolution is by scan.
	resolved, err := manager.GetAgentTokenByOwnerAndName("", "ci")
	require.NoError(t, err)
	require.NotNil(t, resolved)
	assert.Equal(t, auth.HashToken(newRaw, testHMACKey), resolved.TokenHash)
	authed, err := manager.ValidateAgentToken(newRaw, testHMACKey)
	require.NoError(t, err, "the rotated secret must authenticate")
	require.NotNil(t, authed)
}

// TestAgentToken_RegenerateKeepsItsOwnLegacyNameEntryInStep is the other side
// of the rule: when the index DOES point at the record being rotated, it must
// follow the new hash — that is the whole reason the index is maintained. This
// keeps the N5 guard from being too strict and silently stranding the personal
// edition's own lookups.
func TestAgentToken_RegenerateKeepsItsOwnLegacyNameEntryInStep(t *testing.T) {
	manager, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	ownerless, raw := makeOwnedTestToken(t, "deploy-bot", "")
	require.NoError(t, manager.CreateAgentToken(ownerless, raw, testHMACKey))
	require.Equal(t, auth.HashToken(raw, testHMACKey), legacyNameIndexEntry(t, manager, "deploy-bot"),
		"fixture: an ownerless create claims the free slot")

	newRaw, err := auth.GenerateToken()
	require.NoError(t, err)
	_, err = manager.RegenerateAgentToken("deploy-bot", newRaw, testHMACKey)
	require.NoError(t, err)

	assert.Equal(t, auth.HashToken(newRaw, testHMACKey),
		legacyNameIndexEntry(t, manager, "deploy-bot"),
		"the index must follow the record it points at across a rotation")
}

// TestAgentToken_RegenerateKeepsAnOwnedTokensOwnLegacyEntryInStep covers the
// pre-per-owner-scoping case: an OWNED token whose name predates the scoping
// still holds the legacy slot, and rotating it must keep that entry live rather
// than leaving it dangling at a hash that no longer exists.
func TestAgentToken_RegenerateKeepsAnOwnedTokensOwnLegacyEntryInStep(t *testing.T) {
	manager, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	owned, raw := makeOwnedTestToken(t, "ci", "userA")
	require.NoError(t, manager.CreateAgentToken(owned, raw, testHMACKey))
	oldHash := auth.HashToken(raw, testHMACKey)
	require.NoError(t, manager.db.db.Update(func(tx *bbolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(AgentTokenNamesBucket))
		if err != nil {
			return err
		}
		return b.Put([]byte("ci"), []byte(oldHash))
	}))

	newRaw, err := auth.GenerateToken()
	require.NoError(t, err)
	_, err = manager.RegenerateAgentTokenForOwner("userA", "ci", newRaw, testHMACKey, nil)
	require.NoError(t, err)

	assert.Equal(t, auth.HashToken(newRaw, testHMACKey), legacyNameIndexEntry(t, manager, "ci"),
		"an entry that pointed at this very record must follow it")
}
