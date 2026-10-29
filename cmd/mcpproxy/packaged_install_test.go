package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// fakePackagedInstall points packagedPaths at a temp tree that looks like the
// deb/rpm layout (GH discussion #1678) and restores the real paths afterwards.
// withConfig/withUnit choose which of the two package markers exist.
func fakePackagedInstall(t *testing.T, goos string, withConfig, withUnit bool) packagedInstallPaths {
	t.Helper()
	root := t.TempDir()
	p := packagedInstallPaths{
		GOOS:       goos,
		ConfigPath: filepath.Join(root, "etc", "mcpproxy", "mcp_config.json"),
		DataDir:    filepath.Join(root, "var", "lib", "mcpproxy"),
		UnitPaths:  []string{filepath.Join(root, "lib", "systemd", "system", "mcpproxy.service")},
	}
	if withConfig {
		writeListenConfig(t, p.ConfigPath, "127.0.0.1:18777")
	}
	if withUnit {
		if err := os.MkdirAll(filepath.Dir(p.UnitPaths[0]), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p.UnitPaths[0], []byte("[Service]\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old, oldSystem := packagedPaths, systemMode
	packagedPaths, systemMode = p, false
	t.Cleanup(func() { packagedPaths, systemMode = old, oldSystem })
	return p
}

func TestDetectPackagedInstall(t *testing.T) {
	tests := []struct {
		name             string
		goos             string
		withConfig, unit bool
		want             bool
	}{
		{"linux deb layout", "linux", true, true, true},
		{"linux without unit", "linux", true, false, false},
		{"linux without service config", "linux", false, true, false},
		{"darwin never", "darwin", true, true, false},
		{"windows never", "windows", true, true, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := fakePackagedInstall(t, tc.goos, tc.withConfig, tc.unit)
			if got := p.detected(); got != tc.want {
				t.Errorf("detected() = %v, want %v", got, tc.want)
			}
		})
	}
}

// /etc/mcpproxy is 2770 root:mcpproxy, so a user outside the group cannot even
// stat the config. That still proves the package is installed.
func TestDetectPackagedInstall_UnreadableConfigDirCounts(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs POSIX permissions and a non-root user")
	}
	p := fakePackagedInstall(t, "linux", true, true)
	dir := filepath.Dir(p.ConfigPath)
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	if !p.detected() {
		t.Error("detected() = false for a config dir the user cannot search, want true")
	}
}

// The core of #1678: on a packaged Linux install with no config of the user's
// own, a management command must not create ~/.mcpproxy/mcp_config.json and
// report an empty instance; it must point at the service instead.
func TestLoadersOnPackagedInstallWithoutUserConfigPointAtService(t *testing.T) {
	for _, tc := range managementLoaderCases() {
		t.Run(tc.name, func(t *testing.T) {
			resetLoaderGlobals(t)
			p := fakePackagedInstall(t, "linux", true, true)
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Chdir(t.TempDir())

			_, err := tc.load()
			if tc.name == "registry" {
				// Registry discovery deliberately runs on defaults without a
				// config; it must still not create one.
				if err != nil {
					t.Fatalf("registry load: %v", err)
				}
				if _, statErr := os.Stat(filepath.Join(home, ".mcpproxy", "mcp_config.json")); statErr == nil {
					t.Error("registry loader created ~/.mcpproxy/mcp_config.json on a packaged install")
				}
				return
			}
			var pkgErr *packagedInstallError
			if !errors.As(err, &pkgErr) {
				t.Fatalf("load error = %v, want *packagedInstallError", err)
			}
			msg := err.Error()
			for _, want := range []string{p.ConfigPath, "--system", "mcpproxy.service", "mcpproxy serve"} {
				if !strings.Contains(msg, want) {
					t.Errorf("message lacks %q:\n%s", want, msg)
				}
			}
			if _, statErr := os.Stat(filepath.Join(home, ".mcpproxy", "mcp_config.json")); statErr == nil {
				t.Error("loader created ~/.mcpproxy/mcp_config.json on a packaged install")
			}
		})
	}
}

func TestLoadersOnPackagedInstallUseExistingUserConfig(t *testing.T) {
	resetLoaderGlobals(t)
	fakePackagedInstall(t, "linux", true, true)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Chdir(t.TempDir())
	writeListenConfig(t, filepath.Join(home, ".mcpproxy", "mcp_config.json"), "127.0.0.1:18666")

	cfg, err := loadUpstreamConfig()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Listen != "127.0.0.1:18666" {
		t.Errorf("listen = %q, want the user's own config", cfg.Listen)
	}
}

func TestLoadersOnPackagedInstallHonorMCPPConfigEnv(t *testing.T) {
	resetLoaderGlobals(t)
	fakePackagedInstall(t, "linux", true, true)
	t.Setenv("HOME", t.TempDir())
	t.Chdir(t.TempDir())
	own := filepath.Join(t.TempDir(), "own.json")
	body := `{"mcpServers":[{"name":"from-mcpp-config","url":"http://127.0.0.1:1/mcp","protocol":"http"}]}`
	if err := os.WriteFile(own, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MCPP_CONFIG", own)

	cfg, err := loadUpstreamConfig()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(cfg.Servers) != 1 || cfg.Servers[0].Name != "from-mcpp-config" {
		t.Errorf("servers = %+v, want the MCPP_CONFIG file's", cfg.Servers)
	}
}

func TestLoadersOffLinuxKeepCreatingHomeConfig(t *testing.T) {
	resetLoaderGlobals(t)
	fakePackagedInstall(t, "darwin", true, true)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Chdir(t.TempDir())

	if _, err := loadUpstreamConfig(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".mcpproxy", "mcp_config.json")); err != nil {
		t.Errorf("non-Linux behaviour changed: home config not created: %v", err)
	}
}

func TestSystemHintCommand(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"upstream", "list"}, "sudo mcpproxy --system upstream list"},
		{[]string{"--config", "/x.json", "doctor"}, "sudo mcpproxy --system doctor"},
		{[]string{"-c=/x.json", "-d", "/d", "status", "--json"}, "sudo mcpproxy --system status --json"},
		{[]string{"--data-dir=/d", "tools", "list", "--server", "my server"}, "sudo mcpproxy --system tools list --server 'my server'"},
		{[]string{"--system", "status"}, "sudo mcpproxy --system status"},
		{nil, "sudo mcpproxy --system status"},
	}
	for _, tc := range tests {
		if got := systemHintCommand(tc.args); got != tc.want {
			t.Errorf("systemHintCommand(%q) = %q, want %q", tc.args, got, tc.want)
		}
	}
}

