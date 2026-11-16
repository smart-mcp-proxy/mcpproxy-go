package upstream

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

func TestRefreshReplayManagerCloneIsImmutable(t *testing.T) {
	enabled := true
	cfg := &config.ServerConfig{OAuth: &config.OAuthConfig{RefreshReplayEnabled: &enabled}}
	cp := cloneServerConfig(cfg)
	*cp.OAuth.RefreshReplayEnabled = false
	require.True(t, enabled)
}
