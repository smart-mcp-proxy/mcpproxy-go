package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mcpproxy-go/internal/socket"
)

func TestOutputDiagnostics_JSONFormat(t *testing.T) {
	diag := map[string]interface{}{
		"total_issues": 2,
		"upstream_errors": []interface{}{
			map[string]interface{}{
				"server":  "github-server",
				"message": "connection timeout",
			},
		},
		"oauth_required": []interface{}{"sentry-server"},
	}

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = oldStdout }()

	doctorOutput = "json"
	err := outputDiagnostics(diag, nil)

	w.Close()
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if err != nil {
		t.Errorf("outputDiagnostics() returned error: %v", err)
	}

	// Verify valid JSON
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(output), &parsed); err != nil {
		t.Errorf("JSON output is invalid: %v", err)
	}

	// Verify data preserved (now nested under "diagnostics")
	diagData, ok := parsed["diagnostics"].(map[string]interface{})
	if !ok {
		t.Errorf("Expected diagnostics object in JSON output")
	}
	if getIntField(diagData, "total_issues") != 2 {
		t.Errorf("Expected total_issues=2, got %v", diagData["total_issues"])
	}
}

func TestOutputDiagnostics_PrettyFormat_NoIssues(t *testing.T) {
	diag := map[string]interface{}{
		"total_issues": 0,
	}

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = oldStdout }()

	doctorOutput = "pretty"
	err := outputDiagnostics(diag, nil)

	w.Close()
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if err != nil {
		t.Errorf("outputDiagnostics() returned error: %v", err)
	}

	// Verify success message
	if !strings.Contains(output, "All systems operational") {
		t.Error("Expected success message for zero issues")
	}
	if !strings.Contains(output, "No issues detected") {
		t.Error("Expected 'No issues detected' message")
	}
}

func TestOutputDiagnostics_PrettyFormat_WithUpstreamErrors(t *testing.T) {
	diag := map[string]interface{}{
		"total_issues": 2,
		"upstream_errors": []interface{}{
			map[string]interface{}{
				"server_name":   "github-server",
				"error_message": "connection timeout",
			},
			map[string]interface{}{
				"server_name":   "weather-api",
				"error_message": "authentication failed",
			},
		},
	}

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = oldStdout }()

	doctorOutput = "pretty"
	err := outputDiagnostics(diag, nil)

	w.Close()
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if err != nil {
		t.Errorf("outputDiagnostics() returned error: %v", err)
	}

	// Verify upstream errors section
	if !strings.Contains(output, "Upstream Server Connection Errors") {
		t.Error("Missing upstream errors section header")
	}
	if !strings.Contains(output, "github-server") {
		t.Error("Missing server name: github-server")
	}
	if !strings.Contains(output, "connection timeout") {
		t.Error("Missing error message")
	}
	if !strings.Contains(output, "weather-api") {
		t.Error("Missing server name: weather-api")
	}

	// Verify remediation section
	if !strings.Contains(output, "Remediation") {
		t.Error("Missing remediation section")
	}
	if !strings.Contains(output, "mcpproxy upstream logs") {
		t.Error("Missing command suggestion")
	}
}

func TestOutputDiagnostics_PrettyFormat_WithOAuthRequired(t *testing.T) {
	diag := map[string]interface{}{
		"total_issues": 2,
		"oauth_required": []interface{}{
			map[string]interface{}{
				"server_name": "sentry-server",
				"message":     "Authentication required",
			},
			map[string]interface{}{
				"server_name": "github-server",
				"message":     "",
			},
		},
	}

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = oldStdout }()

	doctorOutput = "pretty"
	err := outputDiagnostics(diag, nil)

	w.Close()
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if err != nil {
		t.Errorf("outputDiagnostics() returned error: %v", err)
	}

	// Verify OAuth section
	if !strings.Contains(output, "OAuth Authentication Required") {
		t.Error("Missing OAuth section header")
	}
	if !strings.Contains(output, "sentry-server") {
		t.Error("Missing OAuth server: sentry-server")
	}
	if !strings.Contains(output, "github-server") {
		t.Error("Missing OAuth server: github-server")
	}

	// Verify remediation
	if !strings.Contains(output, "mcpproxy auth login") {
		t.Error("Missing auth command suggestion")
	}
}

