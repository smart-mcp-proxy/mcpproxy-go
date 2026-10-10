package runtime

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	stdruntime "runtime"
	"sync"
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

// settleQuarantinedCapture waits until no startup actor still carrying the
// initial config can replace the manager entry: the supervisor's delayed
// startup reconcile has run with all its actions finished, and
// LoadConfiguredServers' async AddServer has created the client (before any
// exemption, it is the only actor that creates a quarantined client). It then
// grants the inspection exemption and waits for the exempted connection to be
// bound (quiescent, connected, snapshot and discovery generation agree).
//
// Without this, a late startup actor carrying cfgA can legitimately reconcile
// the manager back to the desired config after a test swaps in B, so the
// capture lists a fresh A' and persists A's definition (#1453).
func settleQuarantinedCapture(t *testing.T, rt *Runtime, name, url string) {
	t.Helper()
	require.Eventually(t, func() bool {
		if !rt.Supervisor().ReconcileQuiescent() {
			return false
		}
		client, ok := rt.UpstreamManager().GetClient(name)
		return ok && client.GetConfig().URL == url
	}, 10*time.Second, 10*time.Millisecond, "startup reconcile and the initial client creation must finish before the capture test begins")
	require.NoError(t, rt.Supervisor().RequestInspectionExemption(name, 15*time.Minute))
	waitQuarantinedCaptureBound(t, rt, name, url)
}

