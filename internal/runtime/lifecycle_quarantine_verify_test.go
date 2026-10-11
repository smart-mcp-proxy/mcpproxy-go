package runtime

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// TestRT05_QuarantinedServer_ContractChangeDetectedDuringQuarantine reproduces
// EXP-2a: a tool approved earlier is served with an armed contract while the
// server is (re-)quarantined. Discovery for the quarantined server used to be
// skipped entirely, leaving the stale approved contract in place for the whole
// quarantine window. The contract must now be hash-compared while quarantined
// — marked changed — without anything reaching the search index.
func TestRT05_QuarantinedServer_ContractChangeDetectedDuringQuarantine(t *testing.T) {
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")
	upstream := httptest.NewServer(captureTestServer("armed", "calendar_get_events"))
	t.Cleanup(upstream.Close)

	cfg := &config.ServerConfig{Name: "quarantined", URL: upstream.URL, Protocol: "streamable-http", Enabled: true, Quarantined: true}
	rt, err := New(&config.Config{DataDir: t.TempDir(), Listen: "127.0.0.1:0", Servers: []*config.ServerConfig{cfg}}, "", zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })

	// The stage-1 contract the operator approved before the restart.
	approvedSchema := normalizeJSON(`{"type":"object"}`)
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

	rt.StartBackgroundInitialization()
	settleQuarantinedCapture(t, rt, "quarantined", upstream.URL)

	// The connect-time (or this explicit) discovery must verify the contract
	// even though the server is quarantined.
	require.NoError(t, rt.DiscoverAndIndexToolsForServer(context.Background(), "quarantined"))
	require.Eventually(t, func() bool {
		rec, err := rt.storageManager.GetToolApproval("quarantined", "calendar_get_events")
		return err == nil && rec.Status == storage.ToolApprovalStatusChanged
	}, 10*time.Second, 50*time.Millisecond, "the armed contract must be detected while the server is still quarantined")

	rec, err := rt.storageManager.GetToolApproval("quarantined", "calendar_get_events")
	require.NoError(t, err)
	assert.Equal(t, "Lists calendar events.", rec.PreviousDescription)
	assert.Equal(t, "armed definition", rec.CurrentDescription)

	assert.True(t, rt.serverIsQuarantined("quarantined"), "verification must not unquarantine the server")
	indexed, err := rt.indexManager.GetToolsByServer("quarantined")
	require.NoError(t, err)
	assert.Empty(t, indexed, "a quarantined server's tools must never be indexed")
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
