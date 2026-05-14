package runtime

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

func setupConfigFilterRuntime(t *testing.T, servers []*config.ServerConfig) *Runtime {
	t.Helper()
	tempDir := t.TempDir()
	cfg := &config.Config{
		DataDir: tempDir,
		Listen:  "127.0.0.1:0",
		Servers: servers,
	}
	rt, err := New(cfg, "", zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })
	return rt
}

// TestApplyConfigToolFilter_EnabledTools_DisablesNonListedTools verifies that
// when a server has enabled_tools set, tools not in that list are disabled in
// BBolt so they are hidden from MCP clients.
func TestApplyConfigToolFilter_EnabledTools_DisablesNonListedTools(t *testing.T) {
	rt := setupConfigFilterRuntime(t, []*config.ServerConfig{
		{
			Name:         "github",
			Enabled:      true,
			EnabledTools: []string{"list_issues", "get_issue"},
		},
	})

	tools := []*config.ToolMetadata{
		{ServerName: "github", Name: "list_issues", Description: "List issues", ParamsJSON: `{}`},
		{ServerName: "github", Name: "get_issue", Description: "Get issue", ParamsJSON: `{}`},
		{ServerName: "github", Name: "create_issue", Description: "Create issue", ParamsJSON: `{}`},
		{ServerName: "github", Name: "delete_issue", Description: "Delete issue", ParamsJSON: `{}`},
	}

	err := rt.applyConfigToolFilter("github", tools)
	require.NoError(t, err)

	// Allowed tools should remain enabled (no record or Disabled=false)
	for _, allowed := range []string{"list_issues", "get_issue"} {
		record, err := rt.storageManager.GetToolApproval("github", allowed)
		if err == nil && record != nil {
			assert.False(t, record.Disabled, "tool %q should be enabled", allowed)
		}
		// ErrToolApprovalNotFound is also acceptable (means enabled by default)
	}

	// Non-listed tools must be explicitly disabled
	for _, blocked := range []string{"create_issue", "delete_issue"} {
		record, err := rt.storageManager.GetToolApproval("github", blocked)
		require.NoError(t, err, "expected approval record for %q", blocked)
		assert.True(t, record.Disabled, "tool %q should be disabled", blocked)
	}
}

// TestApplyConfigToolFilter_DisabledTools_DisablesListedTools verifies that
// when a server has disabled_tools set, only those specific tools are disabled.
func TestApplyConfigToolFilter_DisabledTools_DisablesListedTools(t *testing.T) {
	rt := setupConfigFilterRuntime(t, []*config.ServerConfig{
		{
			Name:          "github",
			Enabled:       true,
			DisabledTools: []string{"delete_repo", "force_push"},
		},
	})

	tools := []*config.ToolMetadata{
		{ServerName: "github", Name: "list_repos", Description: "List repos", ParamsJSON: `{}`},
		{ServerName: "github", Name: "delete_repo", Description: "Delete repo", ParamsJSON: `{}`},
		{ServerName: "github", Name: "force_push", Description: "Force push", ParamsJSON: `{}`},
	}

	err := rt.applyConfigToolFilter("github", tools)
	require.NoError(t, err)

	// Listed tools must be disabled
	for _, blocked := range []string{"delete_repo", "force_push"} {
		record, err := rt.storageManager.GetToolApproval("github", blocked)
		require.NoError(t, err, "expected approval record for %q", blocked)
		assert.True(t, record.Disabled, "tool %q should be disabled", blocked)
	}

	// Non-listed tools should remain enabled
	record, err := rt.storageManager.GetToolApproval("github", "list_repos")
	if err == nil && record != nil {
		assert.False(t, record.Disabled, "tool %q should be enabled", "list_repos")
	}
}

// TestApplyConfigToolFilter_NoFilter_NoChanges verifies that when neither
// enabled_tools nor disabled_tools is set, no records are written.
func TestApplyConfigToolFilter_NoFilter_NoChanges(t *testing.T) {
	rt := setupConfigFilterRuntime(t, []*config.ServerConfig{
		{Name: "github", Enabled: true},
	})

	tools := []*config.ToolMetadata{
		{ServerName: "github", Name: "list_issues", Description: "List issues", ParamsJSON: `{}`},
	}

	err := rt.applyConfigToolFilter("github", tools)
	require.NoError(t, err)

	// No approval record should have been written
	_, err = rt.storageManager.GetToolApproval("github", "list_issues")
	assert.ErrorIs(t, err, storage.ErrToolApprovalNotFound)
}

// TestApplyConfigToolFilter_EnabledTools_ReEnablesTool verifies that a tool
// previously disabled (e.g. by the API) is re-enabled if it appears in
// enabled_tools on the next applyConfigToolFilter call.
func TestApplyConfigToolFilter_EnabledTools_ReEnablesTool(t *testing.T) {
	rt := setupConfigFilterRuntime(t, []*config.ServerConfig{
		{
			Name:         "github",
			Enabled:      true,
			EnabledTools: []string{"list_issues", "get_issue"},
		},
	})

	// Manually mark get_issue as disabled (simulating a prior API call)
	require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "github",
		ToolName:   "get_issue",
		Status:     storage.ToolApprovalStatusApproved,
		Disabled:   true,
	}))

	tools := []*config.ToolMetadata{
		{ServerName: "github", Name: "list_issues", Description: "List issues", ParamsJSON: `{}`},
		{ServerName: "github", Name: "get_issue", Description: "Get issue", ParamsJSON: `{}`},
	}

	err := rt.applyConfigToolFilter("github", tools)
	require.NoError(t, err)

	// get_issue is in the enabled_tools list — must be re-enabled
	record, err := rt.storageManager.GetToolApproval("github", "get_issue")
	require.NoError(t, err)
	assert.False(t, record.Disabled, "get_issue should be re-enabled by config")
}

// TestApplyDifferentialToolUpdate_RespectsEnabledToolsConfig is an integration
// test verifying that applyDifferentialToolUpdate honours enabled_tools from the
// server config: tools not in the list end up with Disabled=true in storage.
func TestApplyDifferentialToolUpdate_RespectsEnabledToolsConfig(t *testing.T) {
	rt := setupConfigFilterRuntime(t, []*config.ServerConfig{
		{
			Name:         "github",
			Enabled:      true,
			EnabledTools: []string{"list_issues"},
		},
	})

	tools := []*config.ToolMetadata{
		{ServerName: "github", Name: "list_issues", Description: "List issues", ParamsJSON: `{}`},
		{ServerName: "github", Name: "create_issue", Description: "Create issue", ParamsJSON: `{}`},
	}

	err := rt.applyDifferentialToolUpdate(t.Context(), "github", tools)
	require.NoError(t, err)

	blocked, err := rt.storageManager.GetToolApproval("github", "create_issue")
	require.NoError(t, err)
	assert.True(t, blocked.Disabled, "create_issue should be disabled by enabled_tools config")
}