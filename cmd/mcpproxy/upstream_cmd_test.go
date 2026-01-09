package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mcpproxy-go/internal/cliclient"
	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/socket"
)

// jsonEscapePath escapes a path for embedding in JSON strings.
// On Windows, backslashes must be escaped as \\, or use forward slashes.
func jsonEscapePath(p string) string {
	return filepath.ToSlash(p)
}

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

// ============================================================================
// T012: Server Name Validation Tests
// ============================================================================

func TestValidateServerName(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantErr   bool
		errSubstr string
	}{
		// Valid names
		{"simple lowercase", "myserver", false, ""},
		{"with hyphens", "my-server", false, ""},
		{"with underscores", "my_server", false, ""},
		{"alphanumeric", "server123", false, ""},
		{"mixed case", "MyServer", false, ""},
		{"all chars", "My-Server_123", false, ""},
		{"single char", "a", false, ""},
		{"max length 64", strings.Repeat("a", 64), false, ""},

		// Invalid names
		{"empty", "", true, "cannot be empty"},
		{"too long", strings.Repeat("a", 65), true, "too long"},
		{"with spaces", "my server", true, "invalid character"},
		{"with dots", "my.server", true, "invalid character"},
		{"with slash", "my/server", true, "invalid character"},
		{"with colon", "my:server", true, "invalid character"},
		{"starts with special", "@myserver", true, "invalid character"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateServerName(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateServerName(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if tt.wantErr && tt.errSubstr != "" && err != nil {
				if !strings.Contains(err.Error(), tt.errSubstr) {
					t.Errorf("validateServerName(%q) error = %q, want substr %q", tt.input, err.Error(), tt.errSubstr)
				}
			}
		})
	}
}

// ============================================================================
// T016-T017: Add HTTP Server Tests (Config Mode)
// ============================================================================