// waitQuarantinedCaptureBound waits until the supervisor is quiescent and the
// manager client for name has URL url, is connected, and is the connection
// the supervisor snapshot and discovery generation both agree on.
func waitQuarantinedCaptureBound(t *testing.T, rt *Runtime, name, url string) {
	t.Helper()
	require.Eventually(t, func() bool {
		if !rt.Supervisor().ReconcileQuiescent() {
			return false
		}
		client, ok := rt.UpstreamManager().GetClient(name)
		if !ok || !client.IsConnected() || client.GetConfig().URL != url {
			return false
		}
		state, ok := rt.Supervisor().CurrentSnapshot().Servers[name]
		return ok && state != nil && state.Config != nil && state.Config.URL == url &&
			rt.discoveryGeneration(name).Epoch == client.ConnectionEpoch()
	}, 10*time.Second, 10*time.Millisecond, "exempted connection must be bound to the supervisor snapshot and discovery generation")
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
	var releaseOnce sync.Once
	unblockFirst := func() { releaseOnce.Do(func() { close(release) }) }
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
	// Registered after rt.Close so LIFO cleanup releases the blocked handler
	// before closing the runtime or either httptest server.
	t.Cleanup(unblockFirst)
	rt.StartBackgroundInitialization()
	// Let the initial configuration load and the supervisor's startup
	// reconcile settle before making A's tools/list request block. This keeps
	// startup actors from retaining a stale cfgA reference through replacement.
	settleQuarantinedCapture(t, rt, "quarantined", first.URL)

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

	// Replace the desired configuration as well as the managed client while A's
	// response is in flight. If only the manager is replaced, an overlapping
	// LoadConfiguredServers or supervisor reconcile can still restore cfgA.
	cfgB := *cfgA
	cfgB.URL = second.URL
	desired, err := rt.GetDesiredConfig()
	require.NoError(t, err)
	require.Len(t, desired.Servers, 1)
	desired.Servers[0] = &cfgB
	rt.UpdateConfig(desired, "")
	require.NoError(t, rt.UpstreamManager().AddServerConfig("quarantined", &cfgB))
	clientB, ok := rt.UpstreamManager().GetClient("quarantined")
	require.True(t, ok)
	require.Equal(t, second.URL, clientB.GetConfig().URL)
	connectCtx, cancelConnect := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelConnect()
	if err := clientB.Connect(connectCtx); err != nil {
		require.Contains(t, err.Error(), "connection already in progress or established")
		require.True(t, clientB.IsConnecting() || clientB.IsConnected(), "duplicate connect error without an active or ready connection: %v", err)
	}
	require.Eventually(t, func() bool {
		current, ok := rt.UpstreamManager().GetClient("quarantined")
		if !ok || current != clientB || current.GetConfig().URL != second.URL || !current.IsConnected() {
			return false
		}
		generation := rt.discoveryGeneration("quarantined")
		return current.ConnectionEpoch() > captureA.Epoch && generation.Epoch == current.ConnectionEpoch() && generation != captureA
	}, 10*time.Second, 10*time.Millisecond, "replacement config, manager client, and discovery generation must agree before the old list returns")
	unblockFirst()

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

// TestCaptureQuarantinedToolDefinitions_DropsReplacementBeforePersist covers
// the final capture boundary: tools/list for A has returned and passed its
// first validation, then a reconcile replaces A immediately before approval
// records would be written. The stale response must be discarded and re-listed
// from B rather than becoming a pending review item.
//
// The replacement goes through the production desired-config path
// (UpdateConfig -> supervisor reconcile). A manager-only swap would be
// legitimately undone by any startup reconciler still carrying cfgA, which
// made this test flaky under load (#1453); settleQuarantinedCapture removes
// those actors before the capture starts.
func TestCaptureQuarantinedToolDefinitions_DropsReplacementBeforePersist(t *testing.T) {
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")
	first := httptest.NewServer(captureTestServer("first", "old_definition"))
	t.Cleanup(first.Close)
	second := httptest.NewServer(captureTestServer("second", "current_definition"))
	t.Cleanup(second.Close)

	cfgA := &config.ServerConfig{Name: "quarantined", URL: first.URL, Protocol: "streamable-http", Enabled: true, Quarantined: true}
	rt, err := New(&config.Config{DataDir: t.TempDir(), Listen: "127.0.0.1:0", Servers: []*config.ServerConfig{cfgA}}, "", zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })
	rt.StartBackgroundInitialization()
	settleQuarantinedCapture(t, rt, "quarantined", first.URL)

	var replaced atomic.Bool
	rt.quarantinedCaptureBeforePersist = func() {
		if !replaced.CompareAndSwap(false, true) {
			return
		}
		cfgB := *cfgA
		cfgB.URL = second.URL
		desired, err := rt.GetDesiredConfig()
		require.NoError(t, err)
		require.Len(t, desired.Servers, 1)
		desired.Servers[0] = &cfgB
		rt.UpdateConfig(desired, "")
		waitQuarantinedCaptureBound(t, rt, "quarantined", second.URL)
	}

	require.NoError(t, rt.captureQuarantinedToolDefinitions(context.Background(), "quarantined"))
	assert.True(t, replaced.Load(), "test must replace the client at the final validation boundary")
	_, err = rt.storageManager.GetToolApproval("quarantined", "old_definition")
	assert.ErrorIs(t, err, storage.ErrToolApprovalNotFound, "the definition captured before replacement must not be stored")
	current, err := rt.storageManager.GetToolApproval("quarantined", "current_definition")
	require.NoError(t, err)
	assert.Equal(t, storage.ToolApprovalStatusPending, current.Status)
}

