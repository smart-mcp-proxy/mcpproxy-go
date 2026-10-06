package upstream

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	servertest "github.com/mark3labs/mcp-go/server/servertest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// newBlockingToolUpstreamServer exposes one tool ("wait") whose handler
// blocks until release is closed, and signals via started when entered --
// simulating a tool call that is legitimately slow (e.g. chrome-devtools-mcp
// waiting on a human consent prompt, #1317).
func newBlockingToolUpstreamServer(started, release chan struct{}) *mcpserver.MCPServer {
	srv := mcpserver.NewMCPServer("test-upstream", "0.0.1", mcpserver.WithToolCapabilities(true))
	srv.AddTool(
		mcp.NewTool("wait", mcp.WithDescription("blocks until released")),
		func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			close(started)
			select {
			case <-release:
				return mcp.NewToolResultText("done"), nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
	)
	return srv
}

// TestRetryConnection_DoesNotKillInFlightCall is the round-6 review finding
// for #1317: Manager.RetryConnection (triggered by OAuth completion,
// config-change and token-monitor events) disconnected and reconnected a
// client directly, bypassing tryReconnect/Connect/TryReconnectSync's shared
// in-flight guard entirely. An unrelated transient error recorded on the
// client must not let RetryConnection kill a genuinely in-flight call.
func TestRetryConnection_DoesNotKillInFlightCall(t *testing.T) {
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")

	m := newTestManager(t)
	started := make(chan struct{})
	release := make(chan struct{})
	upstream := newBlockingToolUpstreamServer(started, release)
	testServer := servertest.NewTestStreamableHTTPServer(upstream)
	t.Cleanup(testServer.Close)

	cfg := &config.ServerConfig{Name: "blocking-server", Enabled: true, Protocol: "streamable-http", URL: testServer.URL}
	require.NoError(t, m.AddServerConfig("blocking-server", cfg))

	client, ok := m.GetClient("blocking-server")
	require.True(t, ok)

	connectCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, client.Connect(connectCtx))

	callDone := make(chan error, 1)
	go func() {
		_, callErr := client.CallTool(context.Background(), "wait", nil)
		callDone <- callErr
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the blocking tool call to start")
	}

	// Simulate an unrelated transient failure elsewhere on this client
	// (e.g. a concurrent health-check ping timeout) recording an ambiguous
	// error, then a retry trigger (as OAuth completion / config reload /
	// the token monitor would fire) arriving while the call above is still
	// blocked.
	client.StateManager.SetError(errors.New("context deadline exceeded"))
	require.NoError(t, m.RetryConnection("blocking-server"))

	// Give RetryConnection's background goroutine a moment to run (it would
	// disconnect almost immediately if the guard didn't hold).
	time.Sleep(200 * time.Millisecond)

	close(release) // the call finally resolves, as if consent were granted

	select {
	case callErr := <-callDone:
		assert.NoError(t, callErr,
			"the in-flight call must succeed -- if RetryConnection had disconnected mid-flight, this would error")
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for the blocked tool call to return")
	}
}
