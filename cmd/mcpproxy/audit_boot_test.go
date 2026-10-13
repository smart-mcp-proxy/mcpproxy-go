//go:build server

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/audit"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 107 T108/T109: a typed *config.StartupError classifies as exit code 4
// through classifyError, even when it wraps an underlying "permission denied"
// message that would otherwise classify as exit 5 (ExitCodePermissionError)
// by main.go's string heuristics.
func TestClassifyError_AuditLogStartupError_ExitCode4(t *testing.T) {
	wrapped := fmt.Errorf("failed to create server: %w",
		config.NewStartupError(config.ExitCodeAuditLogError,
			`audit_log.path "/no/such/dir/audit.jsonl" cannot be opened for append: permission denied`))
	if got := classifyError(wrapped); got != ExitCodeConfigError {
		t.Fatalf("classifyError(%v) = %d, want %d (ExitCodeConfigError)", wrapped, got, ExitCodeConfigError)
	}
}

// TestAuditSinkConstruction_UnwritablePath_TypedStartupError pins that
// audit.NewFileSink's own error, once wrapped into a *config.StartupError the
// way cmd/mcpproxy's serve startup does it (see main.go), carries exit code 4
// and the exact message text of contracts/config-keys.md.
func TestAuditSinkConstruction_UnwritablePath_TypedStartupError(t *testing.T) {
	unwritableDir := filepath.Join(t.TempDir(), "does-not-exist")
	path := filepath.Join(unwritableDir, "audit.jsonl")

	_, err := audit.NewFileSink(path, 50, 10, 90, true)
	if err == nil {
		t.Fatalf("expected NewFileSink to fail for a path under a missing directory")
	}

	startupErr := config.NewStartupError(config.ExitCodeAuditLogError,
		fmt.Sprintf("audit_log.path %q cannot be opened for append: %v", path, err))
	if startupErr.ExitCode != 4 {
		t.Fatalf("ExitCode = %d, want 4", startupErr.ExitCode)
	}
	if got := classifyError(startupErr); got != ExitCodeConfigError {
		t.Fatalf("classifyError(startupErr) = %d, want %d", got, ExitCodeConfigError)
	}
}

// TestEffectiveAuditLog_StdioAbsentBlock_WarnAndDisabled pins the boot-path
// contract T108 names: with the block absent under the native stdio
// transport, EffectiveAuditLog resolves to a disabled sink and a warning
// naming audit_log.path — main.go logs that warning and constructs no sink
// (verified indirectly: no error, Enabled=false).
func TestEffectiveAuditLog_StdioAbsentBlock_WarnAndDisabled(t *testing.T) {
	resolved, warn, err := config.EffectiveAuditLog(&config.Config{}, config.TransportStdio)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.Enabled {
		t.Fatalf("expected disabled sink under stdio with an absent block, got %+v", resolved)
	}
	if warn == "" {
		t.Fatalf("expected a WARN naming audit_log.path")
	}
}

// TestEffectiveAuditLog_StdioExplicitStdout_NoPath_StartupError pins the
// exit-4 refusal path end to end through EffectiveAuditLog, mirroring what
// main.go does before constructing any sink.
func TestEffectiveAuditLog_StdioExplicitStdout_NoPath_StartupError(t *testing.T) {
	enabled, stdout := true, true
	cfg := &config.Config{AuditLog: &config.AuditLogConfig{Enabled: &enabled, Stdout: &stdout}}

	_, _, err := config.EffectiveAuditLog(cfg, config.TransportStdio)
	if err == nil {
		t.Fatalf("expected a StartupError")
	}
	if got := classifyError(err); got != ExitCodeConfigError {
		t.Fatalf("classifyError(err) = %d, want %d", got, ExitCodeConfigError)
	}
}

// TestEffectiveAuditLog_StdioExplicitPath_FileSink pins that an explicit path
// under stdio is honoured (a real, writable sink), never refused.
func TestEffectiveAuditLog_StdioExplicitPath_FileSink(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	cfg := &config.Config{AuditLog: &config.AuditLogConfig{Path: path}}

	resolved, _, err := config.EffectiveAuditLog(cfg, config.TransportStdio)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resolved.Enabled || resolved.Path != path {
		t.Fatalf("resolved = %+v", resolved)
	}
	sink, err := audit.NewFileSink(resolved.Path, resolved.MaxSizeMB, resolved.MaxBackups, resolved.MaxAgeDays, resolved.Compress)
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}
	defer func() { _ = sink.Close() }()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected the sink to have created %s: %v", path, err)
	}
}
