package runtime

import (
	"path/filepath"
	"sync"
	"testing"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"

	"github.com/stretchr/testify/require"
)

// TestConfigCommit_ApplyAndReloadConverge is a regression test for the PR #857
// config-store divergence race.
//
// The runtime keeps the active config in TWO stores that must stay in sync:
//   - configSvc (served by ConfigSnapshot / Config() / live subscribers), and
//   - the legacy r.cfg field (served by GetConfig / the REST /api/v1/config
//     handlers).
//
// ReloadConfiguration (disk-reload / fsnotify-watcher path) updates configSvc
// via ReloadFromFile BEFORE taking r.mu to set r.cfg, while ApplyConfig (API
// path) sets r.cfg under r.mu and only updates configSvc AFTER releasing it.
// A watcher-triggered reload of config A interleaving with an API apply of B
// could therefore end with r.cfg=A while configSvc=B permanently — the REST
// surface and the live components disagreeing with no further event to
// reconcile them.
//
// Running the two paths concurrently many times, the two stores must always
// converge to the same value after each round. Before the fix (a shared
// configCommitMu serializing the whole commit in both paths) this fails on
// some round; after it, it always converges. This is a logical lost-update
// race, not a data race, so -race alone won't flag it — the convergence
// assertion is what catches it.
func TestConfigCommit_ApplyAndReloadConverge(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "mcp_config.json")

	base := config.DefaultConfig()
	base.Listen = "127.0.0.1:0"
	base.DataDir = tmpDir
	require.NoError(t, config.SaveConfig(base, cfgPath))

	rt, err := New(base, cfgPath, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })

	const rounds = 200
	for i := 0; i < rounds; i++ {
		// Disk currently holds the previous round's config; the reload reads
		// whatever is on disk, while the apply writes a fresh, distinct value
		// (and overwrites disk). Distinct limits make a divergence observable.
		applyLimit := 100000 + i

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = rt.ReloadConfiguration()
		}()
		go func(limit int) {
			defer wg.Done()
			edited := config.DefaultConfig()
			edited.Listen = base.Listen
			edited.DataDir = base.DataDir
			edited.ToolResponseLimit = limit
			_, _ = rt.ApplyConfig(edited, cfgPath)
		}(applyLimit)
		wg.Wait()

		// r.cfg (GetConfig / REST) and configSvc (ConfigSnapshot / subscribers)
		// must report the same config once both commits have settled.
		legacy, gerr := rt.GetConfig()
		require.NoError(t, gerr)
		snap := rt.ConfigSnapshot().Config
		require.Equal(t, legacy.ToolResponseLimit, snap.ToolResponseLimit,
			"round %d: r.cfg and configSvc diverged (r.cfg=%d configSvc=%d)",
			i, legacy.ToolResponseLimit, snap.ToolResponseLimit)
	}
}
