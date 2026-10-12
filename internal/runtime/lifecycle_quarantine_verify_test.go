package runtime

import (
	"context"
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

// seedRT05Approval writes the stage-1 contract the operator approved before
// the restart that re-quarantined the server.
func seedRT05Approval(t *testing.T, rt *Runtime) {
	t.Helper()
	// The schema mcp-go's NewTool serves for a parameterless tool.
	approvedSchema := normalizeJSON(`{"type":"object","properties":{},"required":[]}`)
	approvedHash := calculateToolApprovalHashWithOutputSchema("calendar_get_events", "Lists calendar events.", approvedSchema, "", nil)
	require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "quarantined", ToolName: "calendar_get_events",
		ApprovedHash: approvedHash, CurrentHash: approvedHash,
		HashSchemaVersion:  storage.ToolApprovalHashSchemaVersion,
		Status:             storage.ToolApprovalStatusApproved,
		ApprovedBy:         "operator",
		CurrentDescription: "Lists calendar events.",
		CurrentSchema:      approvedSchema,
		IdentityKeyed:      true,
	}))
}

func requireRT05Changed(t *testing.T, rt *Runtime, msg string) {
	t.Helper()
	require.Eventually(t, func() bool {
		rec, err := rt.storageManager.GetToolApproval("quarantined", "calendar_get_events")
		return err == nil && rec.Status == storage.ToolApprovalStatusChanged
	}, 10*time.Second, 50*time.Millisecond, msg)
	rec, err := rt.storageManager.GetToolApproval("quarantined", "calendar_get_events")
	require.NoError(t, err)
	assert.Equal(t, "Lists calendar events.", rec.PreviousDescription)
	assert.Equal(t, "armed definition", rec.CurrentDescription)

	assert.True(t, rt.serverIsQuarantined("quarantined"), "verification must not unquarantine the server")
	indexed, err := rt.indexManager.GetToolsByServer("quarantined")
	require.NoError(t, err)
	assert.Empty(t, indexed, "a quarantined server's tools must never be indexed")
}

// TestRT05_QuarantinedServer_ContractChangeDetectedOnConnect reproduces
// EXP-2a: a tool approved earlier is served with an armed contract while the
// server is (re-)quarantined. Discovery for the quarantined server used to be
// skipped entirely, leaving the stale approved contract in place for the whole
// quarantine window. The supervisor's connect-time discovery — with NO
// explicit refresh by the test — must hash-compare the contract and mark it
// changed, without anything reaching the search index (Sol r1 P2).
func TestRT05_QuarantinedServer_ContractChangeDetectedOnConnect(t *testing.T) {
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")
	upstream := httptest.NewServer(captureTestServer("armed", "calendar_get_events"))
	t.Cleanup(upstream.Close)

	cfg := &config.ServerConfig{Name: "quarantined", URL: upstream.URL, Protocol: "streamable-http", Enabled: true, Quarantined: true}
	rt, err := New(&config.Config{DataDir: t.TempDir(), Listen: "127.0.0.1:0", Servers: []*config.ServerConfig{cfg}}, "", zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })
	seedRT05Approval(t, rt)

	rt.StartBackgroundInitialization()
	settleQuarantinedCapture(t, rt, "quarantined", upstream.URL)

	requireRT05Changed(t, rt, "the connect-time discovery must detect the armed contract while the server is still quarantined")
}

// TestRT05_QuarantinedServer_ContractChangeDetectedOnListChanged: the
// upstream serves the APPROVED contract at connect (verification leaves it
// approved), then arms the tool and signals notifications/tools/list_changed.
// The runtime's list_changed handler — the callback the upstream manager
// invokes for that notification — must verify the new contract while the
// server stays quarantined (Sol r1 P2).
func TestRT05_QuarantinedServer_ContractChangeDetectedOnListChanged(t *testing.T) {
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")
	mcpSrv := mcpserver.NewMCPServer("stage", "0.0.1", mcpserver.WithToolCapabilities(true))
	handler := func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	}
	mcpSrv.AddTool(mcp.NewTool("calendar_get_events", mcp.WithDescription("Lists calendar events.")), handler)
	upstream := httptest.NewServer(mcpserver.NewStreamableHTTPServer(mcpSrv))
	t.Cleanup(upstream.Close)

	cfg := &config.ServerConfig{Name: "quarantined", URL: upstream.URL, Protocol: "streamable-http", Enabled: true, Quarantined: true}
	rt, err := New(&config.Config{DataDir: t.TempDir(), Listen: "127.0.0.1:0", Servers: []*config.ServerConfig{cfg}}, "", zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })
	seedRT05Approval(t, rt)

	rt.StartBackgroundInitialization()
	settleQuarantinedCapture(t, rt, "quarantined", upstream.URL)
	// Let the connect-time verification finish against the approved contract.
	require.Eventually(t, func() bool {
		_, busy := rt.discoveryInProgress.Load("quarantined")
		return !busy
	}, 10*time.Second, 20*time.Millisecond)
	rec, err := rt.storageManager.GetToolApproval("quarantined", "calendar_get_events")
	require.NoError(t, err)
	require.Equal(t, storage.ToolApprovalStatusApproved, rec.Status, "the unchanged contract stays approved at connect")

	// Arm the tool, then deliver the list_changed signal through the handler
	// the upstream manager calls for the notification.
	mcpSrv.DeleteTools("calendar_get_events")
	mcpSrv.AddTool(mcp.NewTool("calendar_get_events", mcp.WithDescription("armed definition")), handler)
	// The handler is invoked exactly once — never re-triggered — so a lost
	// notification cannot be masked by a retry (Sol r3 P2).
	require.NoError(t, rt.onUpstreamToolsListChanged(context.Background(), "quarantined"))

	requireRT05Changed(t, rt, "the list_changed handler must detect the armed contract while the server is still quarantined")
}

