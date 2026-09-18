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

// Round-1 review findings.

// EnsureAPIKey lets MCPPROXY_API_KEY win over a key the FILE holds; that is
// an override like Validate's and must not replace the file key on disk.
func TestEnsureAPIKey_EnvKeyOverFileKeyIsNotPersisted(t *testing.T) {
	t.Cleanup(ResetProcessOverrides)
	ResetProcessOverrides()
	path := writeOverrideTestFile(t, `{"listen": "127.0.0.1:8080", "api_key": "file-key", "mcpServers": []}`)
	t.Setenv("MCPPROXY_API_KEY", "env-key")

	cfg, err := LoadFromFile(path)
	require.NoError(t, err)
	require.Equal(t, "file-key", cfg.APIKey, "Validate only fills an EMPTY api_key from env")

	key, generated, source := cfg.EnsureAPIKey()
	require.Equal(t, "env-key", key)
	require.False(t, generated)
	require.Equal(t, APIKeySourceEnvironment, source)

	require.NoError(t, SaveConfig(cfg, path))
	assert.Equal(t, "file-key", readJSON(t, path)["api_key"])
}

// A key EnsureAPIKey generated is the one thing that must persist.
func TestEnsureAPIKey_GeneratedKeyIsPersisted(t *testing.T) {
	t.Cleanup(ResetProcessOverrides)
	ResetProcessOverrides()
	path := writeOverrideTestFile(t, `{"listen": "127.0.0.1:8080", "mcpServers": []}`)
	os.Unsetenv("MCPPROXY_API_KEY")

	cfg, err := LoadFromFile(path)
	require.NoError(t, err)
	key, generated, _ := cfg.EnsureAPIKey()
	require.True(t, generated)

	require.NoError(t, SaveConfig(cfg, path))
	assert.Equal(t, key, readJSON(t, path)["api_key"])
}

// The same field overridden by env AND a flag keeps both records: the flag
// wins in memory, neither leaks, and a reload (which rebuilds the env set)
// must not turn the flag's value into "an edit" by dropping its record.
func TestOverrides_EnvAndFlagOnTheSameFieldBothSurviveReload(t *testing.T) {
	t.Cleanup(ResetProcessOverrides)
	ResetProcessOverrides()
	path := writeOverrideTestFile(t, `{"listen": "127.0.0.1:8080", "mcpServers": []}`)
	t.Setenv("MCPPROXY_LISTEN", "127.0.0.1:9000")

	cfg, err := LoadFromFile(path)
	require.NoError(t, err)
	OverrideForProcess(cfg, FieldListen, OverrideSourceFlag, "127.0.0.1:9999")
	require.Equal(t, "127.0.0.1:9999", cfg.Listen)

	// The reload: the loader rebuilds the env entries on a fresh config.
	reloaded, err := LoadFromFile(path)
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1:9000", reloaded.Listen, "the loader applies env")
	ReapplyFlagOverrides(reloaded, nil)
	assert.Equal(t, "127.0.0.1:9999", reloaded.Listen, "flag overrides are re-applied on reload")

	require.NoError(t, SaveConfig(reloaded, path))
	assert.Equal(t, "127.0.0.1:8080", readJSON(t, path)["listen"])

	// The original (pre-reload) effective config saves the same way.
	require.NoError(t, SaveConfig(cfg, path))
	assert.Equal(t, "127.0.0.1:8080", readJSON(t, path)["listen"])
}

// The flag record's load-time fallback is the FILE value, not the env value
// the flag happened to be layered over.
func TestOverrides_FlagOverEnvFallsBackToFileValue(t *testing.T) {
	t.Cleanup(ResetProcessOverrides)
	ResetProcessOverrides()
	path := writeOverrideTestFile(t, `{"listen": "127.0.0.1:8080", "mcpServers": []}`)
	t.Setenv("MCPPROXY_LISTEN", "127.0.0.1:9000")

	cfg, err := LoadFromFile(path)
	require.NoError(t, err)
	OverrideForProcess(cfg, FieldListen, OverrideSourceFlag, "127.0.0.1:9999")

	persisted := PersistableConfig(cfg, filepath.Join(t.TempDir(), "missing.json"))
	assert.Equal(t, "127.0.0.1:8080", persisted.Listen)
}

