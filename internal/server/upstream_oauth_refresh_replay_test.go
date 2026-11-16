package server

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// oauth.refresh_replay_enabled needs an explicit oauth.token_endpoint. The MCP
// add and patch doors refuse the flag without one at the write, instead of
// storing a config the next full apply rejects.
func TestHandleAddUpstream_RefreshReplayNeedsTokenEndpoint(t *testing.T) {
	p := createTestMCPProxyServer(t)

	res, err := p.handleAddUpstream(context.Background(), fwdRequest(map[string]interface{}{
		"name": "replay-bad", "url": "https://127.0.0.1:1/mcp", "protocol": "streamable-http",
		"oauth_json": `{"refresh_replay_enabled":true}`,
	}))
	require.NoError(t, err)
	require.True(t, res.IsError)
	assert.Contains(t, fwdResultText(t, res), "refresh_replay_enabled")
	servers, lerr := p.storage.ListUpstreamServers()
	require.NoError(t, lerr)
	for _, sc := range servers {
		assert.NotEqual(t, "replay-bad", sc.Name, "the refused server must not be stored")
	}

	res, err = p.handleAddUpstream(context.Background(), fwdRequest(map[string]interface{}{
		"name": "replay-ok", "url": "https://127.0.0.1:1/mcp", "protocol": "streamable-http",
		"oauth_json": `{"refresh_replay_enabled":true,"token_endpoint":"https://as.example/token"}`,
	}))
	require.NoError(t, err)
	require.False(t, res.IsError, fwdResultText(t, res))
}

func TestHandlePatchUpstream_RefreshReplayNeedsTokenEndpoint(t *testing.T) {
	p := createTestMCPProxyServer(t)
	res, err := p.handleAddUpstream(context.Background(), fwdRequest(map[string]interface{}{
		"name": "patchme", "url": "https://127.0.0.1:1/mcp", "protocol": "streamable-http",
		"oauth_json": `{"client_id":"abc"}`,
	}))
	require.NoError(t, err)
	require.False(t, res.IsError, fwdResultText(t, res))

	t.Run("flag without an endpoint refused, stored value untouched", func(t *testing.T) {
		res, _, err := p.handlePatchUpstream(context.Background(), fwdRequest(map[string]interface{}{
			"operation": "patch", "name": "patchme", "oauth_json": `{"refresh_replay_enabled":true}`,
		}))
		require.NoError(t, err)
		require.True(t, res.IsError)
		assert.Contains(t, fwdResultText(t, res), "refresh_replay_enabled")
		stored, gerr := p.storage.GetUpstreamServer("patchme")
		require.NoError(t, gerr)
		require.NotNil(t, stored.OAuth)
		assert.Nil(t, stored.OAuth.RefreshReplayEnabled)
	})
	t.Run("explicit false needs no endpoint", func(t *testing.T) {
		res, _, err := p.handlePatchUpstream(context.Background(), fwdRequest(map[string]interface{}{
			"operation": "patch", "name": "patchme", "oauth_json": `{"refresh_replay_enabled":false}`,
		}))
		require.NoError(t, err)
		require.False(t, res.IsError, fwdResultText(t, res))
	})
	t.Run("flag inherits the stored endpoint", func(t *testing.T) {
		res, _, err := p.handlePatchUpstream(context.Background(), fwdRequest(map[string]interface{}{
			"operation": "patch", "name": "patchme", "oauth_json": `{"token_endpoint":"https://as.example/token"}`,
		}))
		require.NoError(t, err)
		require.False(t, res.IsError, fwdResultText(t, res))
		res, _, err = p.handlePatchUpstream(context.Background(), fwdRequest(map[string]interface{}{
			"operation": "patch", "name": "patchme", "oauth_json": `{"refresh_replay_enabled":true}`,
		}))
		require.NoError(t, err)
		require.False(t, res.IsError, fwdResultText(t, res))
		stored, gerr := p.storage.GetUpstreamServer("patchme")
		require.NoError(t, gerr)
		require.NotNil(t, stored.OAuth.RefreshReplayEnabled)
		assert.True(t, *stored.OAuth.RefreshReplayEnabled)
		assert.Equal(t, "https://as.example/token", stored.OAuth.TokenEndpoint)
	})
}
