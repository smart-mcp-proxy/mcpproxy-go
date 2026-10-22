package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

func newRejectQueryTestServer(t *testing.T, reject bool) *Server {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.Listen = "127.0.0.1:0"
	cfg.APIKey = "test-api-key-reject-query"
	cfg.MCPRejectQueryAPIKey = reject
	srv, err := NewServer(cfg, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Shutdown() })
	return srv
}

// With mcp_reject_query_apikey a URL-borne credential is a 401 on every MCP
// path, and the downstream handler never runs. It must not degrade to the
// anonymous administrator that an unrecognised credential gets when
// require_mcp_auth is off.
func TestMCPAuthMiddleware_RejectQueryAPIKey(t *testing.T) {
	srv := newRejectQueryTestServer(t, true)

	for _, path := range []string{"/mcp", "/mcp/all", "/mcp/code"} {
		t.Run("query credential refused on "+path, func(t *testing.T) {
			for _, q := range []string{"?apikey=test-api-key-reject-query", "?apikey=", "?apikey=mcp_agt_whatever"} {
				seen, code := captureAuthContext(t, srv, httptest.NewRequest(http.MethodPost, path+q, http.NoBody))
				assert.Equal(t, http.StatusUnauthorized, code, q)
				assert.Nil(t, seen, "handler must not run for %s", q)
			}
		})
	}

	t.Run("header credentials still work", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/mcp", http.NoBody)
		req.Header.Set("X-API-Key", "test-api-key-reject-query")
		seen, code := captureAuthContext(t, srv, req)
		require.Equal(t, http.StatusOK, code)
		require.NotNil(t, seen)
		assert.True(t, seen.IsAdmin())
		assert.False(t, seen.Anonymous)

		req = httptest.NewRequest(http.MethodPost, "/mcp", http.NoBody)
		req.Header.Set("Authorization", "Bearer test-api-key-reject-query")
		seen, code = captureAuthContext(t, srv, req)
		require.Equal(t, http.StatusOK, code)
		require.NotNil(t, seen)
		assert.False(t, seen.Anonymous)
	})

	t.Run("other query parameters are untouched", func(t *testing.T) {
		seen, code := captureAuthContext(t, srv, httptest.NewRequest(http.MethodPost, "/mcp?profile=work", http.NoBody))
		require.Equal(t, http.StatusOK, code)
		require.NotNil(t, seen)
	})
}

// The default keeps the query form working, so existing clients that can only
// configure a URL are unaffected.
func TestMCPAuthMiddleware_QueryAPIKeyAcceptedByDefault(t *testing.T) {
	srv := newRejectQueryTestServer(t, false)

	seen, code := captureAuthContext(t, srv, httptest.NewRequest(http.MethodPost, "/mcp?apikey=test-api-key-reject-query", http.NoBody))
	require.Equal(t, http.StatusOK, code)
	require.NotNil(t, seen)
	assert.True(t, seen.IsAdmin())
	assert.False(t, seen.Anonymous, "the query credential must still authenticate by default")
}
