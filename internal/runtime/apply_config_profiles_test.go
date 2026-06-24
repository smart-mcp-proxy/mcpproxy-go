package runtime

import (
	"path/filepath"
	"testing"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDetectConfigChanges_ProfilesTracked verifies that a profiles-only edit is
// detected as a hot-reloadable change. Before MCP-3240's review fix, profile
// add/remove/membership edits were invisible to DetectConfigChanges, so a
// profiles-only reload reported "No configuration changes detected" and never
// reconciled the per-profile Bleve indexes.
func TestDetectConfigChanges_ProfilesTracked(t *testing.T) {
	oldCfg := config.DefaultConfig()
	oldCfg.Servers = []*config.ServerConfig{{Name: "s1"}, {Name: "s2"}}
	oldCfg.Profiles = []config.ProfileConfig{{Name: "alpha", Servers: []string{"s1"}}}

	newCfg := config.DefaultConfig()
	newCfg.Servers = []*config.ServerConfig{{Name: "s1"}, {Name: "s2"}}
	// Add a second profile — servers are unchanged.
	newCfg.Profiles = []config.ProfileConfig{
		{Name: "alpha", Servers: []string{"s1"}},
		{Name: "beta", Servers: []string{"s2"}},
	}

	result := DetectConfigChanges(oldCfg, newCfg)
	require.True(t, result.Success)
	assert.False(t, result.RequiresRestart, "profiles change should be hot-reloadable")
	assert.Contains(t, result.ChangedFields, "profiles", "profiles change must be tracked")
	assert.NotContains(t, result.ChangedFields, "mcpServers", "servers were unchanged")
}

// TestApplyConfig_ProfileOnlyChangeReconcilesIndexes is the regression test for
// the Codex review finding on PR #756: a profile-only ApplyConfig reload (no
// server changes) must trigger per-profile Bleve reconciliation so newly added
// profiles get an index built. Previously reconcileProfileIndexes was only
// reachable through the servers-changed re-discovery path, so adding a profile
// without touching mcpServers left its per-profile index missing.
func TestApplyConfig_ProfileOnlyChangeReconcilesIndexes(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")

	initialCfg := config.DefaultConfig()
	initialCfg.Listen = "127.0.0.1:8080"
	initialCfg.DataDir = tmpDir
	initialCfg.Servers = []*config.ServerConfig{{Name: "s1"}, {Name: "s2"}}
	initialCfg.Profiles = []config.ProfileConfig{{Name: "alpha", Servers: []string{"s1"}}}

	require.NoError(t, config.SaveConfig(initialCfg, cfgPath))

	rt, err := New(initialCfg, cfgPath, zap.NewNop())
	require.NoError(t, err)
	defer func() { _ = rt.Close() }()

	// Populate the shared index: s1 has 2 tools, s2 has 3.
	require.NoError(t, rt.indexManager.BatchIndexTools([]*config.ToolMetadata{
		toolMeta("s1", "a"), toolMeta("s1", "b"),
		toolMeta("s2", "c"), toolMeta("s2", "d"), toolMeta("s2", "e"),
	}))

	// New config: same servers, but add profile "beta" -> [s2] and widen "alpha".
	newCfg := config.DefaultConfig()
	newCfg.Listen = "127.0.0.1:8080"
	newCfg.DataDir = tmpDir
	newCfg.Servers = []*config.ServerConfig{{Name: "s1"}, {Name: "s2"}}
	newCfg.Profiles = []config.ProfileConfig{
		{Name: "alpha", Servers: []string{"s1", "s2"}},
		{Name: "beta", Servers: []string{"s2"}},
	}

	result, err := rt.ApplyConfig(newCfg, cfgPath)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.RequiresRestart, "profile-only change is hot-reloadable")
	assert.Contains(t, result.ChangedFields, "profiles")

	// reconcileProfileIndexes runs synchronously on the profiles-only path, so the
	// per-profile indexes must reflect the new membership immediately.
	assert.Equal(t, uint64(3), profileDocCount(t, rt.indexManager, "beta"),
		"newly added profile beta must have its index built from the shared index")
	assert.Equal(t, uint64(5), profileDocCount(t, rt.indexManager, "alpha"),
		"alpha membership widened to s1+s2 must be reindexed")
}