func TestAddHTTPServerConfigMode(t *testing.T) {
	t.Run("adds HTTP server with URL", func(t *testing.T) {
		// T016: Unit test: add HTTP server with URL
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "mcp_config.json") // Must match config.ConfigFileName

		// Create initial config
		initialConfig := `{
			"listen": "127.0.0.1:8080",
			"data_dir": "` + jsonEscapePath(tmpDir) + `",
			"mcpServers": []
		}`
		err := os.WriteFile(configPath, []byte(initialConfig), 0644)
		if err != nil {
			t.Fatalf("Failed to write initial config: %v", err)
		}

		// Create request
		req := &cliclient.AddServerRequest{
			Name:     "notion",
			URL:      "https://mcp.notion.com/sse",
			Protocol: "streamable-http",
		}

		// Load config
		cfg, err := config.LoadFromFile(configPath)
		if err != nil {
			t.Fatalf("Failed to load config: %v", err)
		}

		// Override config path for saving
		cfg.DataDir = tmpDir

		// Capture output
		oldStdout := os.Stdout
		r, w, _ := os.Pipe()
		os.Stdout = w

		err = runUpstreamAddConfigMode(req, cfg)

		w.Close()
		os.Stdout = oldStdout
		var buf bytes.Buffer
		buf.ReadFrom(r)
		output := buf.String()

		if err != nil {
			t.Errorf("runUpstreamAddConfigMode() error = %v", err)
		}

		// Verify output
		if !strings.Contains(output, "Added server") {
			t.Error("Expected success message")
		}
		if !strings.Contains(output, "notion") {
			t.Error("Expected server name in output")
		}

		// Verify config was updated
		updatedCfg, err := config.LoadFromFile(configPath)
		if err != nil {
			t.Fatalf("Failed to load updated config: %v", err)
		}
		if len(updatedCfg.Servers) != 1 {
			t.Errorf("Expected 1 server, got %d", len(updatedCfg.Servers))
		}
		if updatedCfg.Servers[0].Name != "notion" {
			t.Errorf("Expected server name 'notion', got %s", updatedCfg.Servers[0].Name)
		}
		if updatedCfg.Servers[0].URL != "https://mcp.notion.com/sse" {
			t.Errorf("Expected URL 'https://mcp.notion.com/sse', got %s", updatedCfg.Servers[0].URL)
		}
	})

	t.Run("adds HTTP server with headers", func(t *testing.T) {
		// T017: Unit test: add HTTP server with headers
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "mcp_config.json")

		// Create initial config
		initialConfig := `{
			"listen": "127.0.0.1:8080",
			"data_dir": "` + jsonEscapePath(tmpDir) + `",
			"mcpServers": []
		}`
		err := os.WriteFile(configPath, []byte(initialConfig), 0644)
		if err != nil {
			t.Fatalf("Failed to write initial config: %v", err)
		}

		// Create request with headers
		req := &cliclient.AddServerRequest{
			Name:     "weather",
			URL:      "https://api.weather.com/mcp",
			Protocol: "streamable-http",
			Headers: map[string]string{
				"Authorization": "Bearer secret-token",
				"X-Custom":      "value",
			},
		}

		// Load config
		cfg, err := config.LoadFromFile(configPath)
		if err != nil {
			t.Fatalf("Failed to load config: %v", err)
		}
		cfg.DataDir = tmpDir

		// Capture output
		oldStdout := os.Stdout
		_, w, _ := os.Pipe()
		os.Stdout = w

		err = runUpstreamAddConfigMode(req, cfg)

		w.Close()
		os.Stdout = oldStdout

		if err != nil {
			t.Errorf("runUpstreamAddConfigMode() error = %v", err)
		}

		// Verify config was updated with headers
		updatedCfg, err := config.LoadFromFile(configPath)
		if err != nil {
			t.Fatalf("Failed to load updated config: %v", err)
		}
		if len(updatedCfg.Servers) != 1 {
			t.Errorf("Expected 1 server, got %d", len(updatedCfg.Servers))
		}
		if updatedCfg.Servers[0].Headers["Authorization"] != "Bearer secret-token" {
			t.Errorf("Expected Authorization header, got %v", updatedCfg.Servers[0].Headers)
		}
		if updatedCfg.Servers[0].Headers["X-Custom"] != "value" {
			t.Errorf("Expected X-Custom header, got %v", updatedCfg.Servers[0].Headers)
		}
	})

	t.Run("rejects duplicate server", func(t *testing.T) {
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "mcp_config.json")

		// Create config with existing server
		initialConfig := `{
			"listen": "127.0.0.1:8080",
			"data_dir": "` + jsonEscapePath(tmpDir) + `",
			"mcpServers": [{"name": "existing", "url": "https://example.com"}]
		}`
		err := os.WriteFile(configPath, []byte(initialConfig), 0644)
		if err != nil {
			t.Fatalf("Failed to write initial config: %v", err)
		}

		req := &cliclient.AddServerRequest{
			Name:     "existing",
			URL:      "https://other.com",
			Protocol: "http",
		}

		cfg, err := config.LoadFromFile(configPath)
		if err != nil {
			t.Fatalf("Failed to load config: %v", err)
		}
		cfg.DataDir = tmpDir

		err = runUpstreamAddConfigMode(req, cfg)

		if err == nil {
			t.Error("Expected error for duplicate server")
		}
		if !strings.Contains(err.Error(), "already exists") {
			t.Errorf("Expected 'already exists' error, got: %v", err)
		}
	})

	t.Run("if-not-exists skips duplicate silently", func(t *testing.T) {
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "mcp_config.json")

		// Create config with existing server
		initialConfig := `{
			"listen": "127.0.0.1:8080",
			"data_dir": "` + jsonEscapePath(tmpDir) + `",
			"mcpServers": [{"name": "existing", "url": "https://example.com"}]
		}`
		err := os.WriteFile(configPath, []byte(initialConfig), 0644)
		if err != nil {
			t.Fatalf("Failed to write initial config: %v", err)
		}

		req := &cliclient.AddServerRequest{
			Name:     "existing",
			URL:      "https://other.com",
			Protocol: "http",
		}

		cfg, err := config.LoadFromFile(configPath)
		if err != nil {
			t.Fatalf("Failed to load config: %v", err)
		}
		cfg.DataDir = tmpDir

		// Set if-not-exists flag
		oldIfNotExists := upstreamAddIfNotExists
		upstreamAddIfNotExists = true
		defer func() { upstreamAddIfNotExists = oldIfNotExists }()

		// Capture output
		oldStdout := os.Stdout
		r, w, _ := os.Pipe()
		os.Stdout = w

		err = runUpstreamAddConfigMode(req, cfg)

		w.Close()
		os.Stdout = oldStdout
		var buf bytes.Buffer
		buf.ReadFrom(r)
		output := buf.String()

		if err != nil {
			t.Errorf("Expected no error with --if-not-exists, got: %v", err)
		}
		if !strings.Contains(output, "already exists") || !strings.Contains(output, "skipped") {
			t.Error("Expected skip message for existing server")
		}
	})
}

