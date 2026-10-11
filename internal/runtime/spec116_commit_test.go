package runtime

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/configsvc"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

func unreachableHTTP(name string) *config.ServerConfig {
	sc := &config.ServerConfig{Name: name, URL: "http://127.0.0.1:9/mcp", Protocol: "http", Enabled: true}
	sc.MarkQuarantineExplicitlySet(true)
	return sc
}

func disabledStdio(name string) *config.ServerConfig {
	sc := &config.ServerConfig{Name: name, Command: "true", Protocol: "stdio", Enabled: false}
	sc.MarkQuarantineExplicitlySet(true)
	return sc
}

func storedStamp(t *testing.T, rt *Runtime, name string) *storage.StoredServer {
	t.Helper()
	row, err := rt.storageManager.GetStoredServer(name)
	require.NoError(t, err, "row for %s", name)
	return row
}

// T1.17 (review R8.2): a one-commit remove and re-create is one Replaced
// change with a new incarnation, for an equal and for a changed entry; the
// row and the snapshot carry the same new ServerGen, and a connect worker is
// scheduled for the new generation.
func TestCommit_T1_17_SameNameReplacement(t *testing.T) {
	for _, changed := range []bool{false, true} {
		rt, _ := newSpec116Runtime(t, nil, unreachableHTTP("acme"))
		old, ok := rt.ConfigSnapshot().ServerGeneration("acme")
		require.True(t, ok)

		res, err := rt.Commit("replace", func(tx *CommitTx) error {
			cur, _ := tx.Server("acme")
			if err := tx.RemoveServer("acme"); err != nil {
				return err
			}
			if changed {
				cur.Args = []string{"new"}
			}
			return tx.CreateServer(cur)
		})
		require.NoError(t, err)
		ch := res.Changes["acme"]
		assert.Equal(t, configsvc.ChangeReplaced, ch.Kind, "changed=%v", changed)
		assert.Equal(t, old, ch.Old)
		assert.Equal(t, configsvc.ServerGen{Gen: res.Generation, Incarnation: res.Generation}, ch.New)
		assert.NotEqual(t, old.Incarnation, ch.New.Incarnation, "a replacement always gets a new incarnation")
		snap, _ := res.Snapshot.ServerGeneration("acme")
		assert.Equal(t, ch.New, snap)
		assert.Equal(t, ch.New, storedStamp(t, rt, "acme").Stamp, "row and snapshot agree")
		require.Eventually(t, func() bool {
			c, ok := rt.upstreamManager.GetClient("acme")
			return ok && c.Generation() == res.Generation
		}, 10*time.Second, 5*time.Millisecond, "a connect worker must be scheduled for (acme, G)")
	}
}

// T1.20 (review R9.3): projection repair writes the snapshot's stamp, not G.
func TestCommit_T1_20_ProjectionRepairKeepsStamps(t *testing.T) {
	rt, _ := newSpec116Runtime(t, nil, disabledStdio("u"), disabledStdio("v"))
	want, ok := rt.ConfigSnapshot().ServerGeneration("u")
	require.True(t, ok)

	// Move G forward with an unrelated change, then lose u's row.
	_, err := rt.Commit("bump", func(tx *CommitTx) error {
		return tx.UpdateServer("v", func(cur *config.ServerConfig) error {
			cur.Args = []string{"x"}
			return nil
		})
	})
	require.NoError(t, err)
	require.NoError(t, rt.storageManager.ApplyServerCommit(&storage.ServerCommitBatch{
		Generation: rt.ConfigSnapshot().Version, Removes: []string{"u"},
	}))

	res, err := rt.Commit("reconcile", func(tx *CommitTx) error {
		tx.ReconcileLive()
		return nil
	})
	require.NoError(t, err)
	row := storedStamp(t, rt, "u")
	assert.Equal(t, want, row.Stamp, "the repaired row carries the snapshot's ServerGen, not the commit's G")
	assert.NotEqual(t, res.Generation, row.Stamp.Gen)
}

