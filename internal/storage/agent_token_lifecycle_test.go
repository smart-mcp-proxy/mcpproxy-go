package storage

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
)

// TestListAgentTokens_CorruptRecordDoesNotBreakTheListing pins the same blast
// radius for the LIST path that TestAgentToken_CorruptRecordDoesNotBreakOtherTenants
// pins for the by-name resolver.
//
// ListAgentTokens walks the whole agent_tokens bucket, so aborting on the first
// row that fails json.Unmarshal turned ONE bad record — a truncated write, a
// hand-edited DB, a record from a future schema — into a 500 on
// GET /api/v1/user/tokens for EVERY tenant of the deployment, and on the
// operator's own `mcpproxy token list` with it. The resolver was fixed for
// exactly this; the list path was missed.
//
// Oracle discipline: a positive control lists the healthy tokens BEFORE the
// corruption is planted, on the same manager, so a later success cannot be an
// empty bucket or an unwired store; the count afterwards is pinned exactly, so
// "returns no error" cannot pass by returning nothing.
//
// BITES: restore `return fmt.Errorf("failed to unmarshal agent token: %w", err)`
// inside ListAgentTokens' ForEach and the require.NoError below fails.
func TestListAgentTokens_CorruptRecordDoesNotBreakTheListing(t *testing.T) {
	manager, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	a, rawA := makeOwnedTestToken(t, "ci", "userA")
	require.NoError(t, manager.CreateAgentToken(a, rawA, testHMACKey))
	b, rawB := makeOwnedTestToken(t, "deploy", "userB")
	require.NoError(t, manager.CreateAgentToken(b, rawB, testHMACKey))

	// Positive control on the clean store.
	before, err := manager.ListAgentTokens()
	require.NoError(t, err, "positive control: a clean store must list")
	require.Len(t, before, 2, "positive control: both fixtures must be present")

	// Keyed so it sorts into the middle of the bucket, not conveniently last.
	writeRawAgentTokenRecord(t, manager, "0000corrupt0000", []byte("{not json at all"))

	after, err := manager.ListAgentTokens()
	require.NoError(t, err,
		"one unparseable row must not make the listing fail for every tenant")
	require.Len(t, after, 2,
		"the healthy tokens must still be listed; only the corrupt row is skipped")

	names := map[string]bool{}
	for i := range after {
		names[after[i].Name] = true
	}
	assert.True(t, names["ci"], "user A's token must still be listed")
	assert.True(t, names["deploy"], "user B's token must still be listed")
}

