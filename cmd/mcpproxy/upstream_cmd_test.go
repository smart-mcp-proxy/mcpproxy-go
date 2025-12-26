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

func TestOutputServers_TableFormat(t *testing.T) {
	servers := []map[string]interface{}{
		{
			"name":       "github-server",
			"enabled":    true,
			"protocol":   "http",
			"connected":  true,
			"tool_count": 15,
			"status":     "connected",
		},
		{
			"name":       "ast-grep",
			"enabled":    false,
			"protocol":   "stdio",
			"connected":  false,
			"tool_count": 0,
			"status":     "disabled",
		},
	}

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = oldStdout }()

	// Use global output format (default is "table")
	globalOutputFormat = "table"
	globalJSONOutput = false
	err := outputServers(servers)

	w.Close()
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if err != nil {
		t.Errorf("outputServers() returned error: %v", err)
	}

	// Verify table headers (new unified health status format)
	if !strings.Contains(output, "NAME") {
		t.Error("Table output missing NAME header")
	}
	if !strings.Contains(output, "PROTOCOL") {
		t.Error("Table output missing PROTOCOL header")
	}
	if !strings.Contains(output, "TOOLS") {
		t.Error("Table output missing TOOLS header")
	}
	if !strings.Contains(output, "STATUS") {
		t.Error("Table output missing STATUS header")
	}
	if !strings.Contains(output, "ACTION") {
		t.Error("Table output missing ACTION header")
	}

	// Verify server data
	if !strings.Contains(output, "github-server") {
		t.Error("Table output missing server name: github-server")
	}
	if !strings.Contains(output, "ast-grep") {
		t.Error("Table output missing server name: ast-grep")
	}
}

func TestOutputServers_JSONFormat(t *testing.T) {
	servers := []map[string]interface{}{
		{
			"name":       "test-server",
			"enabled":    true,
			"protocol":   "stdio",
			"connected":  false,
			"tool_count": 5,
			"status":     "disconnected",
		},
	}

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = oldStdout }()

	// Use global output format for JSON
	globalOutputFormat = "json"
	globalJSONOutput = false
	err := outputServers(servers)

	w.Close()
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if err != nil {
		t.Errorf("outputServers() returned error: %v", err)
	}

	// Verify valid JSON
	var parsed []map[string]interface{}
	if err := json.Unmarshal([]byte(output), &parsed); err != nil {
		t.Errorf("JSON output is invalid: %v", err)
	}

	// Verify data
	if len(parsed) != 1 {
		t.Errorf("Expected 1 server in JSON output, got %d", len(parsed))
	}
	if parsed[0]["name"] != "test-server" {
		t.Errorf("Expected server name 'test-server', got %v", parsed[0]["name"])
	}
}

func TestOutputServers_InvalidFormat(t *testing.T) {
	servers := []map[string]interface{}{
		{"name": "test"},
	}

	// Use global output format for invalid format test
	globalOutputFormat = "invalid-format"
	globalJSONOutput = false
	err := outputServers(servers)

	if err == nil {
		t.Error("outputServers() should return error for invalid format")
	}
	if !strings.Contains(err.Error(), "unknown output format") {
		t.Errorf("Expected error about unknown format, got: %v", err)
	}
}

func TestOutputServers_Sorting(t *testing.T) {
	servers := []map[string]interface{}{
		{"name": "zebra-server", "enabled": true, "protocol": "http", "connected": true, "tool_count": 1, "status": "ok"},
		{"name": "alpha-server", "enabled": true, "protocol": "http", "connected": true, "tool_count": 1, "status": "ok"},
		{"name": "beta-server", "enabled": true, "protocol": "http", "connected": true, "tool_count": 1, "status": "ok"},
	}

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = oldStdout }()

	// Use global output format for JSON
	globalOutputFormat = "json"
	globalJSONOutput = false
	err := outputServers(servers)

	w.Close()
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if err != nil {
		t.Errorf("outputServers() returned error: %v", err)
	}

	// Parse JSON to verify order
	var parsed []map[string]interface{}
	if err := json.Unmarshal([]byte(output), &parsed); err != nil {
		t.Errorf("Failed to parse JSON: %v", err)
	}

	// Verify alphabetical order
	if len(parsed) != 3 {
		t.Fatalf("Expected 3 servers, got %d", len(parsed))
	}
	if parsed[0]["name"] != "alpha-server" {
		t.Errorf("Expected first server to be 'alpha-server', got %v", parsed[0]["name"])
	}
	if parsed[1]["name"] != "beta-server" {
		t.Errorf("Expected second server to be 'beta-server', got %v", parsed[1]["name"])
	}
	if parsed[2]["name"] != "zebra-server" {
		t.Errorf("Expected third server to be 'zebra-server', got %v", parsed[2]["name"])
	}
}

