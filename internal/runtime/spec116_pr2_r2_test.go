package runtime

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage/purgetest"
)

// Spec 116 PR2 review round 2 (Sol chunk 1).

// A configured server whose stored projection row is missing still gets the
// full purge when it is removed: the purge set is built from the committed
// removals, not only from the stored rows.
func TestRemoval_R2_MissingRowStillPurges(t *testing.T) {
	paths := map[string]func(t *testing.T, rt *Runtime, cfgPath string){
		"commit_remove": func(t *testing.T, rt *Runtime, _ string) {
			_, err := rt.Commit("remove_server", func(tx *CommitTx) error { return tx.RemoveServer("acme") })
			require.NoError(t, err)
		},
		"apply_config": func(t *testing.T, rt *Runtime, cfgPath string) {
			next := cloneRuntimeConfig(t, rt.Config())
			next.Servers = []*config.ServerConfig{spec116Live(rt, "acme_x")}
			_, err := rt.ApplyConfig(next, cfgPath)
			require.NoError(t, err)
		},
	}
	for name, remove := range paths {
		t.Run(name, func(t *testing.T) {
			rt, cfgPath := newSpec116Runtime(t, nil, disabledStdio("acme"), disabledStdio("acme_x"))
			purgetest.Seed(t, rt.storageManager, "acme")
			rt.lastGoodToolsMu.Lock()
			rt.lastGoodTools["acme"] = []*config.ToolMetadata{{Name: "echo", ServerName: "acme"}}
			rt.noteCaptureLocked("acme")
			rt.lastGoodToolsMu.Unlock()
			// The projection row is lost; everything else is still there.
			require.NoError(t, rt.storageManager.GetBoltDB().DeleteUpstream("acme"))

			remove(t, rt, cfgPath)

			require.Nil(t, spec116Live(rt, "acme"))
			purgetest.AssertPurged(t, rt.storageManager, "acme", name)
			rt.lastGoodToolsMu.Lock()
			_, snap := rt.lastGoodTools["acme"]
			_, stamp := rt.lastCaptureAt["acme"]
			rt.lastGoodToolsMu.Unlock()
			assert.False(t, snap, "the removal drops the last-good snapshot")
			assert.False(t, stamp, "the removal drops the capture stamp")

			_, err := rt.Commit("create_server", func(tx *CommitTx) error { return tx.CreateServer(disabledStdio("acme")) })
			require.NoError(t, err)
			c := purgetest.Counts(t, rt.storageManager, "acme")
			delete(c, purgetest.Upstream)
			for bucket, n := range c {
				assert.Zero(t, n, "the re-added server inherits %d key(s) of %s", n, bucket)
			}
		})
	}
}

// The removal's OAuth refresh cancel and inspection exemption revoke are
// done inside the commit. A removal that is slow in its after-commit work
// (here parked right after the commit lock is released) can no longer clear
// the fresh refresh schedule or inspection exemption of a same-name server a
// later commit re-added.
func TestRemoval_R2_DelayedCleanupKeepsReplacementState(t *testing.T) {
	rt, _ := newSpec116Runtime(t, nil, disabledStdio("acme"))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, rt.refreshManager.Start(ctx))
	t.Cleanup(rt.refreshManager.Stop)

	// The old server's state.
	rt.refreshManager.OnTokenSaved("acme", time.Now().Add(time.Hour))
	require.NoError(t, rt.supervisor.RequestInspectionExemption("acme", time.Hour))
	require.NotNil(t, rt.refreshManager.GetSchedule("acme"))
	require.True(t, rt.supervisor.IsInspectionExempted("acme"))

	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	rt.testHooks.beforeAfterCommit = func(reason string) {
		if reason == "remove_server" {
			once.Do(func() { close(entered); <-release })
		}
	}
	done := make(chan error, 1)
	go func() {
		_, err := rt.Commit("remove_server", func(tx *CommitTx) error { return tx.RemoveServer("acme") })
		done <- err
	}()
	spec116Await(t, entered, "the removal to park after the commit lock is released")
	assert.Nil(t, rt.refreshManager.GetSchedule("acme"), "the removal cancelled the old refresh schedule in the commit")
	assert.False(t, rt.supervisor.IsInspectionExempted("acme"), "the removal revoked the old exemption in the commit")

	// A later commit re-adds the name; the replacement gets its own state.
	_, err := rt.Commit("create_server", func(tx *CommitTx) error { return tx.CreateServer(disabledStdio("acme")) })
	require.NoError(t, err)
	rt.refreshManager.OnTokenSaved("acme", time.Now().Add(time.Hour))
	require.NoError(t, rt.supervisor.RequestInspectionExemption("acme", time.Hour))

	close(release)
	require.NoError(t, spec116AwaitErr(t, done, "the removal commit"))
	assert.NotNil(t, rt.refreshManager.GetSchedule("acme"), "the replacement's refresh schedule survives the old removal")
	assert.True(t, rt.supervisor.IsInspectionExempted("acme"), "the replacement's exemption survives the old removal")
}
