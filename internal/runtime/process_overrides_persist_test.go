package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// The runtime's persist paths (SaveConfiguration on every API-driven server
// change, ApplyConfig on PUT /api/v1/config) write the live config, which
// carries the `serve` CLI flag and MCPPROXY_* env overrides. Those are
// process-only and must not reach the file — while a genuine API edit of the
// same field (the Settings page changing listen) still must.
func newOverriddenRuntime(t *testing.T) (*Runtime, string) {
	t.Helper()
	t.Cleanup(config.ResetProcessOverrides)
	config.ResetProcessOverrides()

	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "mcp_config.json")
	initial := config.DefaultConfig()
	initial.Listen = "127.0.0.1:8080"
	initial.DataDir = tmp
	initial.ToolResponseMode = "full"
	require.NoError(t, config.SaveConfig(initial, cfgPath))

	cfg, err := config.ReadFile(cfgPath)
	require.NoError(t, err)
	config.OverrideForProcess(cfg, config.FieldListen, config.OverrideSourceFlag, ":0")
	config.OverrideForProcess(cfg, config.FieldToolResponseMode, config.OverrideSourceFlag, "compact")
	config.OverrideForProcess(cfg, config.FieldReadOnlyMode, config.OverrideSourceFlag, true)
	config.OverrideForProcess(cfg, config.FieldAPIKey, config.OverrideSourceEnv, "env-secret")

	rt, err := New(cfg, cfgPath, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })
	return rt, cfgPath
}

func readConfigJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(data, &m))
	return m
}

func assertNoOverridesOnDisk(t *testing.T, m map[string]any) {
	t.Helper()
	assert.Equal(t, "127.0.0.1:8080", m["listen"], "--listen must not be persisted")
	assert.Equal(t, "full", m["tool_response_mode"], "--tool-response-mode must not be persisted")
	assert.NotEqual(t, true, m["read_only_mode"], "--read-only must not be persisted")
	assert.NotEqual(t, "env-secret", m["api_key"], "MCPPROXY_API_KEY must not be persisted")
}

func TestSaveConfiguration_DoesNotPersistProcessOverrides(t *testing.T) {
	rt, cfgPath := newOverriddenRuntime(t)

	require.NoError(t, rt.SaveConfiguration())

	assertNoOverridesOnDisk(t, readConfigJSON(t, cfgPath))

	live, err := rt.GetConfig()
	require.NoError(t, err)
	assert.Equal(t, ":0", live.Listen, "the effective config keeps the override")
	assert.Equal(t, "compact", live.ToolResponseMode)
	assert.True(t, live.ReadOnlyMode)
	assert.Equal(t, "env-secret", live.APIKey)
}

// GET /config returns the desired config — which at startup IS the effective
// one, overrides included — and a PUT round-trips it. A field that comes back
// unchanged from that round trip is not an edit.
func TestApplyConfig_RoundTrippedOverrideIsNotPersisted(t *testing.T) {
	rt, cfgPath := newOverriddenRuntime(t)

	desired, err := rt.GetDesiredConfig()
	require.NoError(t, err)
	require.Equal(t, ":0", desired.Listen, "GET /config serves the effective value")
	desired.ToolsLimit = 42 // the actual edit

	_, err = rt.ApplyConfig(desired, cfgPath)
	require.NoError(t, err)

	m := readConfigJSON(t, cfgPath)
	assertNoOverridesOnDisk(t, m)
	assert.Equal(t, float64(42), m["tools_limit"], "the real edit is persisted")
}

// The Settings page changing listen while `--listen` is in force is a real
// edit: it must reach the file (restart-gated, so it stays pending in memory).
func TestApplyConfig_EditOfAnOverriddenFieldIsPersisted(t *testing.T) {
	rt, cfgPath := newOverriddenRuntime(t)

	desired, err := rt.GetDesiredConfig()
	require.NoError(t, err)
	desired.Listen = "127.0.0.1:9090"
	desired.ToolResponseMode = "full" // hot: turning the flag's choice back off

	_, err = rt.ApplyConfig(desired, cfgPath)
	require.NoError(t, err)

	m := readConfigJSON(t, cfgPath)
	assert.Equal(t, "127.0.0.1:9090", m["listen"])
	assert.Equal(t, "full", m["tool_response_mode"])
	assert.NotEqual(t, true, m["read_only_mode"], "the untouched override is still not persisted")

	live, err := rt.GetConfig()
	require.NoError(t, err)
	assert.Equal(t, ":0", live.Listen, "listen is restart-gated: memory keeps the bound value")
	assert.Equal(t, "full", live.ToolResponseMode, "the hot edit is live")

	// A later unrelated save must not revert the persisted edit.
	require.NoError(t, rt.SaveConfiguration())
	m = readConfigJSON(t, cfgPath)
	assert.Equal(t, "127.0.0.1:9090", m["listen"])
}