// TestRT05_NeverApprovedQuarantinedServer_ReviewStaysUncaptured: the
// connect-time verification only protects an approved baseline. A quarantined
// server that was never approved keeps an uncaptured review (no records) until
// an operator or scan inspects it explicitly.
func TestRT05_NeverApprovedQuarantinedServer_ReviewStaysUncaptured(t *testing.T) {
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")
	upstream := httptest.NewServer(captureTestServer("fresh", "some_tool"))
	t.Cleanup(upstream.Close)

	cfg := &config.ServerConfig{Name: "quarantined", URL: upstream.URL, Protocol: "streamable-http", Enabled: true, Quarantined: true}
	rt, err := New(&config.Config{DataDir: t.TempDir(), Listen: "127.0.0.1:0", Servers: []*config.ServerConfig{cfg}}, "", zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })

	rt.StartBackgroundInitialization()
	settleQuarantinedCapture(t, rt, "quarantined", upstream.URL)
	require.NoError(t, rt.DiscoverAndIndexToolsForServer(context.Background(), "quarantined"))

	records, err := rt.storageManager.ListToolApprovals("quarantined")
	require.NoError(t, err)
	assert.Empty(t, records, "no review records are captured for a never-approved quarantined server")
}

// TestRT05_QuarantinedVerification_ReplacedClientIsReverified: the automatic
// verification lists the approved contract from connection A, and A is
// replaced by B (serving the armed contract) before the gate runs. A's
// capture is discarded; B's own connect callback is dropped by the
// in-progress dedup (held here, as the reactive callback for A holds it), and
// periodic discovery skips quarantined servers. The stale-generation retry
// must therefore re-verify under B in the same discovery call (Sol r2 P2).
// The discovery is invoked exactly once and asserted without polling.
func TestRT05_QuarantinedVerification_ReplacedClientIsReverified(t *testing.T) {
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")
	first := httptest.NewServer(approvedRT05Server())
	t.Cleanup(first.Close)
	second := httptest.NewServer(captureTestServer("armed", "calendar_get_events"))
	t.Cleanup(second.Close)

	cfgA := &config.ServerConfig{Name: "quarantined", URL: first.URL, Protocol: "streamable-http", Enabled: true, Quarantined: true}
	rt, err := New(&config.Config{DataDir: t.TempDir(), Listen: "127.0.0.1:0", Servers: []*config.ServerConfig{cfgA}}, "", zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })
	seedRT05Approval(t, rt)

	rt.StartBackgroundInitialization()
	settleQuarantinedCapture(t, rt, "quarantined", first.URL)

	// Take the dedup marker atomically, as the reactive connect callback
	// that started this verification does, and hold it for the whole call:
	// no background connect or notification discovery can run (B's connect
	// callback is coalesced and cannot mask the gap), and none can reach the
	// interleaving hook below (Sol r7 P2). A plain Store after a !busy check
	// would let a background owner that won the race in between run
	// alongside this call.
	require.Eventually(t, func() bool {
		_, loaded := rt.discoveryInProgress.LoadOrStore("quarantined", struct{}{})
		return !loaded
	}, 10*time.Second, 20*time.Millisecond, "the test must own discovery for the server exclusively")
	defer rt.discoveryInProgress.Delete("quarantined")
	rec, err := rt.storageManager.GetToolApproval("quarantined", "calendar_get_events")
	require.NoError(t, err)
	require.Equal(t, storage.ToolApprovalStatusApproved, rec.Status, "A serves the approved contract")

	// The hook fires only inside this goroutine's own discovery call.
	var armed, replaced atomic.Bool
	rt.quarantinedCaptureBeforePersist = func() {
		if !armed.Load() || !replaced.CompareAndSwap(false, true) {
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

	armed.Store(true)
	discoverErr := rt.discoverAndIndexToolsForServer(context.Background(), "quarantined", false)
	armed.Store(false)
	require.NoError(t, discoverErr)
	require.True(t, replaced.Load(), "the client must be replaced during the automatic verification")

	rec, err = rt.storageManager.GetToolApproval("quarantined", "calendar_get_events")
	require.NoError(t, err)
	assert.Equal(t, storage.ToolApprovalStatusChanged, rec.Status, "the replacement connection's armed contract must be verified in the same call")
	assert.Equal(t, "armed definition", rec.CurrentDescription)
	assert.True(t, rt.serverIsQuarantined("quarantined"))
	indexed, err := rt.indexManager.GetToolsByServer("quarantined")
	require.NoError(t, err)
	assert.Empty(t, indexed, "a quarantined server's tools must never be indexed")
}

// approvedRT05Server serves the contract seedRT05Approval approved.
func approvedRT05Server() *mcpserver.StreamableHTTPServer {
	srv := mcpserver.NewMCPServer("approved", "0.0.1", mcpserver.WithToolCapabilities(true))
	srv.AddTool(mcp.NewTool("calendar_get_events", mcp.WithDescription("Lists calendar events.")), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	})
	return mcpserver.NewStreamableHTTPServer(srv)
}

