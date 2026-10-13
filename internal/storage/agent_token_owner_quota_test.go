package storage

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
)

func mintOwnedToken(t *testing.T, m *Manager, userID, name string) error {
	t.Helper()
	raw, err := auth.GenerateToken()
	require.NoError(t, err)
	return m.CreateAgentToken(auth.AgentToken{
		UserID:      userID,
		Name:        name,
		Permissions: []string{auth.PermRead},
	}, raw, testHMACKey)
}

// Issue #1177: one server-edition tenant must not be able to consume the
// deployment's entire shared token pool.
func TestCreateAgentToken_OwnerQuotaIsPerOwner(t *testing.T) {
	m, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	for i := 0; i < auth.MaxTokensPerOwner; i++ {
		require.NoError(t, mintOwnedToken(t, m, "tenant-a", fmt.Sprintf("tok-%02d", i)))
	}

	require.ErrorIs(t, mintOwnedToken(t, m, "tenant-a", "one-too-many"), ErrAgentTokenOwnerLimitReached,
		"the 26th token for one owner must be rejected")
	require.NoError(t, mintOwnedToken(t, m, "tenant-b", "first"),
		"one tenant's quota must not consume another tenant's slots")
}

// Quota counts stored records, including revoked records, so soft revocation
// cannot be used to grow the bounded token bucket forever. Permanent deletion
// is the operation that frees storage and therefore frees a slot.
func TestCreateAgentToken_PermanentDeleteFreesOwnerSlot(t *testing.T) {
	m, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	for i := 0; i < auth.MaxTokensPerOwner; i++ {
		require.NoError(t, mintOwnedToken(t, m, "tenant-a", fmt.Sprintf("tok-%02d", i)))
	}
	require.ErrorIs(t, mintOwnedToken(t, m, "tenant-a", "blocked"), ErrAgentTokenOwnerLimitReached)

	require.NoError(t, m.RevokeAgentTokenForOwner("tenant-a", "tok-00"))
	require.ErrorIs(t, mintOwnedToken(t, m, "tenant-a", "still-blocked"), ErrAgentTokenOwnerLimitReached,
		"soft revocation must not evade the storage bound")

	require.NoError(t, m.DeleteAgentTokenForOwner("tenant-a", "tok-00"))
	require.NoError(t, mintOwnedToken(t, m, "tenant-a", "after-delete"),
		"permanent deletion must free the owner's slot")
}

// Personal-edition tokens are ownerless and keep the established deployment
// cap; adding a server-edition quota must not lower that limit.
func TestCreateAgentToken_OwnerlessKeepsDeploymentCap(t *testing.T) {
	m, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	for i := 0; i < auth.MaxTokensPerOwner+5; i++ {
		require.NoError(t, mintOwnedToken(t, m, "", fmt.Sprintf("personal-%02d", i)))
	}
}
