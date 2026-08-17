package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCodeConfigFilePath pins the CLI half of the Spec 097 config-path
// authority: the in-process server is handed the ACTIVE config FILE path, and
// that path comes from --config or the documented default — never from
// --data-dir, which the loader may override afterwards (research R2 trap 2).
func TestCodeConfigFilePath(t *testing.T) {
	previousConfig, previousDataDir := codeConfigPath, dataDir
	t.Cleanup(func() { codeConfigPath, dataDir = previousConfig, previousDataDir })

	t.Run("defaults to the standard config file", func(t *testing.T) {
		codeConfigPath, dataDir = "", ""
		home, err := os.UserHomeDir()
		if err != nil {
			t.Skipf("no home directory in this environment: %v", err)
		}

		got, err := codeConfigFilePath()
		if err != nil {
			t.Fatalf("codeConfigFilePath() error: %v", err)
		}
		want := filepath.Join(home, ".mcpproxy", "mcp_config.json")
		if got != want {
			t.Fatalf("codeConfigFilePath() = %q, want %q", got, want)
		}
	})

	t.Run("--config wins", func(t *testing.T) {
		custom := filepath.Join(t.TempDir(), "custom", "mcp_config.json")
		codeConfigPath, dataDir = custom, ""

		got, err := codeConfigFilePath()
		if err != nil {
			t.Fatalf("codeConfigFilePath() error: %v", err)
		}
		if got != custom {
			t.Fatalf("codeConfigFilePath() = %q, want %q", got, custom)
		}
	})

	t.Run("--data-dir does not move the config file", func(t *testing.T) {
		custom := filepath.Join(t.TempDir(), "custom", "mcp_config.json")
		codeConfigPath, dataDir = custom, t.TempDir()

		got, err := codeConfigFilePath()
		if err != nil {
			t.Fatalf("codeConfigFilePath() error: %v", err)
		}
		if got != custom {
			t.Fatalf("codeConfigFilePath() = %q, want %q — --data-dir must not redirect script resolution", got, custom)
		}
	})
}
