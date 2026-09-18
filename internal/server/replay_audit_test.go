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
	"sync"
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

// startRuntimeBlockingUpstream is startRuntimeCountingUpstream's variant for
// proving write ORDER: the tool handler signals reached once it is entered
// and then blocks until release is closed, so a test can inspect the audit
// sink while the upstream dispatch is still in flight.
func startRuntimeBlockingUpstream(t *testing.T, server, tool string) (url string, reached chan struct{}, release chan struct{}) {
	t.Helper()
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")
	reached = make(chan struct{})
	release = make(chan struct{})

	mcpSrv := mcpserver.NewMCPServer(server, "1.0.0-test", mcpserver.WithToolCapabilities(true))
	mcpSrv.AddTool(mcp.Tool{Name: tool, Description: "Replay target", InputSchema: mcp.ToolInputSchema{Type: "object"}},
		func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			close(reached)
			<-release
			return mcp.NewToolResultText("ok"), nil
		})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	httpSrv := &http.Server{Handler: mcpserver.NewStreamableHTTPServer(mcpSrv), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = httpSrv.Serve(ln) }()
	t.Cleanup(func() { _ = httpSrv.Shutdown(context.Background()) })
	return fmt.Sprintf("http://%s", ln.Addr().String()), reached, release
}

// seedReplayableCall registers the server identity and one persisted
// ToolCallRecord runtime.ReplayToolCall's own lookup (Server.ReplayToolCall's
// pre-lookup included) can find by ID, and connects the runtime's upstream
// manager to url so the replay dispatch actually reaches an upstream.
func seedReplayableCall(t *testing.T, proxy *MCPProxyServer, mainSrv *Server, server, tool, url string) string {
	t.Helper()
	return seedReplayableCallWithAnnotations(t, proxy, mainSrv, server, tool, url, nil)
}

