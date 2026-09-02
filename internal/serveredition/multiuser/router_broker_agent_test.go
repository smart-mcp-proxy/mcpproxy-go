//go:build server

package multiuser

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
)

// agentCtx builds the request context an agent token produces. Built through
// auth.AgentToken.AuthContext — the single production constructor for the agent
// tier — rather than by hand, so this test cannot pass against a hand-written
// context that happens to differ from what authentication actually installs.
func agentCtx(owner, tokenName string) context.Context {
	tok := &auth.AgentToken{Name: tokenName, UserID: owner, Permissions: []string{auth.PermRead}}
	return auth.WithAuthContext(context.Background(), tok.AuthContext())
}

// TestRouter_BrokeredConnectionKey_AgentTokenNeverPoolsOntoItsOwner is the
// regression guard for the second-order cost of issue #1168.
//
// FR-018 keys a brokered upstream connection per (user, server) so one user's
// injected IdP credential is never reused for another. The key's identity half
// came straight from ac.GetUserID() with no tier check. That was harmless while
// only user sessions carried a UserID — an agent token keyed as the anonymous
// "" — but #1168 made an agent token carry its OWNER's UserID so its activity
// could be attributed. From that moment an agent-token request keyed IDENTICALLY
// to its owner's own session, so it pooled onto the owner's brokered connection
// and rode the owner's injected credential.
//
// The properties, all three of which must hold together:
//
//   - an agent token owned by U must NOT key like U's session (the regression);
//   - two agent tokens with DIFFERENT owners must still key apart, so the fix
//     cannot be "collapse every agent back to anonymous";
//   - a user session's own key is UNCHANGED and still per-user, so the fix does
//     not quietly break FR-018 itself.
//
// BITES: on unfixed code the first assertion fails — the agent key equals the
// owner's session key exactly.
func TestRouter_BrokeredConnectionKey_AgentTokenNeverPoolsOntoItsOwner(t *testing.T) {
	router, _ := setupRouter(t, []string{"shared-ghe"}, nil)

	aliceSession, err := router.BrokeredConnectionKey(userCtx("alice"), "shared-ghe")
	require.NoError(t, err)
	require.NotEmpty(t, aliceSession, "positive control: a user session must key at all")

	aliceAgent, err := router.BrokeredConnectionKey(agentCtx("alice", "ci"), "shared-ghe")
	require.NoError(t, err)

	assert.NotEqual(t, aliceSession, aliceAgent,
		"an agent token must not pool onto its owner's brokered connection: that connection carries the owner's injected IdP credential")

	// A second token of the SAME owner is the same principal as the first, so it
	// may share a pool entry with it — but still not with the session.
	aliceAgent2, err := router.BrokeredConnectionKey(agentCtx("alice", "nightly"), "shared-ghe")
	require.NoError(t, err)
	assert.Equal(t, aliceAgent, aliceAgent2, "agent-token pooling must stay stable for one owner")
	assert.NotEqual(t, aliceSession, aliceAgent2, "no agent token may reach its owner's session pool entry")

	// Different owners must still be kept apart — the fix must not degenerate
	// into keying every agent token as one anonymous principal.
	bobAgent, err := router.BrokeredConnectionKey(agentCtx("bob", "ci"), "shared-ghe")
	require.NoError(t, err)
	assert.NotEqual(t, aliceAgent, bobAgent,
		"two owners' agent tokens must not share a brokered connection")

	// And FR-018 itself is intact for sessions.
	bobSession, err := router.BrokeredConnectionKey(userCtx("bob"), "shared-ghe")
	require.NoError(t, err)
	assert.NotEqual(t, aliceSession, bobSession, "FR-018: user sessions stay per-user")
	assert.NotEqual(t, bobSession, aliceAgent, "an agent token must not land on ANY user's session entry")
}

// TestRouter_BrokeredConnectionKey_AdminUserSessionKeysAsItself pins that the
// tier guard reads IsUser(), which covers "admin_user" as well as "user". An
// admin's own browser session is still a user session and must keep keying by
// its bare user id — a guard written as `ac.Type == AuthTypeUser` would silently
// shunt every admin into the non-user namespace.
func TestRouter_BrokeredConnectionKey_AdminUserSessionKeysAsItself(t *testing.T) {
	router, _ := setupRouter(t, []string{"shared-ghe"}, nil)

	adminKey, err := router.BrokeredConnectionKey(adminCtx("root"), "shared-ghe")
	require.NoError(t, err)
	userKey, err := router.BrokeredConnectionKey(userCtx("root"), "shared-ghe")
	require.NoError(t, err)

	assert.Equal(t, userKey, adminKey,
		"an admin_user session is a user session: same identity, same pool entry")

	agentKey, err := router.BrokeredConnectionKey(agentCtx("root", "ci"), "shared-ghe")
	require.NoError(t, err)
	assert.NotEqual(t, adminKey, agentKey,
		"an agent token owned by an admin must still not pool onto the admin's session")
}