func TestOutputServers_EmptyList(t *testing.T) {
	servers := []map[string]interface{}{}

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = oldStdout }()

	// Use global output format (default is "table")
	globalOutputFormat = "table"
	globalJSONOutput = false
	err := outputServers(servers)

	w.Close()
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if err != nil {
		t.Errorf("outputServers() returned error: %v", err)
	}

	// With new unified formatter, empty tables show "No results found" message
	if !strings.Contains(output, "No results found") {
		t.Error("Empty table should show 'No results found' message")
	}
}

func TestShouldUseUpstreamDaemon(t *testing.T) {
	// Test with non-existent directory
	result := shouldUseUpstreamDaemon("/tmp/nonexistent-mcpproxy-test-dir-12345")
	if result {
		t.Error("shouldUseUpstreamDaemon should return false for non-existent directory")
	}

	// Test with existing directory but no socket
	tmpDir := t.TempDir()
	result = shouldUseUpstreamDaemon(tmpDir)
	if result {
		t.Error("shouldUseUpstreamDaemon should return false when socket doesn't exist")
	}
}

func TestGetLogDirectory(t *testing.T) {
	// Test helper function for getting log directory
	// This is tested indirectly through runUpstreamLogsFromFile
	// Here we document the expected behavior

	t.Run("empty log dir uses default", func(t *testing.T) {
		// When config.Logging.LogDir is empty, should use logs.GetLogDir()
		// This is tested in the actual command execution
	})

	t.Run("custom log dir used when set", func(t *testing.T) {
		// When config.Logging.LogDir is set, should use that path
		// This is tested in the actual command execution
	})
}

func TestSocketDetection(t *testing.T) {
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

func TestLoadUpstreamConfig(t *testing.T) {
	// Save original flag value
	oldConfigPath := upstreamConfigPath
	defer func() { upstreamConfigPath = oldConfigPath }()

	t.Run("default config path", func(t *testing.T) {
		upstreamConfigPath = ""
		// This will attempt to load default config
		// We just verify it doesn't panic
		_, err := loadUpstreamConfig()
		// Error is expected if no config exists, which is fine
		_ = err
	})

	t.Run("custom config path", func(t *testing.T) {
		// Create a temporary config file
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "test_config.json")

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

		upstreamConfigPath = configPath
		cfg, err := loadUpstreamConfig()
		if err != nil {
			t.Errorf("Failed to load custom config: %v", err)
		}
		if cfg != nil && cfg.Listen != "127.0.0.1:8080" {
			t.Errorf("Expected listen address '127.0.0.1:8080', got %s", cfg.Listen)
		}
	})
}

func TestCreateUpstreamLogger(t *testing.T) {
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
			logger, err := createUpstreamLogger(tt.logLevel)
			if (err != nil) != tt.wantErr {
				t.Errorf("createUpstreamLogger() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if logger == nil && !tt.wantErr {
				t.Error("createUpstreamLogger() returned nil logger")
			}
		})
	}
}

