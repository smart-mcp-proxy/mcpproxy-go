package server

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/health"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/stateview"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/types"
)

// A stored, valid OAuth token must not become "Authentication required" on
// upstream_servers/list just because that projection omitted OAuthStatus.
// These fixtures exercise the MCP dispatcher and the real runtime projection
// without connecting to an upstream or reading the operator's token store.
func TestMCPUpstreamListOAuthHealth(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name          string
		token         bool
		expires       time.Time
		refresh       bool
		autodiscovery bool
		staticAuth    bool
		loggedOut     bool
		disabled      bool
		quarantined   bool
		lastError     string
		state         types.ConnectionState
		wantStatus    string
		wantUsable    bool
		wantLevel     string
		wantAction    string
	}{
		{name: "authenticated", token: true, expires: now.Add(48 * time.Hour), state: types.StateReady, wantStatus: health.StatusReady, wantUsable: true, wantLevel: health.LevelHealthy, wantAction: health.ActionNone},
		{name: "autodiscovery authenticated", token: true, expires: now.Add(48 * time.Hour), autodiscovery: true, state: types.StateReady, wantStatus: health.StatusReady, wantUsable: true, wantLevel: health.LevelHealthy, wantAction: health.ActionNone},
		{name: "no expiry", token: true, state: types.StateReady, wantStatus: health.StatusReady, wantUsable: true, wantLevel: health.LevelHealthy, wantAction: health.ActionNone},
		{name: "expiring with refresh", token: true, expires: now.Add(30 * time.Minute), refresh: true, state: types.StateReady, wantStatus: health.StatusReady, wantUsable: true, wantLevel: health.LevelHealthy, wantAction: health.ActionNone},
		{name: "expiring without refresh", token: true, expires: now.Add(30 * time.Minute), state: types.StateReady, wantStatus: health.StatusReady, wantUsable: true, wantLevel: health.LevelDegraded, wantAction: health.ActionLogin},
		{name: "missing token", state: types.StateReady, wantStatus: health.StatusSignInRequired, wantLevel: health.LevelUnhealthy, wantAction: health.ActionLogin},
		{name: "expired token", token: true, expires: now.Add(-time.Hour), state: types.StateReady, wantStatus: health.StatusSignInRequired, wantLevel: health.LevelUnhealthy, wantAction: health.ActionLogin},
		{name: "OAuth error", token: true, expires: now.Add(48 * time.Hour), lastError: "OAuth authorization failed: invalid_grant", state: types.StateReady, wantStatus: health.StatusSignInRequired, wantLevel: health.LevelUnhealthy, wantAction: health.ActionLogin},
		{name: "explicit logout", token: true, expires: now.Add(48 * time.Hour), loggedOut: true, state: types.StateReady, wantStatus: health.StatusSignInRequired, wantLevel: health.LevelUnhealthy, wantAction: health.ActionLogin},
		{name: "effective rejection", token: true, expires: now.Add(48 * time.Hour), lastError: "OAuth authorization required: unexpected status 401 unauthorized", state: types.StateError, wantStatus: health.StatusSignInRequired, wantLevel: health.LevelUnhealthy, wantAction: health.ActionLogin},
		{name: "static auth ignores stale token", token: true, expires: now.Add(-time.Hour), autodiscovery: true, staticAuth: true, state: types.StateReady, wantStatus: health.StatusReady, wantUsable: true, wantLevel: health.LevelHealthy, wantAction: health.ActionNone},
		{name: "disabled with valid token", token: true, expires: now.Add(48 * time.Hour), disabled: true, state: types.StateReady, wantStatus: health.StatusDisabled, wantLevel: health.LevelHealthy, wantAction: health.ActionEnable},
		{name: "quarantined with valid token", token: true, expires: now.Add(48 * time.Hour), quarantined: true, state: types.StateReady, wantStatus: health.StatusNeedsReview, wantLevel: health.LevelHealthy, wantAction: health.ActionApprove},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proxy, rt := createTestProxyWithRuntime(t, nil)
			sc := &config.ServerConfig{Name: "synthetic-oauth", URL: "https://mcp.example.invalid/mcp", Protocol: "http", Enabled: !tc.disabled, Quarantined: tc.quarantined}
			if !tc.autodiscovery {
				sc.OAuth = &config.OAuthConfig{}
			}
			if tc.staticAuth {
				sc.Headers = map[string]string{"Authorization": "Bearer synthetic-static-token"}
			}
			require.NoError(t, rt.StorageManager().SaveUpstreamServer(sc))
			proxy.upstreamManager = rt.UpstreamManager()
			require.NoError(t, proxy.upstreamManager.AddServerConfig(sc.Name, sc))
			client, ok := proxy.upstreamManager.GetClient(sc.Name)
			require.True(t, ok)
			client.StateManager.TransitionTo(types.StateConnecting)
			client.StateManager.TransitionTo(types.StateReady)
			if tc.lastError != "" {
				client.StateManager.SetError(errors.New(tc.lastError))
				if tc.state != types.StateError {
					client.StateManager.TransitionTo(tc.state)
				}
			}
			client.SetUserLoggedOut(tc.loggedOut)
			if tc.token {
				refresh := ""
				if tc.refresh {
					refresh = "synthetic-refresh-token"
				}
				require.NoError(t, rt.StorageManager().GetBoltDB().SaveOAuthToken(&storage.OAuthTokenRecord{
					ServerName: oauth.GenerateServerKey(sc.Name, sc.URL), DisplayName: sc.Name,
					AccessToken: "synthetic-access-token", RefreshToken: refresh, TokenType: "Bearer",
					ExpiresAt: tc.expires, Created: now, Updated: now,
				}))
			}
			rt.Supervisor().StateView().UpdateServer(sc.Name, func(s *stateview.ServerStatus) {
				s.Name, s.Config, s.Enabled = sc.Name, sc, sc.Enabled
				s.Quarantined = sc.Quarantined
				s.Connected = tc.state == types.StateReady
				s.State, s.LastError = tc.state.String(), tc.lastError
				s.ToolCount = 1
				s.Tools = []stateview.ToolInfo{{Name: "synthetic_read"}}
			})

			servers, err := rt.GetAllServers()
			require.NoError(t, err)
			require.Len(t, servers, 1)
			canonical, ok := servers[0]["health"].(*contracts.HealthStatus)
			require.True(t, ok)
			assert.Equal(t, tc.wantStatus, canonical.Status, "runtime precondition")
			assert.Equal(t, tc.wantUsable, canonical.Usable, "runtime precondition")

			got := mcpListedHealth(t, proxy, context.Background(), sc.Name)
			assert.Equal(t, tc.wantStatus, got.Status)
			assert.Equal(t, tc.wantUsable, got.Usable)
			assert.Equal(t, tc.wantLevel, got.Level)
			assert.Equal(t, tc.wantAction, got.Action)
			if tc.wantLevel == health.LevelHealthy && tc.wantStatus == health.StatusReady {
				assert.Equal(t, "Connected (1 tool)", got.Summary)
			}
			assert.Equal(t, canonical, got, "MCP and runtime must project the same health")
		})
	}
}

