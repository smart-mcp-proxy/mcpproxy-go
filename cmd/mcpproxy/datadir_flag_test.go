package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// TestLoadersHonorGlobalDataDirFlag verifies that every per-command config
// loader applies the global --data-dir flag (bound to the package-level
// dataDir variable) on top of the loaded config file. Without this, daemon
// detection (socket.DetectSocketPath) probes the wrong directory and commands
// like `mcpproxy doctor --data-dir D` falsely report that no daemon is
// running (DOCTOR-DATADIR).
//
// These subtests mutate package-level globals (dataDir + per-command config
// path vars), so they must not use t.Parallel.
func TestLoadersHonorGlobalDataDirFlag(t *testing.T) {
	cases := []struct {
		name    string
		setPath func(string) // sets the command's local --config path var
		load    func() (*config.Config, error)
	}{
		{"doctor", func(p string) { doctorConfigPath = p }, loadDoctorConfig},
		{"auth", func(p string) { authConfigPath = p }, loadAuthConfig},
		{"call", func(p string) { callConfigPath = p }, loadCallConfig},
		{"code", func(p string) { codeConfigPath = p }, loadCodeConfig},
		{"tools", func(p string) { configPath = p }, loadToolsConfig},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			cfgDataDir := filepath.Join(tmp, "cfg-data")
			cfgPath := filepath.Join(tmp, "cfg.json")
			cfgJSON := fmt.Sprintf(`{"listen":"127.0.0.1:8080","data_dir":%q,"mcpServers":[]}`, cfgDataDir)
			if err := os.WriteFile(cfgPath, []byte(cfgJSON), 0o600); err != nil {
				t.Fatal(err)
			}

			oldDataDir := dataDir
			defer func() {
				dataDir = oldDataDir
				tc.setPath("")
			}()
			tc.setPath(cfgPath)

			// 1) No flag: the config file's data_dir wins.
			dataDir = ""
			cfg, err := tc.load()
			if err != nil {
				t.Fatalf("load without --data-dir: %v", err)
			}
			if cfg.DataDir != cfgDataDir {
				t.Errorf("no-flag DataDir = %q, want config data_dir %q", cfg.DataDir, cfgDataDir)
			}

			// 2) --data-dir flag overrides the config file (DOCTOR-DATADIR bug).
			flagDir := filepath.Join(tmp, "flag-data")
			dataDir = flagDir
			cfg, err = tc.load()
			if err != nil {
				t.Fatalf("load with --data-dir: %v", err)
			}
			if cfg.DataDir != flagDir {
				t.Errorf("DataDir = %q, want --data-dir %q", cfg.DataDir, flagDir)
			}
		})
	}
}