// The config watcher compares the file with memory to recognise the daemon's
// own saves. With overrides in force the file legitimately differs from memory
// in exactly those fields; that difference must not read as an external edit,
// or every API save would trigger a reload that drops the hot overrides.
func TestConfigWatcher_OwnSaveWithOverridesIsNotAnExternalEdit(t *testing.T) {
	rt, cfgPath := newOverriddenRuntime(t)

	require.NoError(t, rt.SaveConfiguration())
	rt.clearSelfWrites() // the marker path is tested elsewhere; force the snapshot comparison

	rt.reloadFromDiskIfChanged(cfgPath)

	live, err := rt.GetConfig()
	require.NoError(t, err)
	assert.Equal(t, "compact", live.ToolResponseMode, "the daemon's own save must not reload the file over the flag")
	assert.True(t, live.ReadOnlyMode)
}

// A genuine external edit reloads the file; the loader re-applies env
// overrides, and the runtime must re-apply the serve flags the same way, or a
// hand edit of an unrelated key silently switches --read-only off.
func TestReloadConfiguration_KeepsFlagOverridesEffective(t *testing.T) {
	rt, cfgPath := newOverriddenRuntime(t)

	edited, err := config.ReadFile(cfgPath)
	require.NoError(t, err)
	edited.ToolsLimit = 77 // the external edit
	require.NoError(t, config.SaveConfig(edited, cfgPath))

	require.NoError(t, rt.ReloadConfiguration())

	live, err := rt.GetConfig()
	require.NoError(t, err)
	assert.Equal(t, 77, live.ToolsLimit, "the external edit is adopted")
	assert.True(t, live.ReadOnlyMode, "--read-only survives a reload")
	assert.Equal(t, "compact", live.ToolResponseMode, "--tool-response-mode survives a reload")
	assert.Equal(t, ":0", live.Listen)

	// …and the next save still does not persist them.
	require.NoError(t, rt.SaveConfiguration())
	m := readConfigJSON(t, cfgPath)
	assertNoOverridesOnDisk(t, m)
	assert.Equal(t, float64(77), m["tools_limit"])
}

// A hot API edit of an overridden field supersedes the flag for this process:
// a later external edit of an unrelated key must not resurrect it on reload.
func TestReloadConfiguration_DoesNotResurrectAFlagTheAPISuperseded(t *testing.T) {
	rt, cfgPath := newOverriddenRuntime(t)

	desired, err := rt.GetDesiredConfig()
	require.NoError(t, err)
	desired.ToolResponseMode = "full" // the API turns the flag's choice off
	_, err = rt.ApplyConfig(desired, cfgPath)
	require.NoError(t, err)

	edited, err := config.ReadFile(cfgPath)
	require.NoError(t, err)
	require.Equal(t, "full", edited.ToolResponseMode)
	edited.ToolsLimit = 77
	require.NoError(t, config.SaveConfig(edited, cfgPath))
	require.NoError(t, rt.ReloadConfiguration())

	live, err := rt.GetConfig()
	require.NoError(t, err)
	assert.Equal(t, "full", live.ToolResponseMode, "the API edit survives the reload")
	assert.True(t, live.ReadOnlyMode, "the untouched flag survives the reload")
}

