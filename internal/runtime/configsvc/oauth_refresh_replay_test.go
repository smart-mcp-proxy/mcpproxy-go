package configsvc

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

func TestRefreshReplaySnapshotIsImmutable(t *testing.T) {
	enabled := true
	s := Snapshot{Config: &config.Config{Servers: []*config.ServerConfig{{OAuth: &config.OAuthConfig{RefreshReplayEnabled: &enabled}}}}}
	cp := s.Clone()
	*cp.Servers[0].OAuth.RefreshReplayEnabled = false
	require.True(t, enabled)
}
