package contracts

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

func TestRefreshReplayReadback(t *testing.T) {
	for _, value := range []bool{false, true} {
		cfg := &config.ServerConfig{Name: "fixture", OAuth: &config.OAuthConfig{RefreshReplayEnabled: &value}}
		direct := ConvertServerConfig(cfg, nil, "disabled", false, 0, false)
		require.Equal(t, value, *direct.OAuth.RefreshReplayEnabled)
		*direct.OAuth.RefreshReplayEnabled = !value
		require.Equal(t, value, *cfg.OAuth.RefreshReplayEnabled)
		converted := ConvertGenericServersToTyped([]map[string]interface{}{{"name": "fixture", "oauth": map[string]interface{}{"refresh_replay_enabled": value}}})
		require.Len(t, converted, 1)
		require.Equal(t, value, *converted[0].OAuth.RefreshReplayEnabled)
		raw, err := json.Marshal(converted[0])
		require.NoError(t, err)
		require.Contains(t, string(raw), "refresh_replay_enabled")
	}
}