// TestRegenerateAgentTokenForOwner_NarrowScopeHookCannotWiden closes the gap
// between the narrowScope contract and its enforcement.
//
// The hook's "must only narrow" rule was a doc comment: storage persisted
// whatever the hook returned, verbatim. Rotation is supposed to be
// scope-neutral, so a caller whose hook returned a wider list — by accident, or
// because a later refactor passed the ENTITLED set instead of a filter over the
// stored one — would have turned the one repair operation into a privilege
// escalation, silently. The result is now intersected with the token's existing
// AllowedServers inside the same transaction.
//
// Oracle discipline: every case asserts the PERSISTED record read back through
// storage (a return value could be narrowed while the write was not), and the
// first subtest is a positive control proving an honest hook still works.
//
// BITES: drop intersectAllowedServers from RegenerateAgentTokenForOwner and the
// "hook cannot widen" subtests persist the widened list.
func TestRegenerateAgentTokenForOwner_NarrowScopeHookCannotWiden(t *testing.T) {
	rotate := func(t *testing.T, manager *Manager, owner, name string, hook func([]string) []string) *auth.AgentToken {
		t.Helper()
		newRaw, err := auth.GenerateToken()
		require.NoError(t, err)
		updated, err := manager.RegenerateAgentTokenForOwner(owner, name, newRaw, testHMACKey, hook)
		require.NoError(t, err)
		require.NotNil(t, updated)

		stored, err := manager.GetAgentTokenByOwnerAndName(owner, name)
		require.NoError(t, err)
		require.NotNil(t, stored, "the rotated token must still be resolvable")
		require.Equal(t, updated.AllowedServers, stored.AllowedServers,
			"the returned scope must be the one that was persisted")
		return stored
	}

	seed := func(t *testing.T, manager *Manager, owner, name string, allowed []string) {
		t.Helper()
		raw, err := auth.GenerateToken()
		require.NoError(t, err)
		require.NoError(t, manager.CreateAgentToken(auth.AgentToken{
			Name:           name,
			UserID:         owner,
			AllowedServers: allowed,
			Permissions:    []string{"read"},
		}, raw, testHMACKey))
	}

	t.Run("positive control: an honest narrowing hook is applied", func(t *testing.T) {
		manager, cleanup := setupTestStorageForAgentTokens(t)
		defer cleanup()

		seed(t, manager, "userA", "ci", []string{"alpha", "beta"})
		stored := rotate(t, manager, "userA", "ci", func(current []string) []string {
			// The production shape: a filter over what is already there.
			out := []string{}
			for _, s := range current {
				if s == "alpha" {
					out = append(out, s)
				}
			}
			return out
		})
		assert.Equal(t, []string{"alpha"}, stored.AllowedServers,
			"a genuine narrowing must be persisted, or the intersection has broken rotation")
	})

	t.Run("a hook that adds a server is intersected away", func(t *testing.T) {
		manager, cleanup := setupTestStorageForAgentTokens(t)
		defer cleanup()

		seed(t, manager, "userA", "ci", []string{"alpha"})
		stored := rotate(t, manager, "userA", "ci", func([]string) []string {
			return []string{"alpha", "admin-private"}
		})
		assert.Equal(t, []string{"alpha"}, stored.AllowedServers,
			"rotation must not be able to grant a server the token could not already reach")
		assert.NotContains(t, stored.AllowedServers, "admin-private")
	})

	t.Run("a hook that replaces the scope wholesale is intersected away", func(t *testing.T) {
		manager, cleanup := setupTestStorageForAgentTokens(t)
		defer cleanup()

		seed(t, manager, "userA", "ci", []string{"alpha"})
		stored := rotate(t, manager, "userA", "ci", func([]string) []string {
			return []string{"admin-private", "another-tenant"}
		})
		assert.Empty(t, stored.AllowedServers,
			"nothing the token already held survives, so nothing may be granted")
	})

	t.Run("a hook cannot promote a bounded scope to the wildcard", func(t *testing.T) {
		manager, cleanup := setupTestStorageForAgentTokens(t)
		defer cleanup()

		seed(t, manager, "userA", "ci", []string{"alpha"})
		stored := rotate(t, manager, "userA", "ci", func([]string) []string {
			return []string{"*"}
		})
		assert.NotContains(t, stored.AllowedServers, "*",
			`"*" is honoured unconditionally by CanAccessServer; rotation must never introduce it`)
		assert.Empty(t, stored.AllowedServers)
	})

	t.Run("a stored wildcard may still be materialised into a bounded set", func(t *testing.T) {
		manager, cleanup := setupTestStorageForAgentTokens(t)
		defer cleanup()

		// This is the pre-branch token the server edition converts on first
		// rotation. The intersection must NOT empty it: "*" already granted
		// everything, so any concrete list is a narrowing.
		seed(t, manager, "userA", "ci", []string{"*"})
		stored := rotate(t, manager, "userA", "ci", func([]string) []string {
			return []string{"alpha", "shared-ok"}
		})
		assert.Equal(t, []string{"alpha", "shared-ok"}, stored.AllowedServers,
			"materialising a stored wildcard is a narrowing and must survive intact")
	})

	t.Run("nil hook leaves the scope untouched", func(t *testing.T) {
		manager, cleanup := setupTestStorageForAgentTokens(t)
		defer cleanup()

		seed(t, manager, "userA", "ci", []string{"alpha", "beta"})
		stored := rotate(t, manager, "userA", "ci", nil)
		assert.Equal(t, []string{"alpha", "beta"}, stored.AllowedServers,
			"the personal edition passes no hook and must be unaffected")
	})
}

