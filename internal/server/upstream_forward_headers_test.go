package server

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

func fwdRequest(args map[string]interface{}) mcp.CallToolRequest {
	r := mcp.CallToolRequest{}
	r.Params.Arguments = args
	return r
}

func fwdResultText(t *testing.T, r *mcp.CallToolResult) string {
	t.Helper()
	require.NotNil(t, r)
	require.NotEmpty(t, r.Content)
	tc, ok := r.Content[0].(mcp.TextContent)
	require.True(t, ok)
	return tc.Text
}

// Spec 112 FR-020: the upstream_servers patch tool maps forward_headers_json
// into the patch: nil when omitted, non-nil for an array, non-nil empty for [].
func TestBuildPatchConfig_ForwardHeaders(t *testing.T) {
	proxy, _ := createTestProxyWithRuntime(t, nil)
	existing := &config.ServerConfig{Name: "srv", Protocol: "streamable-http", Enabled: true, ForwardHeaders: []string{"X-Old"}}

	t.Run("omitted leaves nil", func(t *testing.T) {
		patch, _, err := proxy.buildPatchConfigFromRequest(fwdRequest(map[string]interface{}{"operation": "patch", "name": "srv", "enabled": true}), existing)
		require.NoError(t, err)
		assert.Nil(t, patch.ForwardHeaders)
	})
	t.Run("array replaces", func(t *testing.T) {
		patch, _, err := proxy.buildPatchConfigFromRequest(fwdRequest(map[string]interface{}{
			"operation": "patch", "name": "srv", "forward_headers_json": `["X-User-Id","X-Tenant-Id"]`,
		}), existing)
		require.NoError(t, err)
		assert.Equal(t, []string{"X-User-Id", "X-Tenant-Id"}, patch.ForwardHeaders)
	})
	t.Run("empty array clears", func(t *testing.T) {
		patch, _, err := proxy.buildPatchConfigFromRequest(fwdRequest(map[string]interface{}{
			"operation": "patch", "name": "srv", "forward_headers_json": `[]`,
		}), existing)
		require.NoError(t, err)
		require.NotNil(t, patch.ForwardHeaders)
		assert.Empty(t, patch.ForwardHeaders)
	})
	t.Run("malformed JSON is an error", func(t *testing.T) {
		_, _, err := proxy.buildPatchConfigFromRequest(fwdRequest(map[string]interface{}{
			"operation": "patch", "name": "srv", "forward_headers_json": `{"a":1}`,
		}), existing)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "forward_headers_json")
	})
}

func TestHandleAddUpstream_ForwardHeaders(t *testing.T) {
	p := createTestMCPProxyServer(t)

	t.Run("valid allowlist is stored", func(t *testing.T) {
		res, err := p.handleAddUpstream(context.Background(), fwdRequest(map[string]interface{}{
			"name": "fwd-ok", "url": "https://127.0.0.1:1/mcp", "protocol": "streamable-http",
			"forward_headers_json": `["X-User-Id","x-tenant-id"]`,
		}))
		require.NoError(t, err)
		require.False(t, res.IsError, fwdResultText(t, res))
		stored, err := p.storage.GetUpstreamServer("fwd-ok")
		require.NoError(t, err)
		assert.Equal(t, []string{"X-User-Id", "x-tenant-id"}, stored.ForwardHeaders)
	})

	for name, tc := range map[string]struct {
		json    string
		headers string
	}{
		"denied":    {json: `["Authorization"]`},
		"wildcard":  {json: `["X-*"]`},
		"static":    {json: `["X-Thing"]`, headers: `{"x-thing":"topsecret"}`},
		"not-array": {json: `"X-User-Id"`},
	} {
		t.Run("refuses "+name, func(t *testing.T) {
			args := map[string]interface{}{
				"name": "fwd-bad-" + name, "url": "https://127.0.0.1:1/mcp", "protocol": "streamable-http",
				"forward_headers_json": tc.json,
			}
			if tc.headers != "" {
				args["headers_json"] = tc.headers
			}
			res, err := p.handleAddUpstream(context.Background(), fwdRequest(args))
			require.NoError(t, err)
			require.True(t, res.IsError)
			text := fwdResultText(t, res)
			assert.Contains(t, text, "forward_headers")
			assert.NotContains(t, text, "topsecret")
			servers, lerr := p.storage.ListUpstreamServers()
			require.NoError(t, lerr)
			for _, sc := range servers {
				assert.NotEqual(t, "fwd-bad-"+name, sc.Name, "the refused server must not be stored")
			}
		})
	}
}

func TestHandlePatchUpstream_ForwardHeadersValidation(t *testing.T) {
	p := createTestMCPProxyServer(t)
	res, err := p.handleAddUpstream(context.Background(), fwdRequest(map[string]interface{}{
		"name": "patchme", "url": "https://127.0.0.1:1/mcp", "protocol": "streamable-http",
		"headers_json": `{"X-Static":"v"}`, "forward_headers_json": `["X-User-Id"]`,
	}))
	require.NoError(t, err)
	require.False(t, res.IsError, fwdResultText(t, res))

	t.Run("denied name refused, stored value untouched", func(t *testing.T) {
		res, _, err := p.handlePatchUpstream(context.Background(), fwdRequest(map[string]interface{}{
			"operation": "patch", "name": "patchme", "forward_headers_json": `["Cookie"]`,
		}))
		require.NoError(t, err)
		require.True(t, res.IsError)
		stored, gerr := p.storage.GetUpstreamServer("patchme")
		require.NoError(t, gerr)
		assert.Equal(t, []string{"X-User-Id"}, stored.ForwardHeaders)
	})
	t.Run("headers change colliding with the allowlist is refused", func(t *testing.T) {
		res, _, err := p.handlePatchUpstream(context.Background(), fwdRequest(map[string]interface{}{
			"operation": "patch", "name": "patchme", "headers_json": `{"x-user-id":"v"}`,
		}))
		require.NoError(t, err)
		require.True(t, res.IsError)
	})
	t.Run("omitted keeps, [] clears", func(t *testing.T) {
		res, _, err := p.handlePatchUpstream(context.Background(), fwdRequest(map[string]interface{}{
			"operation": "patch", "name": "patchme", "enabled": false,
		}))
		require.NoError(t, err)
		require.False(t, res.IsError, fwdResultText(t, res))
		stored, gerr := p.storage.GetUpstreamServer("patchme")
		require.NoError(t, gerr)
		assert.Equal(t, []string{"X-User-Id"}, stored.ForwardHeaders)

		res, _, err = p.handlePatchUpstream(context.Background(), fwdRequest(map[string]interface{}{
			"operation": "patch", "name": "patchme", "forward_headers_json": `[]`,
		}))
		require.NoError(t, err)
		require.False(t, res.IsError, fwdResultText(t, res))
		stored, gerr = p.storage.GetUpstreamServer("patchme")
		require.NoError(t, gerr)
		assert.Empty(t, stored.ForwardHeaders)
	})
}
