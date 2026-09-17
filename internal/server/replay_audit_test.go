package server

// replay_audit_test.go — round-2 cross-review regression (Spec 107 PR-D):
// POST /api/v1/tool-calls/{id}/replay reaches a (server, tool) pair like
// every other upstream dispatch path (FR-012), so it must write exactly one
// `authz` line and one `tool_call` line per replay. Before this fix,
// Server.ReplayToolCall delegated straight to runtime.ReplayToolCall, which
// calls the managed client directly with no audit.Attempt installed — every
// replay dispatch produced zero audit lines.

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// startRuntimeCountingUpstream is startCountingUpstream's counterpart for
// the Runtime's OWN upstream manager (rt.UpstreamManager()), which is a
// distinct instance from the MCPProxyServer's — runtime.ReplayToolCall
// dispatches through the former, every other test in this package through
// the latter.
func startRuntimeCountingUpstream(t *testing.T, proxy *MCPProxyServer, server, tool string) (url string, calls *upstreamCalls) {
	t.Helper()
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")

	mcpSrv := mcpserver.NewMCPServer(server, "1.0.0-test", mcpserver.WithToolCapabilities(true))
	uc := &upstreamCalls{}
	mcpSrv.AddTool(mcp.Tool{Name: tool, Description: "Replay target", InputSchema: mcp.ToolInputSchema{Type: "object"}},
		func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			uc.record(request.Params.Name)
			return mcp.NewToolResultText("ok"), nil
		})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	httpSrv := &http.Server{Handler: mcpserver.NewStreamableHTTPServer(mcpSrv), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = httpSrv.Serve(ln) }()
	t.Cleanup(func() { _ = httpSrv.Shutdown(context.Background()) })
	return fmt.Sprintf("http://%s", ln.Addr().String()), uc
}

// seedReplayableCall registers the server identity and one persisted
// ToolCallRecord runtime.ReplayToolCall's own lookup (Server.ReplayToolCall's
// pre-lookup included) can find by ID, and connects the runtime's upstream
// manager to url so the replay dispatch actually reaches an upstream.
func seedReplayableCall(t *testing.T, proxy *MCPProxyServer, mainSrv *Server, server, tool, url string) string {
	t.Helper()
	sm := mainSrv.runtime.StorageManager()

	serverCfg := &config.ServerConfig{Name: server, URL: url, Protocol: "streamable-http", Enabled: true}
	identity, err := sm.RegisterServerIdentity(serverCfg, "")
	require.NoError(t, err)

	require.NoError(t, mainSrv.runtime.UpstreamManager().AddServerConfig(server, serverCfg))
	require.NoError(t, mainSrv.runtime.UpstreamManager().ConnectAll(context.Background()))

	callID := "replay-fixture-1"
	require.NoError(t, sm.RecordToolCall(&storage.ToolCallRecord{
		ID:         callID,
		ServerID:   identity.ID,
		ServerName: server,
		ToolName:   tool,
		Arguments:  map[string]interface{}{"q": "original"},
		Timestamp:  time.Now(),
	}))
	return callID
}

func TestReplayToolCall_WritesAuthzAllowThenToolCallSuccess(t *testing.T) {
	proxy, rt := createTestProxyWithRuntime(t, nil)
	sink := &recordingAuditSink{}
	proxy.auditSink = sink
	mainSrv := &Server{runtime: rt, mcpProxy: proxy}

	url, calls := startRuntimeCountingUpstream(t, proxy, "a", "erase")
	callID := seedReplayableCall(t, proxy, mainSrv, "a", "erase", url)

	// Wait for the connection to settle (real network dial + MCP handshake).
	require.Eventually(t, func() bool {
		client, ok := rt.UpstreamManager().GetClient("a")
		return ok && client != nil && client.IsConnected()
	}, 5*time.Second, 20*time.Millisecond)

	result, err := mainSrv.ReplayToolCall(context.Background(), callID, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Empty(t, result.Error)
	assert.Equal(t, int64(1), calls.count.Load(), "control: the replay must actually dispatch")

	lines := sink.decoded(t)
	require.Len(t, lines, 2, "one authz + one tool_call per replayed call")
	assert.Equal(t, "authz", lines[0]["event"])
	assert.Equal(t, "allow", lines[0]["decision"])
	assert.Equal(t, "rest", lines[0]["surface"])
	assert.Equal(t, "a", lines[0]["server"])
	assert.Equal(t, "erase", lines[0]["tool"])

	assert.Equal(t, "tool_call", lines[1]["event"])
	assert.Equal(t, "success", lines[1]["outcome"])
	assert.Equal(t, lines[0]["request_id"], lines[1]["request_id"])
}

func TestReplayToolCall_UnresolvedIDDelegatesUnaudited(t *testing.T) {
	proxy, rt := createTestProxyWithRuntime(t, nil)
	sink := &recordingAuditSink{}
	proxy.auditSink = sink
	mainSrv := &Server{runtime: rt, mcpProxy: proxy}

	_, err := mainSrv.ReplayToolCall(context.Background(), "does-not-exist", nil)
	require.Error(t, err)
	assert.Empty(t, sink.decoded(t), "an id that never resolved to a (server, tool) pair writes no line")
}