// TestValidateAgentToken_OwnerGateStopsADisabledOwner is the authentication-path
// half of the disabled-user fix.
//
// A token's authorisation is decided once, when it is minted. Disabling a user
// revoked their sessions and stopped their JWTs, but their agent tokens were
// separate records that nothing re-checked: they kept authenticating, carrying
// the disabled user's UserID into every downstream authorisation and activity
// decision. Disabling is the documented remediation for a compromised account,
// so it has to reach those too.
//
// Oracle discipline: the token validates FIRST, on the same manager with the
// gate already installed, so the later refusal is the owner's state changing and
// not a broken fixture or a mis-hashed token; an ownerless token is validated
// throughout as the personal-edition control; and the refusal is pinned to the
// typed sentinel rather than to any message.
//
// BITES: delete the agentTokenOwnerActive call from ValidateAgentToken (leave
// the gate and the setter in place, so the package still builds) and the
// "disabled" assertions fail.
func TestValidateAgentToken_OwnerGateStopsADisabledOwner(t *testing.T) {
	manager, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	owned, rawOwned := makeOwnedTestToken(t, "ci", "userA")
	require.NoError(t, manager.CreateAgentToken(owned, rawOwned, testHMACKey))
	ownerless, rawOwnerless := makeOwnedTestToken(t, "personal", "")
	require.NoError(t, manager.CreateAgentToken(ownerless, rawOwnerless, testHMACKey))

	disabled := map[string]bool{}
	manager.SetAgentTokenOwnerGate(func(userID string) (bool, error) {
		return !disabled[userID], nil
	})

	// Positive control: with the gate installed and the owner active, the token
	// validates. A later refusal is therefore about the owner.
	got, err := manager.ValidateAgentToken(rawOwned, testHMACKey)
	require.NoError(t, err, "positive control: an active owner's token must validate")
	require.NotNil(t, got)
	require.Equal(t, "userA", got.UserID)

	// Disable the owner. Nothing about the token record changes.
	disabled["userA"] = true

	got, err = manager.ValidateAgentToken(rawOwned, testHMACKey)
	assert.Nil(t, got, "a disabled owner's token must not authenticate")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrAgentTokenOwnerInactive),
		"the refusal must be the typed owner-inactive sentinel, got %v", err)

	// The record itself is untouched: this is a live check, not a mutation.
	stored, err := manager.GetAgentTokenByOwnerAndName("userA", "ci")
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.False(t, stored.Revoked, "the gate must not have rewritten the record")

	// The personal edition's ownerless tokens are never gated.
	personal, err := manager.ValidateAgentToken(rawOwnerless, testHMACKey)
	require.NoError(t, err, "an ownerless token must be unaffected by the owner gate")
	require.NotNil(t, personal)

	// Re-enabling the owner makes the token work again — the gate is a live
	// check. (The admin disable handler ALSO revokes, so that a real disable is
	// not undone this way; see AdminHandlers.disableUser.)
	disabled["userA"] = false
	back, err := manager.ValidateAgentToken(rawOwned, testHMACKey)
	require.NoError(t, err)
	require.NotNil(t, back)
}

// TestValidateAgentToken_OwnerGateFailsClosed pins the direction of the failure.
//
// A gate that cannot answer — user store unavailable, database error — must
// deny. The alternative, treating an unanswerable question as "valid", is
// exactly the hole the gate exists to close, reachable by making the store
// error.
//
// BITES: change the error branch in ValidateAgentToken to fall through to
// `return token, nil` and this fails.
func TestValidateAgentToken_OwnerGateFailsClosed(t *testing.T) {
	manager, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	owned, rawOwned := makeOwnedTestToken(t, "ci", "userA")
	require.NoError(t, manager.CreateAgentToken(owned, rawOwned, testHMACKey))

	// Positive control with a healthy gate.
	manager.SetAgentTokenOwnerGate(func(string) (bool, error) { return true, nil })
	got, err := manager.ValidateAgentToken(rawOwned, testHMACKey)
	require.NoError(t, err, "positive control: a healthy gate must let the token through")
	require.NotNil(t, got)

	boom := errors.New("user store unavailable")
	manager.SetAgentTokenOwnerGate(func(string) (bool, error) { return false, boom })

	got, err = manager.ValidateAgentToken(rawOwned, testHMACKey)
	assert.Nil(t, got, "an unanswerable owner check must deny the token")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrAgentTokenOwnerInactive),
		"the refusal must be the owner-inactive sentinel, got %v", err)
	assert.NotContains(t, err.Error(), boom.Error(),
		"the caller must not be told why the store failed")
}