// T1.18 (review R9.2): a reload whose storage step fails restores the file
// byte for byte (the operator's edit stays on disk), and live config and
// storage keep only what was committed.
func TestCommit_T1_18_ReloadStorageFailureRestoresRawFile(t *testing.T) {
	rt, cfgPath := newSpec116Runtime(t, nil, disabledStdio("a"))

	edited := cloneRuntimeConfig(t, rt.Config())
	edited.Servers = append(edited.Servers, disabledStdio("b"))
	require.NoError(t, config.SaveConfig(edited, cfgPath))
	require.NoError(t, os.Chmod(cfgPath, 0o640))
	before, err := os.ReadFile(cfgPath)
	require.NoError(t, err)

	rt.testHooks.storageFault = func(reason string) error {
		if reason == commitReasonFileReload {
			return errors.New("injected storage failure")
		}
		return nil
	}
	require.Error(t, rt.ReloadConfiguration())
	rt.testHooks.storageFault = nil

	after, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.True(t, bytes.Equal(before, after), "the file must be byte-identical to the edited input")
	info, err := os.Stat(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o640), info.Mode().Perm())
	assert.Nil(t, spec116Live(rt, "b"), "live config must still hold only a")
	assert.Nil(t, spec116Stored(t, rt, "b"), "storage must still hold only a")
}

// T1.18 variant (b): an apply that would create a missing file, failing in
// storage, leaves no file behind.
func TestCommit_T1_18_AbsentFileStaysAbsent(t *testing.T) {
	rt, cfgPath := newSpec116Runtime(t, nil, disabledStdio("a"))
	require.NoError(t, os.Remove(cfgPath))

	next := cloneRuntimeConfig(t, rt.Config())
	next.Servers = append(next.Servers, disabledStdio("b"))
	rt.testHooks.storageFault = func(string) error { return errors.New("injected storage failure") }
	_, err := rt.ApplyConfig(next, cfgPath)
	rt.testHooks.storageFault = nil
	require.Error(t, err)
	_, statErr := os.Stat(cfgPath)
	assert.True(t, errors.Is(statErr, os.ErrNotExist), "no file must exist after the failed commit")
	assert.Nil(t, spec116Live(rt, "b"))
}

// T1.16 (review R5.5): a pending restart-required change survives a storage
// failure during an unrelated server edit: disk bytes and desiredCfg are
// unchanged.
func TestCommit_T1_16_PendingRestartSurvivesStorageFailure(t *testing.T) {
	rt, cfgPath := newSpec116Runtime(t, func(c *config.Config) {
		c.RoutingMode = config.RoutingModeRetrieveTools
	}, disabledStdio("a"))

	pending := cloneRuntimeConfig(t, rt.Config())
	pending.RoutingMode = config.RoutingModeDirect
	res, err := rt.ApplyConfig(pending, cfgPath)
	require.NoError(t, err)
	require.True(t, res.RequiresRestart)
	before, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	rt.mu.RLock()
	desiredBefore := rt.desiredCfg
	rt.mu.RUnlock()

	rt.testHooks.storageFault = func(string) error { return errors.New("injected storage failure") }
	err = rt.EnableServer("a", true)
	rt.testHooks.storageFault = nil
	require.Error(t, err)

	after, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.True(t, bytes.Equal(before, after), "disk must be byte-identical (the pending routing_mode is still there)")
	rt.mu.RLock()
	assert.Same(t, desiredBefore, rt.desiredCfg, "desiredCfg must be untouched")
	rt.mu.RUnlock()
	assert.False(t, spec116Live(rt, "a").Enabled)
	desired, derr := rt.GetDesiredConfig()
	require.NoError(t, derr)
	assert.Equal(t, config.RoutingModeDirect, desired.RoutingMode, "the pending change is still reported")
}

// T1.19 (review R9.1): a non-connection edit reaches the retained instance
// before the commit returns, with the row's generation and no reconnect.
// Regression coverage, also run with step 7 stubbed out to show it detects the
// loss.
func TestCommit_T1_19_RetainedInstanceGetsNewConfig(t *testing.T) {
	for _, stub := range []bool{false, true} {
		rt, _ := newSpec116Runtime(t, nil, func() *config.ServerConfig {
			sc := unreachableHTTP("fh")
			sc.ForwardHeaders = []string{"X-Tenant"}
			return sc
		}())
		require.NoError(t, rt.upstreamManager.AddServerConfigAt("fh", spec116Live(rt, "fh"), storedStamp(t, rt, "fh").Stamp.Gen))
		before, ok := rt.upstreamManager.GetClient("fh")
		require.True(t, ok)

		rt.testHooks.skipAdvance = stub
		res, err := rt.Commit("patch_forward_headers", func(tx *CommitTx) error {
			return tx.UpdateServer("fh", func(cur *config.ServerConfig) error {
				cur.ForwardHeaders = []string{}
				return nil
			})
		})
		rt.testHooks.skipAdvance = false
		require.NoError(t, err)
		require.False(t, res.Changes["fh"].Reconnect, "forward_headers is not a connection field")

		after, ok := rt.upstreamManager.GetClient("fh")
		require.True(t, ok)
		assert.Same(t, before, after, "no reconnect: the same instance is retained")
		row := storedStamp(t, rt, "fh")
		if stub {
			assert.NotEqual(t, row.Stamp.Gen, after.Generation(), "with step 7 stubbed out the instance keeps the old generation")
			assert.Equal(t, []string{"X-Tenant"}, after.GetConfig().ForwardHeaders, "and still forwards the revoked header")
			continue
		}
		assert.Empty(t, after.GetConfig().ForwardHeaders, "the revoked header is no longer forwarded")
		assert.Equal(t, row.Stamp.Gen, after.Generation(), "the instance carries the row's generation, so D15 accepts its discovery")
		assert.Contains(t, res.Restamped, "fh")
	}
}

