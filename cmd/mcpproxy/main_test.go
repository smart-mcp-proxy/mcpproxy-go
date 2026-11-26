package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

const sandboxedEnv = "MCPPROXY_CMD_TEST_SANDBOXED"

// TestMain keeps every test in this package away from the developer's own
// mcpproxy: CLI commands look for a running daemon through the default data
// dir (~/.mcpproxy socket, or the mcpproxy-<USERNAME> named pipe on Windows),
// the configured listen address and API key, and client configs under the
// home/app-data dirs. Tests that need any of these set them with t.Setenv.
func TestMain(m *testing.M) {
	// Tests that re-run this binary as a child pass it an already sandboxed
	// environment plus their own overrides, which must survive.
	if os.Getenv(sandboxedEnv) != "" {
		os.Exit(m.Run())
	}
	home, err := os.MkdirTemp("", "mcpproxy-cmd-test-home-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for k, v := range map[string]string{
		"HOME":            home,
		"USERPROFILE":     home,
		"USERNAME":        "mcpproxy-test-sandbox",
		"APPDATA":         filepath.Join(home, "AppData", "Roaming"),
		"LOCALAPPDATA":    filepath.Join(home, "AppData", "Local"),
		"XDG_CONFIG_HOME": filepath.Join(home, ".config"),
		"XDG_DATA_HOME":   filepath.Join(home, ".local", "share"),
		sandboxedEnv:      "1",
	} {
		_ = os.Setenv(k, v)
	}
	for _, k := range []string{
		"MCPPROXY_TRAY_ENDPOINT", "MCPPROXY_API_KEY", "MCPPROXY_API_KEY_FILE",
		"MCPPROXY_LISTEN", "MCPPROXY_DATA_DIR", "MCPPROXY_DATA",
	} {
		_ = os.Unsetenv(k)
	}
	code := m.Run()
	_ = os.RemoveAll(home)
	os.Exit(code)
}
