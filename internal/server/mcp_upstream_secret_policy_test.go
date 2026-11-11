package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	mcp "github.com/mark3labs/mcp-go/mcp"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func withSecretFileDirsOnProxy(t *testing.T, proxy *MCPProxyServer) {
	t.Helper()
	dir := proxy.config.DataDir
	allowed := filepath.Join(dir, "secrets")
	require.NoError(t, os.MkdirAll(allowed, 0o700))
	w := config.SecretFileDirs{allowed}
	proxy.config.SecretFileDirs = &w
	require.NotNil(t, proxy.mainServer)
	_, err := proxy.mainServer.runtime.ApplyConfig(proxy.config, "")
	require.NoError(t, err)
}

func TestHandleAddUpstream_RefusesSecretPolicyBeforeSave(t *testing.T) {
	proxy := newAddFromRegistryTestServer(t)
	withSecretFileDirsOnProxy(t, proxy)

	req := mcp.CallToolRequest{}
	req.Params.Name = "upstream_servers"
	req.Params.Arguments = map[string]interface{}{
		"operation": "add",
		"name":      "policy-blocked",
		"url":       "http://127.0.0.1:9/mcp",
		"enabled":   false,
		"env_json":  `{"K":"${env:MCPPROXY_API_KEY}"}`,
	}
	res, err := proxy.handleAddUpstream(context.Background(), req)
	require.NoError(t, err)
	require.True(t, res.IsError)
	text := res.Content[0].(mcp.TextContent).Text
	assert.Contains(t, text, secret.ErrReservedEnvVar.Error())
	_, err = proxy.mainServer.runtime.StorageManager().GetUpstreamServer("policy-blocked")
	assert.Error(t, err)
}

func TestHandleUpstreamServers_Patch_RefusesSecretPolicyBeforeSave(t *testing.T) {
	proxy := newAddFromRegistryTestServer(t)
	withSecretFileDirsOnProxy(t, proxy)

	_, err := proxy.mainServer.runtime.Commit("create_server", func(tx *runtime.CommitTx) error {
		return tx.CreateServer(&config.ServerConfig{
			Name:    "s",
			URL:     "http://127.0.0.1:9/mcp",
			Enabled: false,
		})
	})
	require.NoError(t, err)

	req := mcp.CallToolRequest{}
	req.Params.Name = "upstream_servers"
	req.Params.Arguments = map[string]interface{}{
		"operation": "patch",
		"name":      "s",
		"env_json":  `{"K":"${env:MCPPROXY_API_KEY}"}`,
	}
	res, err := proxy.handleUpstreamServers(context.Background(), req)
	require.NoError(t, err)
	require.True(t, res.IsError)
	assert.Contains(t, res.Content[0].(mcp.TextContent).Text, secret.ErrReservedEnvVar.Error())
}