func TestOutputDiagnostics_PrettyFormat_WithMissingSecrets(t *testing.T) {
	diag := map[string]interface{}{
		"total_issues": 1,
		"missing_secrets": []interface{}{
			map[string]interface{}{
				"secret_name": "API_KEY",
				"used_by":     []interface{}{"weather-api"},
			},
		},
	}

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = oldStdout }()

	doctorOutput = "pretty"
	err := outputDiagnostics(diag, nil)

	w.Close()
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if err != nil {
		t.Errorf("outputDiagnostics() returned error: %v", err)
	}

	// Verify missing secrets section
	if !strings.Contains(output, "Missing Secrets") {
		t.Error("Missing secrets section header")
	}
	if !strings.Contains(output, "API_KEY") {
		t.Error("Missing secret name")
	}
	if !strings.Contains(output, "weather-api") {
		t.Error("Missing server name")
	}
}

func TestOutputDiagnostics_PrettyFormat_WithRuntimeWarnings(t *testing.T) {
	diag := map[string]interface{}{
		"total_issues": 1,
		"runtime_warnings": []interface{}{
			map[string]interface{}{
				"title":    "Docker not available",
				"message":  "Isolation features disabled",
				"severity": "warning",
			},
		},
	}

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = oldStdout }()

	doctorOutput = "pretty"
	err := outputDiagnostics(diag, nil)

	w.Close()
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if err != nil {
		t.Errorf("outputDiagnostics() returned error: %v", err)
	}

	// Verify runtime warnings section
	if !strings.Contains(output, "Runtime Warnings") {
		t.Error("Missing runtime warnings section header")
	}
	if !strings.Contains(output, "Docker not available") {
		t.Error("Missing warning title")
	}
	if !strings.Contains(output, "Isolation features disabled") {
		t.Error("Missing warning message")
	}
}

func TestOutputDiagnostics_PrettyFormat_MultipleIssueTypes(t *testing.T) {
	diag := map[string]interface{}{
		"total_issues": 5,
		"upstream_errors": []interface{}{
			map[string]interface{}{
				"server":  "server1",
				"message": "error1",
			},
		},
		"oauth_required": []interface{}{"server2"},
		"missing_secrets": []interface{}{
			map[string]interface{}{
				"name":   "SECRET1",
				"server": "server3",
			},
		},
		"runtime_warnings": []interface{}{
			map[string]interface{}{
				"message":  "warning1",
				"severity": "warning",
			},
		},
	}

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = oldStdout }()

	doctorOutput = "pretty"
	err := outputDiagnostics(diag, nil)

	w.Close()
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if err != nil {
		t.Errorf("outputDiagnostics() returned error: %v", err)
	}

	// Verify all sections present
	if !strings.Contains(output, "Upstream Server Connection Errors") {
		t.Error("Missing upstream errors section")
	}
	if !strings.Contains(output, "OAuth Authentication Required") {
		t.Error("Missing OAuth section")
	}
	if !strings.Contains(output, "Missing Secrets") {
		t.Error("Missing secrets section")
	}
	if !strings.Contains(output, "Runtime Warnings") {
		t.Error("Missing warnings section")
	}

	// Verify issue count
	if !strings.Contains(output, "5") {
		t.Error("Missing total issue count")
	}
	if !strings.Contains(output, "issues") {
		t.Error("Should use plural 'issues' for count > 1")
	}
}