// ============================================================================
// T024-T025: Add Stdio Server Tests (Config Mode)
// ============================================================================

func TestAddStdioServerConfigMode(t *testing.T) {
	t.Run("adds stdio server with command", func(t *testing.T) {
		// T024: Unit test: add stdio server with command
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "mcp_config.json")

		initialConfig := `{
			"listen": "127.0.0.1:8080",
			"data_dir": "` + jsonEscapePath(tmpDir) + `",
			"mcpServers": []
		}`
		err := os.WriteFile(configPath, []byte(initialConfig), 0644)
		if err != nil {
			t.Fatalf("Failed to write initial config: %v", err)
		}

		req := &cliclient.AddServerRequest{
			Name:     "fs",
			Command:  "npx",
			Args:     []string{"-y", "@anthropic/mcp-server-filesystem", "/home/user"},
			Protocol: "stdio",
		}

		cfg, err := config.LoadFromFile(configPath)
		if err != nil {
			t.Fatalf("Failed to load config: %v", err)
		}
		cfg.DataDir = tmpDir

		// Capture output
		oldStdout := os.Stdout
		_, w, _ := os.Pipe()
		os.Stdout = w

		err = runUpstreamAddConfigMode(req, cfg)

		w.Close()
		os.Stdout = oldStdout

		if err != nil {
			t.Errorf("runUpstreamAddConfigMode() error = %v", err)
		}

		// Verify config
		updatedCfg, err := config.LoadFromFile(configPath)
		if err != nil {
			t.Fatalf("Failed to load updated config: %v", err)
		}
		if len(updatedCfg.Servers) != 1 {
			t.Fatalf("Expected 1 server, got %d", len(updatedCfg.Servers))
		}
		srv := updatedCfg.Servers[0]
		if srv.Name != "fs" {
			t.Errorf("Expected name 'fs', got %s", srv.Name)
		}
		if srv.Command != "npx" {
			t.Errorf("Expected command 'npx', got %s", srv.Command)
		}
		if len(srv.Args) != 3 {
			t.Errorf("Expected 3 args, got %d", len(srv.Args))
		}
		if srv.Protocol != "stdio" {
			t.Errorf("Expected protocol 'stdio', got %s", srv.Protocol)
		}
	})

	t.Run("adds stdio server with env and working-dir", func(t *testing.T) {
		// T025: Unit test: add stdio server with env and working-dir
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "mcp_config.json")

		// Create a working directory for the test
		workingDir := filepath.Join(tmpDir, "projects")
		err := os.MkdirAll(workingDir, 0755)
		if err != nil {
			t.Fatalf("Failed to create working directory: %v", err)
		}

		initialConfig := `{
			"listen": "127.0.0.1:8080",
			"data_dir": "` + jsonEscapePath(tmpDir) + `",
			"mcpServers": []
		}`
		err = os.WriteFile(configPath, []byte(initialConfig), 0644)
		if err != nil {
			t.Fatalf("Failed to write initial config: %v", err)
		}

		req := &cliclient.AddServerRequest{
			Name:       "project-tools",
			Command:    "uvx",
			Args:       []string{"mcp-server-project"},
			Protocol:   "stdio",
			WorkingDir: workingDir,
			Env: map[string]string{
				"PROJECT_ROOT": workingDir,
				"DEBUG":        "true",
			},
		}

		cfg, err := config.LoadFromFile(configPath)
		if err != nil {
			t.Fatalf("Failed to load config: %v", err)
		}
		cfg.DataDir = tmpDir

		// Capture output
		oldStdout := os.Stdout
		_, w, _ := os.Pipe()
		os.Stdout = w

		err = runUpstreamAddConfigMode(req, cfg)

		w.Close()
		os.Stdout = oldStdout

		if err != nil {
			t.Errorf("runUpstreamAddConfigMode() error = %v", err)
		}

		// Verify config
		updatedCfg, err := config.LoadFromFile(configPath)
		if err != nil {
			t.Fatalf("Failed to load updated config: %v", err)
		}
		if len(updatedCfg.Servers) != 1 {
			t.Fatalf("Expected 1 server, got %d", len(updatedCfg.Servers))
		}
		srv := updatedCfg.Servers[0]
		if srv.WorkingDir != workingDir {
			t.Errorf("Expected working_dir '%s', got %s", workingDir, srv.WorkingDir)
		}
		if srv.Env["PROJECT_ROOT"] != workingDir {
			t.Errorf("Expected PROJECT_ROOT env '%s', got %v", workingDir, srv.Env)
		}
		if srv.Env["DEBUG"] != "true" {
			t.Errorf("Expected DEBUG env, got %v", srv.Env)
		}
	})
}