// seedReplayableCallWithAnnotations is seedReplayableCall with control over
// the persisted record's annotations snapshot, for asserting the replayed
// audit line's `operation` tier.
func seedReplayableCallWithAnnotations(t *testing.T, proxy *MCPProxyServer, mainSrv *Server, server, tool, url string, annotations *config.ToolAnnotations) string {
	t.Helper()
	sm := mainSrv.runtime.StorageManager()

	serverCfg := &config.ServerConfig{Name: server, URL: url, Protocol: "streamable-http", Enabled: true}
	identity, err := sm.RegisterServerIdentity(serverCfg, "")
	require.NoError(t, err)

	require.NoError(t, mainSrv.runtime.UpstreamManager().AddServerConfig(server, serverCfg))
	require.NoError(t, mainSrv.runtime.UpstreamManager().ConnectAll(context.Background()))

	callID := "replay-fixture-1"
	require.NoError(t, sm.RecordToolCall(&storage.ToolCallRecord{
		ID:          callID,
		ServerID:    identity.ID,
		ServerName:  server,
		ToolName:    tool,
		Arguments:   map[string]interface{}{"q": "original"},
		Timestamp:   time.Now(),
		Annotations: annotations,
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

// TestReplayToolCall_OperationFromAnnotations is a round-3 cross-review
// regression (Spec 107 PR-D): the replayed record's own annotations
// snapshot must supply the audit line's `operation` tier — before this fix
// installAuditAttempt was never given an Operation, so both the `authz` and
// `tool_call` lines reported `operation:"unknown"` even for a known
// destructive tool.
func TestReplayToolCall_OperationFromAnnotations(t *testing.T) {
	proxy, rt := createTestProxyWithRuntime(t, nil)
	sink := &recordingAuditSink{}
	proxy.auditSink = sink
	mainSrv := &Server{runtime: rt, mcpProxy: proxy}

	url, calls := startRuntimeCountingUpstream(t, proxy, "a", "erase")
	callID := seedReplayableCallWithAnnotations(t, proxy, mainSrv, "a", "erase", url,
		&config.ToolAnnotations{DestructiveHint: boolPtr(true)})

	require.Eventually(t, func() bool {
		client, ok := rt.UpstreamManager().GetClient("a")
		return ok && client != nil && client.IsConnected()
	}, 5*time.Second, 20*time.Millisecond)

	result, err := mainSrv.ReplayToolCall(context.Background(), callID, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, int64(1), calls.count.Load())

	lines := sink.decoded(t)
	require.Len(t, lines, 2)
	assert.Equal(t, "destructive", lines[0]["operation"], "authz line must report the recorded tool's actual tier")
	assert.Equal(t, "destructive", lines[1]["operation"], "tool_call line must report the recorded tool's actual tier")
}

// TestReplayToolCall_OperationUnknownWithoutAnnotations proves the missing-
// annotations case defaults to "unknown" rather than tierForAnnotations'
// found=false "destructive" default, which is an AUTHORIZATION fail-closed
// and would misrepresent an unresolved tier as maximally risky on the audit
// line (mirrors mcp.go's own choice for the live dispatch path).
func TestReplayToolCall_OperationUnknownWithoutAnnotations(t *testing.T) {
	proxy, rt := createTestProxyWithRuntime(t, nil)
	sink := &recordingAuditSink{}
	proxy.auditSink = sink
	mainSrv := &Server{runtime: rt, mcpProxy: proxy}

	url, calls := startRuntimeCountingUpstream(t, proxy, "a", "erase")
	callID := seedReplayableCall(t, proxy, mainSrv, "a", "erase", url)

	require.Eventually(t, func() bool {
		client, ok := rt.UpstreamManager().GetClient("a")
		return ok && client != nil && client.IsConnected()
	}, 5*time.Second, 20*time.Millisecond)

	result, err := mainSrv.ReplayToolCall(context.Background(), callID, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, int64(1), calls.count.Load())

	lines := sink.decoded(t)
	require.Len(t, lines, 2)
	assert.Equal(t, "unknown", lines[0]["operation"])
}

// TestReplayToolCall_AuthzWrittenBeforeUpstreamDispatch is a round-3
// cross-review regression (Spec 107 PR-D): FR-012 requires `decision: allow`
// to be written after the last gate and before the upstream call, so a
// crash mid-dispatch still leaves an authorization record — every other
// dispatch path gets this from emitActivityToolCallStarted. Before this fix,
// Server.ReplayToolCall wrote the `authz allow` line only via auditToolCall's
// completion-time backfill, AFTER the upstream call returned.
func TestReplayToolCall_AuthzWrittenBeforeUpstreamDispatch(t *testing.T) {
	proxy, rt := createTestProxyWithRuntime(t, nil)
	sink := &recordingAuditSink{}
	proxy.auditSink = sink
	mainSrv := &Server{runtime: rt, mcpProxy: proxy}

	url, reached, release := startRuntimeBlockingUpstream(t, "a", "erase")
	callID := seedReplayableCall(t, proxy, mainSrv, "a", "erase", url)
	// Always unblock the upstream handler on the way out, even if an
	// assertion below fails early via require/t.Fatal — otherwise the
	// blocked goroutine's connection is never released and t.Cleanup's
	// httpSrv.Shutdown (registered inside startRuntimeBlockingUpstream)
	// hangs forever waiting for it.
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })

	require.Eventually(t, func() bool {
		client, ok := rt.UpstreamManager().GetClient("a")
		return ok && client != nil && client.IsConnected()
	}, 5*time.Second, 20*time.Millisecond)

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = mainSrv.ReplayToolCall(context.Background(), callID, nil)
	}()

	select {
	case <-reached:
	case <-time.After(5 * time.Second):
		t.Fatal("upstream handler was never reached")
	}

	// The upstream call is now blocked inside the handler, before it can
	// possibly have returned to a completion-time backfill. The authz line
	// must already be on the sink.
	lines := sink.decoded(t)
	require.Len(t, lines, 1, "authz allow must be written before the upstream call, not backfilled after it")
	assert.Equal(t, "authz", lines[0]["event"])
	assert.Equal(t, "allow", lines[0]["decision"])

	releaseOnce.Do(func() { close(release) })
	<-done
}

// startRuntimeFailingUpstream is startRuntimeCountingUpstream's variant for a
// tool that answers with an upstream-level failure. isRPCError selects which
// of the two ways MCP has for a call to fail: true returns a Go error from
// the handler (a JSON-RPC-level failure — the pre-existing `err != nil`
// case), false returns mcp.NewToolResultError (a normal RPC response with
// Result.IsError:true — the protocol's convention for a TOOL failure, which
// callErr never sees).
func startRuntimeFailingUpstream(t *testing.T, server, tool string, isRPCError bool) (url string) {
	t.Helper()
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")

	mcpSrv := mcpserver.NewMCPServer(server, "1.0.0-test", mcpserver.WithToolCapabilities(true))
	mcpSrv.AddTool(mcp.Tool{Name: tool, Description: "Replay target", InputSchema: mcp.ToolInputSchema{Type: "object"}},
		func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			if isRPCError {
				return nil, fmt.Errorf("boom")
			}
			return mcp.NewToolResultError("boom"), nil
		})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	httpSrv := &http.Server{Handler: mcpserver.NewStreamableHTTPServer(mcpSrv), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = httpSrv.Serve(ln) }()
	t.Cleanup(func() { _ = httpSrv.Shutdown(context.Background()) })
	return fmt.Sprintf("http://%s", ln.Addr().String())
}