func TestOutputDiagnostics_PrettyFormat_SingleIssue(t *testing.T) {
	diag := map[string]interface{}{
		"total_issues":   1,
		"oauth_required": []interface{}{"test-server"},
	}

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = oldStdout }()

	doctorOutput = "pretty"
	err := outputDiagnostics(diag, nil)

	w.Close()
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if err != nil {
		t.Errorf("outputDiagnostics() returned error: %v", err)
	}

	// Should use singular "issue" not "issues"
	if !strings.Contains(output, "1 issue") {
		t.Error("Should use singular 'issue' for count = 1")
	}
}

func TestOutputDiagnostics_EmptyFormat(t *testing.T) {
	diag := map[string]interface{}{
		"total_issues": 0,
	}

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = oldStdout }()

	// Empty string should default to pretty format
	doctorOutput = ""
	err := outputDiagnostics(diag, nil)

	w.Close()
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if err != nil {
		t.Errorf("outputDiagnostics() returned error: %v", err)
	}

	// Should show pretty format
	if !strings.Contains(output, "MCPProxy Health Check") {
		t.Error("Empty format should default to pretty format")
	}
}

func TestShouldUseDoctorDaemon(t *testing.T) {
	// Test with non-existent directory
	result := shouldUseDoctorDaemon("/tmp/nonexistent-mcpproxy-test-dir-67890")
	if result {
		t.Error("shouldUseDoctorDaemon should return false for non-existent directory")
	}

	// Test with existing directory but no socket
	tmpDir := t.TempDir()
	result = shouldUseDoctorDaemon(tmpDir)
	if result {
		t.Error("shouldUseDoctorDaemon should return false when socket doesn't exist")
	}
}

func TestLoadDoctorConfig(t *testing.T) {
	// Save original flag value
	oldConfigPath := doctorConfigPath
	defer func() { doctorConfigPath = oldConfigPath }()

	t.Run("default config path", func(t *testing.T) {
		doctorConfigPath = ""
		// This will attempt to load default config
		_, err := loadDoctorConfig()
		// Error is expected if no config exists
		_ = err
	})

	t.Run("custom config path", func(t *testing.T) {
		// Create a temporary config file
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "doctor_test_config.json")

		// Write minimal valid config
		configJSON := `{
			"listen": "127.0.0.1:8080",
			"data_dir": "~/.mcpproxy",
			"mcpServers": []
		}`
		err := os.WriteFile(configPath, []byte(configJSON), 0644)
		if err != nil {
			t.Fatalf("Failed to write test config: %v", err)
		}

		doctorConfigPath = configPath
		cfg, err := loadDoctorConfig()
		if err != nil {
			t.Errorf("Failed to load custom config: %v", err)
		}
		if cfg != nil && cfg.Listen != "127.0.0.1:8080" {
			t.Errorf("Expected listen address '127.0.0.1:8080', got %s", cfg.Listen)
		}
	})
}

func TestCreateDoctorLogger(t *testing.T) {
	tests := []struct {
		name     string
		logLevel string
		wantErr  bool
	}{
		{
			name:     "trace level",
			logLevel: "trace",
			wantErr:  false,
		},
		{
			name:     "debug level",
			logLevel: "debug",
			wantErr:  false,
		},
		{
			name:     "info level",
			logLevel: "info",
			wantErr:  false,
		},
		{
			name:     "warn level",
			logLevel: "warn",
			wantErr:  false,
		},
		{
			name:     "error level",
			logLevel: "error",
			wantErr:  false,
		},
		{
			name:     "invalid level defaults to warn",
			logLevel: "invalid",
			wantErr:  false,
		},
		{
			name:     "empty level defaults to warn",
			logLevel: "",
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger, err := createDoctorLogger(tt.logLevel)
			if (err != nil) != tt.wantErr {
				t.Errorf("createDoctorLogger() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if logger == nil && !tt.wantErr {
				t.Error("createDoctorLogger() returned nil logger")
			}
		})
	}
}

