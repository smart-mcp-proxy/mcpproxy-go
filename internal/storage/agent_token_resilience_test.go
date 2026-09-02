package storage

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
)

// writeRawAgentTokenRecord plants an arbitrary byte string into the
// agent_tokens bucket under the given key, bypassing every validation the
// normal write path performs. This is how a truncated write, a hand-edited DB
// or a record from a future schema actually appears at rest.
func writeRawAgentTokenRecord(t *testing.T, m *Manager, key string, value []byte) {
	t.Helper()
	require.NoError(t, m.db.db.Update(func(tx *bbolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(AgentTokensBucket))
		if err != nil {
			return err
		}
		return b.Put([]byte(key), value)
	}))
}

// TestAgentToken_CorruptRecordDoesNotBreakOtherTenants pins the blast radius of
// one unparseable row.
//
// The owner-scoped lookup WALKS the whole agent_tokens bucket (a bare name is
// ambiguous once names are per-owner, so it cannot read a single indexed
// record). Returning an error on the first row that fails json.Unmarshal
// therefore turned every management operation — create, revoke, delete,
// regenerate, for EVERY tenant of the deployment — into a 500 as soon as one
// record went bad. The indexed read it replaced could not do that: it only ever
// touched the one record it was asked for.
//
// The contract now: skip the bad row, log it, keep going. One corrupt record
// degrades to "that token is unresolvable" and nothing more.
//
// Oracle discipline: a positive control runs FIRST on the same manager, so a
// later success cannot be an empty bucket or an unwired store; and every
// mutator is exercised, because the defect was in the shared resolver they all
// call, not in any one of them.
//
// BITES: restore the `return fmt.Errorf("failed to unmarshal agent token: %w",
// err)` inside findAgentTokenHashLocked's ForEach and every assertion below
// that touches an operation after the corrupt row fails with that error.
func TestAgentToken_CorruptRecordDoesNotBreakOtherTenants(t *testing.T) {
	manager, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	// Positive control BEFORE the corruption: the store works at all.
	healthy, rawHealthy := makeOwnedTestToken(t, "ci", "userA")
	require.NoError(t, manager.CreateAgentToken(healthy, rawHealthy, testHMACKey),
		"positive control: a plain create must succeed on a clean store")

	// Now plant an unparseable record. Keyed like a real hash so it sorts into
	// the middle of the bucket rather than being conveniently last.
	writeRawAgentTokenRecord(t, manager, "0000corrupt0000", []byte("{not json at all"))

	// Every by-name path must still work for every OTHER token.
	found, err := manager.GetAgentTokenByOwnerAndName("userA", "ci")
	require.NoError(t, err, "a corrupt row must not break a healthy token's lookup")
	require.NotNil(t, found)
	assert.Equal(t, "ci", found.Name)

	// Create for a different tenant.
	other, rawOther := makeOwnedTestToken(t, "ci", "userB")
	require.NoError(t, manager.CreateAgentToken(other, rawOther, testHMACKey),
		"a corrupt row must not stop another tenant creating a token")

	// Revoke, regenerate and delete on the healthy record.
	require.NoError(t, manager.RevokeAgentTokenForOwner("userA", "ci"),
		"a corrupt row must not break revoke")

	newRaw, err := auth.GenerateToken()
	require.NoError(t, err)
	regenerated, err := manager.RegenerateAgentTokenForOwner("userA", "ci", newRaw, testHMACKey, nil)
	require.NoError(t, err, "a corrupt row must not break regenerate")
	require.NotNil(t, regenerated)
	assert.False(t, regenerated.Revoked, "regenerate clears the revoked flag")

	require.NoError(t, manager.DeleteAgentTokenForOwner("userA", "ci"),
		"a corrupt row must not break delete")

	// And the corrupt row itself degrades to "unresolvable", not to an error.
	gone, err := manager.GetAgentTokenByOwnerAndName("userA", "ci")
	require.NoError(t, err)
	assert.Nil(t, gone, "the deleted token must now resolve to nothing, without an error")
}

