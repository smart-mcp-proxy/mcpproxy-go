package server

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/health"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/stateview"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Server.GetAllServers backs the REST fallback list and the per-server
// diagnostics endpoint (GET /api/v1/servers/{id}/diagnostics). It used to
// rebuild health without OAuthStatus, so a Connected OAuth server with a
// valid stored token and listed tools was reported "Authentication required".
// It must project the same health as the runtime (the #1522/#1525 fix for the
// MCP upstream_servers list), without weakening the missing/expired verdicts.
func TestServerGetAllServersOAuthHealth(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name        string
		token       bool
		expires     time.Time
		quarantined bool
		disabled    bool
		wantStatus  string
		wantUsable  bool
	}{
		{name: "valid token", token: true, expires: now.Add(48 * time.Hour), wantStatus: health.StatusReady, wantUsable: true},
		{name: "token without expiry", token: true, wantStatus: health.StatusReady, wantUsable: true},
		{name: "expired token", token: true, expires: now.Add(-time.Hour), wantStatus: health.StatusSignInRequired},
		{name: "missing token", wantStatus: health.StatusSignInRequired},
		// A valid token must never make a quarantined or disabled server usable.
		{name: "valid token, quarantined", token: true, expires: now.Add(48 * time.Hour), quarantined: true, wantStatus: health.StatusNeedsReview},
		{name: "valid token, disabled", token: true, expires: now.Add(48 * time.Hour), disabled: true, wantStatus: health.StatusDisabled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, rt := createTestProxyWithRuntime(t, nil)
			srv := &Server{runtime: rt, logger: zap.NewNop()}

			sc := &config.ServerConfig{Name: "synthetic-oauth", URL: "https://mcp.example.invalid/mcp", Protocol: "http", Enabled: !tc.disabled, Quarantined: tc.quarantined, OAuth: &config.OAuthConfig{}}
			require.NoError(t, rt.StorageManager().SaveUpstreamServer(sc))
			if tc.token {
				require.NoError(t, rt.StorageManager().GetBoltDB().SaveOAuthToken(&storage.OAuthTokenRecord{
					ServerName: oauth.GenerateServerKey(sc.Name, sc.URL), DisplayName: sc.Name,
					AccessToken: "synthetic-access-token", TokenType: "Bearer",
					ExpiresAt: tc.expires, Created: now, Updated: now,
				}))
			}
			rt.Supervisor().StateView().UpdateServer(sc.Name, func(s *stateview.ServerStatus) {
				s.Name, s.Config, s.Enabled, s.Quarantined = sc.Name, sc, !tc.disabled, tc.quarantined
				s.Connected, s.State = true, "ready"
				s.ToolCount = 1
				s.Tools = []stateview.ToolInfo{{Name: "synthetic_read"}}
			})

			canonical := listedHealth(t, rt.GetAllServers, sc.Name)
			assert.Equal(t, tc.wantStatus, canonical.Status, "runtime precondition")

			got := listedHealth(t, srv.GetAllServers, sc.Name)
			assert.Equal(t, tc.wantStatus, got.Status)
			assert.Equal(t, tc.wantUsable, got.Usable)
			if tc.wantUsable {
				assert.Equal(t, "Connected (1 tool)", got.Summary)
			}
			assert.Equal(t, canonical, got, "REST/diagnostics and runtime must project the same health")
		})
	}
}

func listedHealth(t *testing.T, list func() ([]map[string]interface{}, error), name string) *contracts.HealthStatus {
	t.Helper()
	servers, err := list()
	require.NoError(t, err)
	for _, entry := range servers {
		if entry["name"] == name {
			hs, ok := entry["health"].(*contracts.HealthStatus)
			require.True(t, ok, "health must be a *contracts.HealthStatus")
			require.NotNil(t, hs)
			return hs
		}
	}
	t.Fatalf("server %q missing from list", name)
	return nil
}
