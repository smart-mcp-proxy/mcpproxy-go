package runtime

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage/purgetest"
)

// Spec 116 PR2 runtime tests (quickstart.md §T2): removal purges the whole
// purge set in the commit on every path, and approval writers bound to an
// incarnation write nothing once the server was removed or replaced.

func manualStdio(name string, quarantined bool) *config.ServerConfig {
	sc := disabledStdio(name)
	sc.TrustMode = string(config.TrustModeManual)
	sc.Quarantined = quarantined
	return sc
}

func seedPendingTool(t *testing.T, rt *Runtime, server, tool, hash string) {
	t.Helper()
	require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: server, ToolName: tool, Status: storage.ToolApprovalStatusPending,
		CurrentHash: hash, CurrentDescription: "desc " + hash,
	}))
}

func toolStatus(t *testing.T, rt *Runtime, server, tool string) string {
	t.Helper()
	rec, err := rt.storageManager.GetToolApproval(server, tool)
	require.NoError(t, err)
	return rec.Status
}

func removeAndReAdd(t *testing.T, rt *Runtime, replacement *config.ServerConfig) {
	t.Helper()
	_, err := rt.Commit("remove_server", func(tx *CommitTx) error { return tx.RemoveServer(replacement.Name) })
	require.NoError(t, err)
	_, err = rt.Commit("create_server", func(tx *CommitTx) error { return tx.CreateServer(replacement) })
	require.NoError(t, err)
}

// parkOnce returns a hook that parks the first call for name until release
// is closed, and signals entered when it does.
func parkOnce(name string) (hook func(string, int64), entered, release chan struct{}) {
	entered, release = make(chan struct{}), make(chan struct{})
	var once sync.Once
	hook = func(server string, _ int64) {
		if server != name {
			return
		}
		park := false
		once.Do(func() { park = true })
		if park {
			close(entered)
			<-release
		}
	}
	return hook, entered, release
}

// T2.1 + T2.10: every removal path deletes the whole purge set of the server
// before it returns — the explicit removal, an ApplyConfig that drops it, a
// file reload that drops it, and an ApplyConfig that leaves the server set
// empty — and drops its last-good snapshot. The neighbour acme_x keeps
// everything.
func TestRemoval_T2_1_EveryPathPurges(t *testing.T) {
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
		"file_reload": func(t *testing.T, rt *Runtime, cfgPath string) {
			next := cloneRuntimeConfig(t, rt.Config())
			next.Servers = []*config.ServerConfig{spec116Live(rt, "acme_x")}
			require.NoError(t, config.SaveConfig(next, cfgPath))
			require.NoError(t, rt.ReloadConfiguration())
		},
		"apply_empty_set": func(t *testing.T, rt *Runtime, cfgPath string) {
			next := cloneRuntimeConfig(t, rt.Config())
			next.Servers = nil
			_, err := rt.ApplyConfig(next, cfgPath)
			require.NoError(t, err)
		},
	}
	for name, remove := range paths {
		t.Run(name, func(t *testing.T) {
			rt, cfgPath := newSpec116Runtime(t, nil, disabledStdio("acme"), disabledStdio("acme_x"))
			purgetest.Seed(t, rt.storageManager, "acme")
			purgetest.Seed(t, rt.storageManager, "acme_x")
			purgetest.AssertSeeded(t, rt.storageManager, "acme")
			neighbour := purgetest.Counts(t, rt.storageManager, "acme_x")
			rt.lastGoodToolsMu.Lock()
			rt.lastGoodTools["acme"] = []*config.ToolMetadata{{Name: "echo", ServerName: "acme"}}
			rt.noteCaptureLocked("acme")
			rt.lastGoodToolsMu.Unlock()

			remove(t, rt, cfgPath)

			require.Nil(t, spec116Live(rt, "acme"))
			purgetest.AssertPurged(t, rt.storageManager, "acme", name)
			if name != "apply_empty_set" {
				assert.Equal(t, neighbour, purgetest.Counts(t, rt.storageManager, "acme_x"), "acme_x keeps every record")
			}
			rt.lastGoodToolsMu.Lock()
			_, snap := rt.lastGoodTools["acme"]
			_, stamp := rt.lastCaptureAt["acme"]
			rt.lastGoodToolsMu.Unlock()
			assert.False(t, snap, "the removal drops the last-good snapshot (T2.10)")
			assert.False(t, stamp, "the removal drops the capture stamp (T2.10)")
		})
	}
}

