package runtime

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

func captureTestServer(name, tool string) *mcpserver.StreamableHTTPServer {
	upstream := mcpserver.NewMCPServer(name, "0.0.1", mcpserver.WithToolCapabilities(true))
	upstream.AddTool(mcp.NewTool(tool, mcp.WithDescription(name+" definition")), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	})
	return mcpserver.NewStreamableHTTPServer(upstream)
}

// TestCaptureQuarantinedToolDefinitions_RelistsAfterClientReplacement proves
// that a review capture cannot persist a tools/list result from connection A
// after the supervisor has observed replacement connection B. This is separate
// from normal discovery because quarantined captures deliberately do not
// publish into StateView or the index.
func TestCaptureQuarantinedToolDefinitions_RelistsAfterClientReplacement(t *testing.T) {
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")
	entered := make(chan struct{})
	release := make(chan struct{})
	var listCalls atomic.Int32
	firstHandler := captureTestServer("first", "old_definition")
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		r.Body.Close()
		if bytes.Contains(body, []byte(`"tools/list"`)) && listCalls.Add(1) == 1 {
			close(entered)
			<-release
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		firstHandler.ServeHTTP(w, r)
	}))
	t.Cleanup(first.Close)
	second := httptest.NewServer(captureTestServer("second", "current_definition"))
	t.Cleanup(second.Close)

	cfgA := &config.ServerConfig{Name: "quarantined", URL: first.URL, Protocol: "streamable-http", Enabled: true, Quarantined: true}
	rt, err := New(&config.Config{DataDir: t.TempDir(), Listen: "127.0.0.1:0", Servers: []*config.ServerConfig{cfgA}}, "", zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })
	rt.StartBackgroundInitialization()

	// The capture grants its own inspection exemption, which connects A.
	done := make(chan error, 1)
	go func() { done <- rt.captureQuarantinedToolDefinitions(context.Background(), "quarantined") }()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("first tools/list did not begin")
	}
	captureA := rt.discoveryGeneration("quarantined")
	require.NotZero(t, captureA.Epoch, "the blocked list must be bound to a live connection")

	// Replace the managed client while A's response is still in flight, then
	// connect B directly. The supervisor receives the disconnect/connect events
	// and moves its discovery capture before A can be stored.
	cfgB := *cfgA
	cfgB.URL = second.URL
	require.NoError(t, rt.UpstreamManager().AddServerConfig("quarantined", &cfgB))
	clientB, ok := rt.UpstreamManager().GetClient("quarantined")
	require.True(t, ok)
	require.NoError(t, clientB.Connect(context.Background()))
	require.Eventually(t, func() bool {
		return rt.discoveryGeneration("quarantined") != captureA
	}, 5*time.Second, 10*time.Millisecond, "replacement must advance the capture before the old list returns")
	close(release)

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("definition capture did not complete after reconnect")
	}
	_, err = rt.storageManager.GetToolApproval("quarantined", "old_definition")
	assert.ErrorIs(t, err, storage.ErrToolApprovalNotFound, "the stale client definition must never be persisted")
	record, err := rt.storageManager.GetToolApproval("quarantined", "current_definition")
	require.NoError(t, err)
	assert.Equal(t, storage.ToolApprovalStatusPending, record.Status)
}
