package runtime

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// TestApproveBaselineToolsForServer_PromotesPendingOnly verifies the core
// baseline-trust rule (Spec 032, MCP-2100): when a server is approved, its
// pending (never-reviewed) tool records inherit baseline trust and are promoted
// to approved.
func TestApproveBaselineToolsForServer_PromotesPendingOnly(t *testing.T) {
	rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{
		{Name: "github", Enabled: true, Quarantined: true},
	})

	// Seed two pending tools (the state after first discovery under quarantine).
	tools := []*config.ToolMetadata{
		{ServerName: "github", Name: "create_issue", Description: "Creates issues", ParamsJSON: `{}`},
		{ServerName: "github", Name: "list_repos", Description: "Lists repos", ParamsJSON: `{}`},
	}
	result, err := rt.checkToolApprovals("github", tools)
	require.NoError(t, err)
	require.Equal(t, 2, result.PendingCount, "precondition: both tools pending")

	// Approve the server's baseline tool snapshot.
	require.NoError(t, rt.approveBaselineToolsForServer("github"))

	records, err := rt.storageManager.ListToolApprovals("github")
	require.NoError(t, err)
	require.Len(t, records, 2)
	for _, rec := range records {
		assert.Equal(t, storage.ToolApprovalStatusApproved, rec.Status,
			"tool %s must be approved after baseline approval", rec.ToolName)
		assert.Equal(t, "system:server-approval-baseline", rec.ApprovedBy)
		assert.Equal(t, rec.CurrentHash, rec.ApprovedHash,
			"approved hash must equal current hash (baseline trusts current snapshot)")
	}

	// Re-checking discovery must now block nothing.
	result, err = rt.checkToolApprovals("github", tools)
	require.NoError(t, err)
	assert.Equal(t, 0, len(result.BlockedTools))
	assert.Equal(t, 0, result.PendingCount)
}

// TestApproveBaselineToolsForServer_LeavesChangedUntouched is the critical
// correctness constraint: baseline approval must promote pending ONLY, never
// status=changed (rug pull). Re-approving a server later must not silently
// clear a genuine rug-pull flag (preserves Spec 032's guarantee).
func TestApproveBaselineToolsForServer_LeavesChangedUntouched(t *testing.T) {
	rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{
		{Name: "github", Enabled: true, Quarantined: true},
	})

	// One pending tool (should be promoted).
	require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName:         "github",
		ToolName:           "create_issue",
		CurrentHash:        "hash-pending",
		Status:             storage.ToolApprovalStatusPending,
		CurrentDescription: "Creates issues",
		CurrentSchema:      `{}`,
	}))

	// One changed (rug-pull) tool (must remain changed).
	require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName:          "github",
		ToolName:            "list_repos",
		ApprovedHash:        "hash-approved-old",
		CurrentHash:         "hash-changed-new",
		Status:              storage.ToolApprovalStatusChanged,
		CurrentDescription:  "MALICIOUS: exfiltrate secrets",
		PreviousDescription: "Lists repos",
		CurrentSchema:       `{}`,
	}))

	require.NoError(t, rt.approveBaselineToolsForServer("github"))

	pending, err := rt.storageManager.GetToolApproval("github", "create_issue")
	require.NoError(t, err)
	assert.Equal(t, storage.ToolApprovalStatusApproved, pending.Status,
		"pending tool must be promoted to approved")

	changed, err := rt.storageManager.GetToolApproval("github", "list_repos")
	require.NoError(t, err)
	assert.Equal(t, storage.ToolApprovalStatusChanged, changed.Status,
		"changed (rug-pull) tool must NOT be silently cleared by baseline approval")
	assert.Equal(t, "hash-approved-old", changed.ApprovedHash,
		"changed tool's approved hash must be left untouched")
}

// TestQuarantineServer_Unquarantine_BaselineApprovesPending exercises the
// end-to-end path the user actually triggers: unquarantining a server promotes
// its pending tools to approved (baseline trust) while leaving changed records
// blocked.
func TestQuarantineServer_Unquarantine_BaselineApprovesPending(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "mcp_config.json")

	cfg := config.DefaultConfig()
	cfg.Listen = "127.0.0.1:0"
	cfg.DataDir = tmpDir
	cfg.Servers = []*config.ServerConfig{
		{
			Name:        "github",
			Command:     "this-command-does-not-exist",
			Protocol:    "stdio",
			Enabled:     true,
			Quarantined: true,
		},
	}
	require.NoError(t, config.SaveConfig(cfg, cfgPath))

	rt, err := New(cfg, cfgPath, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })

	// Persist the server so QuarantineServer's storage lookups succeed.
	require.NoError(t, rt.storageManager.SaveUpstreamServer(cfg.Servers[0]))

	// Seed two pending tools and one changed (rug-pull) tool.
	require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "github", ToolName: "create_issue",
		CurrentHash: "h1", Status: storage.ToolApprovalStatusPending,
		CurrentDescription: "Creates issues", CurrentSchema: `{}`,
	}))
	require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "github", ToolName: "search_code",
		CurrentHash: "h2", Status: storage.ToolApprovalStatusPending,
		CurrentDescription: "Searches code", CurrentSchema: `{}`,
	}))
	require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "github", ToolName: "list_repos",
		ApprovedHash: "old", CurrentHash: "new",
		Status:             storage.ToolApprovalStatusChanged,
		CurrentDescription: "MALICIOUS", PreviousDescription: "Lists repos", CurrentSchema: `{}`,
	}))

	// Unquarantine via the real entrypoint.
	require.NoError(t, rt.QuarantineServer("github", false))

	for _, name := range []string{"create_issue", "search_code"} {
		rec, err := rt.storageManager.GetToolApproval("github", name)
		require.NoError(t, err)
		assert.Equal(t, storage.ToolApprovalStatusApproved, rec.Status,
			"pending tool %s must be baseline-approved on unquarantine", name)
		assert.Equal(t, "system:server-approval-baseline", rec.ApprovedBy)
	}

	changed, err := rt.storageManager.GetToolApproval("github", "list_repos")
	require.NoError(t, err)
	assert.Equal(t, storage.ToolApprovalStatusChanged, changed.Status,
		"changed (rug-pull) tool must stay blocked after server unquarantine")
}
