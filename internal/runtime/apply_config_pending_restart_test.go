package runtime

import (
	"path/filepath"
	"testing"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A restart-gated change (routing_mode) is persisted to disk while the running
// process keeps serving the old value. Everything downstream of that has to
// cope with memory and disk deliberately disagreeing:
//
//  1. the hot half of the SAME apply must still apply — otherwise turning on
//     deferred schemas in the same breath as switching to Direct silently does
//     nothing until a restart;
//  2. the next apply must not revert the pending value, which is what happens
//     when a caller merges its patch onto the LIVE config (the Web UI header
//     switcher, the Settings page and every REST client do exactly that).
//
// Both were broken: ApplyConfig returned early on the restart branch, and
// nothing exposed "what is on disk" for a caller to merge onto.
func newPendingRestartRuntime(t *testing.T) (*Runtime, string) {
	t.Helper()
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")

	initial := config.DefaultConfig()
	initial.Listen = "127.0.0.1:8080"
	initial.DataDir = tmpDir
	initial.RoutingMode = config.RoutingModeRetrieveTools
	require.NoError(t, config.SaveConfig(initial, cfgPath))

	rt, err := New(initial, cfgPath, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })
	return rt, cfgPath
}

func TestApplyConfig_HotFieldsApplyAlongsideARestartGatedOne(t *testing.T) {
	rt, cfgPath := newPendingRestartRuntime(t)

	next := config.DefaultConfig()
	next.Listen = "127.0.0.1:8080"
	next.DataDir = filepath.Dir(cfgPath)
	next.RoutingMode = config.RoutingModeDirect // restart-gated
	next.ToolsLimit = 42                        // hot

	result, err := rt.ApplyConfig(next, cfgPath)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.True(t, result.RequiresRestart, "routing_mode still needs a restart")
	assert.Contains(t, result.ChangedFields, "routing_mode")

	// The running config keeps the mode /mcp is bound to…
	live, err := rt.GetConfig()
	require.NoError(t, err)
	assert.Equal(t, config.RoutingModeRetrieveTools, live.RoutingMode,
		"a restart-gated field must never be adopted in memory")
	// …but the hot field is live now, not after a restart.
	assert.Equal(t, 42, live.ToolsLimit,
		"the hot half of a mixed apply must apply immediately")
}

func TestApplyConfig_DesiredConfigCarriesThePendingValue(t *testing.T) {
	rt, cfgPath := newPendingRestartRuntime(t)

	next := config.DefaultConfig()
	next.Listen = "127.0.0.1:8080"
	next.DataDir = filepath.Dir(cfgPath)
	next.RoutingMode = config.RoutingModeDirect

	_, err := rt.ApplyConfig(next, cfgPath)
	require.NoError(t, err)

	desired, err := rt.GetDesiredConfig()
	require.NoError(t, err)
	assert.Equal(t, config.RoutingModeDirect, desired.RoutingMode,
		"the desired config is what the next start will use — the pending value")

	live, err := rt.GetConfig()
	require.NoError(t, err)
	assert.Equal(t, config.RoutingModeRetrieveTools, live.RoutingMode)
}

// The clobber itself, end to end: a second apply built from the DESIRED config
// (what every read-modify-write caller must merge onto) changes only a hot
// field and must leave the pending routing mode on disk.
func TestApplyConfig_SecondApplyKeepsThePendingRoutingMode(t *testing.T) {
	rt, cfgPath := newPendingRestartRuntime(t)

	pending := config.DefaultConfig()
	pending.Listen = "127.0.0.1:8080"
	pending.DataDir = filepath.Dir(cfgPath)
	pending.RoutingMode = config.RoutingModeDirect
	_, err := rt.ApplyConfig(pending, cfgPath)
	require.NoError(t, err)

	desired, err := rt.GetDesiredConfig()
	require.NoError(t, err)
	desired.DirectToolResponseMode = config.DirectToolResponseModeDeferred

	result, err := rt.ApplyConfig(desired, cfgPath)
	require.NoError(t, err)
	assert.Contains(t, result.ChangedFields, "direct_tool_response_mode")
	assert.False(t, result.RequiresRestart,
		"a hot change must not inherit a restart prompt from an already-pending field")

	onDisk, err := config.LoadFromFile(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, config.RoutingModeDirect, onDisk.RoutingMode,
		"the pending routing mode must survive a later hot change")
	assert.Equal(t, config.DirectToolResponseModeDeferred, onDisk.DirectToolResponseMode)

	live, err := rt.GetConfig()
	require.NoError(t, err)
	assert.Equal(t, config.DirectToolResponseModeDeferred, live.DirectToolResponseMode,
		"and the hot change is in effect now")
	assert.Equal(t, config.RoutingModeRetrieveTools, live.RoutingMode)
}

// ServedRoutingMode is what /mcp actually bound at startup. Reading it from the
// live config is not equivalent: the file-watcher reload path adopts a
// hand-edited routing_mode into memory, after which every surface would report a
// mode /mcp is not serving.
func TestServedRoutingMode_IsRecordedNotDerived(t *testing.T) {
	rt, _ := newPendingRestartRuntime(t)

	assert.Equal(t, "", rt.ServedRoutingMode(), "unset until the server binds")

	rt.SetServedRoutingMode(config.RoutingModeRetrieveTools)
	assert.Equal(t, config.RoutingModeRetrieveTools, rt.ServedRoutingMode())
}

