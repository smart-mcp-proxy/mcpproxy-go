package managed

import (
	"context"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/types"
)

// newTestDisconnectedClient returns a managed.Client that has never
// connected, i.e. IsConnected() is false — the state newly-registered or
// dropped servers are in.
func newTestDisconnectedClient(t *testing.T) *Client {
	t.Helper()
	mc := &Client{logger: zap.NewNop()}
	mc.SetConfig(&config.ServerConfig{Name: "test-server"})
	mc.StateManager = types.NewStateManager()
	return mc
}

// newTestPromptManagedUpstream builds a real in-process MCP server that
// advertises Capabilities.Prompts and serves one "greeting" prompt.
func newTestPromptManagedUpstream(t *testing.T) *mcpserver.MCPServer {
	t.Helper()
	srv := mcpserver.NewMCPServer("test-upstream", "0.0.1",
		mcpserver.WithToolCapabilities(true),
		mcpserver.WithPromptCapabilities(true),
	)
	srv.AddPrompt(
		mcp.NewPrompt("greeting", mcp.WithPromptDescription("Say hello")),
		func(_ context.Context, _ mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			return &mcp.GetPromptResult{
				Description: "A greeting",
				Messages: []mcp.PromptMessage{
					{Role: mcp.RoleAssistant, Content: mcp.TextContent{Type: "text", Text: "hello"}},
				},
			}, nil
		},
	)
	return srv
}

// connectedManagedTestClient creates a real managed.Client wired to an
// in-process MCP test server and connects it.
func connectedManagedTestClient(t *testing.T, url string) *Client {
	t.Helper()
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")

	cfg := &config.ServerConfig{
		Name:     "test-server",
		Protocol: "streamable-http",
		URL:      url,
		Enabled:  true,
	}
	mc, err := NewClient("test-client", cfg, zap.NewNop(), nil, nil, nil, secret.NewResolver())
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, mc.Connect(ctx))
	t.Cleanup(func() { _ = mc.Disconnect() })

	return mc
}

func TestClient_ListPrompts_NotConnected_ReturnsError(t *testing.T) {
	mc := newTestDisconnectedClient(t)

	prompts, err := mc.ListPrompts(context.Background())
	require.Error(t, err)
	assert.Nil(t, prompts)
	assert.Contains(t, err.Error(), "not connected")
}

func TestClient_ListPrompts_Success(t *testing.T) {
	upstream := newTestPromptManagedUpstream(t)
	testServer := mcpserver.NewTestStreamableHTTPServer(upstream)
	defer testServer.Close()

	mc := connectedManagedTestClient(t, testServer.URL)

	prompts, err := mc.ListPrompts(context.Background())
	require.NoError(t, err)
	require.Len(t, prompts, 1)
	assert.Equal(t, "greeting", prompts[0].Name)
}

func TestClient_ListPrompts_UpstreamError(t *testing.T) {
	upstream := newTestPromptManagedUpstream(t)
	testServer := mcpserver.NewTestStreamableHTTPServer(upstream)
	mc := connectedManagedTestClient(t, testServer.URL)

	// Close the upstream out from under an established, "ready" client so the
	// next request fails at the transport layer instead of via IsConnected().
	testServer.Close()

	prompts, err := mc.ListPrompts(context.Background())
	require.Error(t, err)
	assert.Nil(t, prompts)
}

func TestClient_GetPrompt_NotConnected_ReturnsError(t *testing.T) {
	mc := newTestDisconnectedClient(t)

	result, err := mc.GetPrompt(context.Background(), "greeting", nil)
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "not connected")
}

func TestClient_GetPrompt_Success(t *testing.T) {
	upstream := newTestPromptManagedUpstream(t)
	testServer := mcpserver.NewTestStreamableHTTPServer(upstream)
	defer testServer.Close()

	mc := connectedManagedTestClient(t, testServer.URL)

	result, err := mc.GetPrompt(context.Background(), "greeting", nil)
	require.NoError(t, err)
	require.Len(t, result.Messages, 1)
	textContent, ok := result.Messages[0].Content.(mcp.TextContent)
	require.True(t, ok)
	assert.Equal(t, "hello", textContent.Text)
}

func TestClient_GetPrompt_UpstreamError(t *testing.T) {
	upstream := newTestPromptManagedUpstream(t)
	testServer := mcpserver.NewTestStreamableHTTPServer(upstream)
	mc := connectedManagedTestClient(t, testServer.URL)

	testServer.Close()

	result, err := mc.GetPrompt(context.Background(), "greeting", nil)
	require.Error(t, err)
	assert.Nil(t, result)
}