func TestDoctorSocketDetection(t *testing.T) {
	// Create temp directory
	tmpDir := t.TempDir()

	// Test socket path detection
	socketPath := socket.DetectSocketPath(tmpDir)

	// Should return a path
	if socketPath == "" {
		t.Error("DetectSocketPath should return non-empty path")
	}

	// Socket should not exist yet
	if socket.IsSocketAvailable(socketPath) {
		t.Error("Socket should not be available in empty temp dir")
	}
}

func TestOutputDiagnostics_WarningWithoutTitle(t *testing.T) {
	diag := map[string]interface{}{
		"total_issues": 1,
		"runtime_warnings": []interface{}{
			map[string]interface{}{
				"message":  "Something went wrong",
				"severity": "warning",
			},
		},
	}

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = oldStdout }()

	doctorOutput = "pretty"
	err := outputDiagnostics(diag, nil)

	w.Close()
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if err != nil {
		t.Errorf("outputDiagnostics() returned error: %v", err)
	}

	// Should display message even without title
	if !strings.Contains(output, "Something went wrong") {
		t.Error("Should display warning message even without title")
	}
}

func TestOutputDiagnostics_HighSeverityWarning(t *testing.T) {
	diag := map[string]interface{}{
		"total_issues": 1,
		"runtime_warnings": []interface{}{
			map[string]interface{}{
				"message":  "Critical issue",
				"severity": "critical",
			},
		},
	}

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = oldStdout }()

	doctorOutput = "pretty"
	err := outputDiagnostics(diag, nil)

	w.Close()
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if err != nil {
		t.Errorf("outputDiagnostics() returned error: %v", err)
	}

	// Should show severity for non-warning levels
	if !strings.Contains(output, "Severity") {
		t.Error("Should display severity for non-warning levels")
	}
	if !strings.Contains(output, "critical") {
		t.Error("Should display critical severity")
	}
}

func TestOutputDiagnostics_SecretWithoutOptionalFields(t *testing.T) {
	diag := map[string]interface{}{
		"total_issues": 1,
		"missing_secrets": []interface{}{
			map[string]interface{}{
				"secret_name": "API_KEY",
				// used_by is optional
			},
		},
	}

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = oldStdout }()

	doctorOutput = "pretty"
	err := outputDiagnostics(diag, nil)

	w.Close()
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if err != nil {
		t.Errorf("outputDiagnostics() returned error: %v", err)
	}

	// Should still display the secret name
	if !strings.Contains(output, "API_KEY") {
		t.Error("Should display secret name even without optional fields")
	}
}

// TestOutputDiagnostics_MissingSecretsRealJSON tests that the doctor command
// correctly parses the actual JSON field names produced by the backend.
// The MissingSecretInfo struct uses json:"secret_name" and json:"used_by",
// NOT "name", "server", "reference".
func TestOutputDiagnostics_MissingSecretsRealJSON(t *testing.T) {
	// This is the ACTUAL JSON structure produced by the backend
	// (see internal/contracts/types.go MissingSecretInfo struct)
	diag := map[string]interface{}{
		"total_issues": 1,
		"missing_secrets": []interface{}{
			map[string]interface{}{
				"secret_name": "GITHUB_TOKEN",              // NOT "name"
				"used_by":     []interface{}{"github-mcp"}, // NOT "server" (and it's an array)
			},
		},
	}

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = oldStdout }()

	doctorOutput = "pretty"
	err := outputDiagnostics(diag, nil)

	w.Close()
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if err != nil {
		t.Errorf("outputDiagnostics() returned error: %v", err)
	}

	// Verify the secret name is displayed
	if !strings.Contains(output, "GITHUB_TOKEN") {
		t.Errorf("Should display secret name 'GITHUB_TOKEN' from secret_name field.\nGot output:\n%s", output)
	}

	// Verify the server name is displayed
	if !strings.Contains(output, "github-mcp") {
		t.Errorf("Should display server 'github-mcp' from used_by field.\nGot output:\n%s", output)
	}
}