// ReapplyFlagOverrides re-layers every flag-sourced override onto a freshly
// loaded config and refreshes the recorded file value.
func TestReapplyFlagOverrides(t *testing.T) {
	t.Cleanup(ResetProcessOverrides)
	ResetProcessOverrides()
	path := writeOverrideTestFile(t, `{"read_only_mode": false, "logging": {"level": "info"}, "mcpServers": []}`)

	cfg, err := LoadFromFile(path)
	require.NoError(t, err)
	OverrideForProcess(cfg, FieldReadOnlyMode, OverrideSourceFlag, true)
	OverrideForProcess(cfg, FieldLogLevel, OverrideSourceFlag, "debug")

	// The file changed and was reloaded.
	require.NoError(t, os.WriteFile(path, []byte(`{"read_only_mode": false, "logging": {"level": "warn"}, "mcpServers": []}`), 0o600))
	reloaded, err := LoadFromFile(path)
	require.NoError(t, err)
	require.Equal(t, "warn", reloaded.Logging.Level)
	ReapplyFlagOverrides(reloaded, nil)
	assert.True(t, reloaded.ReadOnlyMode)
	assert.Equal(t, "debug", reloaded.Logging.Level)

	persisted := PersistableConfig(reloaded, filepath.Join(t.TempDir(), "missing.json"))
	assert.False(t, persisted.ReadOnlyMode)
	assert.Equal(t, "warn", persisted.Logging.Level, "the fallback tracks the RELOADED file value")
}

// Rebuilding the env set on a reload must be atomic with respect to saves on
// other goroutines: a save that lands mid-rebuild must never see an empty (or
// half-built) registry and persist the overrides.
func TestOverrides_EnvRebuildIsAtomicWithSaves(t *testing.T) {
	t.Cleanup(ResetProcessOverrides)
	ResetProcessOverrides()
	path := writeOverrideTestFile(t, `{"listen": "127.0.0.1:8080", "tool_response_mode": "full", "mcpServers": []}`)
	t.Setenv("MCPPROXY_LISTEN", "0.0.0.0:9999")
	t.Setenv("MCPPROXY_TOOL_RESPONSE_MODE", "compact")

	cfg, err := LoadFromFile(path)
	require.NoError(t, err)

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			_, _ = LoadFromFile(path) // rebuilds the env entries
		}
	}()
	for i := 0; i < 200; i++ {
		persisted := PersistableConfig(cfg, path)
		if persisted.Listen != "127.0.0.1:8080" || persisted.ToolResponseMode != "full" {
			close(stop)
			<-done
			t.Fatalf("iteration %d: env override leaked mid-rebuild: listen=%q mode=%q", i, persisted.Listen, persisted.ToolResponseMode)
		}
	}
	close(stop)
	<-done
}

// Round-2 review findings.

// A flag the API superseded in this process (a hot edit to a different
// value) must not come back on a reload: the edit is on disk, the flag is
// retired.
func TestReapplyFlagOverrides_SkipsAFlagTheLiveConfigSuperseded(t *testing.T) {
	t.Cleanup(ResetProcessOverrides)
	ResetProcessOverrides()
	path := writeOverrideTestFile(t, `{"read_only_mode": false, "tool_response_mode": "full", "mcpServers": []}`)

	live, err := LoadFromFile(path)
	require.NoError(t, err)
	OverrideForProcess(live, FieldReadOnlyMode, OverrideSourceFlag, false) // explicit --read-only=false
	OverrideForProcess(live, FieldToolResponseMode, OverrideSourceFlag, "compact")

	live.ReadOnlyMode = true // the API edit, applied hot and persisted
	require.NoError(t, os.WriteFile(path, []byte(`{"read_only_mode": true, "tool_response_mode": "full", "tools_limit": 5, "mcpServers": []}`), 0o600))

	reloaded, err := LoadFromFile(path)
	require.NoError(t, err)
	ReapplyFlagOverrides(reloaded, live)
	assert.True(t, reloaded.ReadOnlyMode, "the superseded flag must not be resurrected")
	assert.Equal(t, "compact", reloaded.ToolResponseMode, "the untouched flag is re-applied")
	assert.ElementsMatch(t, []string{"read_only_mode", "tool_response_mode"}, ProcessOverrideFields(),
		"the record stays: a stale config still carrying the flag value must keep restoring the file")
}