func TestOutputServers_BooleanFields(t *testing.T) {
	// Test that unified health status is displayed correctly based on server state
	tests := []struct {
		name           string
		healthLevel    string
		adminState     string
		expectedEmoji  string
	}{
		{"healthy enabled", "healthy", "enabled", "✅"},
		{"disabled", "healthy", "disabled", "⏸️"},
		{"quarantined", "healthy", "quarantined", "🔒"},
		{"unhealthy", "unhealthy", "enabled", "❌"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			servers := []map[string]interface{}{
				{
					"name":       "test-server",
					"protocol":   "stdio",
					"tool_count": 0,
					"health": map[string]interface{}{
						"level":       tt.healthLevel,
						"admin_state": tt.adminState,
						"summary":     "Test status",
						"action":      "",
					},
				},
			}

			// Capture stdout
			oldStdout := os.Stdout
			r, w, _ := os.Pipe()
			os.Stdout = w
			defer func() { os.Stdout = oldStdout }()

			// Use global output format for table
			globalOutputFormat = "table"
			globalJSONOutput = false
			err := outputServers(servers)

			w.Close()
			var buf bytes.Buffer
			buf.ReadFrom(r)
			output := buf.String()

			if err != nil {
				t.Errorf("outputServers() returned error: %v", err)
			}

			// Verify health status emoji is displayed
			if !strings.Contains(output, tt.expectedEmoji) {
				t.Errorf("Expected emoji '%s' for %s, output: %s", tt.expectedEmoji, tt.name, output)
			}
		})
	}
}

func TestOutputServers_IntegerFields(t *testing.T) {
	servers := []map[string]interface{}{
		{
			"name":       "server-zero",
			"enabled":    true,
			"protocol":   "http",
			"connected":  true,
			"tool_count": 0,
			"status":     "ok",
		},
		{
			"name":       "server-many",
			"enabled":    true,
			"protocol":   "http",
			"connected":  true,
			"tool_count": 42,
			"status":     "ok",
		},
	}

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = oldStdout }()

	// Use global output format (default is "table")
	globalOutputFormat = "table"
	globalJSONOutput = false
	err := outputServers(servers)

	w.Close()
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if err != nil {
		t.Errorf("outputServers() returned error: %v", err)
	}

	// Verify tool counts appear in output
	if !strings.Contains(output, "0") {
		t.Error("Output should contain tool count 0")
	}
	if !strings.Contains(output, "42") {
		t.Error("Output should contain tool count 42")
	}
}

func TestOutputServers_StatusMessages(t *testing.T) {
	servers := []map[string]interface{}{
		{
			"name":       "server1",
			"enabled":    true,
			"protocol":   "http",
			"connected":  false,
			"tool_count": 0,
			"status":     "connection failed: timeout",
		},
		{
			"name":       "server2",
			"enabled":    false,
			"protocol":   "stdio",
			"connected":  false,
			"tool_count": 0,
			"status":     "disabled by user",
		},
	}

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = oldStdout }()

	// Use global output format (default is "table")
	globalOutputFormat = "table"
	globalJSONOutput = false
	err := outputServers(servers)

	w.Close()
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if err != nil {
		t.Errorf("outputServers() returned error: %v", err)
	}

	// Verify status messages appear
	if !strings.Contains(output, "connection failed") {
		t.Error("Output should contain status message")
	}
	if !strings.Contains(output, "disabled") {
		t.Error("Output should contain disabled status")
	}
}

func TestRunUpstreamListFromConfig(t *testing.T) {
	// Create a minimal config
	cfg := &struct {
		Servers []struct {
			Name     string `json:"name"`
			Enabled  bool   `json:"enabled"`
			Protocol string `json:"protocol"`
		} `json:"mcpServers"`
	}{}

	// Add test servers
	cfg.Servers = append(cfg.Servers, struct {
		Name     string `json:"name"`
		Enabled  bool   `json:"enabled"`
		Protocol string `json:"protocol"`
	}{
		Name:     "test-server",
		Enabled:  true,
		Protocol: "stdio",
	})

	// This function is tested through runUpstreamList integration
	// Here we document expected behavior
	t.Run("converts config to output format", func(t *testing.T) {
		// Should create map with:
		// - name, enabled, protocol from config
		// - connected: false (no daemon)
		// - tool_count: 0 (no daemon)
		// - status: "unknown (daemon not running)"
	})
}
