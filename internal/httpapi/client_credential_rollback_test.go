package httpapi

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// T030a — rollback safety (FR-021, research D27).
//
// A `mcp_cli_` secret minted by a Spec 108 build must be worthless to a pre-108
// binary: it must never authenticate as an agent token, so rolling the binary
// back never turns a client credential into a wildcard agent token. This test
// runs a minted secret through FROZEN copies of the pre-108 token branches.
//
// The copies below reproduce, verbatim in logic, the branches of
// commit fad8c679 (= 74df81e20^, the last commit before client credentials):
//
//	internal/httpapi/server.go   authenticateExplicitToken / authenticateBearer
//	internal/server/server.go    mcpAuthMiddleware
//
// To refresh: `git show fad8c679:internal/httpapi/server.go` and
// `git show fad8c679:internal/server/server.go`; they are pure functions of
// (token, apiKey, requireAuth), so a drift in the originals cannot change
// them — only a deliberate edit here can, and review compares them against
// `git show`.

// pre108ValidateTokenFormat is the pre-108 validator: mcp_agt_ + 64 hex only.
// (auth.ValidateTokenFormat is unchanged by Spec 108 and is exactly that.)
func pre108ValidateTokenFormat(token string) bool { return auth.ValidateTokenFormat(token) }

// pre108REST is fad8c679's REST token branch: an mcp_agt_ prefix goes to the
// agent-token path; everything else is compared with the admin key; nothing
// else authenticates.
func pre108REST(token, apiKey string) (status int, ctx *auth.AuthContext) {
	if token != "" && strings.HasPrefix(token, auth.TokenPrefixStr) {
		// agent-token path (needs a valid mcp_agt_ secret)
		if !pre108ValidateTokenFormat(token) {
			return http.StatusUnauthorized, nil
		}
		return http.StatusUnauthorized, nil // not found in this scenario
	}
	if auth.ConstantTimeEqual(token, apiKey) {
		return http.StatusOK, auth.AdminContext()
	}
	return http.StatusUnauthorized, nil
}

// pre108MCP is fad8c679's mcpAuthMiddleware token branch.
func pre108MCP(token, apiKey string, requireAuth bool) (status int, ctx *auth.AuthContext) {
	if strings.HasPrefix(token, auth.TokenPrefixStr) {
		return http.StatusUnauthorized, nil // agent token not found
	}
	if auth.ConstantTimeEqual(token, apiKey) {
		return http.StatusOK, auth.AdminContext()
	}
	if requireAuth {
		return http.StatusUnauthorized, nil
	}
	// Backward compatibility: an unrecognised token is an ANONYMOUS context.
	return http.StatusOK, auth.AnonymousContext()
}

func TestClientCredentialRollback_PreSpec108BinaryNeverTreatsItAsAnAgentToken(t *testing.T) {
	dir := t.TempDir()
	sm, err := storage.NewManager(dir, zap.NewNop().Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { _ = sm.Close() })
	key, err := auth.GetOrCreateHMACKey(dir)
	require.NoError(t, err)

	secret, err := auth.GenerateClientToken()
	require.NoError(t, err)
	_, err = sm.MintClientCredential("cursor", secret, key, auth.ProfileModeLocked, "ro", time.Now().Add(24*time.Hour))
	require.NoError(t, err)

	const adminKey = "pre108-admin-key"

	require.False(t, pre108ValidateTokenFormat(secret), "the pre-108 validator rejects the mcp_cli_ prefix outright")

	t.Run("REST: 401", func(t *testing.T) {
		status, ctx := pre108REST(secret, adminKey)
		require.Equal(t, http.StatusUnauthorized, status)
		require.Nil(t, ctx)
	})

	t.Run("MCP with require_mcp_auth on: 401", func(t *testing.T) {
		status, ctx := pre108MCP(secret, adminKey, true)
		require.Equal(t, http.StatusUnauthorized, status)
		require.Nil(t, ctx)
	})

	t.Run("MCP with require_mcp_auth off: anonymous, never an agent", func(t *testing.T) {
		status, ctx := pre108MCP(secret, adminKey, false)
		require.Equal(t, http.StatusOK, status)
		require.NotNil(t, ctx)
		require.True(t, ctx.Anonymous)
		require.Empty(t, ctx.AgentName)
		require.Empty(t, ctx.TokenKind)
		require.NotEqual(t, auth.AuthTypeAgent, ctx.Type, "a client credential never becomes an agent context on a rolled-back binary")
		require.Empty(t, ctx.AllowedServers, "and never a wildcard grant")
	})

	// The same secret does authenticate on the current binary's MCP door: the
	// scenario above is a real credential, not an unparseable string.
	_, err = sm.ValidateAgentToken(secret, key)
	require.NoError(t, err)
}
