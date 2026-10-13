package storage

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
)

// This file is COMPILE-RED until T076 (storage.OwnerResolution +
// SetAgentTokenOwnerResolver, replacing SetAgentTokenOwnerGate +
// SetAgentTokenScopeResolver) and T077 land. It exercises the single-resolver
// contract described in contracts/entitlement-predicate.md §2 and
// data-model.md §3/§6:
//
//   - ValidateAgentToken calls the installed AgentTokenOwnerResolver exactly
//     once per authentication (not once for the owner gate and once again for
//     the scope resolver, as today's two callbacks do).
//   - OwnerResolution.Entitled is already the NARROWED grant
//     (narrowScopeToEntitled(granted, entitledServerNamesFor(...), isAdmin)):
//     storage only intersects it against the token's stored AllowedServers
//     (the narrow-only fence), it never re-derives entitlement itself.
//   - Email/Provider/Role from the resolution are stamped on the RETURNED
//     token's non-persisted OwnerEmail/OwnerProvider/OwnerRole fields
//     (`json:"-"`) and copied by AgentToken.AuthContext() into
//     AuthContext.Email/Provider/Role; the persisted BBolt record never
//     contains them, and a token re-read from storage (bypassing validation)
//     carries empty Owner* fields.
//   - An administrator's literal "*" grant survives narrowing
//     (Entitled: ["*"] -> AllowedServers == ["*"]), while a tenant's "*"
//     grant is materialised into the entitlement set (Entitled: ["a"] ->
//     AllowedServers == ["a"]).
//   - A deleted/inactive owner (Active: false, or a resolver error) denies
//     the token: ErrAgentTokenOwnerInactive / ErrAgentTokenScopeUnavailable.
//
// BITES: once T076 lands SetAgentTokenOwnerResolver and the single-call
// ValidateAgentToken path, reverting to the old two-callback shape (or
// calling the resolver twice, or stamping Entitled unnarrowed) makes one or
// more of these subtests fail; deleting the resolver call entirely makes the
// package fail to build, which is the compile-red state this file documents
// until then.

// readRawAgentTokenRecordBytes returns the raw JSON bytes stored under hash in
// the agent_tokens bucket, so a test can assert what never reaches disk
// without going through the (necessarily trusted) unmarshal path.
func readRawAgentTokenRecordBytes(t *testing.T, m *Manager, hash string) []byte {
	t.Helper()
	var out []byte
	err := m.db.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(AgentTokensBucket))
		if b == nil {
			return nil
		}
		if v := b.Get([]byte(hash)); v != nil {
			out = append([]byte(nil), v...)
		}
		return nil
	})
	require.NoError(t, err)
	return out
}

// TestValidateAgentToken_OwnerResolverCalledOnce pins the single-resolver
// contract (contracts/entitlement-predicate.md §2, data-model.md §6): one
// GetUser-equivalent call per authentication, not one for an owner gate and
// a second for a scope resolver.
func TestValidateAgentToken_OwnerResolverCalledOnce(t *testing.T) {
	manager, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	owned, rawOwned := makeOwnedTestToken(t, "ci", "userA")
	owned.AllowedServers = []string{"a", "b"}
	require.NoError(t, manager.CreateAgentToken(owned, rawOwned, testHMACKey))

	calls := 0
	manager.SetAgentTokenOwnerResolver(func(userID string, granted []string) (OwnerResolution, error) {
		calls++
		require.Equal(t, "userA", userID)
		require.ElementsMatch(t, []string{"a", "b"}, granted)
		return OwnerResolution{
			Active:   true,
			UserID:   userID,
			Email:    "usera@example.com",
			Provider: "oidc",
			Role:     "user",
			Entitled: []string{"a"},
		}, nil
	})

	got, err := manager.ValidateAgentToken(rawOwned, testHMACKey)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, 1, calls, "the resolver must be consulted exactly once per authentication")
	assert.Equal(t, []string{"a"}, got.AllowedServers)
}