// TestAgentToken_CreateDoesNotStompForeignLegacyNameEntry pins the rollback
// promise the AgentTokenNamesBucket comment makes.
//
// That comment says a pre-upgrade server-edition entry is deliberately left in
// place so a rollback is not stranded — but CreateAgentToken used to Put the
// name->hash mapping unconditionally, so creating an OWNERLESS token of the same
// name overwrote exactly the entry the comment promises to preserve. On the old
// code that entry is how a tenant's token resolves by name, so the overwrite
// repoints their name at somebody else's credential record.
//
// The slot is now CLAIMED: taken when free or dangling, left alone when a live
// record holds it.
//
// BITES: drop the claimAgentTokenNameSlot guard and the index entry below points
// at the new ownerless token's hash instead of the pre-upgrade tenant's.
func TestAgentToken_CreateDoesNotStompForeignLegacyNameEntry(t *testing.T) {
	manager, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	// A pre-upgrade server-edition deployment: a tenant's owned token, plus the
	// owner-blind index entry the OLD code wrote for it.
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
	require.Equal(t, tenantHash, legacyNameIndexEntry(t, manager, "ci"),
		"fixture: the pre-upgrade index entry must be in place before the create under test")

	// Now the personal edition creates an OWNERLESS token with the same name.
	ownerless, ownerlessRaw := makeOwnedTestToken(t, "ci", "")
	require.NoError(t, manager.CreateAgentToken(ownerless, ownerlessRaw, testHMACKey),
		"an ownerless token must still be creatable alongside a tenant's same-named one")

	assert.Equal(t, tenantHash, legacyNameIndexEntry(t, manager, "ci"),
		"the pre-upgrade tenant's index entry must survive: overwriting it is the rollback stranding the bucket comment promises not to cause")

	// The new token is unaffected by not holding the index: resolution is by scan.
	resolved, err := manager.GetAgentTokenByOwnerAndName("", "ci")
	require.NoError(t, err)
	require.NotNil(t, resolved, "the ownerless token must resolve without owning the legacy index slot")
	assert.Equal(t, auth.HashToken(ownerlessRaw, testHMACKey), resolved.TokenHash)
}

// TestAgentToken_CreateClaimsDanglingLegacyNameEntry is the other side of the
// claim rule: an entry pointing at a hash that is no longer in agent_tokens
// resolves to nothing on any code path, so no rollback depends on it and the
// personal edition should reclaim the slot. Without this the guard would be too
// strict — one stale byte would permanently deny the index to its rightful
// owner.
func TestAgentToken_CreateClaimsDanglingLegacyNameEntry(t *testing.T) {
	manager, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	require.NoError(t, manager.db.db.Update(func(tx *bbolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(AgentTokenNamesBucket))
		if err != nil {
			return err
		}
		return b.Put([]byte("ci"), []byte("hash-of-a-token-that-no-longer-exists"))
	}))

	ownerless, ownerlessRaw := makeOwnedTestToken(t, "ci", "")
	require.NoError(t, manager.CreateAgentToken(ownerless, ownerlessRaw, testHMACKey))

	assert.Equal(t, auth.HashToken(ownerlessRaw, testHMACKey), legacyNameIndexEntry(t, manager, "ci"),
		"a dangling index entry points at nothing and must be reclaimed")
}

// TestAgentToken_RegenerateNarrowsScope pins the narrowing hook that closes the
// "un-sharing does not revoke live grants" gap at rotation time. It must apply
// the caller's filter to what is PERSISTED, not merely to what is returned —
// otherwise the trimmed grant stays live in storage.
//
// BITES: drop the `if narrowScope != nil` block in
// RegenerateAgentTokenForOwner; the reread below still shows ["*"].
func TestAgentToken_RegenerateNarrowsScope(t *testing.T) {
	manager, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	// A pre-branch token: a literal star, which the enforcement layer honours
	// unconditionally.
	token, raw := makeOwnedTestToken(t, "legacy", "userA")
	require.Equal(t, []string{"*"}, token.AllowedServers, "fixture: the token starts with a star")
	require.NoError(t, manager.CreateAgentToken(token, raw, testHMACKey))

	newRaw, err := auth.GenerateToken()
	require.NoError(t, err)

	updated, err := manager.RegenerateAgentTokenForOwner("userA", "legacy", newRaw, testHMACKey,
		func([]string) []string { return []string{"only-this"} })
	require.NoError(t, err)
	require.NotNil(t, updated)
	assert.Equal(t, []string{"only-this"}, updated.AllowedServers, "the returned record must carry the narrowed scope")

	// The persisted record is what actually matters: the returned struct could
	// be narrowed while storage kept the star.
	reread, err := manager.GetAgentTokenByOwnerAndName("userA", "legacy")
	require.NoError(t, err)
	require.NotNil(t, reread)
	assert.Equal(t, []string{"only-this"}, reread.AllowedServers,
		"the narrowed scope must be PERSISTED, not just returned")

	// A nil hook leaves the scope alone — the personal edition's contract.
	plain, plainRaw := makeOwnedTestToken(t, "plain", "userA")
	require.NoError(t, manager.CreateAgentToken(plain, plainRaw, testHMACKey))
	plainNewRaw, err := auth.GenerateToken()
	require.NoError(t, err)
	plainUpdated, err := manager.RegenerateAgentTokenForOwner("userA", "plain", plainNewRaw, testHMACKey, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"*"}, plainUpdated.AllowedServers, "a nil hook must not touch the scope")
}