// T1.21 (review R9.6) and T1.10 (R1.6): a boot on an empty storage repairs the
// projection and schedules every enabled server; a restart on matching rows
// still schedules every enabled server and keeps every generation. Bootstrap
// outside the boot commit is an invariant error.
func TestCommit_T1_21_BootSchedulesAndKeepsGenerations(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "mcp_config.json")
	fileCfg := config.DefaultConfig()
	fileCfg.Listen = "127.0.0.1:0"
	fileCfg.DataDir = dir
	fileCfg.Servers = []*config.ServerConfig{unreachableHTTP("up1"), unreachableHTTP("up2"), disabledStdio("off")}
	require.NoError(t, config.SaveConfig(fileCfg, cfgPath))

	boot := func() *Runtime {
		cfg, err := config.LoadFromFile(cfgPath)
		require.NoError(t, err)
		rt, err := New(cfg, cfgPath, zap.NewNop())
		require.NoError(t, err)
		rt.gateInitialConfig()
		require.NoError(t, rt.bootReconcile())
		return rt
	}
	scheduled := func(rt *Runtime) {
		t.Helper()
		for _, name := range []string{"up1", "up2"} {
			require.Eventually(t, func() bool {
				_, ok := rt.upstreamManager.GetClient(name)
				return ok
			}, 10*time.Second, 5*time.Millisecond, "%s must be scheduled", name)
		}
	}

	rt := boot()
	scheduled(rt)
	gens := map[string]configsvc.ServerGen{}
	for _, name := range []string{"up1", "up2", "off"} {
		g, ok := rt.ConfigSnapshot().ServerGeneration(name)
		require.True(t, ok)
		gens[name] = g
		assert.Equal(t, g, storedStamp(t, rt, name).Stamp)
	}
	_, err := rt.Commit("not_boot", func(tx *CommitTx) error {
		tx.ReconcileLive()
		tx.Bootstrap()
		return nil
	})
	require.ErrorIs(t, err, ErrCommitInvariant, "Bootstrap() outside the boot commit is an invariant error")
	diskBefore, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	require.NoError(t, rt.Close())
	diskAfterBoot, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, diskBefore, diskAfterBoot, "the boot never rewrites the operator's file")

	rt2 := boot()
	t.Cleanup(func() { _ = rt2.Close() })
	scheduled(rt2)
	for name, want := range gens {
		got, ok := rt2.ConfigSnapshot().ServerGeneration(name)
		require.True(t, ok)
		assert.Equal(t, want, got, "%s keeps its generation across the restart", name)
	}
	seed, _, err := rt2.storageManager.ConfigCommitState()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, rt2.ConfigSnapshot().Version, seed, "T1.9: the version is seeded from storage")

	// T1.10: an unchanged SaveConfiguration still writes the file.
	require.NoError(t, os.Remove(cfgPath))
	require.NoError(t, rt2.SaveConfiguration())
	_, err = os.Stat(cfgPath)
	assert.NoError(t, err, "SaveConfiguration must write the file even when nothing changed")
}

