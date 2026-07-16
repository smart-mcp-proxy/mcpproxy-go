package runtime

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/configsvc"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newWatcherTestRuntime builds a Runtime backed by a real config file in a
// temp dir and starts the config file watcher on it. Same construction
// pattern as restart_disk_reload_test.go.
func newWatcherTestRuntime(t *testing.T) (*Runtime, *config.Config, string) {
	t.Helper()

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "mcp_config.json")

	initialCfg := config.DefaultConfig()
	initialCfg.Listen = "127.0.0.1:0"
	initialCfg.DataDir = tmpDir
	require.NoError(t, config.SaveConfig(initialCfg, cfgPath))

	rt, err := New(initialCfg, cfgPath, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })

	require.NoError(t, rt.startConfigFileWatcher(rt.AppContext(), cfgPath))

	return rt, initialCfg, cfgPath
}

// editedConfig returns a copy-ish config with a distinct hot-reloadable value
// so tests can assert the reload landed.
func editedConfig(base *config.Config, limit int) *config.Config {
	edited := config.DefaultConfig()
	edited.Listen = base.Listen
	edited.DataDir = base.DataDir
	edited.ToolResponseLimit = limit
	return edited
}

// TestConfigWatcher_RenameWriteTriggersReload covers the atomic-write style
// (`jq ... > tmp && mv tmp mcp_config.json`) that editors and mcpproxy's own
// SaveConfig use: write to a temp file, then rename over the config path.
func TestConfigWatcher_RenameWriteTriggersReload(t *testing.T) {
	rt, initialCfg, cfgPath := newWatcherTestRuntime(t)

	// config.SaveConfig itself is an atomic tmp-file + rename write.
	require.NoError(t, config.SaveConfig(editedConfig(initialCfg, 12345), cfgPath))

	require.Eventually(t, func() bool {
		return rt.ConfigSnapshot().Config.ToolResponseLimit == 12345
	}, 5*time.Second, 25*time.Millisecond,
		"external rename-style config edit must hot-reload into the running core")
}

// TestConfigWatcher_TruncateWriteTriggersReload covers the in-place
// truncate-write style (`echo ... > mcp_config.json`).
func TestConfigWatcher_TruncateWriteTriggersReload(t *testing.T) {
	rt, initialCfg, cfgPath := newWatcherTestRuntime(t)

	data, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	require.NotEmpty(t, data)

	edited := editedConfig(initialCfg, 23456)
	require.NoError(t, config.SaveConfig(edited, cfgPath+".staging"))
	newBytes, err := os.ReadFile(cfgPath + ".staging")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(cfgPath, newBytes, 0o600))

	require.Eventually(t, func() bool {
		return rt.ConfigSnapshot().Config.ToolResponseLimit == 23456
	}, 5*time.Second, 25*time.Millisecond,
		"external in-place config edit must hot-reload into the running core")
}

// TestConfigWatcher_InvalidJSONKeepsOldConfigThenRecovers: a broken write must
// not clobber the running config, and the watcher must survive it and pick up
// the next valid write.
func TestConfigWatcher_InvalidJSONKeepsOldConfigThenRecovers(t *testing.T) {
	rt, initialCfg, cfgPath := newWatcherTestRuntime(t)

	versionBefore := rt.ConfigSnapshot().Version
	limitBefore := rt.ConfigSnapshot().Config.ToolResponseLimit

	require.NoError(t, os.WriteFile(cfgPath, []byte("{not valid json"), 0o600))

	// Give the debounce + reload attempt time to run, then confirm nothing changed.
	time.Sleep(1500 * time.Millisecond)
	assert.Equal(t, limitBefore, rt.ConfigSnapshot().Config.ToolResponseLimit,
		"invalid JSON must keep the previous configuration")
	assert.Equal(t, versionBefore, rt.ConfigSnapshot().Version,
		"invalid JSON must not bump the config version")

	// Watcher must still be alive: a valid write now reloads.
	require.NoError(t, config.SaveConfig(editedConfig(initialCfg, 34567), cfgPath))
	require.Eventually(t, func() bool {
		return rt.ConfigSnapshot().Config.ToolResponseLimit == 34567
	}, 5*time.Second, 25*time.Millisecond,
		"watcher must recover after a failed reload")
}