// newSystemFlagCmd builds a minimal root+child with the same persistent flags
// the real root registers, so applySystemFlag sees real Changed() state.
func newSystemFlagCmd(t *testing.T, args ...string) *cobra.Command {
	t.Helper()
	root := &cobra.Command{Use: "mcpproxy"}
	registerRootPathFlags(root)
	var ran *cobra.Command
	child := &cobra.Command{Use: "upstream", RunE: func(cmd *cobra.Command, _ []string) error { ran = cmd; return nil }}
	root.AddCommand(child)
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	return ran
}

func TestApplySystemFlag_SetsServicePaths(t *testing.T) {
	resetLoaderGlobals(t)
	p := fakePackagedInstall(t, "linux", true, true)
	cmd := newSystemFlagCmd(t, "--system", "upstream")

	if err := applySystemFlag(cmd); err != nil {
		t.Fatalf("applySystemFlag: %v", err)
	}
	if configFile != p.ConfigPath || dataDir != p.DataDir {
		t.Errorf("paths = (%q, %q), want (%q, %q)", configFile, dataDir, p.ConfigPath, p.DataDir)
	}
	cfg, err := loadUpstreamConfig()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Listen != "127.0.0.1:18777" || cfg.DataDir != p.DataDir {
		t.Errorf("loaded listen=%q dataDir=%q, want the service's", cfg.Listen, cfg.DataDir)
	}
}

func TestApplySystemFlag_NoOpWithoutFlag(t *testing.T) {
	resetLoaderGlobals(t)
	fakePackagedInstall(t, "linux", true, true)
	cmd := newSystemFlagCmd(t, "upstream")
	if err := applySystemFlag(cmd); err != nil {
		t.Fatalf("applySystemFlag: %v", err)
	}
	if configFile != "" || dataDir != "" {
		t.Errorf("paths changed without --system: (%q, %q)", configFile, dataDir)
	}
}

func TestApplySystemFlag_Refusals(t *testing.T) {
	tests := []struct {
		name    string
		goos    string
		config  bool
		args    []string
		chmod   bool
		wantSub []string
	}{
		{"conflicts with --config", "linux", true, []string{"--system", "--config", "/x.json", "upstream"}, false, []string{"--system", "--config"}},
		{"conflicts with --data-dir", "linux", true, []string{"--system", "-d", "/d", "upstream"}, false, []string{"--system", "--data-dir"}},
		{"not on macOS", "darwin", true, []string{"--system", "upstream"}, false, []string{"Linux"}},
		{"package not installed", "linux", false, []string{"--system", "upstream"}, false, []string{"not found"}},
		{"unreadable config", "linux", true, []string{"--system", "upstream"}, true, []string{"sudo mcpproxy --system upstream", "usermod -aG mcpproxy"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.chmod && (runtime.GOOS == "windows" || os.Geteuid() == 0) {
				t.Skip("needs POSIX permissions and a non-root user")
			}
			resetLoaderGlobals(t)
			p := fakePackagedInstall(t, tc.goos, tc.config, true)
			if tc.chmod {
				if err := os.Chmod(p.ConfigPath, 0o000); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(p.ConfigPath, 0o600) })
			}
			oldArgs := os.Args
			os.Args = append([]string{"mcpproxy"}, tc.args...)
			t.Cleanup(func() { os.Args = oldArgs })

			cmd := newSystemFlagCmd(t, tc.args...)
			err := applySystemFlag(cmd)
			if err == nil {
				t.Fatal("applySystemFlag: want an error")
			}
			for _, s := range tc.wantSub {
				if !strings.Contains(err.Error(), s) {
					t.Errorf("error lacks %q:\n%v", s, err)
				}
			}
		})
	}
}

