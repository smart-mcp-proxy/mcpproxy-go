package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A process-only override (CLI flag, MCPPROXY_* env, env API key) shadows the
// file value for this one process. The effective config carries the override;
// the persisted config must not — unless something edited the field afterwards
// (an API PUT of a new listen address), which is a real change to persist.

func writeOverrideTestFile(t *testing.T, raw string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mcp_config.json")
	require.NoError(t, os.WriteFile(path, []byte(raw), 0o600))
	return path
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(data, &m))
	return m
}

func TestOverrideForProcess_SetsEffectiveValueAndRecordsIt(t *testing.T) {
	t.Cleanup(ResetProcessOverrides)
	ResetProcessOverrides()

	cfg := DefaultConfig()
	cfg.Listen = "127.0.0.1:8080"
	OverrideForProcess(cfg, FieldListen, OverrideSourceFlag, ":0")

	assert.Equal(t, ":0", cfg.Listen, "the override is the effective value")
	assert.Equal(t, []string{"listen"}, ProcessOverrideFields())
}

func TestPersistableConfig_RestoresFileValueWhileOverrideStillApplies(t *testing.T) {
	t.Cleanup(ResetProcessOverrides)
	ResetProcessOverrides()
	path := writeOverrideTestFile(t, `{"listen": "127.0.0.1:8080", "mcpServers": []}`)

	cfg, err := ReadFile(path)
	require.NoError(t, err)
	OverrideForProcess(cfg, FieldListen, OverrideSourceFlag, ":0")

	persisted := PersistableConfig(cfg, path)
	assert.Equal(t, "127.0.0.1:8080", persisted.Listen)
	assert.Equal(t, ":0", cfg.Listen, "the effective config is left untouched")
}

func TestPersistableConfig_KeepsAnEditOfTheOverriddenField(t *testing.T) {
	t.Cleanup(ResetProcessOverrides)
	ResetProcessOverrides()
	path := writeOverrideTestFile(t, `{"listen": "127.0.0.1:8080", "mcpServers": []}`)

	cfg, err := ReadFile(path)
	require.NoError(t, err)
	OverrideForProcess(cfg, FieldListen, OverrideSourceFlag, ":0")

	// An API edit: the effective value no longer matches the override.
	edited := *cfg
	edited.Listen = "127.0.0.1:9090"
	persisted := PersistableConfig(&edited, path)
	assert.Equal(t, "127.0.0.1:9090", persisted.Listen, "an edit of an overridden field is persisted")
}

func TestPersistableConfig_PrefersTheCurrentFileOverTheLoadTimeValue(t *testing.T) {
	t.Cleanup(ResetProcessOverrides)
	ResetProcessOverrides()
	path := writeOverrideTestFile(t, `{"listen": "127.0.0.1:8080", "mcpServers": []}`)

	cfg, err := ReadFile(path)
	require.NoError(t, err)
	OverrideForProcess(cfg, FieldListen, OverrideSourceFlag, ":0")

	// Something else (an API edit, an external editor) moved the file on.
	require.NoError(t, os.WriteFile(path, []byte(`{"listen": "127.0.0.1:7070", "mcpServers": []}`), 0o600))

	persisted := PersistableConfig(cfg, path)
	assert.Equal(t, "127.0.0.1:7070", persisted.Listen, "a round-tripped override must not resurrect the load-time file value")
}

func TestPersistableConfig_FallsBackToLoadTimeValueWhenFileUnreadable(t *testing.T) {
	t.Cleanup(ResetProcessOverrides)
	ResetProcessOverrides()

	cfg := DefaultConfig()
	cfg.Listen = "127.0.0.1:8080"
	OverrideForProcess(cfg, FieldListen, OverrideSourceFlag, ":0")

	persisted := PersistableConfig(cfg, filepath.Join(t.TempDir(), "missing.json"))
	assert.Equal(t, "127.0.0.1:8080", persisted.Listen)
}

