package core

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	servertest "github.com/mark3labs/mcp-go/server/servertest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
)

func disableOAuthForTest(t *testing.T) {
	t.Helper()
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")
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
	testServer := servertest.NewTestStreamableHTTPServer(upstream)
	defer testServer.Close()

	c := connectedTestClient(t, testServer.URL, nil)

	prompts, err := c.ListPrompts(context.Background())
	require.NoError(t, err)
	require.Len(t, prompts, 1)
	assert.Equal(t, "greeting", prompts[0].Name)
}

func TestClient_ListPrompts_NoCapability_ReturnsNil(t *testing.T) {
	upstream := newTestPromptUpstream(t, false, nil)
	testServer := servertest.NewTestStreamableHTTPServer(upstream)
	defer testServer.Close()

	c := connectedTestClient(t, testServer.URL, nil)

	prompts, err := c.ListPrompts(context.Background())
	require.NoError(t, err)
	assert.Nil(t, prompts)
}

func TestClient_ListPrompts_ExposePromptsFalse_SkipsUpstreamCall(t *testing.T) {
	calls := 0
	upstream := newTestPromptUpstream(t, true, &calls)
	testServer := servertest.NewTestStreamableHTTPServer(upstream)
	defer testServer.Close()

	c := connectedTestClient(t, testServer.URL, config.BoolPtr(false))

	prompts, err := c.ListPrompts(context.Background())
	require.NoError(t, err)
	assert.Nil(t, prompts)
	assert.Equal(t, 0, calls, "GetPrompt handler on the upstream should never be invoked by ListPrompts")
}

// TestClient_SetExposePrompts_TakesEffectWithoutReconnect is the regression
// test for PR #973 review's P2 finding: c.config is set once in NewClient
// and never reassigned, so ListPrompts previously kept enforcing whatever
// ExposePrompts value was in effect when the connection was created — a
// hot-reloaded toggle only took effect after a reconnect. SetExposePrompts
// must flip the gate on the same live connection.
func TestClient_SetExposePrompts_TakesEffectWithoutReconnect(t *testing.T) {
	calls := 0
	upstream := newTestPromptUpstream(t, true, &calls)
	testServer := servertest.NewTestStreamableHTTPServer(upstream)
	defer testServer.Close()

	c := connectedTestClient(t, testServer.URL, nil)

	prompts, err := c.ListPrompts(context.Background())
	require.NoError(t, err)
	assert.NotEmpty(t, prompts, "prompts must be exposed before the toggle")

	exposePrompts := false
	c.SetExposePrompts(&exposePrompts)

	prompts, err = c.ListPrompts(context.Background())
	require.NoError(t, err)
	assert.Nil(t, prompts, "SetExposePrompts(false) must take effect on the same live connection")

	exposePromptsTrue := true
	c.SetExposePrompts(&exposePromptsTrue)

	prompts, err = c.ListPrompts(context.Background())
	require.NoError(t, err)
	assert.NotEmpty(t, prompts, "SetExposePrompts(true) must re-enable prompts without a reconnect")
}

func TestClient_GetPrompt_ReturnsUpstreamResult(t *testing.T) {
	upstream := newTestPromptUpstream(t, true, nil)
	testServer := servertest.NewTestStreamableHTTPServer(upstream)
	defer testServer.Close()

	c := connectedTestClient(t, testServer.URL, nil)

	result, err := c.GetPrompt(context.Background(), "greeting", nil)
	require.NoError(t, err)
	require.Len(t, result.Messages, 1)
	textContent, ok := result.Messages[0].Content.(mcp.TextContent)
	require.True(t, ok)
	assert.Equal(t, "hello", textContent.Text)
}

// TestClient_GetPrompt_ExposePromptsFalse_ReturnsErrorNoUpstreamCall is the
// PR #973 finding F3 regression: expose_prompts:false was enforced only at
// list time, so GetPrompt still forwarded to the upstream. It must now fail
// closed with an error and never dispatch.
func TestClient_GetPrompt_ExposePromptsFalse_ReturnsErrorNoUpstreamCall(t *testing.T) {
	calls := 0
	upstream := newTestPromptUpstream(t, true, &calls)
	testServer := servertest.NewTestStreamableHTTPServer(upstream)
	defer testServer.Close()

	c := connectedTestClient(t, testServer.URL, config.BoolPtr(false))

	result, err := c.GetPrompt(context.Background(), "greeting", nil)
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "not exposed")
	assert.Equal(t, 0, calls, "opted-out GetPrompt must never reach the upstream prompt handler")
}

