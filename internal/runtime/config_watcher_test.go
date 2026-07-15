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
