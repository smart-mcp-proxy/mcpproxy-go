package runtime

import (
	"context"
	"net/http/httptest"
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
	require.Eventually(t, func() bool {
		return rt.onUpstreamToolsListChanged(context.Background(), "quarantined") == nil && func() bool {
			rec, err := rt.storageManager.GetToolApproval("quarantined", "calendar_get_events")
			return err == nil && rec.Status == storage.ToolApprovalStatusChanged
		}()
	}, 10*time.Second, 100*time.Millisecond)

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