// Toggling an overridden hot field away from the flag and back again via the
// API: the second edit is a real edit and must persist, not be swapped for
// the file's value.
func TestApplyConfig_TogglingAnOverriddenFieldBackPersists(t *testing.T) {
	rt, cfgPath := newOverriddenRuntime(t)

	desired, err := rt.GetDesiredConfig()
	require.NoError(t, err)
	desired.ToolResponseMode = "full"
	_, err = rt.ApplyConfig(desired, cfgPath)
	require.NoError(t, err)
	assert.Equal(t, "full", readConfigJSON(t, cfgPath)["tool_response_mode"])

	desired, err = rt.GetDesiredConfig()
	require.NoError(t, err)
	desired.ToolResponseMode = "compact"
	_, err = rt.ApplyConfig(desired, cfgPath)
	require.NoError(t, err)
	assert.Equal(t, "compact", readConfigJSON(t, cfgPath)["tool_response_mode"])
}

// A reload must keep the DESIRED config equal to the file: a restart-gated
// flag (listen) is re-applied to the running config only, so a pending file
// edit of that field is still reported as pending and not clobbered.
func TestReloadConfiguration_RestartGatedFlagDoesNotHideAPendingFileEdit(t *testing.T) {
	rt, cfgPath := newOverriddenRuntime(t)

	edited, err := config.ReadFile(cfgPath)
	require.NoError(t, err)
	edited.Listen = "127.0.0.1:9090"
	require.NoError(t, config.SaveConfig(edited, cfgPath))
	require.NoError(t, rt.ReloadConfiguration())

	live, err := rt.GetConfig()
	require.NoError(t, err)
	assert.Equal(t, ":0", live.Listen, "the bound listener keeps the flag value")
	desired, err := rt.GetDesiredConfig()
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1:9090", desired.Listen, "the desired config is the file")

	require.NoError(t, rt.SaveConfiguration())
	assert.Equal(t, "127.0.0.1:9090", readConfigJSON(t, cfgPath)["listen"], "a later save keeps the pending edit")
}

// The reload's per-component side effects (upstream manager global config,
// truncator, logging, telemetry) must follow the RUNNING config — the file
// plus the flags — exactly as ApplyConfig applies hotCfg, not the raw file.
func TestReloadConfiguration_ComponentsFollowTheRunningConfig(t *testing.T) {
	t.Cleanup(config.ResetProcessOverrides)
	config.ResetProcessOverrides()

	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "mcp_config.json")
	initial := config.DefaultConfig()
	initial.DataDir = tmp
	initial.ToolResponseLimit = 20000
	initial.ToolResponseMode = "full"
	require.NoError(t, config.SaveConfig(initial, cfgPath))

	cfg, err := config.ReadFile(cfgPath)
	require.NoError(t, err)
	config.OverrideForProcess(cfg, config.FieldToolResponseLimit, config.OverrideSourceFlag, 500)
	config.OverrideForProcess(cfg, config.FieldToolResponseMode, config.OverrideSourceFlag, "compact")

	rt, err := New(cfg, cfgPath, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })
	require.Equal(t, 500, rt.Truncator().Limit())

	edited, err := config.ReadFile(cfgPath)
	require.NoError(t, err)
	edited.ToolsLimit = 77
	require.NoError(t, config.SaveConfig(edited, cfgPath))
	require.NoError(t, rt.ReloadConfiguration())

	assert.Equal(t, 500, rt.Truncator().Limit(), "the truncator must not be rebuilt from the file's limit")
	if um := rt.upstreamManager; um != nil {
		assert.Equal(t, "compact", um.GlobalConfig().ToolResponseMode, "the upstream manager must see the running config")
	}
	snap := rt.ConfigSnapshot()
	assert.Equal(t, "compact", snap.Config.ToolResponseMode, "the published snapshot is the running config")
}

// An API edit of a restart-gated overridden field (listen under --listen)
// ends the override for this process even though the listener stays bound:
// a second edit back to the flag's value is then persisted as asked, so disk,
// the desired config and the API result agree.
func TestApplyConfig_EditingListenUnderAFlagEndsTheOverride(t *testing.T) {
	rt, cfgPath := newOverriddenRuntime(t)

	desired, err := rt.GetDesiredConfig()
	require.NoError(t, err)
	desired.Listen = "127.0.0.1:9090"
	_, err = rt.ApplyConfig(desired, cfgPath)
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1:9090", readConfigJSON(t, cfgPath)["listen"])

	desired, err = rt.GetDesiredConfig()
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1:9090", desired.Listen)
	desired.Listen = ":0" // cancel: back to what this process is bound to
	_, err = rt.ApplyConfig(desired, cfgPath)
	require.NoError(t, err)

	assert.Equal(t, ":0", readConfigJSON(t, cfgPath)["listen"], "the explicit edit is persisted as asked")
	desired, err = rt.GetDesiredConfig()
	require.NoError(t, err)
	assert.Equal(t, ":0", desired.Listen)
	assert.NotContains(t, config.ProcessOverrideFields(), "listen")
	assert.Contains(t, config.ProcessOverrideFields(), "read_only_mode", "untouched overrides stay")
}

