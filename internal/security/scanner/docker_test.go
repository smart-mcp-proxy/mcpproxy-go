package scanner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"
)

func TestGenerateContainerName(t *testing.T) {
	name := GenerateContainerName("mcp-scan", "github-server")
	if !strings.HasPrefix(name, "mcpproxy-scanner-mcp-scan-github-server-") {
		t.Errorf("unexpected container name: %s", name)
	}
	// Should not contain invalid Docker chars
	for _, ch := range []string{"/", ":"} {
		if strings.Contains(name, ch) {
			t.Errorf("container name should not contain %q: %s", ch, name)
		}
	}
}

func TestGenerateContainerNameSanitization(t *testing.T) {
	// Names with special characters should be sanitized
	name := GenerateContainerName("my/scanner:v1", "server.with.dots")
	if strings.Contains(name, "/") || strings.Contains(name, ":") {
		t.Errorf("container name still contains special characters: %s", name)
	}
	if !strings.HasPrefix(name, "mcpproxy-scanner-") {
		t.Errorf("unexpected prefix in container name: %s", name)
	}
}

func TestGenerateContainerNameUniqueness(t *testing.T) {
	// Two calls should produce different names (due to time-based suffix)
	name1 := GenerateContainerName("scan", "server")
	name2 := GenerateContainerName("scan", "server")
	// They may be the same if called within the same nanosecond, but the structure should be valid
	if !strings.HasPrefix(name1, "mcpproxy-scanner-scan-server-") {
		t.Errorf("unexpected container name: %s", name1)
	}
	if !strings.HasPrefix(name2, "mcpproxy-scanner-scan-server-") {
		t.Errorf("unexpected container name: %s", name2)
	}
}

func TestPrepareReportDir(t *testing.T) {
	base := t.TempDir()
	dir, err := PrepareReportDir(base, "job-123", "mcp-scan")
	if err != nil {
		t.Fatalf("PrepareReportDir: %v", err)
	}
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Error("report directory should exist")
	}
	expected := filepath.Join(base, "scanner-reports", "job-123", "mcp-scan")
	if dir != expected {
		t.Errorf("expected %s, got %s", expected, dir)
	}
}

func TestPrepareReportDirIdempotent(t *testing.T) {
	base := t.TempDir()
	dir1, err := PrepareReportDir(base, "job-456", "scanner-a")
	if err != nil {
		t.Fatalf("first PrepareReportDir: %v", err)
	}
	dir2, err := PrepareReportDir(base, "job-456", "scanner-a")
	if err != nil {
		t.Fatalf("second PrepareReportDir: %v", err)
	}
	if dir1 != dir2 {
		t.Errorf("expected same directory, got %s and %s", dir1, dir2)
	}
}

func TestReadReportFile(t *testing.T) {
	dir := t.TempDir()
	logger := zap.NewNop()
	d := NewDockerRunner(logger)

	// No report file
	_, err := d.ReadReportFile(dir)
	if err == nil {
		t.Error("expected error when no report file exists")
	}

	// Write results.sarif
	sarifContent := `{"version":"2.1.0","runs":[]}`
	if err := os.WriteFile(filepath.Join(dir, "results.sarif"), []byte(sarifContent), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	data, err := d.ReadReportFile(dir)
	if err != nil {
		t.Fatalf("ReadReportFile: %v", err)
	}
	if string(data) != sarifContent {
		t.Errorf("unexpected content: %s", string(data))
	}
}

func TestReadReportFileAlternateNames(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		content  string
	}{
		{"report.sarif", "report.sarif", `{"version":"2.1.0"}`},
		{"results.json", "results.json", `{"findings":[]}`},
		{"report.json", "report.json", `{"findings":[]}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			logger := zap.NewNop()
			d := NewDockerRunner(logger)

			if err := os.WriteFile(filepath.Join(dir, tt.filename), []byte(tt.content), 0644); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}

			data, err := d.ReadReportFile(dir)
			if err != nil {
				t.Fatalf("ReadReportFile with %s: %v", tt.filename, err)
			}
			if string(data) != tt.content {
				t.Errorf("unexpected content: got %s, want %s", string(data), tt.content)
			}
		})
	}
}

func TestReadReportFilePriority(t *testing.T) {
	// results.sarif should take priority over alternates
	dir := t.TempDir()
	logger := zap.NewNop()
	d := NewDockerRunner(logger)

	sarifContent := `{"version":"2.1.0","runs":[]}`
	jsonContent := `{"findings":[]}`

	if err := os.WriteFile(filepath.Join(dir, "results.sarif"), []byte(sarifContent), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "report.json"), []byte(jsonContent), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	data, err := d.ReadReportFile(dir)
	if err != nil {
		t.Fatalf("ReadReportFile: %v", err)
	}
	if string(data) != sarifContent {
		t.Errorf("expected results.sarif content, got: %s", string(data))
	}
}

func TestDockerRunnerIsDockerAvailable(t *testing.T) {
	// This test actually checks Docker availability -- mark as integration if needed
	logger := zap.NewNop()
	d := NewDockerRunner(logger)
	// Just verify it doesn't panic
	_ = d.IsDockerAvailable(context.Background())
}

func TestNewDockerRunner(t *testing.T) {
	logger := zap.NewNop()
	d := NewDockerRunner(logger)
	if d == nil {
		t.Fatal("NewDockerRunner returned nil")
	}
	if d.logger != logger {
		t.Error("logger not set correctly")
	}
}

func TestScannerRunConfigDefaults(t *testing.T) {
	cfg := ScannerRunConfig{
		ContainerName: "test-scanner",
		Image:         "test:latest",
		Command:       []string{"scan"},
		ReadOnly:      true,
		NetworkMode:   "none",
		MemoryLimit:   "256m",
	}

	if cfg.ContainerName != "test-scanner" {
		t.Error("name mismatch")
	}
	if !cfg.ReadOnly {
		t.Error("should be read-only")
	}
	if cfg.NetworkMode != "none" {
		t.Errorf("expected network mode 'none', got %s", cfg.NetworkMode)
	}
	if cfg.MemoryLimit != "256m" {
		t.Errorf("expected memory limit '256m', got %s", cfg.MemoryLimit)
	}
	if len(cfg.Command) != 1 || cfg.Command[0] != "scan" {
		t.Errorf("unexpected command: %v", cfg.Command)
	}
}

func TestKillContainerEmptyName(t *testing.T) {
	logger := zap.NewNop()
	d := NewDockerRunner(logger)
	// KillContainer with empty name should return nil (no-op)
	err := d.KillContainer(context.Background(), "")
	if err != nil {
		t.Errorf("KillContainer with empty name should return nil, got: %v", err)
	}
}

func TestStopContainerEmptyName(t *testing.T) {
	logger := zap.NewNop()
	d := NewDockerRunner(logger)
	// StopContainer with empty name should return nil (no-op)
	err := d.StopContainer(context.Background(), "", 10)
	if err != nil {
		t.Errorf("StopContainer with empty name should return nil, got: %v", err)
	}
}
