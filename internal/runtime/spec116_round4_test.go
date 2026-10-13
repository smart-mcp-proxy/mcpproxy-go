package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/configsvc"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// r4SeedRowDrift rewrites name's stored row with mutate applied, keeping its
// stamp: the stored projection drifts from the live config without any change
// to the live snapshot.
func r4SeedRowDrift(t *testing.T, rt *Runtime, name string, mutate func(*config.ServerConfig)) {
	t.Helper()
	row := storedStamp(t, rt, name)
	drifted := cloneRuntimeConfig(t, &config.Config{Servers: []*config.ServerConfig{spec116Live(rt, name)}}).Servers[0]
	mutate(drifted)
	require.NoError(t, rt.storageManager.ApplyServerCommit(&storage.ServerCommitBatch{
		Generation: rt.ConfigSnapshot().Version,
		Saves:      []storage.ServerSave{{Config: drifted, Stamp: row.Stamp}},
	}))
	require.False(t, storage.ServerRowMatches(spec116Live(rt, name), storedStamp(t, rt, name).Config), "precondition: the row drifted")
}

// r4ParkDiscovery starts a discovery pass for name that parks after its
// approval check while holding name's approval lock.
func r4ParkDiscovery(t *testing.T, rt *Runtime, name string, tools []*config.ToolMetadata) (release func(), done <-chan error) {
	t.Helper()
	entered := make(chan struct{})
	rel := make(chan struct{})
	var once sync.Once
	rt.testHooks.discoveryBeforeIndex = func(server string) {
		if server != name {
			return
		}
		first := false
		once.Do(func() { first = true })
		if first {
			close(entered)
			<-rel
		}
	}
	passDone := make(chan error, 1)
	go func() { passDone <- rt.applyDifferentialToolUpdate(context.Background(), name, tools) }()
	spec116Await(t, entered, "the discovery pass to park holding the approval lock")
	var relOnce sync.Once
	return func() { relOnce.Do(func() { close(rel) }) }, passDone
}

func r4UnrelatedCommit(rt *Runtime) <-chan error {
	done := make(chan error, 1)
	go func() {
		_, err := rt.Commit("unrelated", func(tx *CommitTx) error {
			return tx.UpdateServer("other", func(cur *config.ServerConfig) error {
				cur.Args = []string{"bump"}
				return nil
			})
		})
		done <- err
	}()
	return done
}

// Round 4, finding 1 (#1571, D15/D16): a projection repair of a server the
// commit did not change rewrites its stored approval policy, so it takes that
// server's approval lock. Live says manual trust, the row says auto; a
// discovery pass parked under the lock must finish before the repair lands,
// and a later changed tool is held for review.
func TestCommit_R4_ProjectionRepairTakesApprovalLock(t *testing.T) {
	drift := unreachableHTTP("drift")
	drift.TrustMode = string(config.TrustModeManual)
	rt, _ := newSpec116Runtime(t, nil, drift, disabledStdio("other"))
	r4SeedRowDrift(t, rt, "drift", func(sc *config.ServerConfig) { sc.TrustMode = string(config.TrustModeAuto) })

	release, passDone := r4ParkDiscovery(t, rt, "drift", []*config.ToolMetadata{
		{ServerName: "drift", Name: "t1", Description: "d", ParamsJSON: `{"type":"object"}`, Hash: "h1"},
	})
	defer release()
	commitDone := r4UnrelatedCommit(rt)
	returned, err := spec116AwaitReturnOrBlocked(t, commitDone, func() bool { return rt.approvalLockWaiters("drift") > 0 })
	assert.False(t, returned, "the projection repair must wait for the pass that holds the server's approval lock (err=%v)", err)
	release()
	require.NoError(t, spec116AwaitErr(t, passDone, "pass"))
	if !returned {
		require.NoError(t, spec116AwaitErr(t, commitDone, "commit"))
	}
	rt.testHooks.discoveryBeforeIndex = nil
	assert.Equal(t, string(config.TrustModeManual), storedStamp(t, rt, "drift").Config.TrustMode, "the row is repaired")

	require.NoError(t, rt.applyDifferentialToolUpdate(context.Background(), "drift", []*config.ToolMetadata{
		{ServerName: "drift", Name: "t1", Description: "changed", ParamsJSON: `{"type":"object"}`, Hash: "h2"},
	}))
	rec, err := rt.storageManager.GetToolApproval("drift", "t1")
	require.NoError(t, err)
	assert.Equal(t, storage.ToolApprovalStatusChanged, rec.Status, "after the repair a changed tool is held for review (manual)")
}

