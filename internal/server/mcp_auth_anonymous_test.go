package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/transport"
)

func newAuthMiddlewareTestServer(t *testing.T, apiKey string) *Server {
	t.Helper()

	cfg := config.DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.Listen = "127.0.0.1:0"
	cfg.APIKey = apiKey

	srv, err := NewServer(cfg, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Shutdown() })
	return srv
}

// captureAuthContext runs one request through mcpAuthMiddleware and returns the
// AuthContext the downstream handler saw.
func captureAuthContext(t *testing.T, srv *Server, req *http.Request) (*auth.AuthContext, int) {
	t.Helper()

	var seen *auth.AuthContext
	handler := srv.mcpAuthMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = auth.AuthContextFromContext(r.Context())
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return seen, rec.Code
}

// TestMCPAuthMiddleware_AnonymousVsAuthenticated is the (b) half of issue
// #1148. The middleware upgrades an unauthenticated /mcp request to admin for
// backward compatibility — that stays — but the resulting context must now
// declare that it is anonymous, so a secret-revealing operation can tell the
// two apart. Authenticated callers (API key, tray socket) must be unaffected.
func TestMCPAuthMiddleware_AnonymousVsAuthenticated(t *testing.T) {
	const apiKey = "test-api-key-1148"
	srv := newAuthMiddlewareTestServer(t, apiKey)

	t.Run("unauthenticated TCP is anonymous admin", func(t *testing.T) {
		ctx, code := captureAuthContext(t, srv, httptest.NewRequest(http.MethodPost, "/mcp", http.NoBody))
		require.Equal(t, http.StatusOK, code)
		require.NotNil(t, ctx)
		require.True(t, ctx.IsAdmin(), "back-compat: unauthenticated MCP clients must keep admin behaviour")
		require.True(t, ctx.Anonymous, "unauthenticated caller must be marked anonymous")
		require.False(t, ctx.CanRevealSecrets())
	})

	t.Run("valid API key is a real admin", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/mcp", http.NoBody)
		req.Header.Set("X-API-Key", apiKey)
		ctx, code := captureAuthContext(t, srv, req)
		require.Equal(t, http.StatusOK, code)
		require.NotNil(t, ctx)
		require.False(t, ctx.Anonymous)
		require.True(t, ctx.CanRevealSecrets())
	})

	t.Run("tray socket connection is a real admin", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/mcp", http.NoBody)
		req = req.WithContext(transport.TagConnectionContext(req.Context(), transport.ConnectionSourceTray))
		ctx, code := captureAuthContext(t, srv, req)
		require.Equal(t, http.StatusOK, code)
		require.NotNil(t, ctx)
		require.False(t, ctx.Anonymous, "OS-level socket auth is a real identity, not an anonymous fallback")
		require.True(t, ctx.CanRevealSecrets())
	})

	t.Run("unrecognized token is anonymous admin", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/mcp", http.NoBody)
		req.Header.Set("X-API-Key", "not-the-key")
		ctx, code := captureAuthContext(t, srv, req)
		require.Equal(t, http.StatusOK, code)
		require.NotNil(t, ctx)
		require.True(t, ctx.IsAdmin())
		require.True(t, ctx.Anonymous)
		require.False(t, ctx.CanRevealSecrets())
	})

	t.Run("unrecognized token on a tray connection is a real admin", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/mcp", http.NoBody)
		req.Header.Set("X-API-Key", "not-the-key")
		req = req.WithContext(transport.TagConnectionContext(req.Context(), transport.ConnectionSourceTray))
		ctx, code := captureAuthContext(t, srv, req)
		require.Equal(t, http.StatusOK, code)
		require.NotNil(t, ctx)
		require.False(t, ctx.Anonymous)
	})
}

// TestStdioAuthContext_IsRealAdmin pins the stdio transport declaring its
// identity explicitly. Before #1148 stdio ran with NO auth context at all and
// relied on gates of the shape `authCtx != nil && !authCtx.IsAdmin()` treating
// nil as admin — which is the same nil-is-privileged reading the issue reports.
func TestStdioAuthContext_IsRealAdmin(t *testing.T) {
	ctx := stdioAuthContext(t.Context())
	authCtx := auth.AuthContextFromContext(ctx)
	require.NotNil(t, authCtx, "stdio must install an explicit auth context")
	require.True(t, authCtx.IsAdmin())
	require.False(t, authCtx.Anonymous)
	require.True(t, authCtx.CanRevealSecrets(), "stdio is a local, OS-authenticated transport")
}

// TestMCPAuthMiddleware_AgentTokenWithoutConfigIsRefused (Spec 105 PR D
// critique round 1): an agent-token request that arrives while the runtime
// has NO published configuration must be refused, never forwarded. Before
// this guard the middleware passed such a request through with no
// AuthContext at all, and every scope predicate downstream reads an absent
// context as an administrator (auth.IsScopedCaller → false) — the one path
// where "no identity" widened into "full identity". A 503 keeps the
// fail-closed shape of the sibling storage-unavailable branch.
func TestMCPAuthMiddleware_AgentTokenWithoutConfigIsRefused(t *testing.T) {
	// A zero Runtime publishes no configuration (no config service, no legacy
	// config). The middleware reads only s.runtime and s.logger on this path,
	// so a bare Server is enough — publishing a nil snapshot through a real
	// runtime's config service would wake the supervisor's reconcile loop on
	// a nil config instead.
	srv := &Server{runtime: &runtime.Runtime{}, logger: zap.NewNop()}
	require.Nil(t, srv.runtime.Config(), "fixture: the runtime must publish no configuration")

	reached := false
	handler := srv.mcpAuthMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		reached = true
	}))
	req := httptest.NewRequest(http.MethodPost, "/mcp/p/research", http.NoBody)
	req.Header.Set("Authorization", "Bearer "+auth.TokenPrefixStr+"not-validated-without-config")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code, "an agent token cannot be validated without a configuration: %s", rec.Body.String())
	require.False(t, reached, "the request must not reach the MCP handler without an AuthContext")
}
