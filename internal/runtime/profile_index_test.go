package runtime

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/index"
)

// newProfileTestRuntime builds a minimal Runtime wired to a real on-disk index
// manager and the given config, sufficient to exercise per-profile index
// reconciliation without standing up the full runtime.
func newProfileTestRuntime(t *testing.T, cfg *config.Config) (*Runtime, *index.Manager) {
	t.Helper()
	dataDir := t.TempDir()
	cfg.DataDir = dataDir

	mgr, err := index.NewManager(dataDir, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close() })

	r := &Runtime{
		logger:            zap.NewNop(),
		cfg:               cfg,
		indexManager:      mgr,
		profileMembership: make(map[string][]string),
	}
	return r, mgr
}

func toolMeta(server, tool string) *config.ToolMetadata {
	return &config.ToolMetadata{
		Name:        server + ":" + tool,
		ServerName:  server,
		Description: "tool " + tool,
		ParamsJSON:  `{"type":"object","properties":{},"required":[]}`,
		Hash:        server + "-" + tool,
	}
}

func profileDocCount(t *testing.T, mgr *index.Manager, slug string) uint64 {
	t.Helper()
	pm, err := mgr.ForProfile(slug)
	require.NoError(t, err)
	c, err := pm.GetDocumentCount()
	require.NoError(t, err)
	return c
}

func TestReconcileProfileIndexes_BuildsAndIsolates(t *testing.T) {
	cfg := &config.Config{
		Servers: []*config.ServerConfig{{Name: "s1"}, {Name: "s2"}},
		Profiles: []config.ProfileConfig{
			{Name: "alpha", Servers: []string{"s1"}},
			{Name: "beta", Servers: []string{"s2"}},
		},
	}
	r, mgr := newProfileTestRuntime(t, cfg)

	// Populate the shared index: s1 has 2 tools, s2 has 3.
	require.NoError(t, mgr.BatchIndexTools([]*config.ToolMetadata{
		toolMeta("s1", "a"), toolMeta("s1", "b"),
		toolMeta("s2", "c"), toolMeta("s2", "d"), toolMeta("s2", "e"),
	}))

	r.reconcileProfileIndexes()

	assert.Equal(t, uint64(2), profileDocCount(t, mgr, "alpha"))
	assert.Equal(t, uint64(3), profileDocCount(t, mgr, "beta"))

	// Reload isolation: widen alpha to {s1, s2}; beta must be untouched.
	cfg.Profiles[0].Servers = []string{"s1", "s2"}
	r.reconcileProfileIndexes()

	assert.Equal(t, uint64(5), profileDocCount(t, mgr, "alpha"), "alpha rebuilt with both servers")
	assert.Equal(t, uint64(3), profileDocCount(t, mgr, "beta"), "beta doc-count must be unchanged")
}

func TestReconcileProfileIndexes_DropsRemovedProfile(t *testing.T) {
	cfg := &config.Config{
		Servers: []*config.ServerConfig{{Name: "s1"}},
		Profiles: []config.ProfileConfig{
			{Name: "alpha", Servers: []string{"s1"}},
		},
	}
	r, mgr := newProfileTestRuntime(t, cfg)
	require.NoError(t, mgr.BatchIndexTools([]*config.ToolMetadata{toolMeta("s1", "a")}))

	r.reconcileProfileIndexes()
	alphaDir := filepath.Join(cfg.DataDir, "index.bleve", "profiles", "alpha")
	_, statErr := os.Stat(alphaDir)
	require.NoError(t, statErr, "alpha index dir should exist after build")

	// Remove the profile from config and reconcile: its dir must be dropped.
	cfg.Profiles = nil
	r.reconcileProfileIndexes()

	_, statErr = os.Stat(alphaDir)
	assert.True(t, os.IsNotExist(statErr), "removed profile's index dir must be deleted")
}

func TestReindexAffectedProfiles_OnlyTouchesProfilesWithServer(t *testing.T) {
	cfg := &config.Config{
		Servers: []*config.ServerConfig{{Name: "s1"}, {Name: "s2"}},
		Profiles: []config.ProfileConfig{
			{Name: "alpha", Servers: []string{"s1"}},
			{Name: "beta", Servers: []string{"s2"}},
		},
	}
	r, mgr := newProfileTestRuntime(t, cfg)
	require.NoError(t, mgr.BatchIndexTools([]*config.ToolMetadata{
		toolMeta("s1", "a"), toolMeta("s2", "c"),
	}))
	r.reconcileProfileIndexes()
	assert.Equal(t, uint64(1), profileDocCount(t, mgr, "alpha"))
	assert.Equal(t, uint64(1), profileDocCount(t, mgr, "beta"))

	// A new tool appears on s1 in the shared index; refresh only s1's profiles.
	require.NoError(t, mgr.IndexTool(toolMeta("s1", "b")))
	r.reindexAffectedProfiles("s1")

	assert.Equal(t, uint64(2), profileDocCount(t, mgr, "alpha"), "alpha picks up new s1 tool")
	assert.Equal(t, uint64(1), profileDocCount(t, mgr, "beta"), "beta (no s1) is untouched")
}
