package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDefaultConfig_CodeExecutionEnabled pins the v0.66.0 policy change: the
// code_execution tool ships ON. A config file that omits the key inherits the
// default; a file that says false explicitly still wins (default-then-merge).
func TestDefaultConfig_CodeExecutionEnabled(t *testing.T) {
	require.True(t, DefaultConfig().EnableCodeExecution, "DefaultConfig must enable code_execution")

	dir := t.TempDir()

	omitted := filepath.Join(dir, "omitted.json")
	require.NoError(t, os.WriteFile(omitted, []byte(`{"listen":"127.0.0.1:0","data_dir":"`+filepath.ToSlash(dir)+`"}`), 0o600))
	cfg, err := LoadFromFile(omitted)
	require.NoError(t, err)
	require.True(t, cfg.EnableCodeExecution, "a config without the key inherits the enabled default")

	explicit := filepath.Join(dir, "explicit.json")
	require.NoError(t, os.WriteFile(explicit, []byte(`{"listen":"127.0.0.1:0","data_dir":"`+filepath.ToSlash(dir)+`","enable_code_execution":false}`), 0o600))
	cfg, err = LoadFromFile(explicit)
	require.NoError(t, err)
	require.False(t, cfg.EnableCodeExecution, "an explicit false in the file must still win")

	// A freshly written default config file carries the key explicitly, so a
	// later default flip cannot silently change an existing install.
	raw, err := json.Marshal(DefaultConfig())
	require.NoError(t, err)
	var probe map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &probe))
	require.JSONEq(t, `true`, string(probe["enable_code_execution"]))
}