// T1.11: a removal parked inside its commit excludes a toggle and two updates
// of the removed server; after release the toggle lands and both updates see
// "not found".
func TestCommit_T1_11_RemovalExcludesCompetitors(t *testing.T) {
	rt, _ := newSpec116Runtime(t, nil, disabledStdio("gone"), disabledStdio("bravo"))
	entered := make(chan struct{})
	release := make(chan struct{})
	rt.testHooks.afterStorage = func(reason string) {
		if reason == "remove" {
			close(entered)
			<-release
		}
	}
	removeDone := make(chan error, 1)
	go func() {
		_, err := rt.Commit("remove", func(tx *CommitTx) error { return tx.RemoveServer("gone") })
		removeDone <- err
	}()
	spec116Await(t, entered, "the removal to park")

	toggle := make(chan error, 1)
	upd1 := make(chan error, 1)
	upd2 := make(chan error, 1)
	go func() { toggle <- rt.EnableServer("bravo", true) }()
	update := func(out chan<- error) {
		_, err := rt.Commit("update", func(tx *CommitTx) error {
			return tx.UpdateServer("gone", func(cur *config.ServerConfig) error {
				cur.Args = []string{"x"}
				return nil
			})
		})
		out <- err
	}
	go update(upd1)
	go update(upd2)
	require.Eventually(t, func() bool { return rt.commitWaiters() == 3 }, 10*time.Second, time.Millisecond,
		"all three competitors must be blocked on the commit lock")
	close(release)
	require.NoError(t, spec116AwaitErr(t, removeDone, "removal"))
	require.NoError(t, spec116AwaitErr(t, toggle, "toggle"))
	assert.ErrorIs(t, spec116AwaitErr(t, upd1, "update 1"), ErrServerNotFound)
	assert.ErrorIs(t, spec116AwaitErr(t, upd2, "update 2"), ErrServerNotFound)
	assert.Nil(t, spec116Live(rt, "gone"))
	assert.Nil(t, spec116Stored(t, rt, "gone"))
	assert.True(t, spec116Live(rt, "bravo").Enabled)
}

// T1.13 (review R3.1): a discovery pass that removes a genuinely absent tool
// completes without re-entering the approval lock it holds.
func TestCommit_T1_13_DiscoveryDoesNotReenterApprovalLock(t *testing.T) {
	rt, _ := newSpec116Runtime(t, nil, unreachableHTTP("disc"))
	tools := []*config.ToolMetadata{
		{ServerName: "disc", Name: "keep", Description: "keep", ParamsJSON: `{"type":"object"}`, Hash: "hk"},
		{ServerName: "disc", Name: "drop", Description: "drop", ParamsJSON: `{"type":"object"}`, Hash: "hd"},
	}
	require.NoError(t, rt.applyDifferentialToolUpdate(context.Background(), "disc", tools))
	done := make(chan error, 1)
	go func() { done <- rt.applyDifferentialToolUpdate(context.Background(), "disc", tools[:1]) }()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("discovery re-entered the approval lock it holds (deadlock)")
	}
	_, err := rt.storageManager.GetToolApproval("disc", "drop")
	assert.Error(t, err, "the absent tool's record is removed")
}

// T1.14 (D16, review R3.2): between a commit's storage step and its publish,
// an approval decision uses the trust mode from storage, not the
// still-published one.
func TestCommit_T1_14_ApprovalPolicyFromStorage(t *testing.T) {
	sc := unreachableHTTP("pol")
	sc.TrustMode = string(config.TrustModeAuto)
	rt, _ := newSpec116Runtime(t, nil, sc)
	require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "pol", ToolName: "base", Status: storage.ToolApprovalStatusApproved,
		CurrentHash: "h", ApprovedHash: "h", ApprovedBy: "test", ApprovedAt: time.Now(),
	}))

	entered := make(chan struct{})
	release := make(chan struct{})
	rt.testHooks.afterStorage = func(reason string) {
		if reason == "to_manual" {
			close(entered)
			<-release
		}
	}
	commitDone := make(chan error, 1)
	go func() {
		_, err := rt.Commit("to_manual", func(tx *CommitTx) error {
			return tx.UpdateServer("pol", func(cur *config.ServerConfig) error {
				cur.TrustMode = string(config.TrustModeManual)
				return nil
			})
		})
		commitDone <- err
	}()
	spec116Await(t, entered, "the commit to park after storage")
	require.Equal(t, string(config.TrustModeAuto), spec116Live(rt, "pol").TrustMode, "precondition: auto is still published")

	_, err := rt.checkToolApprovals("pol", []*config.ToolMetadata{
		{ServerName: "pol", Name: "fresh", Description: "new tool", ParamsJSON: `{"type":"object"}`},
	})
	require.NoError(t, err)
	close(release)
	require.NoError(t, spec116AwaitErr(t, commitDone, "commit"))

	rec, err := rt.storageManager.GetToolApproval("pol", "fresh")
	require.NoError(t, err)
	assert.Equal(t, storage.ToolApprovalStatusPending, rec.Status, "the decision must use manual (from storage)")
}

