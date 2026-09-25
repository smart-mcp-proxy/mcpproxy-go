package main

import (
	"path/filepath"
	"strings"
	"testing"

	clioutput "github.com/smart-mcp-proxy/mcpproxy-go/internal/cli/output"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/connect"
)

// TestPrintConnectResult_ShowsDisplayPathAndReloadHint is T032's connect
// golden: a table-format connect result prints "Config: ~/…" (never the raw
// full path) and "Next: <reload_hint>" on success.
func TestPrintConnectResult_ShowsDisplayPathAndReloadHint(t *testing.T) {
	home := t.TempDir()

	result := &connect.ConnectResult{
		Success:     true,
		Client:      "cursor",
		ConfigPath:  filepath.Join(home, ".cursor", "mcp.json"),
		DisplayPath: connect.DisplayPath(filepath.Join(home, ".cursor", "mcp.json"), home),
		ServerName:  "mcpproxy",
		Action:      "created",
		Message:     "Successfully connected mcpproxy to cursor",
		ReloadHint:  "Reload the Cursor window (or restart Cursor) to load MCPProxy",
	}

	formatter, err := clioutput.NewFormatter("table")
	if err != nil {
		t.Fatalf("NewFormatter: %v", err)
	}

	out := captureStdout(t, func() {
		if err := printConnectResult(result, formatter, "table"); err != nil {
			t.Errorf("printConnectResult: %v", err)
		}
	})

	if !strings.Contains(out, "Config: "+filepath.Join("~", ".cursor", "mcp.json")) {
		t.Errorf("output missing shortened Config line, got:\n%s", out)
	}
	if strings.Contains(out, result.ConfigPath) {
		t.Errorf("output must not print the full config path in table mode, got:\n%s", out)
	}
	if !strings.Contains(out, "Next: "+result.ReloadHint) {
		t.Errorf("output missing Next: <reload_hint> line, got:\n%s", out)
	}
}

// TestPrintConnectResult_ShortensBackupPath is review round 4's finding: the
// table-format success block printed the raw, un-shortened BackupPath
// directly above the home-shortened "Config: ~/…" line (connectResultDisplayPath),
// mixing a full path and a "~"-shortened path in the same output block
// (FR-037's compact-display goal is per-block, not just per-line).
func TestPrintConnectResult_ShortensBackupPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	result := &connect.ConnectResult{
		Success:     true,
		Client:      "cursor",
		ConfigPath:  filepath.Join(home, ".cursor", "mcp.json"),
		DisplayPath: connect.DisplayPath(filepath.Join(home, ".cursor", "mcp.json"), home),
		BackupPath:  filepath.Join(home, ".cursor", "mcp.json.bak.20260101"),
		ServerName:  "mcpproxy",
		Action:      "updated",
		Message:     "Successfully connected mcpproxy to cursor",
		ReloadHint:  "Reload the Cursor window (or restart Cursor) to load MCPProxy",
	}

	formatter, err := clioutput.NewFormatter("table")
	if err != nil {
		t.Fatalf("NewFormatter: %v", err)
	}

	out := captureStdout(t, func() {
		if err := printConnectResult(result, formatter, "table"); err != nil {
			t.Errorf("printConnectResult: %v", err)
		}
	})

	if strings.Contains(out, result.BackupPath) {
		t.Errorf("output must not print the full, un-shortened backup path, got:\n%s", out)
	}
	wantBackup := "Backup: " + connect.DisplayPath(result.BackupPath, "")
	if !strings.Contains(out, wantBackup) {
		t.Errorf("output missing shortened Backup line %q, got:\n%s", wantBackup, out)
	}
}

// TestPrintConnectResult_FailureHasNoNextLine asserts a failed result skips
// the Config/Next lines entirely (they describe a write that didn't happen).
func TestPrintConnectResult_FailureHasNoNextLine(t *testing.T) {
	result := &connect.ConnectResult{
		Success: false,
		Client:  "cursor",
		Action:  "already_exists",
		Message: "mcpproxy already registered in cursor (use --force to overwrite)",
	}
	formatter, err := clioutput.NewFormatter("table")
	if err != nil {
		t.Fatalf("NewFormatter: %v", err)
	}

	out := captureStdout(t, func() {
		if err := printConnectResult(result, formatter, "table"); err != nil {
			t.Errorf("printConnectResult: %v", err)
		}
	})

	if !strings.Contains(out, "Failed: "+result.Message) {
		t.Errorf("output missing Failed: line, got:\n%s", out)
	}
	if strings.Contains(out, "Next:") {
		t.Errorf("output must not print a Next: line for a failed result, got:\n%s", out)
	}
}

// TestPrintConnectStatus_ListShowsDisplayPath is T032's --list golden: the
// CONFIG PATH column shows the home-shortened path.
func TestPrintConnectStatus_ListShowsDisplayPath(t *testing.T) {
	home := t.TempDir()
	svc := connect.NewServiceWithHome("127.0.0.1:8080", "", home)

	formatter, err := clioutput.NewFormatter("table")
	if err != nil {
		t.Fatalf("NewFormatter: %v", err)
	}

	out := captureStdout(t, func() {
		if err := printConnectStatus(svc, formatter, "table"); err != nil {
			t.Errorf("printConnectStatus: %v", err)
		}
	})

	if !strings.Contains(out, filepath.Join("~", ".cursor", "mcp.json")) {
		t.Errorf("CONFIG PATH column should show the shortened path, got:\n%s", out)
	}
	// The fake home is a t.TempDir() path, which is long and distinctive
	// enough that its literal appearance would mean the full path leaked
	// into the table instead of being shortened.
	if strings.Contains(out, home) {
		t.Errorf("CONFIG PATH column must not print the full home-rooted path, got:\n%s", out)
	}
}
