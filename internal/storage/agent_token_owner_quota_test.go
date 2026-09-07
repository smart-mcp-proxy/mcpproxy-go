package storage

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
)

// Issue #1177. The token cap was a single deployment-wide number enforced as a
// raw bucket key count:
//
//	count := tokenBucket.Stats().KeyN
//	if count >= auth.MaxTokens { ... }
//
// with no owner filter — while the duplicate-NAME check three lines above was
// already owner-scoped. In the server edition any authenticated tenant can
// reach POST /api/v1/user/tokens, so one tenant could mint 100 tokens and
// every other tenant, and the operator, would be permanently unable to create
// one.
//
// Two properties are pinned here: a per-owner quota, and that the count is of
// LIVE tokens. Revocation is a soft delete (token.Revoked = true, re-Put under
// the same key), so a key count treats revoked tokens as occupying slots
// forever — which turned the DoS into an unrecoverable one, because there is
// no API path to hard-delete another tenant's tokens.

func mintFor(t *testing.T, m *Manager, userID, name string) error {
	t.Helper()
	raw, err := auth.GenerateToken()
	require.NoError(t, err)
	return m.CreateAgentToken(auth.AgentToken{
		UserID:      userID,
		Name:        name,
		Permissions: []string{auth.PermRead},
		CreatedAt:   time.Now().UTC(),
	}, raw, nil)
}

func TestCreateAgentToken_OwnerQuotaIsPerOwner(t *testing.T) {
	m, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	// Fill tenant A to its own quota.
	for i := 0; i < auth.MaxTokensPerOwner; i++ {
		require.NoError(t, mintFor(t, m, "tenant-a", fmt.Sprintf("tok-%d", i)),
			"tenant-a token %d should be allowed", i)
	}

	// One more for A is refused, with the owner-specific error.
	require.ErrorIs(t, mintFor(t, m, "tenant-a", "one-too-many"), ErrAgentTokenOwnerLimitReached,
		"a tenant past its own quota must get the owner error, not the deployment one")

	// Tenant B is untouched by A's exhaustion — this is the whole bug.
	require.NoError(t, mintFor(t, m, "tenant-b", "first"),
		"one tenant must not be able to exhaust another tenant's slots")
}

func TestCreateAgentToken_RevokedTokensDoNotConsumeSlots(t *testing.T) {
	m, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	for i := 0; i < auth.MaxTokensPerOwner; i++ {
		require.NoError(t, mintFor(t, m, "tenant-a", fmt.Sprintf("tok-%d", i)))
	}
	require.ErrorIs(t, mintFor(t, m, "tenant-a", "blocked"), ErrAgentTokenOwnerLimitReached)

	// Revoking is a soft delete; the slot must still come back, or the tenant
	// can never recover without hand-editing bbolt.
	require.NoError(t, m.RevokeAgentTokenForOwner("tenant-a", "tok-0"))

	require.NoError(t, mintFor(t, m, "tenant-a", "after-revoke"),
		"revoking a token must free its slot")
}

// The personal edition has exactly one (ownerless) principal, so the per-owner
// quota must not silently lower its long-standing limit. Ownerless tokens keep
// the deployment-wide cap.
func TestCreateAgentToken_OwnerlessKeepsDeploymentCap(t *testing.T) {
	m, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	for i := 0; i < auth.MaxTokensPerOwner+5; i++ {
		require.NoError(t, mintFor(t, m, "", fmt.Sprintf("personal-%d", i)),
			"ownerless token %d must not hit a per-owner quota", i)
	}
}