// Registering the same override twice (loadConfig and runServer both apply
// --tool-response-limit; Validate runs twice) must keep the FILE value as the
// fallback, not the flag value the second registration finds in place.
func TestOverrideForProcess_RepeatedRegistrationKeepsTheFileFallback(t *testing.T) {
	t.Cleanup(ResetProcessOverrides)
	ResetProcessOverrides()

	cfg := DefaultConfig()
	cfg.ToolResponseLimit = 20000
	OverrideForProcess(cfg, FieldToolResponseLimit, OverrideSourceFlag, 500)
	OverrideForProcess(cfg, FieldToolResponseLimit, OverrideSourceFlag, 500)

	persisted := PersistableConfig(cfg, filepath.Join(t.TempDir(), "missing.json"))
	assert.Equal(t, 20000, persisted.ToolResponseLimit)
}

// Round-4 review findings.

// With env AND a flag on the same field only the flag is effective; the env
// record must not intercept an API edit that happens to equal the env value.
func TestPersistableConfig_StackedEnvAndFlag_EditToTheEnvValuePersists(t *testing.T) {
	t.Cleanup(ResetProcessOverrides)
	ResetProcessOverrides()
	path := writeOverrideTestFile(t, `{"direct_tool_response_mode": "full", "mcpServers": []}`)
	t.Setenv("MCPPROXY_DIRECT_TOOL_RESPONSE_MODE", "deferred")

	cfg, err := LoadFromFile(path)
	require.NoError(t, err)
	OverrideForProcess(cfg, FieldDirectToolResponseMode, OverrideSourceFlag, "compact")

	cfg.DirectToolResponseMode = "deferred" // the API edit: visibly different from "compact"
	require.NoError(t, SaveConfig(cfg, path))
	assert.Equal(t, "deferred", readJSON(t, path)["direct_tool_response_mode"])
}

// Round-5 review finding.

// Edit-aware saves (review rounds 2-8). An override is never "retired": the
// save that persists an API edit ignores the overrides of the fields the
// caller MOVED relative to its merge base, and every other save keeps
// restoring the file value. No mutable state means no window in which a
// concurrent save of the still-live config is unprotected.

func TestSaveConfigWithEdits_PersistsAnEditBackToTheOverrideValue(t *testing.T) {
	t.Cleanup(ResetProcessOverrides)
	ResetProcessOverrides()
	path := writeOverrideTestFile(t, `{"tool_response_mode": "full", "listen": "127.0.0.1:8080", "mcpServers": []}`)

	cfg, err := ReadFile(path)
	require.NoError(t, err)
	OverrideForProcess(cfg, FieldToolResponseMode, OverrideSourceFlag, "compact")
	OverrideForProcess(cfg, FieldListen, OverrideSourceFlag, ":0")

	base := *cfg
	next := base
	next.ToolResponseMode = "full" // away from the flag
	require.NoError(t, SaveConfigWithEdits(&next, &base, path))
	assert.Equal(t, "full", readJSON(t, path)["tool_response_mode"])

	base = next
	next.ToolResponseMode = "compact" // and back to it: still an edit
	require.NoError(t, SaveConfigWithEdits(&next, &base, path))
	m := readJSON(t, path)
	assert.Equal(t, "compact", m["tool_response_mode"])
	assert.Equal(t, "127.0.0.1:8080", m["listen"], "the untouched override is still not persisted")
}