// TestReplayToolCall_FailedUpstreamCallAuditsAsError is a round-4
// cross-review regression (Spec 107 PR-D): runtime.ReplayToolCall folds a
// non-shed upstream failure into the returned record's own Error field and
// hands back a NIL Go error (only a limiter shed returns non-nil) — before
// this fix Server.ReplayToolCall's outcome switch only branched on `err`, so
// every failed replay of this shape fell into `default` and was audited as
// `outcome:"success"`.
func TestReplayToolCall_FailedUpstreamCallAuditsAsError(t *testing.T) {
	proxy, rt := createTestProxyWithRuntime(t, nil)
	sink := &recordingAuditSink{}
	proxy.auditSink = sink
	mainSrv := &Server{runtime: rt, mcpProxy: proxy}

	url := startRuntimeFailingUpstream(t, "a", "erase", true)
	callID := seedReplayableCall(t, proxy, mainSrv, "a", "erase", url)

	require.Eventually(t, func() bool {
		client, ok := rt.UpstreamManager().GetClient("a")
		return ok && client != nil && client.IsConnected()
	}, 5*time.Second, 20*time.Millisecond)

	result, err := mainSrv.ReplayToolCall(context.Background(), callID, nil)
	require.NoError(t, err, "a non-shed upstream failure is still a completed replay, not a Server.ReplayToolCall error")
	require.NotNil(t, result)
	require.NotEmpty(t, result.Error)

	lines := sink.decoded(t)
	require.Len(t, lines, 2)
	assert.Equal(t, "tool_call", lines[1]["event"])
	assert.Equal(t, "error", lines[1]["outcome"], "a failed replay must never be audited as outcome:success")
}

// TestReplayToolCall_ToolLevelIsErrorResponseAuditsAsError is
// TestReplayToolCall_FailedUpstreamCallAuditsAsError's companion for the
// OTHER shape a tool failure takes on the wire: the MCP protocol answers a
// tool-level failure as a normal (err==nil) RPC response with
// Result.IsError:true — which neither callErr nor runtime.ReplayToolCall's
// own record.Error field (only ever set from callErr) ever observes.
func TestReplayToolCall_ToolLevelIsErrorResponseAuditsAsError(t *testing.T) {
	proxy, rt := createTestProxyWithRuntime(t, nil)
	sink := &recordingAuditSink{}
	proxy.auditSink = sink
	mainSrv := &Server{runtime: rt, mcpProxy: proxy}

	url := startRuntimeFailingUpstream(t, "a", "erase", false)
	callID := seedReplayableCall(t, proxy, mainSrv, "a", "erase", url)

	require.Eventually(t, func() bool {
		client, ok := rt.UpstreamManager().GetClient("a")
		return ok && client != nil && client.IsConnected()
	}, 5*time.Second, 20*time.Millisecond)

	result, err := mainSrv.ReplayToolCall(context.Background(), callID, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Empty(t, result.Error, "control: an IsError:true tool response does NOT populate record.Error")

	lines := sink.decoded(t)
	require.Len(t, lines, 2)
	assert.Equal(t, "tool_call", lines[1]["event"])
	assert.Equal(t, "error", lines[1]["outcome"], "an IsError:true tool response must never be audited as outcome:success")
}
