package runtime

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// UX-02 cross-review round 5.

// Finding 1: a discovery pass that passed its (config-based) eligibility check
// pauses before taking the approval lock; the server is then quarantined and
// purged from the index; the pass resumes. It must not re-publish the
// quarantined server's definitions — including previously approved ones —
// to the shared or the per-profile index.
func TestQuarantinedServerNotRepublishedByPausedPass(t *testing.T) {
	cfg := &config.Config{
		DataDir:  t.TempDir(),
		Listen:   "127.0.0.1:0",
		Servers:  []*config.ServerConfig{{Name: "lib", Enabled: true}},
		Profiles: []config.ProfileConfig{{Name: "p", Servers: []string{"lib"}}},
	}
	rt, err := New(cfg, "", zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })
	require.NoError(t, rt.storageManager.SaveUpstreamServer(&config.ServerConfig{Name: "lib", Enabled: true}))

	ctx := context.Background()
	require.NoError(t, rt.applyDifferentialToolUpdate(ctx, "lib", ux02Tools("lib", 2)))
	require.Empty(t, ux02NotApproved(t, rt, "lib"), "trusted baseline approves both tools")
	require.NotNil(t, indexedTool(t, rt, "lib", "read_000"))
	rt.reindexAffectedProfiles("lib")
	require.Equal(t, uint64(2), profileDocCount(t, rt.IndexManager(), "p"))

	paused, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	rt.inventoryApplyHook = func(string) {
		hold := false
		once.Do(func() { hold = true })
		if hold {
			close(paused)
			<-release
		}
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = rt.applyDifferentialToolUpdate(ctx, "lib", ux02Tools("lib", 2))
	}()
	require.True(t, waitOrTimeout(paused, 5*time.Second))

	// The config save fails in this harness (no config path), so the runtime
	// config keeps saying "trusted" — the storage flip and the purge are
	// what a resumed pass has to respect.
	_ = rt.QuarantineServer("lib", true)
	require.Nil(t, indexedTool(t, rt, "lib", "read_000"), "quarantine purges the index")

	close(release)
	require.True(t, waitOrTimeout(done, 5*time.Second))

	tools, err := rt.indexManager.GetToolsByServer("lib")
	require.NoError(t, err)
	require.Empty(t, tools, "a resumed pass must not re-publish a quarantined server to the shared index")
	require.Equal(t, uint64(0), profileDocCount(t, rt.IndexManager(), "p"), "nor to a profile index")
}

// newUX02R5ApprovalRuntime opens a runtime on dir (config file included, so
// quarantine toggles persist) with one manual-trust server "srv".
func newUX02R5ApprovalRuntime(t *testing.T, dir string, quarantined bool) *Runtime {
	t.Helper()
	cfgPath := filepath.Join(dir, "mcp_config.json")
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		require.NoError(t, os.WriteFile(cfgPath, []byte(`{"listen":"127.0.0.1:0","mcpServers":[{"name":"srv","protocol":"stdio","command":"true","enabled":true,"quarantined":true}]}`), 0o600))
	}
	srvCfg := &config.ServerConfig{Name: "srv", Protocol: "stdio", Command: "true", Enabled: true, Quarantined: quarantined}
	cfg := &config.Config{DataDir: dir, Listen: "127.0.0.1:0", Servers: []*config.ServerConfig{srvCfg}}
	rt, err := New(cfg, cfgPath, zap.NewNop())
	require.NoError(t, err)
	if quarantined {
		require.NoError(t, rt.storageManager.SaveUpstreamServer(&config.ServerConfig{Name: "srv", Protocol: "stdio", Command: "true", Enabled: true, Quarantined: true}))
	}
	return rt
}

// Finding 2: approving a quarantined server whose reviewed inventory is EMPTY
// (expected_hashes {}) is still a tool decision. A destructive tool that
// appears afterwards — between the commit and the unquarantine, or after the
// unquarantine — must stay pending and blocked through repeated discovery and
// a restart, not be auto-approved as a "first trusted baseline".
func TestServerApproval_EmptyReviewedInventoryKeepsLaterToolsHeld(t *testing.T) {
	for _, timing := range []string{"before-unquarantine", "after-unquarantine"} {
		t.Run(timing, func(t *testing.T) {
			dir := t.TempDir()
			rt := newUX02R5ApprovalRuntime(t, dir, true)

			n, err := rt.CommitServerApprovalDecision("srv", nil, map[string]string{}, "api", func() error { return nil })
			require.NoError(t, err)
			require.Equal(t, 0, n)

			inventory := []*config.ToolMetadata{ux02Destructive("srv")}
			if timing == "before-unquarantine" {
				_, err = rt.checkToolApprovals("srv", inventory)
				require.NoError(t, err)
			}
			require.NoError(t, rt.UnquarantineServerKeepingToolDecisions("srv", nil, nil))
			srv, err := rt.storageManager.GetUpstreamServer("srv")
			require.NoError(t, err)
			require.False(t, srv.Quarantined)

			for i := 0; i < 2; i++ {
				res, err := rt.checkToolApprovals("srv", inventory)
				require.NoError(t, err)
				require.True(t, res.BlockedTools["drop_all"], "pass %d: the tool must stay blocked", i)
				require.Equal(t, []string{"drop_all=pending"}, ux02NotApproved(t, rt, "srv"), "pass %d", i)
			}

			// Restart: the decision is durable.
			require.NoError(t, rt.Close())
			rt = newUX02R5ApprovalRuntime(t, dir, false)
			t.Cleanup(func() { _ = rt.Close() })
			res, err := rt.checkToolApprovals("srv", inventory)
			require.NoError(t, err)
			require.True(t, res.BlockedTools["drop_all"], "after restart the tool must stay blocked")
			require.Equal(t, []string{"drop_all=pending"}, ux02NotApproved(t, rt, "srv"))
		})
	}
}