// TestCaptureQuarantinedToolDefinitions_SerializesReplacementWithPersistence
// proves the final linearization point. Once capture has established the live
// client/epoch, AddServerConfig cannot replace that manager entry until the
// bounded approval-record and last-good snapshot writes commit. The next
// capture will then observe the replacement rather than an interleaved state.
func TestCaptureQuarantinedToolDefinitions_SerializesReplacementWithPersistence(t *testing.T) {
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")
	first := httptest.NewServer(captureTestServer("first", "old_definition"))
	t.Cleanup(first.Close)
	second := httptest.NewServer(captureTestServer("second", "current_definition"))
	t.Cleanup(second.Close)

	cfgA := &config.ServerConfig{Name: "quarantined", URL: first.URL, Protocol: "streamable-http", Enabled: true, Quarantined: true}
	rt, err := New(&config.Config{DataDir: t.TempDir(), Listen: "127.0.0.1:0", Servers: []*config.ServerConfig{cfgA}}, "", zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })
	rt.StartBackgroundInitialization()
	settleQuarantinedCapture(t, rt, "quarantined", first.URL)

	replaceStarted := make(chan struct{})
	replaceDone := make(chan error, 1)
	var replaceOnce atomic.Bool
	rt.quarantinedCaptureDuringPersist = func() {
		if !replaceOnce.CompareAndSwap(false, true) {
			return
		}
		go func() {
			close(replaceStarted)
			cfgB := *cfgA
			cfgB.URL = second.URL
			replaceDone <- rt.UpstreamManager().AddServerConfig("quarantined", &cfgB)
		}()
		<-replaceStarted
		deadline := time.Now().Add(time.Second)
		for !rt.UpstreamManager().CaptureReplacementQueued() && time.Now().Before(deadline) {
			stdruntime.Gosched()
		}
		require.True(t, rt.UpstreamManager().CaptureReplacementQueued(), "AddServerConfig writer must be queued behind capture persistence")
		select {
		case err := <-replaceDone:
			t.Fatalf("AddServerConfig replaced the client before capture committed: %v", err)
		default:
		}
	}

	require.NoError(t, rt.captureQuarantinedToolDefinitions(context.Background(), "quarantined"))
	require.NoError(t, <-replaceDone, "replacement completes once the capture's bounded persistence has committed")
	record, err := rt.storageManager.GetToolApproval("quarantined", "old_definition")
	require.NoError(t, err)
	assert.Equal(t, storage.ToolApprovalStatusPending, record.Status)
	client, ok := rt.UpstreamManager().GetClient("quarantined")
	require.True(t, ok)
	assert.Equal(t, second.URL, client.GetConfig().URL, "the replacement becomes current only after A's review capture commits")
	require.NoError(t, client.Connect(context.Background()))
	require.Eventually(t, func() bool {
		return rt.discoveryGeneration("quarantined").Epoch == client.ConnectionEpoch()
	}, 5*time.Second, 10*time.Millisecond, "supervisor must bind the replacement client before its review capture")
	tools, err := client.ListTools(context.Background())
	require.NoError(t, err)
	published, err := rt.captureQuarantinedToolDefinitionsFromCurrentClient("quarantined", client, rt.discoveryGeneration("quarantined"), tools)
	require.NoError(t, err)
	require.True(t, published)
	current, err := rt.storageManager.GetToolApproval("quarantined", "current_definition")
	require.NoError(t, err)
	assert.Equal(t, storage.ToolApprovalStatusPending, current.Status, "the replacement definition becomes the active review record")
}

// UX-01 r9: a commit that replaced the quarantined endpoint retires the old
// client at publication, before the manager reconciliation swaps it. A
// tools/list response from that retired client must not be persisted into the
// server's review records, whatever the pointer/epoch/generation checks say.
func TestCaptureQuarantinedToolDefinitions_RetiredClientNeverPersists(t *testing.T) {
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")
	first := httptest.NewServer(captureTestServer("first", "old_definition"))
	t.Cleanup(first.Close)

	cfgA := &config.ServerConfig{Name: "quarantined", URL: first.URL, Protocol: "streamable-http", Enabled: true, Quarantined: true}
	rt, err := New(&config.Config{DataDir: t.TempDir(), Listen: "127.0.0.1:0", Servers: []*config.ServerConfig{cfgA}}, "", zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })
	rt.StartBackgroundInitialization()
	settleQuarantinedCapture(t, rt, "quarantined", first.URL)

	var retired atomic.Bool
	rt.quarantinedCaptureBeforePersist = func() {
		if !retired.CompareAndSwap(false, true) {
			return
		}
		// What the config commit does at publication, with the reconciliation
		// that would replace the client withheld.
		cfgB := *cfgA
		cfgB.URL = "http://127.0.0.1:1/other"
		rt.upstreamManager.RetireStaleClients([]*config.ServerConfig{&cfgB})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	require.Error(t, rt.captureQuarantinedToolDefinitions(ctx, "quarantined"))
	assert.True(t, retired.Load())
	_, err = rt.storageManager.GetToolApproval("quarantined", "old_definition")
	assert.ErrorIs(t, err, storage.ErrToolApprovalNotFound, "a retired client's definitions must never be persisted")
}
