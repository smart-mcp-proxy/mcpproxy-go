package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sandboxedEnv = "MCPPROXY_TEST_SANDBOXED"

// TestMain keeps every test in this package away from the developer's own
// mcpproxy: CLI commands look for a running daemon through the default data
// dir (~/.mcpproxy socket, or the mcpproxy-<USERNAME> named pipe on Windows),
// the listen address, API key and data dir from MCPPROXY_* variables, and
// client configs under the home/app-data dirs. Tests that need any of these
// set them with t.Setenv.
func TestMain(m *testing.M) {
	// Tests that re-run this binary as a child pass it an already sandboxed
	// environment plus their own overrides, which must survive.
	// The marker carries the sandbox home, so a stale or inherited marker that
	// does not match this process's HOME does not switch the sandbox off.
	if marker := os.Getenv(sandboxedEnv); marker != "" && marker == os.Getenv("HOME") {
		os.Exit(m.Run())
	}
	home, err := os.MkdirTemp("", "mcpproxy-cmd-test-home-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	// Any MCPPROXY_* setting can point a command at a daemon, a data dir or a
	// key, so drop them all; keep the test switches (MCPPROXY_TEST_*, golden updates).
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		up := strings.ToUpper(k)
		// MCPP_CONFIG names a config file for config.Load, like --config.
		if up == "MCPP_CONFIG" {
			_ = os.Unsetenv(k)
			continue
		}
		if strings.HasPrefix(up, "MCPPROXY_") && !strings.HasPrefix(up, "MCPPROXY_TEST_") && up != "MCPPROXY_UPDATE_GOLDEN" {
			_ = os.Unsetenv(k)
		}
	}
	for k, v := range map[string]string{
		"HOME":            home,
		"USERPROFILE":     home,
		"USERNAME":        "mcpproxy-test-sandbox",
		"APPDATA":         filepath.Join(home, "AppData", "Roaming"),
		"LOCALAPPDATA":    filepath.Join(home, "AppData", "Local"),
		"XDG_CONFIG_HOME": filepath.Join(home, ".config"),
		"XDG_DATA_HOME":   filepath.Join(home, ".local", "share"),
		sandboxedEnv:      home,
	} {
		_ = os.Setenv(k, v)
	}
	code := m.Run()
	_ = os.RemoveAll(home)
	os.Exit(code)
}
