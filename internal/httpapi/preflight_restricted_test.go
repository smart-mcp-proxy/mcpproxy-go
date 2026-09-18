package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/preflight"
)

// Spec 107 FR-006 / T071+T077: the REST preflight door carries an explicit
// restricted marker for every non-administrator context, so an unentitled
// token (AllowedServers == []string{} after storage narrowing) evaluates every
// id OUT OF SCOPE on POST /api/v1/preflight, exactly as on /mcp — never
// "unrestricted" (the empty-list two-semantics trap). Administrators and the
// no-context passthrough keep today's inputs (Restricted false).
func TestPreflightParams_RestrictedMarker(t *testing.T) {
	body := &contracts.PreflightRequest{}
	tools := []preflight.ToolRef{{ID: "ctl:echo"}}

	t.Run("unentitled agent token is restricted with an empty scope", func(t *testing.T) {
		authCtx := (&auth.AgentToken{Name: "ci", AllowedServers: []string{}}).AuthContext()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/preflight", nil)
		req = req.WithContext(auth.WithAuthContext(req.Context(), authCtx))

		params := preflightParams(req, body, tools)
		assert.Equal(t, preflight.TierAgentToken, params.Tier)
		assert.True(t, params.Restricted, "every non-administrator context must carry the restricted marker")
		require.NotNil(t, params.TokenServers)
		assert.Empty(t, params.TokenServers)

		scope := preflight.ResolveScope(preflight.ScopeInputs{Restricted: params.Restricted, TokenServers: params.TokenServers})
		require.NotNil(t, scope, "an unentitled token must resolve to a deny-all scope, not to unrestricted")
		assert.False(t, scope.Allows("ctl"), "every id must be out of scope for an unentitled token")
	})

	t.Run("scoped agent token is restricted to its grant", func(t *testing.T) {
		authCtx := (&auth.AgentToken{Name: "ci", AllowedServers: []string{"ctl"}}).AuthContext()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/preflight", nil)
		req = req.WithContext(auth.WithAuthContext(req.Context(), authCtx))

		params := preflightParams(req, body, tools)
		assert.True(t, params.Restricted)
		assert.Equal(t, []string{"ctl"}, params.TokenServers)
	})

	t.Run("tenant user context is restricted", func(t *testing.T) {
		// A tenant principal (FR-002) carries its entitlement set; a nil set
		// on a user context must still be deny-all, never unrestricted.
		req := httptest.NewRequest(http.MethodPost, "/api/v1/preflight", nil)
		req = req.WithContext(auth.WithAuthContext(req.Context(),
			auth.UserContext("01J0USER", "user@tenant.example", "Tenant User", "google")))

		params := preflightParams(req, body, tools)
		assert.True(t, params.Restricted)
		scope := preflight.ResolveScope(preflight.ScopeInputs{Restricted: params.Restricted, TokenServers: params.TokenServers})
		require.NotNil(t, scope)
		assert.False(t, scope.Allows("ctl"))
	})

	t.Run("administrator is not restricted", func(t *testing.T) {
		for _, ac := range []*auth.AuthContext{
			auth.AdminContext(),
			auth.AdminUserContext("01J0ADMIN", "admin@tenant.example", "Tenant Admin", "google"),
		} {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/preflight", nil)
			req = req.WithContext(auth.WithAuthContext(req.Context(), ac))
			params := preflightParams(req, body, tools)
			assert.False(t, params.Restricted, "%s must keep today's unrestricted inputs (SC-006)", ac.Type)
			assert.Nil(t, params.TokenServers)
		}
	})

	t.Run("no auth context invents no restriction", func(t *testing.T) {
		// The no-config passthrough: the tier narrows, but a missing
		// credential is not a deny-all scope (disclosure and visibility are
		// separate decisions) — unchanged from Spec 099.
		req := httptest.NewRequest(http.MethodPost, "/api/v1/preflight", nil)
		params := preflightParams(req, body, tools)
		assert.Equal(t, preflight.TierAgentToken, params.Tier)
		assert.False(t, params.Restricted)
		assert.Nil(t, params.TokenServers)
	})
}