// T2.4: an approval parked after it captured the incarnation, released after
// the server was removed and re-added, writes nothing and fails with
// server_replaced. An unrelated edit (disabled_tools) in the same window keeps
// the incarnation, so that approval succeeds.
func TestApproval_T2_4_ReAddRejectsParkedApproval(t *testing.T) {
	for _, reAdd := range []bool{true, false} {
		name := map[bool]string{true: "re-add", false: "unrelated_edit"}[reAdd]
		t.Run(name, func(t *testing.T) {
			rt, _ := newSpec116Runtime(t, nil, manualStdio("acme", false))
			seedPendingTool(t, rt, "acme", "echo", "old")
			hook, entered, release := parkOnce("acme")
			rt.testHooks.approvalAfterCapture = hook
			done := make(chan error, 1)
			go func() { done <- rt.ApproveTools("acme", []string{"echo"}, "api") }()
			spec116Await(t, entered, "the approval to park after its capture")

			if reAdd {
				removeAndReAdd(t, rt, manualStdio("acme", false))
				// The replacement's own discovery records its (identical) tool.
				seedPendingTool(t, rt, "acme", "echo", "old")
			} else {
				_, err := rt.Commit("update_server", func(tx *CommitTx) error {
					return tx.UpdateServer("acme", func(cur *config.ServerConfig) error {
						cur.DisabledTools = []string{"other"}
						return nil
					})
				})
				require.NoError(t, err)
			}
			close(release)
			err := spec116AwaitErr(t, done, "ApproveTools")
			if reAdd {
				require.ErrorIs(t, err, ErrServerReplaced)
				assert.Contains(t, err.Error(), "server_replaced")
				assert.Equal(t, storage.ToolApprovalStatusPending, toolStatus(t, rt, "acme", "echo"),
					"the replacement's record must stay pending")
			} else {
				require.NoError(t, err)
				assert.Equal(t, storage.ToolApprovalStatusApproved, toolStatus(t, rt, "acme", "echo"))
			}
		})
	}
}

// T2.19 (review R9.5): the baseline promotion after an unquarantine is bound
// to the incarnation of the commit that unquarantined. Parked after that
// commit, released after a remove and a manual-trust re-add, it promotes none
// of the replacement's pending records.
func TestApproval_T2_19_UnquarantineBaselineBoundToIncarnation(t *testing.T) {
	rt, _ := newSpec116Runtime(t, nil, manualStdio("acme", true))
	hook, entered, release := parkOnce("acme")
	rt.testHooks.approvalAfterUnquarantineCommit = hook
	done := make(chan error, 1)
	go func() { done <- rt.QuarantineServer("acme", false) }()
	spec116Await(t, entered, "the unquarantine to park before its baseline promotion")

	removeAndReAdd(t, rt, manualStdio("acme", true))
	seedPendingTool(t, rt, "acme", "echo", "replacement")
	close(release)
	require.NoError(t, spec116AwaitErr(t, done, "QuarantineServer"))
	assert.Equal(t, storage.ToolApprovalStatusPending, toolStatus(t, rt, "acme", "echo"),
		"the replacement's pending record must not be baseline-promoted")
	assert.True(t, spec116Live(rt, "acme").Quarantined, "the replacement stays quarantined")
}

