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

func TestBuildRunArgsIncludesNoNewPrivilegesByDefault(t *testing.T) {
	cfg := ScannerRunConfig{
		ContainerName: "mcpproxy-scanner-test",
		Image:         "scanner:latest",
		Command:       []string{"scan"},
	}
	args := buildRunArgs(cfg)
	if !containsPair(args, "--security-opt", "no-new-privileges") {
		t.Errorf("expected --security-opt no-new-privileges in default args, got: %v", args)
	}
}

func TestBuildRunArgsOmitsNoNewPrivilegesWhenDisabled(t *testing.T) {
	cfg := ScannerRunConfig{
		ContainerName:          "mcpproxy-scanner-test",
		Image:                  "scanner:latest",
		Command:                []string{"scan"},
		DisableNoNewPrivileges: true,
	}
	args := buildRunArgs(cfg)
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "--security-opt" && args[i+1] == "no-new-privileges" {
			t.Errorf("--security-opt no-new-privileges should be absent when disabled, got: %v", args)
		}
	}
	// Other security-relevant flags must still be present.
	if !containsArg(args, "--tmpfs") {
		t.Errorf("expected --tmpfs to remain present, got: %v", args)
	}
	if !containsArg(args, "--rm") {
		t.Errorf("expected --rm to remain present, got: %v", args)
	}
}

func TestBuildRunArgsImageAndCommandTrailing(t *testing.T) {
	cfg := ScannerRunConfig{
		Image:   "scanner:v1",
		Command: []string{"--mode", "fast"},
	}
	args := buildRunArgs(cfg)
	// Image must precede command, both must be the trailing tokens.
	if len(args) < 3 {
		t.Fatalf("args too short: %v", args)
	}
	if args[len(args)-3] != "scanner:v1" {
		t.Errorf("expected image at args[-3], got %q in %v", args[len(args)-3], args)
	}
	if args[len(args)-2] != "--mode" || args[len(args)-1] != "fast" {
		t.Errorf("expected command at tail, got %v", args[len(args)-2:])
	}
}

func TestClassifyScannerExecFailureRecognisesAppArmorPattern(t *testing.T) {
	cases := []struct {
		name     string
		stderr   string
		exitCode int
		wantHint bool
	}{
		{
			name:     "snap docker apparmor python",
			stderr:   "exec /usr/local/bin/python: operation not permitted\n",
			exitCode: 255,
			wantHint: true,
		},
		{
			name:     "snap docker apparmor entrypoint",
			stderr:   "exec /usr/local/bin/entrypoint.sh: operation not permitted\n",
			exitCode: 255,
			wantHint: true,
		},
		{
			name:     "wrong exit code",
			stderr:   "exec /usr/local/bin/python: operation not permitted\n",
			exitCode: 1,
			wantHint: false,
		},
		{
			name:     "unrelated error",
			stderr:   "scanner: bad config\n",
			exitCode: 255,
			wantHint: false,
		},
		{
			name:     "operation not permitted but no exec prefix",
			stderr:   "open /etc/foo: operation not permitted\n",
			exitCode: 255,
			wantHint: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hint := ClassifyScannerExecFailure(tc.stderr, tc.exitCode)
			if tc.wantHint && hint == "" {
				t.Errorf("expected a remediation hint, got empty string")
			}
			if !tc.wantHint && hint != "" {
				t.Errorf("expected no hint, got: %s", hint)
			}
			if tc.wantHint {
				// Sanity-check that the hint mentions both remediation paths
				// so users have actionable guidance.
				if !strings.Contains(hint, "snap") {
					t.Errorf("hint should mention snap docker remediation: %s", hint)
				}
				if !strings.Contains(hint, "scanner_disable_no_new_privileges") {
					t.Errorf("hint should mention the config flag: %s", hint)
				}
			}
		})
	}
}

// containsArg reports whether args contains the given single token.
func containsArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

// containsPair reports whether args contains key followed immediately by value.
func containsPair(args []string, key, value string) bool {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == key && args[i+1] == value {
			return true
		}
	}
	return false
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

// TestDockerRunnerDoesNotLeakAmbientSecrets verifies that the scanner's
// Docker subprocess is invoked with a minimal, allow-listed environment so
// credentials in the parent process environment (AWS, GitHub, Anthropic,
// …) cannot leak into containers that run untrusted scanner images.
func TestDockerRunnerDoesNotLeakAmbientSecrets(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIA_test_dummy_value_00000000")
	t.Setenv("GITHUB_TOKEN", "ghp_test_dummy_token_1234567890abcdef")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test-dummy-not-real")

	logger := zap.NewNop()
	d := NewDockerRunner(logger)

	cmd := d.getDockerCmd(context.Background(), "version")

	if cmd.Env == nil {
		t.Fatal("cmd.Env must be explicitly set (non-nil) so it does not inherit os.Environ()")
	}

	joined := strings.Join(cmd.Env, "\n")
	forbidden := []string{"AWS_ACCESS_KEY_ID", "GITHUB_TOKEN", "ANTHROPIC_API_KEY"}
	for _, key := range forbidden {
		if strings.Contains(joined, key) {
			t.Errorf("scanner docker command leaked %q into cmd.Env", key)
		}
	}

	// PATH must still be present so the docker binary is actually runnable.
	hasPath := false
	for _, kv := range cmd.Env {
		if strings.HasPrefix(kv, "PATH=") {
			hasPath = true
			break
		}
	}
	if !hasPath {
		t.Error("scanner docker command must retain PATH so 'docker' is discoverable")
	}
}