// TestRevokeAgentTokensForOwner_BurnsOnlyThatOwnersTokens is the persistence
// half of the disabled-user fix: re-enabling an account must not hand back the
// credentials that may be why it was disabled.
//
// Oracle discipline: another tenant's token and an OWNERLESS (personal-edition)
// token are both present and must both survive — "did it revoke?" is only half
// the property, and a bulk revoke that swept the ownerless namespace would be an
// outage rather than a remediation.
//
// BITES: it is new behaviour; without RevokeAgentTokensForOwner the package does
// not build, so the bite is shown at the handler level instead
// (TestDisableUser_RevokesTheUsersAgentTokens). Here the sharp assertions are
// the survivors and the empty-owner refusal.
func TestRevokeAgentTokensForOwner_BurnsOnlyThatOwnersTokens(t *testing.T) {
	manager, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	aOne, rawAOne := makeOwnedTestToken(t, "ci", "userA")
	require.NoError(t, manager.CreateAgentToken(aOne, rawAOne, testHMACKey))
	aTwo, rawATwo := makeOwnedTestToken(t, "deploy", "userA")
	require.NoError(t, manager.CreateAgentToken(aTwo, rawATwo, testHMACKey))
	bOne, rawBOne := makeOwnedTestToken(t, "ci", "userB")
	require.NoError(t, manager.CreateAgentToken(bOne, rawBOne, testHMACKey))
	personal, rawPersonal := makeOwnedTestToken(t, "local", "")
	require.NoError(t, manager.CreateAgentToken(personal, rawPersonal, testHMACKey))

	// Positive control: every token authenticates before the revoke.
	for _, raw := range []string{rawAOne, rawATwo, rawBOne, rawPersonal} {
		_, err := manager.ValidateAgentToken(raw, testHMACKey)
		require.NoError(t, err, "positive control: all four fixtures must validate first")
	}

	// The ownerless namespace may never be bulk-revoked: "" is every
	// personal-edition token.
	_, err := manager.RevokeAgentTokensForOwner("")
	require.Error(t, err, "an empty owner must be refused, not treated as the ownerless namespace")

	count, err := manager.RevokeAgentTokensForOwner("userA")
	require.NoError(t, err)
	assert.Equal(t, 2, count, "both of user A's tokens must be revoked")

	for _, raw := range []string{rawAOne, rawATwo} {
		_, err := manager.ValidateAgentToken(raw, testHMACKey)
		assert.Error(t, err, "a revoked token must not authenticate")
	}

	// The survivors.
	surviving, err := manager.ValidateAgentToken(rawBOne, testHMACKey)
	require.NoError(t, err, "another tenant's token must survive")
	require.NotNil(t, surviving)
	survivingPersonal, err := manager.ValidateAgentToken(rawPersonal, testHMACKey)
	require.NoError(t, err, "an ownerless personal-edition token must survive")
	require.NotNil(t, survivingPersonal)

	// Soft delete: the records are still there for the operator to see.
	listed, err := manager.ListAgentTokens()
	require.NoError(t, err)
	assert.Len(t, listed, 4, "revoke is a soft delete; no record may disappear")

	// Idempotent.
	again, err := manager.RevokeAgentTokensForOwner("userA")
	require.NoError(t, err)
	assert.Equal(t, 0, again, "a second revoke has nothing left to change")
}
