package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/stateview"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// #1522: the MCP upstream_servers list built its own health input and never
// supplied the OAuth token state, so a server with a valid stored token was
// reported "Authentication required" there while REST/tray said healthy.
func TestUpstreamServersList_OAuthHealthMatchesRuntime(t *testing.T) {
	week := 7 * 24 * time.Hour
	cases := []struct {
		name         string
		expiresIn    time.Duration // 0 => no stored token
		refresh      string
		disabled     bool
		quarantined  bool
		wantHealthy  bool
		wantNotLogin bool
	}{
		{name: "valid_token_48h", expiresIn: 48 * time.Hour, wantHealthy: true, wantNotLogin: true},
		{name: "valid_token_no_expiry_info_week", expiresIn: week, wantHealthy: true, wantNotLogin: true},
		{name: "no_token"},
		{name: "expired_no_refresh", expiresIn: -time.Hour},
		{name: "disabled", expiresIn: 48 * time.Hour, disabled: true},
		{name: "quarantined", expiresIn: 48 * time.Hour, quarantined: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			proxy, rt := createTestProxyWithRuntimeCfg(t, nil, nil)
			up := startCountingUpstream(t, proxy, rt, "gh", readSpec("list_issues"))

			sc := &config.ServerConfig{
				Name: "gh", URL: up.URL, Protocol: "streamable-http",
				Enabled: !tc.disabled, Quarantined: tc.quarantined, OAuth: &config.OAuthConfig{},
			}
			require.NoError(t, proxy.storage.SaveUpstreamServer(sc))
			rt.Supervisor().StateView().UpdateServer("gh", func(s *stateview.ServerStatus) {
				s.Config = sc
				s.Enabled = sc.Enabled
				s.Quarantined = sc.Quarantined
				s.State = "connected"
				s.Connected = true
			})
			if tc.expiresIn != 0 {
				require.NoError(t, rt.StorageManager().GetBoltDB().SaveOAuthToken(&storage.OAuthTokenRecord{
					ServerName: oauth.GenerateServerKey("gh", up.URL), DisplayName: "gh",
					AccessToken: "tok", TokenType: "Bearer", RefreshToken: tc.refresh,
					ExpiresAt: time.Now().Add(tc.expiresIn), Created: time.Now(), Updated: time.Now(),
				}))
			}

			want := runtimeHealth(t, rt, "gh")
			got := listHealth(t, proxy, "gh")
			require.Equal(t, want.Level, got.Level)
			require.Equal(t, want.AdminState, got.AdminState)
			require.Equal(t, want.Summary, got.Summary)
			require.Equal(t, want.Action, got.Action)
			if tc.wantHealthy {
				require.Equal(t, "healthy", got.Level)
			}
			if tc.wantNotLogin {
				require.NotEqual(t, "login", got.Action)
			}
			if !tc.wantHealthy && !tc.disabled && !tc.quarantined {
				require.NotEqual(t, "healthy", got.Level)
			}
			if tc.disabled {
				require.Equal(t, "disabled", got.AdminState)
			}
			if tc.quarantined {
				require.Equal(t, "quarantined", got.AdminState)
			}
		})
	}
}

func runtimeHealth(t *testing.T, rt *runtime.Runtime, name string) *contracts.HealthStatus {
	t.Helper()
	servers, err := rt.GetAllServers()
	require.NoError(t, err)
	for _, s := range servers {
		if s["name"] == name {
			hs, ok := s["health"].(*contracts.HealthStatus)
			require.True(t, ok, "health type %T", s["health"])
			return hs
		}
	}
	t.Fatalf("server %q not in runtime projection", name)
	return nil
}

func listHealth(t *testing.T, proxy *MCPProxyServer, name string) *contracts.HealthStatus {
	t.Helper()
	res, err := proxy.handleUpstreamServers(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]interface{}{"operation": "list"}},
	})
	require.NoError(t, err)
	var payload struct {
		Servers []struct {
			Name   string                  `json:"name"`
			Health *contracts.HealthStatus `json:"health"`
		} `json:"servers"`
	}
	require.NoError(t, json.Unmarshal([]byte(resultText(t, res)), &payload))
	for _, s := range payload.Servers {
		if s.Name == name {
			require.NotNil(t, s.Health)
			return s.Health
		}
	}
	t.Fatalf("server %q not in MCP list: %s", name, resultText(t, res))
	return nil
}
