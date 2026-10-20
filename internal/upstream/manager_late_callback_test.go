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
		// mcp-go v1.0.0's streamable-HTTP server can drop ONE in-request
		// notification per call (see the retry loop below), so lead with a
		// sacrificial one that core ignores; it absorbs the loss most of the
		// time, and the retry loop covers the rest.
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
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, client.Connect(ctx))

	// 2. Callbacks registered afterwards, as runtime.LoadConfiguredServers does.
	// The callbacks signal rather than count: a non-blocking send keeps a
	// repeated notify call from ever blocking core's notification goroutine
	// (and with it the SSE reader the CallTool response rides on).
	toolsFired := make(chan string, 1)
	promptsFired := make(chan string, 1)
	m.SetToolDiscoveryCallback(func(_ context.Context, serverName string) error {
		select {
		case toolsFired <- serverName:
		default:
		}
		return nil
	})
	m.SetPromptsChangedCallback(func(serverName string) {
		select {
		case promptsFired <- serverName:
		default:
		}
	})

	// 3. Upstream sends tools/list_changed + prompts/list_changed.
	//
	// mcp-go v1.0.0's streamable-HTTP server (server/streamable_http.go,
	// handlePost) forwards in-request notifications to the SSE response from
	// a separate goroutine. When the tool handler returns, the request
	// goroutine takes the stream mutex, drains whatever is still queued,
	// closes `done`, and the forwarder then discards the notification it had
	// already dequeued but not yet written (the non-resumable branch). So any
	// ONE of the notifications a call sends can be lost, and which one is a
	// matter of scheduling: on a loaded CI runner it is the tools or prompts
	// notification often enough to fail every run. (A forwarder from an
	// earlier request that has not yet observed its own `done` can discard a
	// later call's notification too, so no fixed per-call minimum holds.) The
	// subject here is the manager → client callback plumbing, not transport
	// delivery, so the call is repeated until both callbacks have fired; in
	// practice one or two attempts suffice, and the deadline is generous for
	// a loaded runner.
	deadline := time.Now().Add(10 * time.Second)
	gotTools, gotPrompts := false, false
	attempts := 0
	for !gotTools || !gotPrompts {
		if time.Now().After(deadline) {
			t.Fatalf("after %d notify calls the late-registered callbacks had not both fired: tools=%v prompts=%v",
				attempts, gotTools, gotPrompts)
		}
		attempts++
		_, err := client.CallTool(ctx, "notify", nil)
		require.NoError(t, err)

		settle := time.After(500 * time.Millisecond)
	collect:
		for !gotTools || !gotPrompts {
			select {
			case name := <-toolsFired:
				require.Equal(t, "late-server", name)
				gotTools = true
			case name := <-promptsFired:
				require.Equal(t, "late-server", name)
				gotPrompts = true
			case <-settle:
				break collect
			}
		}
	}
}