func TestPersistableConfig_NestedFieldsAreCopiedNotMutated(t *testing.T) {
	t.Cleanup(ResetProcessOverrides)
	ResetProcessOverrides()

	cfg := DefaultConfig()
	cfg.Logging = &LogConfig{Level: "info"}
	cfg.TLS.Enabled = false
	OverrideForProcess(cfg, FieldLogLevel, OverrideSourceFlag, "debug")
	OverrideForProcess(cfg, FieldTLSEnabled, OverrideSourceEnv, true)
	require.Equal(t, "debug", cfg.Logging.Level)
	require.True(t, cfg.TLS.Enabled)

	persisted := PersistableConfig(cfg, filepath.Join(t.TempDir(), "missing.json"))
	assert.Equal(t, "info", persisted.Logging.Level)
	assert.False(t, persisted.TLS.Enabled)
	assert.Equal(t, "debug", cfg.Logging.Level, "restoring must not write through the shared Logging pointer")
	assert.True(t, cfg.TLS.Enabled, "restoring must not write through the shared TLS pointer")
	assert.NotSame(t, cfg.Logging, persisted.Logging)
	assert.NotSame(t, cfg.TLS, persisted.TLS)
}

func TestPersistableConfig_NoOverridesReturnsInput(t *testing.T) {
	t.Cleanup(ResetProcessOverrides)
	ResetProcessOverrides()
	cfg := DefaultConfig()
	assert.Same(t, cfg, PersistableConfig(cfg, "/nonexistent"))
}

func TestSaveConfig_DoesNotPersistProcessOverrides(t *testing.T) {
	t.Cleanup(ResetProcessOverrides)
	ResetProcessOverrides()
	path := writeOverrideTestFile(t, `{"listen": "127.0.0.1:8080", "read_only_mode": false, "mcpServers": []}`)

	cfg, err := ReadFile(path)
	require.NoError(t, err)
	OverrideForProcess(cfg, FieldListen, OverrideSourceFlag, ":0")
	OverrideForProcess(cfg, FieldReadOnlyMode, OverrideSourceFlag, true)
	OverrideForProcess(cfg, FieldAPIKey, OverrideSourceEnv, "env-secret")
	cfg.ToolsLimit = 42 // an ordinary in-memory edit, persisted as usual

	require.NoError(t, SaveConfig(cfg, path))

	m := readJSON(t, path)
	assert.Equal(t, "127.0.0.1:8080", m["listen"])
	assert.NotEqual(t, true, m["read_only_mode"])
	assert.NotEqual(t, "env-secret", m["api_key"])
	assert.Equal(t, float64(42), m["tools_limit"])
}

func TestLoadFromFile_RecordsEnvOverrides(t *testing.T) {
	t.Cleanup(ResetProcessOverrides)
	ResetProcessOverrides()
	path := writeOverrideTestFile(t, `{"listen": "127.0.0.1:8080", "tool_response_mode": "full", "mcpServers": []}`)
	t.Setenv("MCPPROXY_LISTEN", "0.0.0.0:9999")
	t.Setenv("MCPPROXY_TOOL_RESPONSE_MODE", "compact")
	t.Setenv("MCPPROXY_API_KEY", "from-env")

	cfg, err := LoadFromFile(path)
	require.NoError(t, err)
	require.Equal(t, "0.0.0.0:9999", cfg.Listen)
	require.Equal(t, "compact", cfg.ToolResponseMode)
	require.Equal(t, "from-env", cfg.APIKey)

	require.NoError(t, SaveConfig(cfg, path))
	m := readJSON(t, path)
	assert.Equal(t, "127.0.0.1:8080", m["listen"])
	assert.Equal(t, "full", m["tool_response_mode"])
	assert.NotEqual(t, "from-env", m["api_key"])
}

func TestLoadFromFile_ReplacesEnvOverridesButKeepsFlagOverrides(t *testing.T) {
	t.Cleanup(ResetProcessOverrides)
	ResetProcessOverrides()
	path := writeOverrideTestFile(t, `{"listen": "127.0.0.1:8080", "mcpServers": []}`)

	t.Setenv("MCPPROXY_TOOL_RESPONSE_MODE", "compact")
	cfg, err := LoadFromFile(path)
	require.NoError(t, err)
	OverrideForProcess(cfg, FieldListen, OverrideSourceFlag, ":0")
	assert.ElementsMatch(t, []string{"listen", "tool_response_mode"}, ProcessOverrideFields())

	// A reload with the variable gone drops the env entry and keeps the flag.
	os.Unsetenv("MCPPROXY_TOOL_RESPONSE_MODE")
	_, err = LoadFromFile(path)
	require.NoError(t, err)
	assert.Equal(t, []string{"listen"}, ProcessOverrideFields())
}
