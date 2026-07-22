package runtime

import (
	"path/filepath"
	"sync"
	"testing"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"

	"github.com/stretchr/testify/require"
)

// TestConfigCommit_CommitPathsConverge is a regression test for the PR #857
// config-store divergence race.
//
// The runtime keeps the active config in TWO stores that must stay in sync:
//   - configSvc (served by ConfigSnapshot / Config() / live subscribers), and
//   - the legacy r.cfg field (served by GetConfig / the REST /api/v1/config
//     handlers).
//
// Several methods commit to BOTH stores but historically did so under r.mu in
// different orders, releasing r.mu between the two writes:
//   - ApplyConfig       (REST apply path)      — r.cfg first, configSvc last.
//   - ReloadConfiguration (disk / fsnotify)    — configSvc first, r.cfg last.
//   - UpdateConfig      (registry-source CRUD) — configSvc first, r.cfg last.
//   - SaveConfiguration (enable/quarantine/…)  — snapshot read, then configSvc
//   - r.cfg.Servers.
//
// Interleaved, two of these could leave r.cfg and configSvc reporting
// different configs permanently — the REST surface disagreeing with the live
// components with no further event to reconcile them. The fix serializes every
// two-store commit under a shared configCommitMu (acquired outside r.mu). This
// test drives all four paths concurrently and asserts the two stores always
// converge. It is a logical lost-update race, not a data race, so -race alone
// won't flag it — the convergence assertion is what catches it. Before the fix
// this fails on an early round; after it, it always converges.
func TestConfigCommit_CommitPathsConverge(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "mcp_config.json")

	base := config.DefaultConfig()
	base.Listen = "127.0.0.1:0"
	base.DataDir = tmpDir
	require.NoError(t, config.SaveConfig(base, cfgPath))

	rt, err := New(base, cfgPath, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })

	editedWithLimit := func(limit int) *config.Config {
		c := config.DefaultConfig()
		c.Listen = base.Listen
		c.DataDir = base.DataDir
		c.ToolResponseLimit = limit
		return c
	}

	const rounds = 200
	for i := 0; i < rounds; i++ {
		// Distinct per-path limits make any divergence between the two stores
		// observable regardless of which path commits last.
		applyLimit := 100000 + i
		updateLimit := 200000 + i

		var wg sync.WaitGroup
		wg.Add(4)
		go func() {
			defer wg.Done()
			_ = rt.ReloadConfiguration()
		}()
		go func() {
			defer wg.Done()
			_, _ = rt.ApplyConfig(editedWithLimit(applyLimit), cfgPath)
		}()
		go func() {
			defer wg.Done()
			rt.UpdateConfig(editedWithLimit(updateLimit), cfgPath)
		}()
		go func() {
			defer wg.Done()
			_ = rt.SaveConfiguration()
		}()
		wg.Wait()

		// Once all commits have settled, r.cfg (GetConfig / REST) and configSvc
		// (ConfigSnapshot / subscribers) must report the same config.
		legacy, gerr := rt.GetConfig()
		require.NoError(t, gerr)
		snap := rt.ConfigSnapshot().Config
		require.Equal(t, legacy.ToolResponseLimit, snap.ToolResponseLimit,
			"round %d: r.cfg and configSvc diverged (r.cfg=%d configSvc=%d)",
			i, legacy.ToolResponseLimit, snap.ToolResponseLimit)
	}
}