// TestValidateAgentToken_OwnerResolutionStampsIdentityWithoutPersisting closes
// the carrier contract: Email/Provider/Role travel on the returned token's
// non-persisted Owner* fields, AuthContext() copies them, and the persisted
// record on disk never contains the sentinel values — including on a plain
// re-read that bypasses validation.
func TestValidateAgentToken_OwnerResolutionStampsIdentityWithoutPersisting(t *testing.T) {
	manager, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	owned, rawOwned := makeOwnedTestToken(t, "ci", "userA")
	owned.AllowedServers = []string{"a"}
	require.NoError(t, manager.CreateAgentToken(owned, rawOwned, testHMACKey))

	const sentinelEmail = "sentinel-owner@example.com"
	const sentinelProvider = "sentinel-oidc-provider"
	const sentinelRole = "sentinel-role-user"

	manager.SetAgentTokenOwnerResolver(func(userID string, granted []string) (OwnerResolution, error) {
		return OwnerResolution{
			Active:   true,
			UserID:   userID,
			Email:    sentinelEmail,
			Provider: sentinelProvider,
			Role:     sentinelRole,
			Entitled: granted,
		}, nil
	})

	got, err := manager.ValidateAgentToken(rawOwned, testHMACKey)
	require.NoError(t, err)
	require.NotNil(t, got)

	assert.Equal(t, sentinelEmail, got.OwnerEmail)
	assert.Equal(t, sentinelProvider, got.OwnerProvider)
	assert.Equal(t, sentinelRole, got.OwnerRole)

	ctx := got.AuthContext()
	require.NotNil(t, ctx)
	assert.Equal(t, sentinelEmail, ctx.Email)
	assert.Equal(t, sentinelProvider, ctx.Provider)
	assert.Equal(t, sentinelRole, ctx.Role)

	hash := auth.HashToken(rawOwned, testHMACKey)
	raw := readRawAgentTokenRecordBytes(t, manager, hash)
	require.NotEmpty(t, raw, "the record must exist in the bucket")
	rawStr := string(raw)
	assert.NotContains(t, rawStr, sentinelEmail, "OwnerEmail must never reach the persisted record")
	assert.NotContains(t, rawStr, sentinelProvider, "OwnerProvider must never reach the persisted record")
	assert.NotContains(t, rawStr, sentinelRole, "OwnerRole must never reach the persisted record")

	// A plain re-read (not through ValidateAgentToken) must carry empty
	// Owner* fields: nothing stamps them outside the validation path, and
	// nothing persisted them either.
	reread, err := manager.GetAgentTokenByOwnerAndName("userA", "ci")
	require.NoError(t, err)
	require.NotNil(t, reread)
	assert.Empty(t, reread.OwnerEmail)
	assert.Empty(t, reread.OwnerProvider)
	assert.Empty(t, reread.OwnerRole)
}

// TestValidateAgentToken_AdministratorLiteralStarSurvives pins FR-009: an
// administrator's literal "*" grant is not materialised into the current
// configuration snapshot, while a tenant's "*" grant is narrowed to the
// entitlement set the resolver computed.
func TestValidateAgentToken_AdministratorLiteralStarSurvives(t *testing.T) {
	manager, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	dana, rawDana := makeOwnedTestToken(t, "dana-token", "dana")
	dana.AllowedServers = []string{"*"}
	require.NoError(t, manager.CreateAgentToken(dana, rawDana, testHMACKey))

	alice, rawAlice := makeOwnedTestToken(t, "alice-token", "alice")
	alice.AllowedServers = []string{"*"}
	require.NoError(t, manager.CreateAgentToken(alice, rawAlice, testHMACKey))

	manager.SetAgentTokenOwnerResolver(func(userID string, granted []string) (OwnerResolution, error) {
		switch userID {
		case "dana":
			// Administrator: Entitled stays the literal wildcard.
			return OwnerResolution{Active: true, UserID: userID, Entitled: []string{"*"}}, nil
		case "alice":
			// Tenant: Entitled is the materialised, narrowed set.
			return OwnerResolution{Active: true, UserID: userID, Entitled: []string{"a"}}, nil
		default:
			return OwnerResolution{}, errors.New("unknown user")
		}
	})

	gotDana, err := manager.ValidateAgentToken(rawDana, testHMACKey)
	require.NoError(t, err)
	require.NotNil(t, gotDana)
	assert.Equal(t, []string{"*"}, gotDana.AllowedServers,
		"an administrator's literal star grant must survive validation unexpanded")

	gotAlice, err := manager.ValidateAgentToken(rawAlice, testHMACKey)
	require.NoError(t, err)
	require.NotNil(t, gotAlice)
	assert.Equal(t, []string{"a"}, gotAlice.AllowedServers,
		"a tenant's star grant must narrow to the resolver's materialised entitlement set")
}

