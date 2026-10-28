package storage

import (
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// STORE-MODE-01/02: use both real persistence paths and join queued work via
// StopAsync before reading. No polling, sleeps, or external upstreams.
func TestArgumentErrorModePersistence(t *testing.T) {
	for _, async := range []bool{false, true} {
		name := "sync"
		if async {
			name = "async"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			open := func() *Manager {
				m, err := NewManager(dir, zap.NewNop().Sugar())
				require.NoError(t, err)
				t.Cleanup(func() { _ = m.Close() })
				return m
			}
			m := open()
			control := &config.ServerConfig{Name: "control", Enabled: true, URL: "http://127.0.0.1:1/control", Headers: map[string]string{"Authorization": "synthetic-control"}}
			require.NoError(t, m.SaveUpstreamServer(control))
			controlBefore, err := m.GetUpstreamServer("control")
			require.NoError(t, err)
			for _, mode := range []string{"", "detailed", "redacted"} {
				sc := &config.ServerConfig{Name: "mode-" + mode, ToolArgumentErrorMode: mode, Quarantined: true, Enabled: false, OAuth: &config.OAuthConfig{ClientID: "synthetic-client", ClientSecret: "synthetic-secret"}}
				if async {
					m.asyncMgr.SaveServerAsync(sc)
				} else {
					require.NoError(t, m.SaveUpstreamServer(sc))
				}
			}
			invalid := &config.ServerConfig{Name: "mode-redacted", ToolArgumentErrorMode: "unknown"}
			if async {
				m.asyncMgr.SaveServerAsync(invalid)
				m.StopAsync()
			} else {
				require.ErrorContains(t, m.SaveUpstreamServer(invalid), "tool_argument_error_mode")
			}
			verify := func(m *Manager) {
				for _, mode := range []string{"", "detailed", "redacted"} {
					got, err := m.GetUpstreamServer("mode-" + mode)
					require.NoError(t, err)
					require.Equal(t, mode, got.ToolArgumentErrorMode)
					require.False(t, got.Enabled)
					require.NotNil(t, got.OAuth)
					require.Equal(t, "synthetic-secret", got.OAuth.ClientSecret)
				}
				for _, list := range []func() ([]*config.ServerConfig, error){m.ListUpstreamServers, m.ListQuarantinedUpstreamServers} {
					servers, err := list()
					require.NoError(t, err)
					found := false
					for _, sc := range servers {
						if sc.Name == "mode-redacted" {
							found = true
							require.Equal(t, "redacted", sc.ToolArgumentErrorMode)
						}
					}
					require.True(t, found)
				}
				gotControl, err := m.GetUpstreamServer("control")
				require.NoError(t, err)
				require.Equal(t, controlBefore, gotControl)
			}
			verify(m)
			require.NoError(t, m.Close())
			m = open()
			verify(m)
		})
	}
}
