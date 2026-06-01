package runtime

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

func newUsageTestService(t *testing.T) (*ActivityService, *storage.Manager) {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "usage_svc_test_*")
	require.NoError(t, err)
	mgr, err := storage.NewManager(tmpDir, zap.NewNop().Sugar())
	require.NoError(t, err)
	t.Cleanup(func() {
		mgr.Close() // close before RemoveAll (Windows handle gotcha)
		os.RemoveAll(tmpDir)
	})
	return NewActivityService(mgr, zap.NewNop()), mgr
}

func saveToolCall(t *testing.T, mgr *storage.Manager, server, tool, status string, respBytes int, ts time.Time) {
	t.Helper()
	require.NoError(t, mgr.SaveActivity(&storage.ActivityRecord{
		Type:          storage.ActivityTypeToolCall,
		ServerName:    server,
		ToolName:      tool,
		Status:        status,
		ResponseBytes: respBytes,
		Timestamp:     ts,
	}))
}

func TestActivityService_ColdStart_RebuildsFromScan(t *testing.T) {
	svc, mgr := newUsageTestService(t)
	base := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	saveToolCall(t, mgr, "github", "search", "success", 1000, base)
	saveToolCall(t, mgr, "github", "search", "error", 500, base.Add(time.Minute))
	saveToolCall(t, mgr, "gitlab", "list", "success", 2000, base)

	svc.initUsageFromStorage()

	snap := svc.UsageSnapshot()
	require.NotNil(t, snap)
	gh := snap.Tools[toolKey("github", "search")]
	require.NotNil(t, gh)
	assert.Equal(t, int64(2), gh.Calls)
	assert.Equal(t, int64(1), gh.Errors)
	assert.Equal(t, int64(1500), gh.RespBytesSum)
	require.NotNil(t, snap.Tools[toolKey("gitlab", "list")])
}

func TestActivityService_ColdStart_LoadsPersistedSnapshotWithoutRebuild(t *testing.T) {
	svc, mgr := newUsageTestService(t)
	// Storage has NO tool_call records, but a persisted snapshot claims a tool
	// with 99 calls. A correct load (not a rebuild-from-scan) yields 99.
	persisted := newUsageAggregate()
	persisted.Apply(&storage.ActivityRecord{
		Type: storage.ActivityTypeToolCall, ServerName: "cached", ToolName: "tool",
		Status: "success", ResponseBytes: 10, Timestamp: time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC),
	})
	// Bump to 99 by applying 98 more.
	for i := 0; i < 98; i++ {
		persisted.Apply(&storage.ActivityRecord{
			Type: storage.ActivityTypeToolCall, ServerName: "cached", ToolName: "tool",
			Status: "success", ResponseBytes: 10, Timestamp: time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC),
		})
	}
	data, err := encodeUsageAggregate(persisted)
	require.NoError(t, err)
	require.NoError(t, mgr.SaveUsageSnapshot(data))

	svc.initUsageFromStorage()

	snap := svc.UsageSnapshot()
	require.NotNil(t, snap)
	tu := snap.Tools[toolKey("cached", "tool")]
	require.NotNil(t, tu, "loaded from snapshot, not rebuilt from (empty) scan")
	assert.Equal(t, int64(99), tu.Calls)
}

func TestActivityService_PersistUsage_RoundTrips(t *testing.T) {
	svc, mgr := newUsageTestService(t)
	ts := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	svc.usage.Apply(&storage.ActivityRecord{
		Type: storage.ActivityTypeToolCall, ServerName: "s", ToolName: "t",
		Status: "success", ResponseBytes: 123, Timestamp: ts,
	})

	svc.persistUsage()

	data, err := mgr.LoadUsageSnapshot()
	require.NoError(t, err)
	require.NotEmpty(t, data)
	decoded, err := decodeUsageAggregate(data)
	require.NoError(t, err)
	tu := decoded.Tools[toolKey("s", "t")]
	require.NotNil(t, tu)
	assert.Equal(t, int64(1), tu.Calls)
	assert.Equal(t, int64(123), tu.RespBytesSum)
}

func TestActivityService_HandleEvent_AppliesToolCallToUsage(t *testing.T) {
	svc, _ := newUsageTestService(t)
	evt := Event{
		Type:      EventTypeActivityToolCallCompleted,
		Timestamp: time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC),
		Payload: map[string]any{
			"server_name":    "github",
			"tool_name":      "search",
			"status":         "success",
			"response_bytes": int64(4096),
			"request_bytes":  int64(128),
			"duration_ms":    int64(42),
		},
	}

	svc.handleEvent(evt)

	snap := svc.UsageSnapshot()
	require.NotNil(t, snap)
	tu := snap.Tools[toolKey("github", "search")]
	require.NotNil(t, tu)
	assert.Equal(t, int64(1), tu.Calls)
	assert.Equal(t, int64(4096), tu.RespBytesSum)
	assert.Equal(t, int64(128), tu.ReqBytesSum)
}

// TestActivityService_HandleEvent_CountsBlockedPolicyDecision (MCP-835 / Codex
// finding #2): a blocked tool attempt is emitted as a policy_decision event,
// not a tool_call. The live path must fold it into the usage aggregate so the
// per-tool `blocked` count is non-zero — matching what a cold-start scan would
// rebuild from the persisted record.
func TestActivityService_HandleEvent_CountsBlockedPolicyDecision(t *testing.T) {
	svc, _ := newUsageTestService(t)
	evt := Event{
		Type:      EventTypeActivityPolicyDecision,
		Timestamp: time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC),
		Payload: map[string]any{
			"server_name": "github",
			"tool_name":   "search",
			"decision":    "blocked",
			"reason":      "Server is quarantined for security review",
		},
	}

	svc.handleEvent(evt)

	snap := svc.UsageSnapshot()
	require.NotNil(t, snap)
	tu := snap.Tools[toolKey("github", "search")]
	require.NotNil(t, tu, "blocked policy decision must reach the usage aggregate")
	assert.Equal(t, int64(1), tu.Blocked)
	assert.Equal(t, int64(0), tu.Calls, "blocked attempt is not an executed call")
}

func TestActivityService_SetUsagePersistInterval_HotReload(t *testing.T) {
	svc, _ := newUsageTestService(t)
	assert.Equal(t, DefaultUsagePersistInterval, svc.usagePersistInterval())

	svc.SetUsagePersistInterval(2 * time.Second)
	assert.Equal(t, 2*time.Second, svc.usagePersistInterval())

	// Non-positive values are ignored (keeps last good cadence).
	svc.SetUsagePersistInterval(0)
	assert.Equal(t, 2*time.Second, svc.usagePersistInterval())
}
