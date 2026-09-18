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