// TestClient_GetPrompt_SetExposePromptsFalse_TakesEffectWithoutReconnect is the
// GetPrompt twin of TestClient_SetExposePrompts_TakesEffectWithoutReconnect: a
// hot-reloaded opt-out must gate GetPrompt on the same live connection, not just
// ListPrompts.
func TestClient_GetPrompt_SetExposePromptsFalse_TakesEffectWithoutReconnect(t *testing.T) {
	calls := 0
	upstream := newTestPromptUpstream(t, true, &calls)
	testServer := servertest.NewTestStreamableHTTPServer(upstream)
	defer testServer.Close()

	c := connectedTestClient(t, testServer.URL, nil)

	result, err := c.GetPrompt(context.Background(), "greeting", nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 1, calls)

	exposePrompts := false
	c.SetExposePrompts(&exposePrompts)

	result, err = c.GetPrompt(context.Background(), "greeting", nil)
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "not exposed")
	assert.Equal(t, 1, calls, "flip to expose_prompts:false must block GetPrompt without a reconnect")
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
	testServer := servertest.NewTestStreamableHTTPServer(upstream)
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
	testServer := servertest.NewTestStreamableHTTPServer(upstream)
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
	testServer := servertest.NewTestStreamableHTTPServer(upstream)
	c := connectedTestClient(t, testServer.URL, nil)

	testServer.Close()

	result, err := c.GetPrompt(context.Background(), "greeting", nil)
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "GetPrompt failed")
}

// --- Finding F14: bounded pagination ---

// newPaginatedPromptUpstream builds an in-process MCP server advertising `count`
// prompts and paginating prompts/list at `pageLimit` per page (mcp-go sets
// NextCursor whenever a page is full), so ListPrompts must follow the cursor
// across pages to see them all.
func newPaginatedPromptUpstream(t *testing.T, count, pageLimit int) *mcpserver.MCPServer {
	t.Helper()
	srv := mcpserver.NewMCPServer("test-upstream", "0.0.1",
		mcpserver.WithToolCapabilities(true),
		mcpserver.WithPromptCapabilities(true),
		mcpserver.WithPaginationLimit(pageLimit),
	)
	for i := 0; i < count; i++ {
		srv.AddPrompt(
			mcp.NewPrompt(fmt.Sprintf("p%04d", i), mcp.WithPromptDescription("gen")),
			func(_ context.Context, _ mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
				return &mcp.GetPromptResult{
					Messages: []mcp.PromptMessage{
						{Role: mcp.RoleAssistant, Content: mcp.TextContent{Type: "text", Text: "x"}},
					},
				}, nil
			},
		)
	}
	return srv
}

func TestClient_ListPrompts_FollowsPaginationAcrossPages(t *testing.T) {
	upstream := newPaginatedPromptUpstream(t, 5, 2) // 3 pages: [2][2][1]
	testServer := servertest.NewTestStreamableHTTPServer(upstream)
	defer testServer.Close()

	c := connectedTestClient(t, testServer.URL, nil)

	prompts, err := c.ListPrompts(context.Background())
	require.NoError(t, err)
	assert.Len(t, prompts, 5, "all three pages must be aggregated")
}

func TestClient_ListPrompts_StopsAtItemCap(t *testing.T) {
	upstream := newPaginatedPromptUpstream(t, 250, 50) // 5 pages available
	testServer := servertest.NewTestStreamableHTTPServer(upstream)
	defer testServer.Close()

	c := connectedTestClient(t, testServer.URL, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	prompts, err := c.ListPrompts(ctx)
	require.NoError(t, err)
	assert.Len(t, prompts, maxListPromptsItems, "must stop at the item cap, not fetch all 250")
}

func TestClient_ListPrompts_EndlessCursorTerminatesAtPageCap(t *testing.T) {
	// 60 prompts at 1/page => a NextCursor on every page; the page cap must cut it.
	upstream := newPaginatedPromptUpstream(t, maxListPromptsPages+10, 1)
	testServer := servertest.NewTestStreamableHTTPServer(upstream)
	defer testServer.Close()

	c := connectedTestClient(t, testServer.URL, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	prompts, err := c.ListPrompts(ctx)
	require.NoError(t, err)
	assert.Len(t, prompts, maxListPromptsPages, "endless-cursor upstream must terminate at the page cap")
}

// TestClient_HandlePromptsListChanged_FiresCallback is the F13 wiring test: the
// core prompts/list_changed handler must invoke the onPromptsChanged callback
// with the server name. (Real server->client push delivery is covered by manual
// QA; this asserts the dispatch + callback the proxy relies on.)
func TestClient_HandlePromptsListChanged_FiresCallback(t *testing.T) {
	upstream := newTestPromptUpstream(t, true, nil)
	testServer := servertest.NewTestStreamableHTTPServer(upstream)
	defer testServer.Close()

	c := connectedTestClient(t, testServer.URL, nil)

	got := make(chan string, 1)
	c.SetOnPromptsChangedCallback(func(serverName string) {
		got <- serverName
	})

	c.handlePromptsListChangedNotification()

	select {
	case name := <-got:
		assert.Equal(t, c.config.Name, name)
	case <-time.After(time.Second):
		t.Fatal("onPromptsChanged callback did not fire")
	}
}

// TestClient_HandlePromptsListChanged_NilCallbackNoPanic ensures the handler is
// safe when no callback is registered.
func TestClient_HandlePromptsListChanged_NilCallbackNoPanic(t *testing.T) {
	upstream := newTestPromptUpstream(t, true, nil)
	testServer := servertest.NewTestStreamableHTTPServer(upstream)
	defer testServer.Close()

	c := connectedTestClient(t, testServer.URL, nil)
	c.handlePromptsListChangedNotification() // must not panic
}