// ============================================================================
// T032-T034: Remove Server Tests (Config Mode)
// ============================================================================

func TestRemoveServerConfigMode(t *testing.T) {
	t.Run("removes server successfully", func(t *testing.T) {
		// T032/T033: Remove server (confirmation bypassed in config mode tests)
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "mcp_config.json")

		// Create config with server
		initialConfig := `{
			"listen": "127.0.0.1:8080",
			"data_dir": "` + jsonEscapePath(tmpDir) + `",
			"mcpServers": [
				{"name": "github", "url": "https://api.github.com/mcp"},
				{"name": "notion", "url": "https://mcp.notion.com/sse"}
			]
		}`
		err := os.WriteFile(configPath, []byte(initialConfig), 0644)
		if err != nil {
			t.Fatalf("Failed to write initial config: %v", err)
		}

		cfg, err := config.LoadFromFile(configPath)
		if err != nil {
			t.Fatalf("Failed to load config: %v", err)
		}
		cfg.DataDir = tmpDir

		// Capture output
		oldStdout := os.Stdout
		r, w, _ := os.Pipe()
		os.Stdout = w

		err = runUpstreamRemoveConfigMode("github", cfg)

		w.Close()
		os.Stdout = oldStdout
		var buf bytes.Buffer
		buf.ReadFrom(r)
		output := buf.String()

		if err != nil {
			t.Errorf("runUpstreamRemoveConfigMode() error = %v", err)
		}

		// Verify output
		if !strings.Contains(output, "Removed server") {
			t.Error("Expected success message")
		}
		if !strings.Contains(output, "github") {
			t.Error("Expected server name in output")
		}

		// Verify config was updated
		updatedCfg, err := config.LoadFromFile(configPath)
		if err != nil {
			t.Fatalf("Failed to load updated config: %v", err)
		}
		if len(updatedCfg.Servers) != 1 {
			t.Errorf("Expected 1 server remaining, got %d", len(updatedCfg.Servers))
		}
		if updatedCfg.Servers[0].Name != "notion" {
			t.Errorf("Expected 'notion' to remain, got %s", updatedCfg.Servers[0].Name)
		}
	})

	t.Run("returns error for non-existent server", func(t *testing.T) {
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "mcp_config.json")

		initialConfig := `{
			"listen": "127.0.0.1:8080",
			"data_dir": "` + jsonEscapePath(tmpDir) + `",
			"mcpServers": [{"name": "existing", "url": "https://example.com"}]
		}`
		err := os.WriteFile(configPath, []byte(initialConfig), 0644)
		if err != nil {
			t.Fatalf("Failed to write initial config: %v", err)
		}

		cfg, err := config.LoadFromFile(configPath)
		if err != nil {
			t.Fatalf("Failed to load config: %v", err)
		}
		cfg.DataDir = tmpDir

		err = runUpstreamRemoveConfigMode("nonexistent", cfg)

		if err == nil {
			t.Error("Expected error for non-existent server")
		}
		if !strings.Contains(err.Error(), "not found") {
			t.Errorf("Expected 'not found' error, got: %v", err)
		}
	})

	t.Run("if-exists skips non-existent server silently", func(t *testing.T) {
		// T034: Unit test: remove non-existent server with --if-exists
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "mcp_config.json")

		initialConfig := `{
			"listen": "127.0.0.1:8080",
			"data_dir": "` + jsonEscapePath(tmpDir) + `",
			"mcpServers": [{"name": "existing", "url": "https://example.com"}]
		}`
		err := os.WriteFile(configPath, []byte(initialConfig), 0644)
		if err != nil {
			t.Fatalf("Failed to write initial config: %v", err)
		}

		cfg, err := config.LoadFromFile(configPath)
		if err != nil {
			t.Fatalf("Failed to load config: %v", err)
		}
		cfg.DataDir = tmpDir

		// Set if-exists flag
		oldIfExists := upstreamRemoveIfExists
		upstreamRemoveIfExists = true
		defer func() { upstreamRemoveIfExists = oldIfExists }()

		// Capture output
		oldStdout := os.Stdout
		r, w, _ := os.Pipe()
		os.Stdout = w

		err = runUpstreamRemoveConfigMode("nonexistent", cfg)

		w.Close()
		os.Stdout = oldStdout
		var buf bytes.Buffer
		buf.ReadFrom(r)
		output := buf.String()

		if err != nil {
			t.Errorf("Expected no error with --if-exists, got: %v", err)
		}
		if !strings.Contains(output, "not found") || !strings.Contains(output, "skipped") {
			t.Error("Expected skip message for non-existent server")
		}
	})
}