func TestMCPUpstreamListRuntimeHealthRedactionAndScope(t *testing.T) {
	proxy, rt := createTestProxyWithRuntime(t, nil)
	for _, name := range []string{"visible", "hidden"} {
		sc := &config.ServerConfig{Name: name, URL: "https://mcp.example.invalid/" + name, Protocol: "http", Enabled: true, OAuth: &config.OAuthConfig{}}
		require.NoError(t, rt.StorageManager().SaveUpstreamServer(sc))
		rt.Supervisor().StateView().UpdateServer(name, func(s *stateview.ServerStatus) {
			s.Name, s.Config, s.Enabled = name, sc, true
			s.State = "error"
			s.LastError = "OAuth invalid_grant at https://mcp.example.invalid/mcp?token=synthetic-url-secret"
		})
	}
	ctx := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type: auth.AuthTypeAgent, AgentName: "synthetic-agent", AllowedServers: []string{"visible"}, Permissions: []string{auth.PermRead},
	})
	got := mcpListedHealth(t, proxy, ctx, "visible")
	assert.Equal(t, health.StatusSignInRequired, got.Status)
	assert.False(t, got.Usable)
	assert.Contains(t, got.Detail, "mcp.example.invalid")
	assert.NotContains(t, got.Detail, "synthetic-url-secret")

	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]interface{}{"operation": "list"}
	result, err := proxy.handleUpstreamServers(ctx, request)
	require.NoError(t, err)
	require.False(t, result.IsError)
	content, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.NotContains(t, content.Text, "hidden", "canonical health must not expand server visibility")
	assert.NotContains(t, content.Text, "synthetic-url-secret")
}

func TestMCPUpstreamListHealthWithoutRuntimeSnapshot(t *testing.T) {
	for _, wired := range []bool{false, true} {
		t.Run(map[bool]string{false: "no runtime", true: "snapshot not hydrated"}[wired], func(t *testing.T) {
			var proxy *MCPProxyServer
			if wired {
				proxy, _ = createTestProxyWithRuntime(t, nil)
			} else {
				proxy = createTestMCPProxyServer(t)
			}
			sc := &config.ServerConfig{Name: "unstarted", URL: "https://mcp.example.invalid/mcp", Enabled: true, OAuth: &config.OAuthConfig{}}
			require.NoError(t, proxy.storage.SaveUpstreamServer(sc))
			got := mcpListedHealth(t, proxy, context.Background(), sc.Name)
			assert.False(t, got.Usable, "absence of canonical state must never imply authentication")
			assert.Equal(t, health.StatusError, got.Status)
		})
	}
}

func mcpListedHealth(t *testing.T, proxy *MCPProxyServer, ctx context.Context, name string) *contracts.HealthStatus {
	t.Helper()
	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]interface{}{"operation": "list"}
	result, err := proxy.handleUpstreamServers(ctx, request)
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Len(t, result.Content, 1)
	content, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	var listed struct {
		Servers []struct {
			Name   string                  `json:"name"`
			Health *contracts.HealthStatus `json:"health"`
		} `json:"servers"`
	}
	require.NoError(t, json.Unmarshal([]byte(content.Text), &listed))
	for _, server := range listed.Servers {
		if server.Name == name {
			require.NotNil(t, server.Health)
			return server.Health
		}
	}
	t.Fatalf("server %q missing from MCP list", name)
	return nil
}
