package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
)

// TestLoadConfig_ListenFlag pins how `serve --listen` reaches cfg.Listen.
//
// An explicit `--listen ""` is the documented way to ask for native stdio
// transport, but config.Validate() runs right after the flag override and
// resets an empty Listen to the 127.0.0.1:8080 default — so the process
// silently booted in HTTP mode. The server only recognises ":0" as the stdio
// sentinel after validation, so loadConfig must map an explicit empty flag
// onto it. A config file with no listen key must still default to 8080.
//
// Mutates package-level globals (configFile, dataDir); not parallel.
func TestLoadConfig_ListenFlag(t *testing.T) {
	cases := []struct {
		name string
		env  string // MCPPROXY_LISTEN; applied by config.Load before the flag
		args []string
		want string
	}{
		{"unset flag keeps config default", "", nil, "127.0.0.1:8080"},
		{"unset flag keeps env override", "127.0.0.1:7777", nil, "127.0.0.1:7777"},
		{"explicit address wins", "", []string{"--listen", "127.0.0.1:9999"}, "127.0.0.1:9999"},
		{"explicit :0 is the stdio sentinel", "", []string{"--listen", ":0"}, ":0"},
		{"explicit empty string means stdio too", "", []string{"--listen", ""}, ":0"},
		{"explicit empty string beats env override", "127.0.0.1:7777", []string{"--listen", ""}, ":0"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			cfgPath := filepath.Join(tmp, "cfg.json")
			if err := os.WriteFile(cfgPath, []byte(`{"mcpServers":[]}`), 0o600); err != nil {
				t.Fatal(err)
			}

			// Isolate from the developer's shell: loader.go applies
			// MCPPROXY_LISTEN even when an explicit config file is given.
			t.Setenv("MCPPROXY_LISTEN", tc.env)

			oldConfigFile, oldDataDir := configFile, dataDir
			defer func() { configFile, dataDir = oldConfigFile, oldDataDir }()
			configFile, dataDir = cfgPath, tmp

			cmd := &cobra.Command{Use: "serve"}
			cmd.Flags().String("listen", "", "")
			if err := cmd.Flags().Parse(tc.args); err != nil {
				t.Fatal(err)
			}

			cfg, _, err := loadConfig(cmd)
			if err != nil {
				t.Fatalf("loadConfig: %v", err)
			}
			if cfg.Listen != tc.want {
				t.Errorf("cfg.Listen = %q, want %q", cfg.Listen, tc.want)
			}
		})
	}
}