// ============================================================================
// T040-T041: Add-JSON Tests
// ============================================================================

func TestAddJSONParsing(t *testing.T) {
	t.Run("parses valid HTTP JSON config", func(t *testing.T) {
		// T040: Unit test: add-json with valid JSON
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "mcp_config.json")

		initialConfig := `{
			"listen": "127.0.0.1:8080",
			"data_dir": "` + jsonEscapePath(tmpDir) + `",
			"mcpServers": []
		}`
		err := os.WriteFile(configPath, []byte(initialConfig), 0644)
		if err != nil {
			t.Fatalf("Failed to write initial config: %v", err)
		}

		// Simulate the JSON parsing logic from runUpstreamAddJSON
		jsonStr := `{"url":"https://api.weather.com/mcp","headers":{"Authorization":"Bearer token"}}`

		var jsonConfig struct {
			URL        string            `json:"url"`
			Command    string            `json:"command"`
			Args       []string          `json:"args"`
			Env        map[string]string `json:"env"`
			Headers    map[string]string `json:"headers"`
			WorkingDir string            `json:"working_dir"`
			Protocol   string            `json:"protocol"`
		}

		err = json.Unmarshal([]byte(jsonStr), &jsonConfig)
		if err != nil {
			t.Errorf("Failed to parse JSON: %v", err)
		}

		// Verify parsed values
		if jsonConfig.URL != "https://api.weather.com/mcp" {
			t.Errorf("Expected URL 'https://api.weather.com/mcp', got %s", jsonConfig.URL)
		}
		if jsonConfig.Headers["Authorization"] != "Bearer token" {
			t.Errorf("Expected Authorization header, got %v", jsonConfig.Headers)
		}

		// Test auto-detect protocol
		protocol := jsonConfig.Protocol
		if protocol == "" {
			if jsonConfig.URL != "" {
				protocol = "streamable-http"
			}
		}
		if protocol != "streamable-http" {
			t.Errorf("Expected auto-detected protocol 'streamable-http', got %s", protocol)
		}
	})

	t.Run("parses valid stdio JSON config", func(t *testing.T) {
		jsonStr := `{"command":"uvx","args":["mcp-server-sqlite","--db","mydb.db"],"env":{"DEBUG":"1"}}`

		var jsonConfig struct {
			URL        string            `json:"url"`
			Command    string            `json:"command"`
			Args       []string          `json:"args"`
			Env        map[string]string `json:"env"`
			Headers    map[string]string `json:"headers"`
			WorkingDir string            `json:"working_dir"`
			Protocol   string            `json:"protocol"`
		}

		err := json.Unmarshal([]byte(jsonStr), &jsonConfig)
		if err != nil {
			t.Errorf("Failed to parse JSON: %v", err)
		}

		if jsonConfig.Command != "uvx" {
			t.Errorf("Expected command 'uvx', got %s", jsonConfig.Command)
		}
		if len(jsonConfig.Args) != 3 {
			t.Errorf("Expected 3 args, got %d", len(jsonConfig.Args))
		}
		if jsonConfig.Env["DEBUG"] != "1" {
			t.Errorf("Expected DEBUG env, got %v", jsonConfig.Env)
		}

		// Test auto-detect protocol
		protocol := jsonConfig.Protocol
		if protocol == "" {
			if jsonConfig.Command != "" {
				protocol = "stdio"
			}
		}
		if protocol != "stdio" {
			t.Errorf("Expected auto-detected protocol 'stdio', got %s", protocol)
		}
	})

	t.Run("rejects invalid JSON", func(t *testing.T) {
		// T041: Unit test: add-json with invalid JSON returns error
		invalidJSONs := []struct {
			name     string
			jsonStr  string
			errMatch string
		}{
			{"malformed", `{invalid json}`, "invalid character"},
			{"unclosed brace", `{"url": "test"`, "unexpected end"},
			{"not an object", `["array", "not", "object"]`, "cannot unmarshal"},
		}

		for _, tc := range invalidJSONs {
			t.Run(tc.name, func(t *testing.T) {
				var jsonConfig struct {
					URL     string `json:"url"`
					Command string `json:"command"`
				}

				err := json.Unmarshal([]byte(tc.jsonStr), &jsonConfig)
				if err == nil {
					t.Errorf("Expected error for invalid JSON: %s", tc.jsonStr)
				}
				if !strings.Contains(err.Error(), tc.errMatch) {
					t.Errorf("Expected error containing '%s', got: %v", tc.errMatch, err)
				}
			})
		}
	})

	t.Run("rejects JSON without url or command", func(t *testing.T) {
		jsonStr := `{"headers":{"X-Key":"value"}}`

		var jsonConfig struct {
			URL     string `json:"url"`
			Command string `json:"command"`
		}

		err := json.Unmarshal([]byte(jsonStr), &jsonConfig)
		if err != nil {
			t.Fatalf("Failed to parse JSON: %v", err)
		}

		// Validate that either url or command is required
		if jsonConfig.URL == "" && jsonConfig.Command == "" {
			// This is the expected validation error
			return
		}
		t.Error("Expected validation to require either 'url' or 'command'")
	})
}