// T1.15 (FR-008c, review R4.2): a commit that changes the global
// quarantine_enabled waits for a discovery pass that holds a server's
// approval lock.
func TestCommit_T1_15_GlobalPolicyChangeTakesEveryApprovalLock(t *testing.T) {
	disabled := false
	rt, _ := newSpec116Runtime(t, func(c *config.Config) { c.QuarantineEnabled = &disabled }, unreachableHTTP("glob"))
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	rt.testHooks.discoveryBeforeIndex = func(name string) {
		first := false
		once.Do(func() { first = true })
		if first {
			close(entered)
			<-release
		}
	}
	passDone := make(chan error, 1)
	go func() {
		passDone <- rt.applyDifferentialToolUpdate(context.Background(), "glob", []*config.ToolMetadata{
			{ServerName: "glob", Name: "t1", Description: "d", ParamsJSON: `{"type":"object"}`, Hash: "h1"},
		})
	}()
	spec116Await(t, entered, "the pass to park holding the approval lock")

	commitDone := make(chan error, 1)
	go func() {
		_, err := rt.Commit("enable_quarantine", func(tx *CommitTx) error {
			return tx.MutateGlobal(func(cfg *config.Config) error {
				enabled := true
				cfg.QuarantineEnabled = &enabled
				return nil
			})
		})
		commitDone <- err
	}()
	returned, _ := spec116AwaitReturnOrBlocked(t, commitDone, func() bool { return rt.approvalLockWaiters("glob") > 0 })
	assert.False(t, returned, "the global policy commit must wait for the pass that holds the server's lock")
	close(release)
	require.NoError(t, spec116AwaitErr(t, passDone, "pass"))
	if !returned {
		require.NoError(t, spec116AwaitErr(t, commitDone, "commit"))
	}

	_, err := rt.checkToolApprovals("glob", []*config.ToolMetadata{
		{ServerName: "glob", Name: "t2", Description: "new", ParamsJSON: `{"type":"object"}`},
	})
	require.NoError(t, err)
	rec, err := rt.storageManager.GetToolApproval("glob", "t2")
	require.NoError(t, err)
	assert.Equal(t, storage.ToolApprovalStatusPending, rec.Status, "a new tool after the policy change is held")
}

// T019: lock-order watchdogs. A re-entrant Commit from inside fn is an error,
// not a deadlock; a commit and a discovery pass holding the approval lock, and
// a commit and a capture section holding captureMu (R), never deadlock.
func TestLockOrder_PR1(t *testing.T) {
	rt, _ := newSpec116Runtime(t, nil, unreachableHTTP("lo"))

	t.Run("reentrant_commit", func(t *testing.T) {
		done := make(chan error, 1)
		go func() {
			_, err := rt.Commit("outer", func(tx *CommitTx) error {
				_, inner := rt.Commit("inner", nil)
				return inner
			})
			done <- err
		}()
		select {
		case err := <-done:
			require.ErrorIs(t, err, ErrCommitInvariant)
		case <-time.After(5 * time.Second):
			t.Fatal("re-entrant Commit deadlocked")
		}
	})

	t.Run("commit_vs_discovery", func(t *testing.T) {
		var wg sync.WaitGroup
		for i := 0; i < 10; i++ {
			wg.Add(2)
			go func(i int) {
				defer wg.Done()
				_ = rt.applyDifferentialToolUpdate(context.Background(), "lo", []*config.ToolMetadata{
					{ServerName: "lo", Name: "t", Description: "d", ParamsJSON: `{"type":"object"}`, Hash: "h"},
				})
			}(i)
			go func(i int) {
				defer wg.Done()
				_, _ = rt.Commit("toggle", func(tx *CommitTx) error {
					return tx.UpdateServer("lo", func(cur *config.ServerConfig) error {
						cur.DisabledTools = []string{string(rune('a' + i))}
						return nil
					})
				})
			}(i)
		}
		waitOrDump(t, &wg, 5*time.Second)
	})

	t.Run("commit_vs_capture", func(t *testing.T) {
		client, ok := rt.upstreamManager.GetClient("lo")
		if !ok {
			require.NoError(t, rt.upstreamManager.AddServerConfigAt("lo", spec116Live(rt, "lo"), 1))
			client, _ = rt.upstreamManager.GetClient("lo")
		}
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = rt.upstreamManager.WithCurrentClientOnEpoch("lo", client, client.ConnectionEpoch(), func() error {
				time.Sleep(20 * time.Millisecond)
				return nil
			})
		}()
		go func() {
			defer wg.Done()
			_, _ = rt.Commit("restamp", func(tx *CommitTx) error {
				return tx.UpdateServer("lo", func(cur *config.ServerConfig) error {
					cur.DisabledTools = []string{"z"}
					return nil
				})
			})
		}()
		waitOrDump(t, &wg, 5*time.Second)
	})
}

func waitOrDump(t *testing.T, wg *sync.WaitGroup, d time.Duration) {
	t.Helper()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(d):
		buf := make([]byte, 1<<20)
		n := runtimeStack(buf)
		t.Fatalf("deadlock: goroutines did not finish within %v\n%s", d, buf[:n])
	}
}