// Round 4, finding 1, quarantined variant: live quarantined, row not. The row
// makes discovery eligible, so a pass can be parked about to index; the
// repair and its index purge wait for it, and the index ends up empty.
func TestCommit_R4_QuarantinedProjectionRepairPurgesAfterPass(t *testing.T) {
	q := unreachableHTTP("q")
	q.Quarantined = true
	q.TrustMode = string(config.TrustModeAuto)
	rt, _ := newSpec116Runtime(t, nil, q, disabledStdio("other"))
	r4SeedRowDrift(t, rt, "q", func(sc *config.ServerConfig) { sc.Quarantined = false })

	release, passDone := r4ParkDiscovery(t, rt, "q", []*config.ToolMetadata{
		{ServerName: "q", Name: "t1", Description: "d", ParamsJSON: `{"type":"object"}`, Hash: "h1"},
	})
	defer release()
	commitDone := r4UnrelatedCommit(rt)
	returned, err := spec116AwaitReturnOrBlocked(t, commitDone, func() bool { return rt.approvalLockWaiters("q") > 0 })
	assert.False(t, returned, "the quarantine repair must wait for the pass that holds the server's approval lock (err=%v)", err)
	release()
	require.NoError(t, spec116AwaitErr(t, passDone, "pass"))
	if !returned {
		require.NoError(t, spec116AwaitErr(t, commitDone, "commit"))
	}
	assert.True(t, storedStamp(t, rt, "q").Config.Quarantined, "the row is repaired")
	tools, err := rt.indexManager.GetToolsByServer("q")
	require.NoError(t, err)
	assert.Empty(t, tools, "a quarantined server's tools are purged after the in-flight pass, not before it")
}

// Round 4, finding 2: a path-only UpdateConfig writes the committed config at
// the new path, both when no file is there and when one with other servers is.
func TestCommit_R4_PathOnlyUpdateConfigWritesNewPath(t *testing.T) {
	for _, existing := range []bool{false, true} {
		rt, pathA := newSpec116Runtime(t, nil, disabledStdio("a"))
		pathB := filepath.Join(filepath.Dir(pathA), "other.json")
		if existing {
			foreign := cloneRuntimeConfig(t, rt.Config())
			foreign.Servers = []*config.ServerConfig{disabledStdio("foreign")}
			require.NoError(t, config.SaveConfig(foreign, pathB))
		}
		rt.UpdateConfig(cloneRuntimeConfig(t, rt.Config()), pathB)
		assert.Equal(t, pathB, rt.ConfigPath())
		onDisk, err := config.LoadFromFile(pathB)
		require.NoError(t, err, "existing=%v: the new path holds a config", existing)
		names := map[string]bool{}
		for _, sc := range onDisk.Servers {
			names[sc.Name] = true
		}
		assert.Equal(t, map[string]bool{"a": true}, names, "existing=%v: the new path holds the committed config", existing)

		// A reload from the new path keeps the committed set.
		require.NoError(t, rt.ReloadConfiguration())
		assert.NotNil(t, spec116Live(rt, "a"))
		assert.Nil(t, spec116Live(rt, "foreign"))
	}
}