func TestSaveConfigWithEdits_RoundTripIsNotAnEdit(t *testing.T) {
	t.Cleanup(ResetProcessOverrides)
	ResetProcessOverrides()
	path := writeOverrideTestFile(t, `{"read_only_mode": false, "mcpServers": []}`)

	cfg, err := ReadFile(path)
	require.NoError(t, err)
	OverrideForProcess(cfg, FieldReadOnlyMode, OverrideSourceFlag, true)

	base := *cfg
	next := base
	next.ToolsLimit = 42 // the only thing the caller changed
	require.NoError(t, SaveConfigWithEdits(&next, &base, path))
	m := readJSON(t, path)
	assert.NotEqual(t, true, m["read_only_mode"])
	assert.Equal(t, float64(42), m["tools_limit"])
}

// Moving a field from a base value to the override's own value is an edit
// (base != next == override is distinguishable, unlike a round trip).
func TestSaveConfigWithEdits_MovingToTheOverrideValueIsAnEdit(t *testing.T) {
	t.Cleanup(ResetProcessOverrides)
	ResetProcessOverrides()
	path := writeOverrideTestFile(t, `{"listen": "127.0.0.1:8080", "mcpServers": []}`)

	cfg, err := ReadFile(path)
	require.NoError(t, err)
	OverrideForProcess(cfg, FieldListen, OverrideSourceFlag, "127.0.0.1:9000")

	base := *cfg
	base.Listen = "127.0.0.1:8080" // the desired config after a disk reload
	next := base
	next.Listen = "127.0.0.1:9000" // the operator makes the flag's address permanent
	require.NoError(t, SaveConfigWithEdits(&next, &base, path))
	assert.Equal(t, "127.0.0.1:9000", readJSON(t, path)["listen"])
}

// With env AND a flag on the same field only the flag is effective; the env
// record must not intercept a plain save of an edit that equals the env value
// once that edit is on disk.
func TestPersistableConfig_StackedEnvAndFlag_RestoresFromTheFile(t *testing.T) {
	t.Cleanup(ResetProcessOverrides)
	ResetProcessOverrides()
	path := writeOverrideTestFile(t, `{"direct_tool_response_mode": "full", "mcpServers": []}`)
	t.Setenv("MCPPROXY_DIRECT_TOOL_RESPONSE_MODE", "deferred")

	cfg, err := LoadFromFile(path)
	require.NoError(t, err)
	OverrideForProcess(cfg, FieldDirectToolResponseMode, OverrideSourceFlag, "compact")

	base := *cfg
	next := base
	next.DirectToolResponseMode = "deferred" // visibly different from "compact"
	require.NoError(t, SaveConfigWithEdits(&next, &base, path))
	assert.Equal(t, "deferred", readJSON(t, path)["direct_tool_response_mode"])

	// A later plain save of the same effective config keeps what is on disk.
	require.NoError(t, SaveConfig(&next, path))
	assert.Equal(t, "deferred", readJSON(t, path)["direct_tool_response_mode"])
}

// While an API save persists a rotated api_key, a concurrent plain save of
// the still-live config (telemetry, another runtime path) must never write
// the env secret: the override record is never removed, only ignored by the
// one save that edits the field.
func TestSaveConfigWithEdits_ConcurrentStaleSaveNeverLeaksTheEnvKey(t *testing.T) {
	t.Cleanup(ResetProcessOverrides)
	ResetProcessOverrides()
	path := writeOverrideTestFile(t, `{"listen": "127.0.0.1:8080", "mcpServers": []}`)

	live, err := ReadFile(path)
	require.NoError(t, err)
	OverrideForProcess(live, FieldAPIKey, OverrideSourceEnv, "env-secret")

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = SaveConfig(live, path) // the stale live config, env-secret and all
		}
	}()
	for i := 0; i < 100; i++ {
		base := *live
		next := base
		next.APIKey = "rotated-key"
		require.NoError(t, SaveConfigWithEdits(&next, &base, path))
		if got := readJSON(t, path)["api_key"]; got == "env-secret" {
			close(stop)
			<-done
			t.Fatalf("iteration %d: env API key leaked into the file", i)
		}
	}
	close(stop)
	<-done
	assert.NotEqual(t, "env-secret", readJSON(t, path)["api_key"])
}