// TestConfigWatcher_SelfWriteSuppressed: mcpproxy's own ApplyConfig saves the
// file to disk; the watcher must not echo that save back into a second
// disk-reload (ConnectAll + reindex churn).
func TestConfigWatcher_SelfWriteSuppressed(t *testing.T) {
	rt, initialCfg, cfgPath := newWatcherTestRuntime(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	updates := rt.ConfigService().Subscribe(ctx)
	defer rt.ConfigService().Unsubscribe(updates)

	result, err := rt.ApplyConfig(editedConfig(initialCfg, 45678), cfgPath)
	require.NoError(t, err)
	require.True(t, result.Success)

	// Drain the expected in-memory apply update (plus the initial snapshot).
	deadline := time.After(5 * time.Second)
	sawApply := false
	for !sawApply {
		select {
		case u := <-updates:
			if u.Source == "api_apply_config" {
				sawApply = true
			}
		case <-deadline:
			t.Fatal("never saw the api_apply_config update")
		}
	}

	// Now assert the watcher does NOT follow up with a file_reload echo.
	timeout := time.After(1500 * time.Millisecond)
	for {
		select {
		case u := <-updates:
			assert.NotEqual(t, configsvc.UpdateTypeReload, u.Type,
				"watcher must not echo mcpproxy's own config save back as a disk reload (source=%s)", u.Source)
		case <-timeout:
			return
		}
	}
}

// TestConfigWatcher_RestartRequiredApplyNotEchoed: a restart-required
// ApplyConfig (e.g. `listen` change) saves the new config to disk but
// intentionally does NOT apply it in-memory (`requires_restart=true`,
// `applied_immediately=false`). The watcher must not treat that self-write as
// an external edit — otherwise it would hot-apply the change ~500ms after the
// API just promised it was deferred until restart.
func TestConfigWatcher_RestartRequiredApplyNotEchoed(t *testing.T) {
	rt, initialCfg, cfgPath := newWatcherTestRuntime(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	updates := rt.ConfigService().Subscribe(ctx)
	defer rt.ConfigService().Unsubscribe(updates)

	limitBefore := rt.ConfigSnapshot().Config.ToolResponseLimit

	edited := editedConfig(initialCfg, 56789)
	edited.Listen = "127.0.0.1:1" // restart-required field
	result, err := rt.ApplyConfig(edited, cfgPath)
	require.NoError(t, err)
	require.True(t, result.RequiresRestart, "listen change must require restart")

	// Give the watcher debounce ample time to fire, then assert the deferred
	// config was NOT hot-applied behind the API's back.
	timeout := time.After(1500 * time.Millisecond)
	for {
		select {
		case u := <-updates:
			assert.NotEqual(t, configsvc.UpdateTypeReload, u.Type,
				"watcher must not echo a restart-required self-save back as a disk reload (source=%s)", u.Source)
		case <-timeout:
			assert.Equal(t, limitBefore, rt.ConfigSnapshot().Config.ToolResponseLimit,
				"restart-required apply must stay deferred; watcher must not hot-apply it")
			return
		}
	}
}

// TestConfigWatcher_ExternalRevertToLastSelfWriteReloads: the recorded
// self-write must not outlive the next genuine external reload. Sequence:
// ApplyConfig saves A (recorded), external edit B hot-reloads, then the user
// reverts the file to byte-identical A (editor undo, `git checkout`). That
// revert is a genuine external edit and must reload — a stale self-write
// record would suppress it and leave the running config on B until restart.
func TestConfigWatcher_ExternalRevertToLastSelfWriteReloads(t *testing.T) {
	rt, initialCfg, cfgPath := newWatcherTestRuntime(t)

	// Self-save A (records lastSelfWrite = A).
	cfgA := editedConfig(initialCfg, 45678)
	result, err := rt.ApplyConfig(cfgA, cfgPath)
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Equal(t, 45678, rt.ConfigSnapshot().Config.ToolResponseLimit)

	// External edit B hot-reloads.
	require.NoError(t, config.SaveConfig(editedConfig(initialCfg, 11111), cfgPath))
	require.Eventually(t, func() bool {
		return rt.ConfigSnapshot().Config.ToolResponseLimit == 11111
	}, 5*time.Second, 25*time.Millisecond, "external edit B must hot-reload")

	// External revert back to byte-identical A must reload too.
	require.NoError(t, config.SaveConfig(cfgA, cfgPath))
	require.Eventually(t, func() bool {
		return rt.ConfigSnapshot().Config.ToolResponseLimit == 45678
	}, 5*time.Second, 25*time.Millisecond,
		"external revert to the last self-written bytes must still hot-reload")
}

// TestConfigWatcher_RevertThenRewriteOfSelfWrittenBytesReloads: the recorded
// self-write must also be invalidated when the file moves past it via the
// snapshot-match branch. Sequence: restart-required ApplyConfig saves A
// (marker=A, memory stays O), user externally reverts the file to O
// (suppressed as disk==memory), then externally writes A again. That last
// write is a genuine external edit whose hot-reloadable parts must apply —
// a stale marker would suppress it until restart.
func TestConfigWatcher_RevertThenRewriteOfSelfWrittenBytesReloads(t *testing.T) {
	rt, initialCfg, cfgPath := newWatcherTestRuntime(t)

	limitBefore := rt.ConfigSnapshot().Config.ToolResponseLimit

	// Restart-required apply: disk=A, memory=O, marker=A.
	cfgA := editedConfig(initialCfg, 88888)
	cfgA.Listen = "127.0.0.1:1"
	result, err := rt.ApplyConfig(cfgA, cfgPath)
	require.NoError(t, err)
	require.True(t, result.RequiresRestart)

	// Let the self-write event be (correctly) suppressed.
	time.Sleep(1200 * time.Millisecond)
	require.Equal(t, limitBefore, rt.ConfigSnapshot().Config.ToolResponseLimit)

	// External revert to O: disk==memory, suppressed — but the file has now
	// moved past our last save, so the marker must be dropped here.
	require.NoError(t, config.SaveConfig(initialCfg, cfgPath))
	time.Sleep(1200 * time.Millisecond)

	// External re-write of A: genuine edit, must hot-reload its hot parts.
	require.NoError(t, config.SaveConfig(cfgA, cfgPath))
	require.Eventually(t, func() bool {
		return rt.ConfigSnapshot().Config.ToolResponseLimit == 88888
	}, 5*time.Second, 25*time.Millisecond,
		"external re-write of previously self-saved bytes must hot-reload")
}

// TestConfigWatcher_ReloadSyncsLegacyGetConfig: a watcher reload must land in
// BOTH config surfaces — the configsvc snapshot AND the legacy r.cfg read by
// Runtime.GetConfig(), which still backs GET/PATCH /api/v1/config and other
// httpapi handlers. If only the snapshot updates, the API keeps serving the
// stale config and a subsequent PATCH would merge onto the stale base and
// save it, silently reverting the external edit on disk.
func TestConfigWatcher_ReloadSyncsLegacyGetConfig(t *testing.T) {
	rt, initialCfg, cfgPath := newWatcherTestRuntime(t)

	require.NoError(t, config.SaveConfig(editedConfig(initialCfg, 77777), cfgPath))

	require.Eventually(t, func() bool {
		return rt.ConfigSnapshot().Config.ToolResponseLimit == 77777
	}, 5*time.Second, 25*time.Millisecond, "snapshot must pick up the external edit")

	legacyCfg, err := rt.GetConfig()
	require.NoError(t, err)
	assert.Equal(t, 77777, legacyCfg.ToolResponseLimit,
		"legacy GetConfig (backing GET/PATCH /api/v1/config) must see the reloaded config")
}

// TestConfigWatcher_ReloadPropagatesGlobalConfigToUpstream: a watcher reload
// must reach the upstream manager (and through it every running managed
// client), matching what ApplyConfig does via SetGlobalConfig — otherwise
// external edits to global fields like health_check_interval update the
// snapshot/API but running clients keep resolving the old values.
func TestConfigWatcher_ReloadPropagatesGlobalConfigToUpstream(t *testing.T) {
	rt, initialCfg, cfgPath := newWatcherTestRuntime(t)

	require.NoError(t, config.SaveConfig(editedConfig(initialCfg, 99999), cfgPath))

	require.Eventually(t, func() bool {
		return rt.ConfigSnapshot().Config.ToolResponseLimit == 99999
	}, 5*time.Second, 25*time.Millisecond, "snapshot must pick up the external edit")

	gc := rt.upstreamManager.GlobalConfig()
	require.NotNil(t, gc)
	assert.Equal(t, 99999, gc.ToolResponseLimit,
		"watcher reload must propagate the new global config to the upstream manager")
}

// TestConfigWatcher_DebounceCoalesces: a burst of rapid writes must collapse
// into one (or at most a couple of) reloads, not one per write.
func TestConfigWatcher_DebounceCoalesces(t *testing.T) {
	rt, initialCfg, cfgPath := newWatcherTestRuntime(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	updates := rt.ConfigService().Subscribe(ctx)
	defer rt.ConfigService().Unsubscribe(updates)

	for i := 1; i <= 10; i++ {
		require.NoError(t, config.SaveConfig(editedConfig(initialCfg, 50000+i), cfgPath))
		time.Sleep(20 * time.Millisecond)
	}

	require.Eventually(t, func() bool {
		return rt.ConfigSnapshot().Config.ToolResponseLimit == 50010
	}, 5*time.Second, 25*time.Millisecond, "final write must land")

	// Allow any straggler debounce window to fire, then count reloads.
	time.Sleep(1 * time.Second)
	reloads := 0
	for {
		select {
		case u := <-updates:
			if u.Type == configsvc.UpdateTypeReload {
				reloads++
			}
			continue
		default:
		}
		break
	}
	assert.LessOrEqual(t, reloads, 3,
		"10 rapid writes must be debounced into a few reloads, got %d", reloads)
	assert.GreaterOrEqual(t, reloads, 1, "at least one reload must have happened")
}

// TestConfigWatcher_MissingDirGracefulDegradation: a watch path whose parent
// directory doesn't exist must fail gracefully (error, no panic) and leave the
// runtime usable.
func TestConfigWatcher_MissingDirGracefulDegradation(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "mcp_config.json")

	initialCfg := config.DefaultConfig()
	initialCfg.Listen = "127.0.0.1:0"
	initialCfg.DataDir = tmpDir
	require.NoError(t, config.SaveConfig(initialCfg, cfgPath))

	rt, err := New(initialCfg, cfgPath, zap.NewNop())
	require.NoError(t, err)
	defer func() { _ = rt.Close() }()

	bogus := filepath.Join(tmpDir, "does", "not", "exist", "mcp_config.json")
	err = rt.startConfigFileWatcher(rt.AppContext(), bogus)
	assert.Error(t, err, "watching a nonexistent directory must return an error")

	// Runtime still works.
	assert.NotNil(t, rt.ConfigSnapshot())
}

// TestConfigWatcher_FailedSelfSaveDoesNotSuppressExternalWrite: the self-write
// marker is armed BEFORE config.SaveConfig runs (pre-arming closes the
// event-outruns-marker race), so a FAILED save (permissions, disk) must clear
// it again. Otherwise a stale marker survives with bytes that never reached
// disk, and a later genuine external write of byte-identical JSON to the
// watched file would be misread as our own echo and silently suppressed.
func TestConfigWatcher_FailedSelfSaveDoesNotSuppressExternalWrite(t *testing.T) {
	rt, initialCfg, cfgPath := newWatcherTestRuntime(t)

	// A save path whose parent is a regular file makes config.SaveConfig fail
	// deterministically (MkdirAll -> ENOTDIR), independent of uid/umask.
	blocker := filepath.Join(filepath.Dir(cfgPath), "blocker")
	require.NoError(t, os.WriteFile(blocker, []byte("not a dir"), 0o600))
	failPath := filepath.Join(blocker, "mcp_config.json")

	cfgA := editedConfig(initialCfg, 66666)
	_, err := rt.ApplyConfig(cfgA, failPath)
	require.Error(t, err, "ApplyConfig must fail when the config cannot be saved")

	// Genuine external write of the SAME bytes the failed apply tried to save
	// (ApplyConfig marshals the config it was handed; SaveConfig serializes it
	// identically). This is a real edit — nothing ever reached disk — so it
	// must hot-reload, not be suppressed by the stale pre-armed marker.
	require.NoError(t, config.SaveConfig(cfgA, cfgPath))
	require.Eventually(t, func() bool {
		return rt.ConfigSnapshot().Config.ToolResponseLimit == 66666
	}, 5*time.Second, 25*time.Millisecond,
		"external write byte-identical to a FAILED self-save must still hot-reload")
}