func TestApplySystemFlag_RefusesServe(t *testing.T) {
	resetLoaderGlobals(t)
	fakePackagedInstall(t, "linux", true, true)
	root := &cobra.Command{Use: "mcpproxy"}
	registerRootPathFlags(root)
	var ran *cobra.Command
	serve := &cobra.Command{Use: "serve", RunE: func(cmd *cobra.Command, _ []string) error { ran = cmd; return nil }}
	root.AddCommand(serve)
	root.SetArgs([]string{"--system", "serve"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	err := applySystemFlag(ran)
	if err == nil || !strings.Contains(err.Error(), "systemctl") {
		t.Fatalf("applySystemFlag(serve) = %v, want a systemctl pointer", err)
	}
}

func TestRootRegistersSystemFlag(t *testing.T) {
	root := newRootCmd()
	if root.PersistentFlags().Lookup("system") == nil {
		t.Fatal("root has no persistent --system flag")
	}
}

func TestDifferentInstanceWarning(t *testing.T) {
	resetLoaderGlobals(t)
	p := fakePackagedInstall(t, "linux", true, true)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Chdir(t.TempDir())
	userCfg := filepath.Join(home, ".mcpproxy", "mcp_config.json")
	writeListenConfig(t, userCfg, "127.0.0.1:18666")

	active := true
	old := systemServiceActive
	systemServiceActive = func() bool { return active }
	t.Cleanup(func() { systemServiceActive = old })

	w := differentInstanceWarning("", []string{"doctor"})
	for _, want := range []string{"different", p.ConfigPath, userCfg, "sudo mcpproxy --system doctor"} {
		if !strings.Contains(w, want) {
			t.Errorf("warning lacks %q:\n%s", want, w)
		}
	}

	active = false
	if w := differentInstanceWarning("", []string{"doctor"}); w != "" {
		t.Errorf("warning with the service inactive: %q", w)
	}
	active = true

	// Looking at the service's own config: nothing to warn about.
	configFile = p.ConfigPath
	if w := differentInstanceWarning("", []string{"doctor"}); w != "" {
		t.Errorf("warning while reading the service config: %q", w)
	}
	configFile = ""

	// Off Linux the probe is never consulted.
	packagedPaths.GOOS = "darwin"
	if w := differentInstanceWarning("", []string{"doctor"}); w != "" {
		t.Errorf("warning off Linux: %q", w)
	}
}

func TestCLIConfigPathInUse(t *testing.T) {
	resetLoaderGlobals(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Chdir(t.TempDir())

	if got, want := cliConfigPathInUse(""), filepath.Join(home, ".mcpproxy", config.ConfigFileName); got != want {
		t.Errorf("default = %q, want %q", got, want)
	}
	dd := t.TempDir()
	dataDir = dd
	if got, want := cliConfigPathInUse(""), config.GetConfigPath(dd); got != want {
		t.Errorf("data-dir = %q, want %q", got, want)
	}
	if got := cliConfigPathInUse("/explicit.json"); got != "/explicit.json" {
		t.Errorf("explicit = %q", got)
	}
}

// The service's socket only accepts its own uid (SO_PEERCRED), so under sudo or
// as a mcpproxy-group member a socket that exists is still unusable. --system
// must verify the socket and fall back to listen + api_key over HTTP.
func TestDaemonEndpoint_SystemModeFallsBackToHTTPWhenSocketRefuses(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix socket test not applicable on Windows")
	}
	clearDaemonEnv(t)
	old := systemMode
	systemMode = true
	t.Cleanup(func() { systemMode = old })

	ts := newStatusServer(t, "svc-key")
	defer ts.Close()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "mcpproxy.sock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	listen := strings.TrimPrefix(ts.URL, "http://")
	cfg := &config.Config{DataDir: dir, Listen: listen, APIKey: "svc-key"}

	endpoint, key, ok := daemonEndpoint(cfg)
	if !ok || endpoint != "http://"+listen || key != "svc-key" {
		t.Fatalf("daemonEndpoint = (%q, %q, %v), want the HTTP listener", endpoint, key, ok)
	}
}
