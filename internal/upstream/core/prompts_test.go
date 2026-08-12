package core

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
)

func disableOAuthForTest(t *testing.T) {
	t.Helper()
	old := os.Getenv("MCPPROXY_DISABLE_OAUTH")
	os.Setenv("MCPPROXY_DISABLE_OAUTH", "true")
	t.Cleanup(func() {
		if old == "" {
			os.Unsetenv("MCPPROXY_DISABLE_OAUTH")
		} else {
			os.Setenv("MCPPROXY_DISABLE_OAUTH", old)
		}
	})
}

// newTestPromptUpstream builds a real in-process MCP server. When
// withPrompts is true it advertises Capabilities.Prompts and serves one
// "greeting" prompt; listCalls counts ListPrompts invocations so tests can
// assert the upstream was never asked when ExposePrompts opts a server out.
func newTestPromptUpstream(t *testing.T, withPrompts bool, listCalls *int) *mcpserver.MCPServer {
	t.Helper()
	opts := []mcpserver.ServerOption{mcpserver.WithToolCapabilities(true)}
	if withPrompts {
		opts = append(opts, mcpserver.WithPromptCapabilities(true))
	}
	srv := mcpserver.NewMCPServer("test-upstream", "0.0.1", opts...)
	if withPrompts {
		srv.AddPrompt(
			mcp.NewPrompt("greeting", mcp.WithPromptDescription("Say hello")),
			func(_ context.Context, _ mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
				if listCalls != nil {
					*listCalls++
				}
				return &mcp.GetPromptResult{
					Description: "A greeting",
					Messages: []mcp.PromptMessage{
						{Role: mcp.RoleAssistant, Content: mcp.TextContent{Type: "text", Text: "hello"}},
					},
				}, nil
			},
		)
	}
	return srv
}

func connectedTestClient(t *testing.T, url string, exposePrompts *bool) *Client {
	t.Helper()
	disableOAuthForTest(t)

	cfg := &config.ServerConfig{
		Name:          "test-server",
		Protocol:      "streamable-http",
		URL:           url,
		Enabled:       true,
		ExposePrompts: exposePrompts,
	}
	c, err := NewClient("test-client", cfg, zap.NewNop(), nil, nil, nil, secret.NewResolver())
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, c.Connect(ctx))
	t.Cleanup(func() { _ = c.Disconnect() })

	return c
}

func TestClient_ListPrompts_ReturnsUpstreamPrompts(t *testing.T) {
	upstream := newTestPromptUpstream(t, true, nil)
	testServer := mcpserver.NewTestStreamableHTTPServer(upstream)
	defer testServer.Close()

	c := connectedTestClient(t, testServer.URL, nil)

	prompts, err := c.ListPrompts(context.Background())
	require.NoError(t, err)
	require.Len(t, prompts, 1)
	assert.Equal(t, "greeting", prompts[0].Name)
}

func TestClient_ListPrompts_NoCapability_ReturnsNil(t *testing.T) {
	upstream := newTestPromptUpstream(t, false, nil)
	testServer := mcpserver.NewTestStreamableHTTPServer(upstream)
	defer testServer.Close()

	c := connectedTestClient(t, testServer.URL, nil)

	prompts, err := c.ListPrompts(context.Background())
	require.NoError(t, err)
	assert.Nil(t, prompts)
}

func TestClient_ListPrompts_ExposePromptsFalse_SkipsUpstreamCall(t *testing.T) {
	calls := 0
	upstream := newTestPromptUpstream(t, true, &calls)
	testServer := mcpserver.NewTestStreamableHTTPServer(upstream)
	defer testServer.Close()

	c := connectedTestClient(t, testServer.URL, config.BoolPtr(false))

	prompts, err := c.ListPrompts(context.Background())
	require.NoError(t, err)
	assert.Nil(t, prompts)
	assert.Equal(t, 0, calls, "GetPrompt handler on the upstream should never be invoked by ListPrompts")
}

func TestClient_GetPrompt_ReturnsUpstreamResult(t *testing.T) {
	upstream := newTestPromptUpstream(t, true, nil)
	testServer := mcpserver.NewTestStreamableHTTPServer(upstream)
	defer testServer.Close()

	c := connectedTestClient(t, testServer.URL, nil)

	result, err := c.GetPrompt(context.Background(), "greeting", nil)
	require.NoError(t, err)
	require.Len(t, result.Messages, 1)
	textContent, ok := result.Messages[0].Content.(mcp.TextContent)
	require.True(t, ok)
	assert.Equal(t, "hello", textContent.Text)
}

func unconnectedTestClient(t *testing.T) *Client {
	t.Helper()
	cfg := &config.ServerConfig{Name: "test-server", Protocol: "streamable-http", URL: "http://127.0.0.1:1"}
	c, err := NewClient("test-client", cfg, zap.NewNop(), nil, nil, nil, secret.NewResolver())
	require.NoError(t, err)
	return c
}

func TestClient_ListPrompts_NotConnected_ReturnsError(t *testing.T) {
	c := unconnectedTestClient(t)

	prompts, err := c.ListPrompts(context.Background())
	require.Error(t, err)
	assert.Nil(t, prompts)
	assert.Contains(t, err.Error(), "not connected")
}

func TestClient_ListPrompts_ServerInfoNil_ReturnsError(t *testing.T) {
	upstream := newTestPromptUpstream(t, true, nil)
	testServer := mcpserver.NewTestStreamableHTTPServer(upstream)
	defer testServer.Close()

	c := connectedTestClient(t, testServer.URL, nil)
	// White-box: force the post-handshake server info to nil while leaving
	// the transport connected, to exercise the defensive nil-check that
	// distinguishes "never initialized" from "no Prompts capability".
	c.mu.Lock()
	c.serverInfo = nil
	c.mu.Unlock()

	prompts, err := c.ListPrompts(context.Background())
	require.Error(t, err)
	assert.Nil(t, prompts)
	assert.Contains(t, err.Error(), "server info not available")
}

func TestClient_ListPrompts_UpstreamError_ReturnsWrappedError(t *testing.T) {
	upstream := newTestPromptUpstream(t, true, nil)
	testServer := mcpserver.NewTestStreamableHTTPServer(upstream)
	c := connectedTestClient(t, testServer.URL, nil)

	// Close the upstream out from under an already-connected client so the
	// next request fails at the transport layer instead of via IsConnected().
	testServer.Close()

	prompts, err := c.ListPrompts(context.Background())
	require.Error(t, err)
	assert.Nil(t, prompts)
	assert.Contains(t, err.Error(), "failed to list prompts")
}

func TestClient_GetPrompt_NotConnected_ReturnsError(t *testing.T) {
	c := unconnectedTestClient(t)

	result, err := c.GetPrompt(context.Background(), "greeting", nil)
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "not connected")
}

func TestClient_GetPrompt_UpstreamError_ReturnsWrappedError(t *testing.T) {
	upstream := newTestPromptUpstream(t, true, nil)
	testServer := mcpserver.NewTestStreamableHTTPServer(upstream)
	c := connectedTestClient(t, testServer.URL, nil)

	testServer.Close()

	result, err := c.GetPrompt(context.Background(), "greeting", nil)
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "GetPrompt failed")
}