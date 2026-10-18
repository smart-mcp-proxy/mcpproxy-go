package upstream

import (
	"context"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	servertest "github.com/mark3labs/mcp-go/server/servertest"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// TestManager_LateCallbacks_ReachExistingClients reproduces the SEC-01 startup
// race: the supervisor adds (and connects) upstream clients before the runtime
// registers the tool-discovery and prompts-changed callbacks on the manager.
// Previously the callbacks were copied into a client only at creation, so a
// client that already existed silently dropped every list_changed
// notification and a rug-pulled tool was never re-discovered or re-gated
// until restart.
func TestManager_LateCallbacks_ReachExistingClients(t *testing.T) {
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")
	m := newTestManager(t)

	// The "notify" tool makes the upstream emit both list_changed
	// notifications on the caller's session, so they travel the real
	// transport → core → managed → manager path.
	upstream := mcpserver.NewMCPServer("late-cb-upstream", "0.0.1",
		mcpserver.WithToolCapabilities(true),
		mcpserver.WithPromptCapabilities(true),
	)
	upstream.AddTool(mcp.NewTool("notify"), func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		srv := mcpserver.ServerFromContext(ctx)
		// The first in-request notification on a streamable-HTTP response is
		// consumed while the response upgrades to SSE and never reaches the
		// client handler, so lead with a sacrificial one that core ignores.
		if err := srv.SendNotificationToClient(ctx, "notifications/test/warmup", nil); err != nil {
			return nil, err
		}
		if err := srv.SendNotificationToClient(ctx, string(mcp.MethodNotificationPromptsListChanged), nil); err != nil {
			return nil, err
		}
		if err := srv.SendNotificationToClient(ctx, string(mcp.MethodNotificationToolsListChanged), nil); err != nil {
			return nil, err
		}
		return mcp.NewToolResultText("ok"), nil
	})
	testServer := servertest.NewTestStreamableHTTPServer(upstream)
	t.Cleanup(testServer.Close)

	// 1. Client created and connected BEFORE any callback is set.
	require.NoError(t, m.AddServerConfig("srv-late", &config.ServerConfig{
		Name:     "late-server",
		Protocol: "streamable-http",
		URL:      testServer.URL,
		Enabled:  true,
	}))
	client, ok := m.GetClient("srv-late")
	require.True(t, ok)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, client.Connect(ctx))

	// 2. Callbacks registered afterwards, as runtime.LoadConfiguredServers does.
	toolsFired := make(chan string, 4)
	promptsFired := make(chan string, 4)
	m.SetToolDiscoveryCallback(func(_ context.Context, serverName string) error {
		toolsFired <- serverName
		return nil
	})
	m.SetPromptsChangedCallback(func(serverName string) {
		promptsFired <- serverName
	})

	// 3. Upstream sends tools/list_changed + prompts/list_changed.
	_, err := client.CallTool(ctx, "notify", nil)
	require.NoError(t, err)

	select {
	case name := <-toolsFired:
		require.Equal(t, "late-server", name)
	case <-time.After(5 * time.Second):
		t.Fatal("tool discovery callback set after client creation was not invoked on tools/list_changed")
	}
	select {
	case name := <-promptsFired:
		require.Equal(t, "late-server", name)
	case <-time.After(5 * time.Second):
		t.Fatal("prompts-changed callback set after client creation was not invoked on prompts/list_changed")
	}
}
