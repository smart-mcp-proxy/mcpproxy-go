package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func TestConfigFlagsRejectEmptyValue(t *testing.T) {
	t.Run("root flags reject empty values", func(t *testing.T) {
		oldCfg, oldDir := configFile, dataDir
		t.Cleanup(func() { configFile, dataDir = oldCfg, oldDir })

		cases := []struct {
			name string
			args []string
			want string
		}{
			{"short before child", []string{"-c", "", "child"}, "--config"},
			{"long before child", []string{"--config", "", "child"}, "--config"},
			{"equals form", []string{"--config=", "child"}, "--config"},
			{"after child", []string{"child", "-c", ""}, "--config"},
			{"data dir", []string{"-d", "", "child"}, "--data-dir"},
			{"whitespace only", []string{"-c", "  ", "child"}, "--config"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				home := t.TempDir()
				t.Setenv("HOME", home)
				ran := false
				root := &cobra.Command{Use: "root", SilenceUsage: true, SilenceErrors: true}
				registerRootPathFlags(root)
				root.AddCommand(&cobra.Command{Use: "child", RunE: func(*cobra.Command, []string) error { ran = true; return nil }})
				root.SetArgs(tc.args)
				err := root.Execute()
				if err == nil {
					t.Fatalf("expected an error for %v", tc.args)
				}
				if !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "must not be empty") {
					t.Errorf("error %q should name %s and say it must not be empty", err.Error(), tc.want)
				}
				if ran {
					t.Error("command must not run with an empty path flag")
				}
				if _, statErr := os.Stat(filepath.Join(home, ".mcpproxy", "mcp_config.json")); statErr == nil {
					t.Error("no default config may be created")
				}
				if got := classifyError(err); got == ExitCodeSuccess {
					t.Error("exit code must be non-zero")
				}
			})
		}
	})

	t.Run("non-empty values and help type are unchanged", func(t *testing.T) {
		oldCfg, oldDir := configFile, dataDir
		t.Cleanup(func() { configFile, dataDir = oldCfg, oldDir })
		root := &cobra.Command{Use: "root", SilenceUsage: true, SilenceErrors: true, RunE: func(*cobra.Command, []string) error { return nil }}
		registerRootPathFlags(root)
		root.SetArgs([]string{"-c", "/x/cfg.json", "-d", "/x/data"})
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		if configFile != "/x/cfg.json" || dataDir != "/x/data" {
			t.Errorf("got config=%q data=%q", configFile, dataDir)
		}
		for _, n := range []string{"config", "data-dir"} {
			if typ := root.PersistentFlags().Lookup(n).Value.Type(); typ != "string" {
				t.Errorf("%s Type() = %q, want string", n, typ)
			}
		}
	})

	t.Run("every command-local config flag rejects empty", func(t *testing.T) {
		cmds := map[string]*cobra.Command{
			"upstream list":         upstreamListCmd,
			"upstream logs":         upstreamLogsCmd,
			"doctor":                doctorCmd,
			"doctor fix":            doctorFixCmd,
			"auth login":            authLoginCmd,
			"auth status":           authStatusCmd,
			"auth logout":           authLogoutCmd,
			"call tool":             callToolCmd,
			"call tool-read":        callToolReadCmd,
			"call tool-write":       callToolWriteCmd,
			"call tool-destructive": callToolDestructiveCmd,
			"code exec":             codeExecCmd,
			"code scripts list":     codeScriptsListCmd,
			"tools list":            toolsListCmd,
			"token (persistent)":    GetTokenCommand(),
			"catalog (persistent)":  GetCatalogCommand(),
			"registry (persistent)": GetRegistryCommand(),
		}
		for name, c := range cmds {
			t.Run(name, func(t *testing.T) {
				var f *pflag.Flag
				if f = c.Flags().Lookup("config"); f == nil {
					f = c.PersistentFlags().Lookup("config")
				}
				if f == nil {
					t.Fatalf("%s has no config flag", name)
				}
				if err := f.Value.Set(""); err == nil {
					t.Errorf("%s: empty --config must be rejected", name)
				}
			})
		}
	})
}