// ============================================================================
// Confirmation Prompt Tests (for documentation)
// ============================================================================

func TestConfirmationPromptBehavior(t *testing.T) {
	// The promptConfirmation function reads from stdin, which is difficult to test directly.
	// These tests document the expected behavior.

	t.Run("yes flag skips confirmation", func(t *testing.T) {
		// When --yes flag is set, confirmation prompt should not appear
		// Tested via integration with upstreamRemoveYes flag
		// The upstreamRemoveYes flag defaults to false
		_ = upstreamRemoveYes // Reference to avoid unused warning; value is checked by cobra integration
	})

	t.Run("y flag also skips confirmation", func(t *testing.T) {
		// -y should work the same as --yes
		// Both set upstreamRemoveYes to true
		// This is tested via cobra flag binding
	})
}

// ============================================================================
// Server Quarantine Tests
// ============================================================================

func TestNewServerQuarantineDefault(t *testing.T) {
	t.Run("new server is quarantined by default", func(t *testing.T) {
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "mcp_config.json")

		initialConfig := `{
			"listen": "127.0.0.1:8080",
			"data_dir": "` + jsonEscapePath(tmpDir) + `",
			"mcpServers": []
		}`
		err := os.WriteFile(configPath, []byte(initialConfig), 0644)
		if err != nil {
			t.Fatalf("Failed to write initial config: %v", err)
		}

		req := &cliclient.AddServerRequest{
			Name:     "new-server",
			URL:      "https://example.com/mcp",
			Protocol: "http",
		}

		cfg, err := config.LoadFromFile(configPath)
		if err != nil {
			t.Fatalf("Failed to load config: %v", err)
		}
		cfg.DataDir = tmpDir

		// Capture output
		oldStdout := os.Stdout
		r, w, _ := os.Pipe()
		os.Stdout = w

		err = runUpstreamAddConfigMode(req, cfg)

		w.Close()
		os.Stdout = oldStdout
		var buf bytes.Buffer
		buf.ReadFrom(r)
		output := buf.String()

		if err != nil {
			t.Fatalf("runUpstreamAddConfigMode() error = %v", err)
		}

		// Verify quarantine message appears
		if !strings.Contains(output, "quarantined") {
			t.Error("Expected quarantine warning in output")
		}

		// Verify config
		updatedCfg, err := config.LoadFromFile(configPath)
		if err != nil {
			t.Fatalf("Failed to load updated config: %v", err)
		}
		if len(updatedCfg.Servers) != 1 {
			t.Fatalf("Expected 1 server, got %d", len(updatedCfg.Servers))
		}
		if !updatedCfg.Servers[0].Quarantined {
			t.Error("New server should be quarantined by default")
		}
	})
}