// T2.17 (review R8.2): a one-commit remove and re-create runs the full purge
// and rejects an approval parked on the old incarnation.
func TestApproval_T2_17_SameCommitReplacementPurgesAndRejects(t *testing.T) {
	for _, changed := range []bool{false, true} {
		rt, _ := newSpec116Runtime(t, nil, manualStdio("acme", false))
		purgetest.Seed(t, rt.storageManager, "acme")
		seedPendingTool(t, rt, "acme", "fresh", "x")
		hook, entered, release := parkOnce("acme")
		rt.testHooks.approvalAfterCapture = hook
		done := make(chan error, 1)
		go func() { done <- rt.ApproveTools("acme", []string{"fresh"}, "api") }()
		spec116Await(t, entered, "the approval to park")

		res, err := rt.Commit("replace", func(tx *CommitTx) error {
			cur, _ := tx.Server("acme")
			if err := tx.RemoveServer("acme"); err != nil {
				return err
			}
			if changed {
				cur.Args = []string{"v2"}
			}
			return tx.CreateServer(cur)
		})
		require.NoError(t, err)
		c := purgetest.Counts(t, rt.storageManager, "acme")
		assert.Equal(t, 1, c[purgetest.Upstream])
		delete(c, purgetest.Upstream)
		for bucket, n := range c {
			assert.Zero(t, n, "changed=%v: %s holds %d key(s) of the replaced server", changed, bucket, n)
		}
		_ = res
		seedPendingTool(t, rt, "acme", "fresh", "x")
		close(release)
		require.ErrorIs(t, spec116AwaitErr(t, done, "ApproveTools"), ErrServerReplaced)
		assert.Equal(t, storage.ToolApprovalStatusPending, toolStatus(t, rt, "acme", "fresh"))
	}
}

// T2.12: a connect worker parked before it registers the server identity,
// released after the server was removed, registers nothing.
func TestRemoval_T2_12_LateIdentityRegistrationDropped(t *testing.T) {
	rt, _ := newSpec116Runtime(t, nil)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	rt.testHooks.beforeRegisterIdentity = func(name string) {
		if name == "acme" {
			once.Do(func() { close(entered); <-release })
		}
	}
	echo := newHeaderEchoUpstream(t)
	sc := explicitServer(&config.ServerConfig{Name: "acme", URL: echo.srv.URL, Protocol: "streamable-http", Enabled: true})
	_, err := rt.Commit("create_server", func(tx *CommitTx) error { return tx.CreateServer(sc) })
	require.NoError(t, err)
	spec116Await(t, entered, "the connect worker to park before the identity registration")
	_, err = rt.Commit("remove_server", func(tx *CommitTx) error { return tx.RemoveServer("acme") })
	require.NoError(t, err)
	close(release)
	assert.Never(t, func() bool {
		return purgetest.Counts(t, rt.storageManager, "acme")[purgetest.Identities] > 0
	}, time.Second, 10*time.Millisecond, "the late worker must not register an identity for the removed server")
}

// T2.7 (regression coverage of PR1's D15 check): a review capture taken from
// the old instance and released after a remove and re-add writes no record.
func TestRemoval_T2_7_StaleCaptureAfterReAddWritesNothing(t *testing.T) {
	rt, _ := newSpec116Runtime(t, nil, manualStdio("acme", true))
	old, ok := rt.ConfigSnapshot().ServerGeneration("acme")
	require.True(t, ok)
	removeAndReAdd(t, rt, manualStdio("acme", true))
	persisted, err := rt.persistQuarantinedToolDefinitionsAt("acme",
		[]*config.ToolMetadata{{Name: "echo", ServerName: "acme", Description: "old"}}, old.Gen)
	require.NoError(t, err)
	assert.False(t, persisted)
	_, err = rt.storageManager.GetToolApproval("acme", "echo")
	assert.ErrorIs(t, err, storage.ErrToolApprovalNotFound)
}
