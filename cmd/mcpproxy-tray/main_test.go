//go:build darwin

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/logs"
)

func TestShellQuote(t *testing.T) {
	tcases := map[string]string{
		"":         "''",
		"simple":   "'simple'",
		"with spa": "'with spa'",
		"a'b":      "'a'\\''b'",
	}

	for input, expected := range tcases {
		if got := shellQuote(input); got != expected {
			t.Fatalf("shellQuote(%q) = %q, expected %q", input, got, expected)
		}
	}
}

func TestBuildShellExecCommand(t *testing.T) {
	cmd := buildShellExecCommand("/usr/local/bin/mcpproxy", []string{"serve", "--listen", "127.0.0.1:8080"})
	expected := "exec '/usr/local/bin/mcpproxy' 'serve' '--listen' '127.0.0.1:8080'"
	if cmd != expected {
		t.Fatalf("buildShellExecCommand produced %q, expected %q", cmd, expected)
	}
}

func TestNewTrayLogConfig_DarwinUsesConsoleAndRotationDefaults(t *testing.T) {
	cfg := newTrayLogConfig(platformDarwin, "/tmp/tray-logs")

	if cfg.Level != logs.LogLevelInfo {
		t.Fatalf("Level = %q, expected %q", cfg.Level, logs.LogLevelInfo)
	}
	if !cfg.EnableFile {
		t.Fatal("EnableFile = false, expected true")
	}
	if !cfg.EnableConsole {
		t.Fatal("EnableConsole = false, expected true on darwin")
	}
	if cfg.Filename != "tray.log" {
		t.Fatalf("Filename = %q, expected tray.log", cfg.Filename)
	}
	if cfg.LogDir != "/tmp/tray-logs" {
		t.Fatalf("LogDir = %q, expected /tmp/tray-logs", cfg.LogDir)
	}
	if !cfg.JSONFormat {
		t.Fatal("JSONFormat = false, expected true")
	}
	if cfg.MaxSize != 10 || cfg.MaxBackups != 5 || cfg.MaxAge != 30 || !cfg.Compress {
		t.Fatalf("rotation defaults = size:%d backups:%d age:%d compress:%t, expected 10/5/30/true", cfg.MaxSize, cfg.MaxBackups, cfg.MaxAge, cfg.Compress)
	}
}

func TestNewTrayLogConfig_WindowsDisablesConsole(t *testing.T) {
	cfg := newTrayLogConfig(platformWindows, `C:\logs`)

	if cfg.EnableConsole {
		t.Fatal("EnableConsole = true, expected false on windows")
	}
	if !cfg.EnableFile {
		t.Fatal("EnableFile = false, expected true")
	}
	if cfg.Filename != "tray.log" {
		t.Fatalf("Filename = %q, expected tray.log", cfg.Filename)
	}
	if cfg.LogDir != `C:\logs` {
		t.Fatalf("LogDir = %q, expected C:\\logs", cfg.LogDir)
	}
}

func TestSetupLogging_WritesTrayLogToRotatingFile(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	logger, err := setupLogging()
	if err != nil {
		t.Fatalf("setupLogging(): %v", err)
	}
	defer func() {
		_ = logger.Sync()
	}()

	logger.Info("rotation test message")
	_ = logger.Sync()

	logPath := filepath.Join(tempHome, "Library", "Logs", "mcpproxy", "tray.log")
	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", logPath, err)
	}
	if !strings.Contains(string(content), "\"message\":\"rotation test message\"") {
		t.Fatalf("tray.log did not contain expected JSON message: %s", string(content))
	}
}