// TestValidateAgentToken_DeletedOwnerDeniesAll pins the deny-all shape for a
// deleted/inactive owner: Active:false must deny with the typed sentinel, not
// merely narrow AllowedServers to empty.
func TestValidateAgentToken_DeletedOwnerDeniesAll(t *testing.T) {
	manager, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	owned, rawOwned := makeOwnedTestToken(t, "ci", "ghost")
	owned.AllowedServers = []string{"a"}
	require.NoError(t, manager.CreateAgentToken(owned, rawOwned, testHMACKey))

	manager.SetAgentTokenOwnerResolver(func(userID string, granted []string) (OwnerResolution, error) {
		// The owner record no longer exists.
		return OwnerResolution{Active: false}, nil
	})

	got, err := manager.ValidateAgentToken(rawOwned, testHMACKey)
	assert.Nil(t, got)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrAgentTokenOwnerInactive),
		"a deleted owner must deny with the typed owner-inactive sentinel, got %v", err)
}

// TestValidateAgentToken_OwnerResolverErrorFailsClosed pins the second
// sentinel: a resolver that cannot answer (store unavailable) must deny with
// ErrAgentTokenScopeUnavailable, distinct from an explicit Active:false.
func TestValidateAgentToken_OwnerResolverErrorFailsClosed(t *testing.T) {
	manager, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	owned, rawOwned := makeOwnedTestToken(t, "ci", "userA")
	require.NoError(t, manager.CreateAgentToken(owned, rawOwned, testHMACKey))

	boom := errors.New("user store unavailable")
	manager.SetAgentTokenOwnerResolver(func(userID string, granted []string) (OwnerResolution, error) {
		return OwnerResolution{}, boom
	})

	got, err := manager.ValidateAgentToken(rawOwned, testHMACKey)
	assert.Nil(t, got)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrAgentTokenScopeUnavailable),
		"an unanswerable resolver must deny with the scope-unavailable sentinel, got %v", err)
	assert.NotContains(t, err.Error(), boom.Error(),
		"the caller must not be told why the store failed")
}

// TestIntersectAllowedServers_NonNilEmpty pins the non-nil-empty contract for
// intersectAllowedServers (data-model.md §6 / contracts §2): an empty result
// and an empty `proposed` both return a non-nil empty slice, never nil, so a
// caller distinguishing "no resolver installed" (nil) from "resolver denied
// everything" ([]string{}) is not misled by an implicit nil result.
func TestIntersectAllowedServers_NonNilEmpty(t *testing.T) {
	empty := intersectAllowedServers([]string{"a", "b"}, []string{"c"})
	require.NotNil(t, empty, "no overlap must return a non-nil empty slice")
	assert.Empty(t, empty)

	emptyProposed := intersectAllowedServers([]string{"a", "b"}, []string{})
	require.NotNil(t, emptyProposed, "an empty proposed list must return a non-nil empty slice")
	assert.Empty(t, emptyProposed)
}
