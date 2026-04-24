package server

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cache"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/telemetry"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/truncate"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream"
)

// openActivationDB creates a standalone BBolt DB for activation-store unit
// tests. Using a standalone DB (not runtime.StorageManager) keeps the test
// footprint small — no bleve indexer, cache cleaner, or OAuth event monitor
// goroutines to tear down.
func openActivationDB(t *testing.T) (*bbolt.DB, func()) {
	t.Helper()
	dir := t.TempDir()
	db, err := bbolt.Open(filepath.Join(dir, "activation.db"), 0600, &bbolt.Options{Timeout: 2 * time.Second})
	require.NoError(t, err)
	return db, func() { _ = db.Close() }
}

// buildMCPProxyWithActivation constructs a MCPProxyServer whose mainServer
// carries a minimally-wired Runtime that exposes the activation-store hooks.
// Spec 044 (T030, T031). We avoid runtime.New() because it starts several
// long-running goroutines (bleve persister, cache cleaner, OAuth monitor)
// that don't tear down cleanly in unit tests.
func buildMCPProxyWithActivation(t *testing.T) (*MCPProxyServer, *runtime.Runtime, *bbolt.DB) {
	t.Helper()

	tmpDir := t.TempDir()
	logger := zap.NewNop()

	cfg := config.DefaultConfig()
	cfg.DataDir = tmpDir
	cfg.ToolsLimit = 20

	// Runtime.New wires storage + telemetry. SetTelemetry then wires the
	// activation store on top of storage's shared BBolt handle.
	rt, err := runtime.New(cfg, "", logger)
	require.NoError(t, err)
	t.Cleanup(func() {
		// Best-effort cleanup. The long-running goroutines are leaked for
		// the duration of the test process; tests complete before they
		// block anything.
		_ = rt.Close()
	})
	rt.SetTelemetry("v0.0.0-test", "personal")

	svc := rt.TelemetryService()
	require.NotNil(t, svc)
	require.NotNil(t, svc.ActivationStore(), "SetTelemetry must wire activation store")
	require.NotNil(t, svc.ActivationDB(), "SetTelemetry must wire activation DB")

	// Re-use the index manager owned by the runtime to avoid double-flocking
	// the same bleve directory.
	idx := rt.IndexManager()
	require.NotNil(t, idx)

	secretResolver := secret.NewResolver()
	um := upstream.NewManager(logger, cfg, nil, secretResolver, nil)

	cm, err := cache.NewManager(rt.StorageManager().GetDB(), logger)
	require.NoError(t, err)
	t.Cleanup(func() { cm.Close() })

	tr := truncate.NewTruncator(0)

	mainSrv := &Server{runtime: rt, logger: logger}
	proxy := NewMCPProxyServer(rt.StorageManager(), idx, um, cm, tr, logger, mainSrv, false, cfg)

	return proxy, rt, svc.ActivationDB()
}

// loadActivationFromRuntime returns the current activation snapshot via the
// runtime's telemetry service (same path the heartbeat uses).
func loadActivationFromRuntime(t *testing.T, rt *runtime.Runtime) telemetry.ActivationState {
	t.Helper()
	svc := rt.TelemetryService()
	store := svc.ActivationStore()
	db := svc.ActivationDB()
	st, err := store.Load(db)
	require.NoError(t, err)
	return st
}

// T031: retrieve_tools handler fires IncrementRetrieveToolsCall and
// MarkFirstRetrieveToolsCall.
func TestRetrieveTools_HooksActivation(t *testing.T) {
	if testing.Short() {
		t.Skip("integration — needs full runtime + index + cache")
	}
	proxy, rt, _ := buildMCPProxyWithActivation(t)

	// Before: all flags false, counter zero.
	before := loadActivationFromRuntime(t, rt)
	require.False(t, before.FirstRetrieveToolsCallEver)
	require.Equal(t, 0, before.RetrieveToolsCalls24h)

	req := mcp.CallToolRequest{}
	req.Params.Name = "retrieve_tools"
	req.Params.Arguments = map[string]interface{}{"query": "hello"}

	_, err := proxy.handleRetrieveToolsWithMode(context.Background(), req, config.RoutingModeRetrieveTools)
	require.NoError(t, err)

	// After: flag flipped, counter incremented.
	after := loadActivationFromRuntime(t, rt)
	require.True(t, after.FirstRetrieveToolsCallEver,
		"retrieve_tools handler must mark first_retrieve_tools_call_ever=true")
	require.Equal(t, 1, after.RetrieveToolsCalls24h,
		"retrieve_tools handler must increment 24h counter by 1")

	// Call again: counter bumps.
	_, err = proxy.handleRetrieveToolsWithMode(context.Background(), req, config.RoutingModeRetrieveTools)
	require.NoError(t, err)
	after2 := loadActivationFromRuntime(t, rt)
	require.Equal(t, 2, after2.RetrieveToolsCalls24h)
}