// Round 4, finding 2: a failed disk or storage step leaves the old path
// published in both stores and the destination as it was.
func TestCommit_R4_PathOnlyUpdateConfigFailureKeepsOldPath(t *testing.T) {
	for _, fault := range []string{"disk", "storage"} {
		rt, pathA := newSpec116Runtime(t, nil, disabledStdio("a"))
		pathB := filepath.Join(filepath.Dir(pathA), "other.json")
		foreign := cloneRuntimeConfig(t, rt.Config())
		foreign.Servers = []*config.ServerConfig{disabledStdio("foreign")}
		require.NoError(t, config.SaveConfig(foreign, pathB))
		before, err := os.ReadFile(pathB)
		require.NoError(t, err)
		injected := func(string) error { return errors.New("injected " + fault + " failure") }
		if fault == "disk" {
			rt.testHooks.diskWriteFault = injected
		} else {
			rt.testHooks.storageFault = injected
		}
		rt.UpdateConfig(cloneRuntimeConfig(t, rt.Config()), pathB)
		rt.testHooks.diskWriteFault, rt.testHooks.storageFault = nil, nil

		assert.Equal(t, pathA, rt.ConfigPath(), "fault=%s", fault)
		rt.mu.RLock()
		assert.Equal(t, pathA, rt.cfgPath, "fault=%s", fault)
		rt.mu.RUnlock()
		after, err := os.ReadFile(pathB)
		require.NoError(t, err)
		assert.Equal(t, string(before), string(after), "fault=%s: the destination is untouched or restored", fault)
	}
}

// Round 4, finding 5: a whole-config ReplaceConfig never expresses a
// same-name replacement. An equal same-name entry is no change, a changed one
// is Updated with the old incarnation, and a rename a->b is Removed(a) plus
// Created(b). Only an explicit RemoveServer(n)+CreateServer(n) is Replaced
// (T1.17).
func TestCommit_R4_ReplaceConfigSameNameIsUpdateRenameIsRemoveCreate(t *testing.T) {
	rt, _ := newSpec116Runtime(t, nil, disabledStdio("a"), disabledStdio("keep"))
	oldA, ok := rt.ConfigSnapshot().ServerGeneration("a")
	require.True(t, ok)
	oldKeep, ok := rt.ConfigSnapshot().ServerGeneration("keep")
	require.True(t, ok)

	// Equal same-name entries: no change at all.
	res, err := rt.Commit("apply_equal", func(tx *CommitTx) error {
		tx.ReplaceConfig(cloneRuntimeConfig(t, rt.Config()))
		return nil
	})
	require.NoError(t, err)
	assert.Empty(t, res.Changes, "an equal whole-config apply changes nothing")

	// Changed same-name entry: Updated, incarnation kept.
	next := cloneRuntimeConfig(t, rt.Config())
	for _, sc := range next.Servers {
		if sc.Name == "keep" {
			sc.Args = []string{"changed"}
		}
	}
	res, err = rt.Commit("apply_changed", func(tx *CommitTx) error {
		tx.ReplaceConfig(next)
		return nil
	})
	require.NoError(t, err)
	ch := res.Changes["keep"]
	assert.Equal(t, configsvc.ChangeUpdated, ch.Kind)
	assert.Equal(t, oldKeep.Incarnation, ch.New.Incarnation, "a same-name update keeps the incarnation")

	// Rename a -> b: Removed(a) + Created(b), never Replaced.
	renamed := cloneRuntimeConfig(t, rt.Config())
	for _, sc := range renamed.Servers {
		if sc.Name == "a" {
			sc.Name = "b"
		}
	}
	res, err = rt.Commit("apply_rename", func(tx *CommitTx) error {
		tx.ReplaceConfig(renamed)
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, configsvc.ChangeRemoved, res.Changes["a"].Kind)
	assert.Equal(t, oldA, res.Changes["a"].Old)
	assert.Equal(t, configsvc.ChangeCreated, res.Changes["b"].Kind)
	for name, c := range res.Changes {
		assert.NotEqual(t, configsvc.ChangeReplaced, c.Kind, "%s: a whole-config apply is never a replacement", name)
	}
}