// After a disk reload the desired config must still carry the hot flags, as
// it does at startup, so a GET→PUT round trip of an unrelated edit neither
// retires --read-only nor hot-applies the file's value over it.
func TestApplyConfig_UnrelatedEditAfterReloadKeepsHotFlags(t *testing.T) {
	rt, cfgPath := newOverriddenRuntime(t)

	edited, err := config.ReadFile(cfgPath)
	require.NoError(t, err)
	edited.ToolsLimit = 77
	require.NoError(t, config.SaveConfig(edited, cfgPath))
	require.NoError(t, rt.ReloadConfiguration())

	desired, err := rt.GetDesiredConfig()
	require.NoError(t, err)
	assert.True(t, desired.ReadOnlyMode, "GET /config shows the effective hot flag after a reload")
	assert.Equal(t, "compact", desired.ToolResponseMode)
	desired.ToolsLimit = 99 // the unrelated edit
	_, err = rt.ApplyConfig(desired, cfgPath)
	require.NoError(t, err)

	live, err := rt.GetConfig()
	require.NoError(t, err)
	assert.True(t, live.ReadOnlyMode, "--read-only must survive an unrelated API edit")
	assert.Equal(t, "compact", live.ToolResponseMode)
	assert.Equal(t, 99, live.ToolsLimit)
	assert.Contains(t, config.ProcessOverrideFields(), "read_only_mode")

	m := readConfigJSON(t, cfgPath)
	assertNoOverridesOnDisk(t, m)
	assert.Equal(t, float64(99), m["tools_limit"])
}

// A round trip of the file's listen value after a reload is not an edit of
// listen: the --listen override survives and is still not persisted.
func TestApplyConfig_RoundTripAfterReloadKeepsTheListenOverride(t *testing.T) {
	rt, cfgPath := newOverriddenRuntime(t)

	edited, err := config.ReadFile(cfgPath)
	require.NoError(t, err)
	edited.ToolsLimit = 77
	require.NoError(t, config.SaveConfig(edited, cfgPath))
	require.NoError(t, rt.ReloadConfiguration())

	desired, err := rt.GetDesiredConfig()
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1:8080", desired.Listen, "restart-gated: the desired config is the file")
	desired.ToolsLimit = 99
	_, err = rt.ApplyConfig(desired, cfgPath)
	require.NoError(t, err)

	assert.Contains(t, config.ProcessOverrideFields(), "listen")
	require.NoError(t, rt.SaveConfiguration())
	assert.Equal(t, "127.0.0.1:8080", readConfigJSON(t, cfgPath)["listen"])
}

// After a reload the desired listen is the file's; an API edit that sets it
// to the flag's own address is a distinguishable, legitimate edit and must
// reach the file.
func TestApplyConfig_EditingListenToTheFlagValueAfterReloadPersists(t *testing.T) {
	rt, cfgPath := newOverriddenRuntime(t)

	edited, err := config.ReadFile(cfgPath)
	require.NoError(t, err)
	edited.ToolsLimit = 77
	require.NoError(t, config.SaveConfig(edited, cfgPath))
	require.NoError(t, rt.ReloadConfiguration())

	desired, err := rt.GetDesiredConfig()
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1:8080", desired.Listen)
	desired.Listen = ":0" // make the flag's address permanent
	_, err = rt.ApplyConfig(desired, cfgPath)
	require.NoError(t, err)

	assert.Equal(t, ":0", readConfigJSON(t, cfgPath)["listen"], "the edit must reach the file")
	desired, err = rt.GetDesiredConfig()
	require.NoError(t, err)
	assert.Equal(t, ":0", desired.Listen)
	assert.NotContains(t, config.ProcessOverrideFields(), "listen")
}
