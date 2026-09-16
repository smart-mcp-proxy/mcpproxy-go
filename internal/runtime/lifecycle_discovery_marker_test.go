package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/mark3labs/mcp-go/server/servertest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/stateview"
)

// Spec 105 FR-009 (round-3 finding 5): a server whose tools/list completes
// with ZERO tools has finished its discovery. The authoritative refresh
// already published that (RefreshServerToolsFromDiscovery); the lenient
// reactive connect path and the sweep returned early without stamping, so a
// genuinely tool-less server stayed in the connect→discovery window forever —
// same refusal, wrong remediation ("retry shortly" instead of "the tool is
// not listed"). Both paths now stamp the marker without touching the tool set.
func TestDiscovery_ZeroToolServerIsStampedDiscoveredOnLenientAndSweepPaths(t *testing.T) {
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")
	// A prompt-only upstream: tools/list succeeds and returns nothing.
	upstream := mcpserver.NewMCPServer("empty", "0.0.1", mcpserver.WithToolCapabilities(true), mcpserver.WithPromptCapabilities(true))
	upstream.AddPrompt(mcp.NewPrompt("greeting"), func(_ context.Context, _ mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		return &mcp.GetPromptResult{}, nil
	})
	httpSrv := servertest.NewTestStreamableHTTPServer(upstream)
	t.Cleanup(httpSrv.Close)

	serverCfg := &config.ServerConfig{Name: "a", URL: httpSrv.URL, Protocol: "streamable-http", Enabled: true}
	rt, err := New(&config.Config{
		DataDir: t.TempDir(), Listen: "127.0.0.1:0",
		Servers: []*config.ServerConfig{serverCfg},
	}, "", zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })

	require.NoError(t, rt.UpstreamManager().AddServerConfig("a", serverCfg))
	require.NoError(t, rt.UpstreamManager().ConnectAll(context.Background()))
	require.Eventually(t, func() bool {
		client, ok := rt.UpstreamManager().GetClient("a")
		return ok && client.IsConnected()
	}, 10*time.Second, 50*time.Millisecond)

	// The connect→discovery window as the StateView holds it on every start.
	resetWindow := func() {
		rt.Supervisor().StateView().UpdateServer("a", func(s *stateview.ServerStatus) {
			s.Name, s.Enabled, s.Connected = "a", true, true
			s.Tools, s.ToolsDiscovered = nil, false
		})
	}
	status := func() *stateview.ServerStatus {
		st, ok := rt.Supervisor().StateView().GetServer("a")
		require.True(t, ok)
		return st
	}

	t.Run("lenient connect path", func(t *testing.T) {
		resetWindow()
		require.False(t, status().ToolsDiscovered)
		require.NoError(t, rt.DiscoverAndIndexToolsForServer(context.Background(), "a"))
		st := status()
		assert.True(t, st.ToolsDiscovered, "a completed zero-tool list is a completed discovery")
		assert.Empty(t, st.Tools, "the lenient path never touches the tool set")
	})

	t.Run("sweep", func(t *testing.T) {
		resetWindow()
		require.False(t, status().ToolsDiscovered)
		require.NoError(t, rt.DiscoverAndIndexTools(context.Background()))
		st := status()
		assert.True(t, st.ToolsDiscovered, "the sweep stamps a server that listed zero tools")
		assert.Empty(t, st.Tools)
	})

	// astra r1 I1: after a reconnect the StateView holds the PREVIOUS
	// connection's tool set (restored for counts, MCP-2094) with the marker
	// cleared. A zero-tool list on the new connection must not stamp that
	// set as this connection's discovery result — the names were never
	// listed by it, and the stale annotations would drive the tier check.
	for _, path := range []struct {
		name string
		run  func() error
	}{
		{"lenient connect path", func() error { return rt.DiscoverAndIndexToolsForServer(context.Background(), "a") }},
		{"sweep", func() error { return rt.DiscoverAndIndexTools(context.Background()) }},
	} {
		t.Run(path.name+": a retained set is never certified by an empty list", func(t *testing.T) {
			resetWindow()
			rt.Supervisor().StateView().UpdateServer("a", func(s *stateview.ServerStatus) {
				s.Tools = []stateview.ToolInfo{{Name: "erase", Description: "previous connection's tool"}}
				s.ToolCount = 1
			})
			require.NoError(t, path.run())
			st := status()
			assert.False(t, st.ToolsDiscovered, "a retained set must not be stamped as this connection's result")
			assert.Len(t, st.Tools, 1, "the retained set is kept for counts, not wiped")
		})
	}
}