// T021: Test that CLI prints request_id on error
func TestOutputError_WithRequestID(t *testing.T) {
	t.Run("prints request_id when APIError has one", func(t *testing.T) {
		// Capture stderr
		oldStderr := os.Stderr
		r, w, _ := os.Pipe()
		os.Stderr = w
		defer func() { os.Stderr = oldStderr }()

		// Set table format (human-readable output)
		globalOutputFormat = "table"
		globalJSONOutput = false

		// Create an APIError with request_id
		apiErr := &cliclient.APIError{
			Message:   "server not found",
			RequestID: "test-request-id-12345",
		}

		// Call outputError
		_ = outputError(apiErr, "SERVER_NOT_FOUND")

		w.Close()
		var buf bytes.Buffer
		buf.ReadFrom(r)
		output := buf.String()

		// Verify request_id is printed
		if !strings.Contains(output, "test-request-id-12345") {
			t.Errorf("Expected output to contain request_id 'test-request-id-12345', got: %s", output)
		}

		// Verify suggestion to use activity list
		if !strings.Contains(output, "mcpproxy activity list --request-id") {
			t.Errorf("Expected output to contain activity list suggestion, got: %s", output)
		}
	})

	t.Run("JSON output includes request_id field", func(t *testing.T) {
		// Capture stdout (JSON/YAML goes to stdout)
		oldStdout := os.Stdout
		r, w, _ := os.Pipe()
		os.Stdout = w
		defer func() { os.Stdout = oldStdout }()

		// Set JSON format
		globalOutputFormat = "json"
		globalJSONOutput = true

		// Create an APIError with request_id
		apiErr := &cliclient.APIError{
			Message:   "server not found",
			RequestID: "json-request-id-67890",
		}

		// Call outputError
		_ = outputError(apiErr, "SERVER_NOT_FOUND")

		w.Close()
		var buf bytes.Buffer
		buf.ReadFrom(r)
		output := buf.String()

		// Verify JSON output contains request_id
		var jsonOutput map[string]interface{}
		if err := json.Unmarshal([]byte(output), &jsonOutput); err != nil {
			t.Fatalf("Failed to parse JSON output: %v (output was: %s)", err, output)
		}

		requestID, ok := jsonOutput["request_id"].(string)
		if !ok {
			t.Errorf("Expected JSON to contain request_id field, got: %v", jsonOutput)
		}
		if requestID != "json-request-id-67890" {
			t.Errorf("Expected request_id 'json-request-id-67890', got: %s", requestID)
		}
	})
}

// T022: Test that CLI does NOT print request_id on success
func TestOutputError_WithoutRequestID(t *testing.T) {
	t.Run("does not print request_id when APIError has none", func(t *testing.T) {
		// Capture stderr
		oldStderr := os.Stderr
		r, w, _ := os.Pipe()
		os.Stderr = w
		defer func() { os.Stderr = oldStderr }()

		// Set table format
		globalOutputFormat = "table"
		globalJSONOutput = false

		// Create an APIError without request_id
		apiErr := &cliclient.APIError{
			Message:   "server not found",
			RequestID: "", // Empty request_id
		}

		// Call outputError
		_ = outputError(apiErr, "SERVER_NOT_FOUND")

		w.Close()
		var buf bytes.Buffer
		buf.ReadFrom(r)
		output := buf.String()

		// Verify "Request ID:" is NOT printed
		if strings.Contains(output, "Request ID:") {
			t.Errorf("Expected output to NOT contain 'Request ID:' when request_id is empty, got: %s", output)
		}

		// Verify error message is still printed
		if !strings.Contains(output, "server not found") {
			t.Errorf("Expected output to contain error message, got: %s", output)
		}
	})

	t.Run("does not print request_id for regular errors", func(t *testing.T) {
		// Capture stderr
		oldStderr := os.Stderr
		r, w, _ := os.Pipe()
		os.Stderr = w
		defer func() { os.Stderr = oldStderr }()

		// Set table format
		globalOutputFormat = "table"
		globalJSONOutput = false

		// Create a regular error (not APIError)
		regularErr := os.ErrNotExist

		// Call outputError
		_ = outputError(regularErr, "FILE_NOT_FOUND")

		w.Close()
		var buf bytes.Buffer
		buf.ReadFrom(r)
		output := buf.String()

		// Verify "Request ID:" is NOT printed
		if strings.Contains(output, "Request ID:") {
			t.Errorf("Expected output to NOT contain 'Request ID:' for regular errors, got: %s", output)
		}
	})
}