// Cancelling a pending switch — putting the field back to what the process is
// already running — changes the file but must not promise a restart. Without
// this the switcher's "Cancel — keep Retrieve" answered "Saved — restart
// required" for the one action that removes the need to restart.
func TestApplyConfig_RevertingToTheRunningValueNeedsNoRestart(t *testing.T) {
	rt, cfgPath := newPendingRestartRuntime(t)

	pending := config.DefaultConfig()
	pending.Listen = "127.0.0.1:8080"
	pending.DataDir = filepath.Dir(cfgPath)
	pending.RoutingMode = config.RoutingModeDirect
	res, err := rt.ApplyConfig(pending, cfgPath)
	require.NoError(t, err)
	require.True(t, res.RequiresRestart)

	back, err := rt.GetDesiredConfig()
	require.NoError(t, err)
	back.RoutingMode = config.RoutingModeRetrieveTools

	result, err := rt.ApplyConfig(back, cfgPath)
	require.NoError(t, err)
	assert.False(t, result.RequiresRestart,
		"reverting to the mode already being served cannot need a restart")

	desired, err := rt.GetDesiredConfig()
	require.NoError(t, err)
	assert.Equal(t, config.RoutingModeRetrieveTools, desired.RoutingMode, "nothing is pending any more")
}

// A hand edit of the config file is a supported way to change the routing mode,
// and the watcher hot-reloads it — but /mcp stays bound to the instance it
// registered at startup. The recorded served mode is what keeps every surface
// from reporting a mode nobody is being served.
func TestServedRoutingMode_SurvivesADiskReload(t *testing.T) {
	rt, cfgPath := newPendingRestartRuntime(t)
	rt.SetServedRoutingMode(config.RoutingModeRetrieveTools)

	edited := config.DefaultConfig()
	edited.Listen = "127.0.0.1:8080"
	edited.DataDir = filepath.Dir(cfgPath)
	edited.RoutingMode = config.RoutingModeDirect
	require.NoError(t, config.SaveConfig(edited, cfgPath))
	require.NoError(t, rt.ReloadConfiguration())

	assert.Equal(t, config.RoutingModeRetrieveTools, rt.ServedRoutingMode(),
		"a file edit cannot rebind /mcp")

	desired, err := rt.GetDesiredConfig()
	require.NoError(t, err)
	assert.Equal(t, config.RoutingModeDirect, desired.RoutingMode,
		"the file IS the desired config, so the edit shows up as pending")

	// The running config must not adopt it either — otherwise the next apply
	// pins to a value that was never live, and every surface reading the config
	// names a mode nobody is being served.
	live, err := rt.GetConfig()
	require.NoError(t, err)
	assert.Equal(t, config.RoutingModeRetrieveTools, live.RoutingMode)
	// …and the configsvc snapshot, which live subscribers read, must agree with
	// it rather than carrying the raw file.
	assert.Equal(t, config.RoutingModeRetrieveTools, rt.ConfigSnapshot().Config.RoutingMode)
}

// desiredCfg is the merge base for every read-modify-write of the config
// (PATCH /api/v1/config, the raw editor, the tray). It therefore has to track
// EVERY commit path, not just ApplyConfig — UpdateConfig and SaveConfiguration
// replace or mutate the live config too, and a desired copy frozen at boot
// would make the next PATCH re-persist a document that has lost whatever those
// paths added. Registries are the sharpest case: they live only in the config
// file, so nothing splices them back from config.db.
func TestGetDesiredConfig_TracksNonApplyCommitPaths(t *testing.T) {
	rt, cfgPath := newPendingRestartRuntime(t)

	live, err := rt.GetConfig()
	require.NoError(t, err)
	updated := *live
	updated.Registries = append(append([]config.RegistryEntry{}, live.Registries...),
		config.RegistryEntry{ID: "custom", Name: "Custom", URL: "https://example.test"})
	rt.UpdateConfig(&updated, cfgPath)

	desired, err := rt.GetDesiredConfig()
	require.NoError(t, err)
	var found bool
	for _, reg := range desired.Registries {
		if reg.ID == "custom" {
			found = true
		}
	}
	assert.True(t, found, "a config committed outside ApplyConfig must reach the desired config")

	// …and the PATCH round trip that merges onto it must not erase it.
	desired.ToolsLimit = 33
	_, err = rt.ApplyConfig(desired, cfgPath)
	require.NoError(t, err)

	onDisk, err := config.LoadFromFile(cfgPath)
	require.NoError(t, err)
	found = false
	for _, reg := range onDisk.Registries {
		if reg.ID == "custom" {
			found = true
		}
	}
	assert.True(t, found, "the next apply must not re-persist a document without it")
}

// The other direction: a commit path that is NOT ApplyConfig must not adopt or
// erase a restart-gated value that is waiting for a restart. Enabling a server
// while a routing switch is pending rewrites the whole file.
func TestSaveConfiguration_KeepsThePendingRestartGatedValue(t *testing.T) {
	rt, cfgPath := newPendingRestartRuntime(t)

	pending := config.DefaultConfig()
	pending.Listen = "127.0.0.1:8080"
	pending.DataDir = filepath.Dir(cfgPath)
	pending.RoutingMode = config.RoutingModeDirect
	res, err := rt.ApplyConfig(pending, cfgPath)
	require.NoError(t, err)
	require.True(t, res.RequiresRestart)

	// Any non-ApplyConfig commit — here the one every enable/disable/quarantine
	// toggle runs through.
	require.NoError(t, rt.SaveConfiguration())

	onDisk, err := config.LoadFromFile(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, config.RoutingModeDirect, onDisk.RoutingMode,
		"a server toggle must not revert a switch the API still reports as pending")

	desired, err := rt.GetDesiredConfig()
	require.NoError(t, err)
	assert.Equal(t, config.RoutingModeDirect, desired.RoutingMode)

	live, err := rt.GetConfig()
	require.NoError(t, err)
	assert.Equal(t, config.RoutingModeRetrieveTools, live.RoutingMode, "still not adopted in memory")
}