// TestRT05_ListChangedDuringInFlightVerification_IsFollowedUp (Sol r3 P2): a
// single tools/list_changed notification arrives while a verification of the
// same server is in flight — after that verification already listed the
// approved contract. The notification must not be dropped by the in-progress
// dedup: the in-flight owner runs one follow-up verification that sees the
// armed contract. Periodic discovery skips quarantined servers, so nothing
// else would. Deterministic: one outer trigger, one notification, no polling.
func TestRT05_ListChangedDuringInFlightVerification_IsFollowedUp(t *testing.T) {
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")
	mcpSrv := mcpserver.NewMCPServer("stage", "0.0.1", mcpserver.WithToolCapabilities(true))
	handler := func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	}
	mcpSrv.AddTool(mcp.NewTool("calendar_get_events", mcp.WithDescription("Lists calendar events.")), handler)
	upstream := httptest.NewServer(mcpserver.NewStreamableHTTPServer(mcpSrv))
	t.Cleanup(upstream.Close)

	cfg := &config.ServerConfig{Name: "quarantined", URL: upstream.URL, Protocol: "streamable-http", Enabled: true, Quarantined: true}
	rt, err := New(&config.Config{DataDir: t.TempDir(), Listen: "127.0.0.1:0", Servers: []*config.ServerConfig{cfg}}, "", zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })
	seedRT05Approval(t, rt)

	rt.StartBackgroundInitialization()
	settleQuarantinedCapture(t, rt, "quarantined", upstream.URL)
	require.Eventually(t, func() bool {
		_, busy := rt.discoveryInProgress.Load("quarantined")
		return !busy
	}, 10*time.Second, 20*time.Millisecond)
	rec, err := rt.storageManager.GetToolApproval("quarantined", "calendar_get_events")
	require.NoError(t, err)
	require.Equal(t, storage.ToolApprovalStatusApproved, rec.Status)

	// While the outer verification is between its tools/list (approved
	// contract) and persisting it, the server arms the tool and sends ONE
	// notification. The dedup marker is held, so the notification returns
	// at once — it must be coalesced, not dropped.
	var notified atomic.Int32
	var coalescedReturned atomic.Bool
	rt.quarantinedCaptureBeforePersist = func() {
		if !notified.CompareAndSwap(0, 1) {
			return
		}
		mcpSrv.DeleteTools("calendar_get_events")
		mcpSrv.AddTool(mcp.NewTool("calendar_get_events", mcp.WithDescription("armed definition")), handler)
		_, busy := rt.discoveryInProgress.Load("quarantined")
		require.True(t, busy, "the notification must race an in-flight verification")
		require.NoError(t, rt.onUpstreamToolsListChanged(context.Background(), "quarantined"))
		coalescedReturned.Store(true)
	}

	require.NoError(t, rt.onUpstreamToolsListChanged(context.Background(), "quarantined"))
	require.True(t, coalescedReturned.Load(), "the single notification was delivered during the in-flight verification")
	assert.Equal(t, int32(1), notified.Load())

	// No polling: the outer call returns only after its follow-up ran.
	rec, err = rt.storageManager.GetToolApproval("quarantined", "calendar_get_events")
	require.NoError(t, err)
	assert.Equal(t, storage.ToolApprovalStatusChanged, rec.Status, "the coalesced notification triggered an automatic follow-up verification")
	assert.Equal(t, "armed definition", rec.CurrentDescription)
	assert.True(t, rt.serverIsQuarantined("quarantined"))
	indexed, err := rt.indexManager.GetToolsByServer("quarantined")
	require.NoError(t, err)
	assert.Empty(t, indexed, "a quarantined server's tools must never be indexed")
	_, busy := rt.discoveryInProgress.Load("quarantined")
	assert.False(t, busy, "the marker is released after the follow-up")
}