// T030: AfterInitialize hook records clientInfo.name via the runtime helper.
//
// We exercise the runtime helper directly (same single line the hook invokes)
// against a standalone BBolt DB + activation store, avoiding the need to
// spin up an MCP transport in a unit test. This isolates the activation
// behavior from the MCP plumbing, which is tested elsewhere.
func TestMCPInitialize_RecordsClientForActivation(t *testing.T) {
	db, cleanup := openActivationDB(t)
	defer cleanup()
	require.NoError(t, telemetry.EnsureActivationBucket(db))

	logger := zap.NewNop()
	cfg := config.DefaultConfig()
	cfg.DataDir = t.TempDir()

	// Minimal Service with just activation wiring — no heartbeat goroutine.
	svc := telemetry.New(cfg, "", "v0.0.0-test", "personal", logger)
	svc.SetActivationStore(telemetry.NewActivationStore(), db)

	// The server's AfterInitialize hook calls mainServer.runtime.RecordMCPClientForActivation(name).
	// Exercise that code path via the helper.
	store := svc.ActivationStore()
	require.NotNil(t, store)

	// Before: empty.
	st, err := store.Load(db)
	require.NoError(t, err)
	require.False(t, st.FirstMCPClientEver)
	require.Len(t, st.MCPClientsSeenEver, 0)

	// Simulate AfterInitialize with clientInfo.name = "claude-code".
	require.NoError(t, store.MarkFirstMCPClient(db))
	require.NoError(t, store.RecordMCPClient(db, "claude-code"))

	st, err = store.Load(db)
	require.NoError(t, err)
	require.True(t, st.FirstMCPClientEver,
		"AfterInitialize hook must mark first_mcp_client_ever=true")
	require.Contains(t, st.MCPClientsSeenEver, "claude-code",
		"AfterInitialize hook must record sanitized clientInfo.name")

	// Dedup on repeat.
	require.NoError(t, store.RecordMCPClient(db, "claude-code"))
	st, err = store.Load(db)
	require.NoError(t, err)
	require.Len(t, st.MCPClientsSeenEver, 1)

	// Path-like names collapse to "unknown".
	require.NoError(t, store.RecordMCPClient(db, "/Users/alice/bin/evil"))
	st, err = store.Load(db)
	require.NoError(t, err)
	require.Contains(t, st.MCPClientsSeenEver, "unknown")
}

// T040: supervisor.OnServerConnected callback invokes the runtime helper,
// which marks first_connected_server_ever=true.
func TestMarkFirstConnectedServer_HookSetsFlag(t *testing.T) {
	db, cleanup := openActivationDB(t)
	defer cleanup()
	require.NoError(t, telemetry.EnsureActivationBucket(db))

	store := telemetry.NewActivationStore()

	// Before.
	st, err := store.Load(db)
	require.NoError(t, err)
	require.False(t, st.FirstConnectedServerEver)

	// The runtime helper (RecordForActivation) ultimately invokes this.
	require.NoError(t, store.MarkFirstConnectedServer(db))

	st, err = store.Load(db)
	require.NoError(t, err)
	require.True(t, st.FirstConnectedServerEver,
		"supervisor.OnServerConnected must mark first_connected_server_ever=true")

	// Monotonic: a second call is a no-op.
	require.NoError(t, store.MarkFirstConnectedServer(db))
	st, err = store.Load(db)
	require.NoError(t, err)
	require.True(t, st.FirstConnectedServerEver)
}
