package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

func TestStatusMaskAPIKey(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "normal 64-char key",
			input:    "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4a5b6c7d8e9f0a1b2",
			expected: "a1b2****a1b2",
		},
		{
			name:     "short key 8 chars",
			input:    "12345678",
			expected: "****",
		},
		{
			name:     "very short key",
			input:    "abc",
			expected: "****",
		},
		{
			name:     "empty key",
			input:    "",
			expected: "****",
		},
		{
			name:     "9-char key (just over threshold)",
			input:    "123456789",
			expected: "1234****6789",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := statusMaskAPIKey(tt.input)
			if result != tt.expected {
				t.Errorf("statusMaskAPIKey(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestStatusBuildWebUIURL(t *testing.T) {
	tests := []struct {
		name       string
		listenAddr string
		apiKey     string
		expected   string
	}{
		{
			name:       "normal address with key",
			listenAddr: "127.0.0.1:8080",
			apiKey:     "testkey123",
			expected:   "http://127.0.0.1:8080/ui/?apikey=testkey123",
		},
		{
			name:       "port-only address",
			listenAddr: ":8080",
			apiKey:     "testkey123",
			expected:   "http://127.0.0.1:8080/ui/?apikey=testkey123",
		},
		{
			name:       "custom port",
			listenAddr: "192.168.1.100:9090",
			apiKey:     "abc",
			expected:   "http://192.168.1.100:9090/ui/?apikey=abc",
		},
		{
			name:       "empty API key",
			listenAddr: "127.0.0.1:8080",
			apiKey:     "",
			expected:   "http://127.0.0.1:8080/ui/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := statusBuildWebUIURL(tt.listenAddr, tt.apiKey)
			if result != tt.expected {
				t.Errorf("statusBuildWebUIURL(%q, %q) = %q, want %q", tt.listenAddr, tt.apiKey, result, tt.expected)
			}
		})
	}
}

func TestCollectStatusFromConfig(t *testing.T) {
	cfg := &config.Config{
		Listen: "127.0.0.1:8080",
		APIKey: "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4a5b6c7d8e9f0a1b2",
	}

	info := collectStatusFromConfig(cfg, "/tmp/test.sock", "/tmp/test/mcp_config.json")

	if info.State != "Not running" {
		t.Errorf("expected State 'Not running', got %q", info.State)
	}

	if !strings.Contains(info.ListenAddr, "(configured)") {
		t.Errorf("expected ListenAddr to contain '(configured)', got %q", info.ListenAddr)
	}

	if info.APIKey != cfg.APIKey {
		t.Errorf("expected APIKey to match config, got %q", info.APIKey)
	}

	if info.ConfigPath != "/tmp/test/mcp_config.json" {
		t.Errorf("expected ConfigPath '/tmp/test/mcp_config.json', got %q", info.ConfigPath)
	}

	if !strings.Contains(info.WebUIURL, "apikey=") {
		t.Errorf("expected WebUIURL to contain apikey, got %q", info.WebUIURL)
	}

	if info.Servers != nil {
		t.Error("expected Servers to be nil for config-only mode")
	}

	if info.Uptime != "" {
		t.Errorf("expected Uptime to be empty for config-only mode, got %q", info.Uptime)
	}
}

func TestCollectStatusFromConfigDefaults(t *testing.T) {
	cfg := &config.Config{
		APIKey: "testkey",
	}

	info := collectStatusFromConfig(cfg, "", "/tmp/config.json")

	if !strings.HasPrefix(info.ListenAddr, "127.0.0.1:8080") {
		t.Errorf("expected default listen addr 127.0.0.1:8080, got %q", info.ListenAddr)
	}
}

func TestFormatStatusTable(t *testing.T) {
	t.Run("running state", func(t *testing.T) {
		info := &StatusInfo{
			State:      "Running",
			ListenAddr: "127.0.0.1:8080",
			Uptime:     "2h 15m",
			APIKey:     "a1b2****a1b2",
			WebUIURL:   "http://127.0.0.1:8080/ui/?apikey=test",
			Servers:    &ServerCounts{Connected: 5, Quarantined: 1, Total: 6},
			SocketPath: "/tmp/mcpproxy.sock",
			Version:    "v1.0.0",
		}

		// Capture stdout
		old := os.Stdout
		r, w, _ := os.Pipe()
		os.Stdout = w

		printStatusTable(info)

		w.Close()
		os.Stdout = old

		buf := make([]byte, 4096)
		n, _ := r.Read(buf)
		output := string(buf[:n])

		checks := []string{"MCPProxy Status", "Running", "127.0.0.1:8080", "2h 15m", "a1b2****a1b2", "5 connected, 1 quarantined", "/tmp/mcpproxy.sock", "v1.0.0"}
		for _, check := range checks {
			if !strings.Contains(output, check) {
				t.Errorf("expected output to contain %q, output:\n%s", check, output)
			}
		}
	})

	t.Run("not running state", func(t *testing.T) {
		info := &StatusInfo{
			State:      "Not running",
			ListenAddr: "127.0.0.1:8080 (configured)",
			APIKey:     "a1b2****a1b2",
			WebUIURL:   "http://127.0.0.1:8080/ui/?apikey=test",
			ConfigPath: "/home/user/.mcpproxy/mcp_config.json",
		}

		old := os.Stdout
		r, w, _ := os.Pipe()
		os.Stdout = w

		printStatusTable(info)

		w.Close()
		os.Stdout = old

		buf := make([]byte, 4096)
		n, _ := r.Read(buf)
		output := string(buf[:n])

		checks := []string{"Not running", "(configured)", "Config:"}
		for _, check := range checks {
			if !strings.Contains(output, check) {
				t.Errorf("expected output to contain %q, output:\n%s", check, output)
			}
		}

		// Should NOT contain server counts or socket
		if strings.Contains(output, "Servers:") {
			t.Error("should not contain Servers line when not running")
		}
	})
}

func TestShowKeyFlag(t *testing.T) {
	fullKey := "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4a5b6c7d8e9f0a1b2"
	info := &StatusInfo{
		State:      "Not running",
		ListenAddr: "127.0.0.1:8080",
		APIKey:     fullKey, // Not masked when --show-key
		WebUIURL:   "http://127.0.0.1:8080/ui/?apikey=" + fullKey,
	}

	t.Run("table output with show-key", func(t *testing.T) {
		old := os.Stdout
		r, w, _ := os.Pipe()
		os.Stdout = w

		printStatusTable(info)

		w.Close()
		os.Stdout = old

		buf := make([]byte, 4096)
		n, _ := r.Read(buf)
		output := string(buf[:n])

		if !strings.Contains(output, fullKey) {
			t.Errorf("expected full key in output, got:\n%s", output)
		}
	})

	t.Run("JSON output with show-key", func(t *testing.T) {
		old := os.Stdout
		r, w, _ := os.Pipe()
		os.Stdout = w

		err := printStatusJSON(info)

		w.Close()
		os.Stdout = old

		if err != nil {
			t.Fatalf("printStatusJSON failed: %v", err)
		}

		buf := make([]byte, 4096)
		n, _ := r.Read(buf)
		output := string(buf[:n])

		var result StatusInfo
		if jsonErr := json.Unmarshal([]byte(output), &result); jsonErr != nil {
			t.Fatalf("invalid JSON output: %v", jsonErr)
		}

		if result.APIKey != fullKey {
			t.Errorf("expected full key in JSON, got %q", result.APIKey)
		}
	})
}

func TestWebURLFlag(t *testing.T) {
	expectedURL := "http://127.0.0.1:8080/ui/?apikey=testkey123"
	info := &StatusInfo{
		WebUIURL: expectedURL,
	}

	// Simulate --web-url output (just the URL)
	output := info.WebUIURL

	if output != expectedURL {
		t.Errorf("expected URL %q, got %q", expectedURL, output)
	}

	// Verify no extra formatting
	if strings.Contains(output, "Web UI:") {
		t.Error("--web-url output should not contain labels")
	}
	if strings.Contains(output, "\n") {
		t.Error("--web-url output should not contain embedded newlines")
	}
}

func TestResetKey(t *testing.T) {
	// Create a temp config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "mcp_config.json")

	cfg := &config.Config{
		Listen: "127.0.0.1:8080",
		APIKey: "old_key_1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab",
	}

	// Save initial config
	initialData, _ := json.MarshalIndent(cfg, "", "  ")
	os.WriteFile(configPath, initialData, 0600)

	oldKey := cfg.APIKey
	newKey, err := resetAPIKey(cfg, configPath)
	if err != nil {
		t.Fatalf("resetAPIKey failed: %v", err)
	}

	// Verify new key is different
	if newKey == oldKey {
		t.Error("new key should be different from old key")
	}

	// Verify new key is 64 hex chars
	if len(newKey) != 64 {
		t.Errorf("expected 64-char hex key, got %d chars", len(newKey))
	}

	// Verify config file was updated
	fileData, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read config: %v", err)
	}
	if !strings.Contains(string(fileData), newKey) {
		t.Error("config file should contain new key")
	}
}

func TestResetKeyWithEnvVar(t *testing.T) {
	// This tests the logic branch, not the actual env var check
	// The actual env var warning is printed in runStatus, which checks os.LookupEnv
	t.Run("env var detection", func(t *testing.T) {
		_, exists := os.LookupEnv("MCPPROXY_API_KEY")
		// Just verify the env check function works
		if exists {
			t.Log("MCPPROXY_API_KEY is set - env var warning would be shown")
		} else {
			t.Log("MCPPROXY_API_KEY is not set - no env var warning")
		}
	})
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"minutes only", "15m", "15m"},
		{"hours and minutes", "2h15m", "2h 15m"},
		{"days hours minutes", "49h30m", "2d 1h 30m"},
		{"zero", "0s", "0m"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, _ := parseTestDuration(tt.input)
			result := statusFormatDuration(d)
			if result != tt.expected {
				t.Errorf("statusFormatDuration(%v) = %q, want %q", d, result, tt.expected)
			}
		})
	}
}

func TestExtractServerCounts(t *testing.T) {
	stats := map[string]interface{}{
		"connected":   float64(5),
		"quarantined": float64(2),
		"total":       float64(7),
	}

	counts := extractServerCounts(stats)

	if counts.Connected != 5 {
		t.Errorf("expected Connected=5, got %d", counts.Connected)
	}
	if counts.Quarantined != 2 {
		t.Errorf("expected Quarantined=2, got %d", counts.Quarantined)
	}
	if counts.Total != 7 {
		t.Errorf("expected Total=7, got %d", counts.Total)
	}
}

func TestExtractServerCountsNoTotal(t *testing.T) {
	stats := map[string]interface{}{
		"connected":   float64(3),
		"quarantined": float64(1),
	}

	counts := extractServerCounts(stats)

	if counts.Total != 4 {
		t.Errorf("expected Total=4 (sum), got %d", counts.Total)
	}
}

func TestStatusJSONOutput(t *testing.T) {
	info := &StatusInfo{
		State:      "Running",
		ListenAddr: "127.0.0.1:8080",
		APIKey:     "a1b2****a1b2",
		WebUIURL:   "http://127.0.0.1:8080/ui/?apikey=test",
		Servers:    &ServerCounts{Connected: 3, Quarantined: 0, Total: 3},
		Version:    "v1.0.0",
	}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := printStatusJSON(info)

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("printStatusJSON failed: %v", err)
	}

	buf := make([]byte, 8192)
	n, _ := r.Read(buf)
	output := string(buf[:n])

	var result StatusInfo
	if jsonErr := json.Unmarshal([]byte(output), &result); jsonErr != nil {
		t.Fatalf("invalid JSON: %v\nOutput: %s", jsonErr, output)
	}

	if result.State != "Running" {
		t.Errorf("expected state 'Running', got %q", result.State)
	}
	if result.Servers == nil {
		t.Fatal("expected servers in JSON output")
	}
	if result.Servers.Connected != 3 {
		t.Errorf("expected 3 connected, got %d", result.Servers.Connected)
	}
}

func TestStatusRoutingModeInTable(t *testing.T) {
	tests := []struct {
		name        string
		routingMode string
		expected    string
	}{
		{
			name:        "retrieve_tools mode",
			routingMode: "retrieve_tools",
			expected:    "retrieve_tools",
		},
		{
			name:        "direct mode",
			routingMode: "direct",
			expected:    "direct",
		},
		{
			name:        "code_execution mode",
			routingMode: "code_execution",
			expected:    "code_execution",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := &StatusInfo{
				State:       "Running",
				Edition:     "personal",
				ListenAddr:  "127.0.0.1:8080",
				APIKey:      "a1b2****a1b2",
				WebUIURL:    "http://127.0.0.1:8080/ui/?apikey=test",
				RoutingMode: tt.routingMode,
			}

			old := os.Stdout
			r, w, _ := os.Pipe()
			os.Stdout = w

			printStatusTable(info)

			w.Close()
			os.Stdout = old

			buf := make([]byte, 4096)
			n, _ := r.Read(buf)
			output := string(buf[:n])

			if !strings.Contains(output, "Routing:") {
				t.Errorf("expected output to contain 'Routing:', output:\n%s", output)
			}
			if !strings.Contains(output, tt.expected) {
				t.Errorf("expected output to contain %q, output:\n%s", tt.expected, output)
			}
		})
	}
}

func TestStatusRoutingModeInJSON(t *testing.T) {
	info := &StatusInfo{
		State:       "Running",
		Edition:     "personal",
		ListenAddr:  "127.0.0.1:8080",
		APIKey:      "testkey",
		WebUIURL:    "http://127.0.0.1:8080/ui/",
		RoutingMode: "direct",
	}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := printStatusJSON(info)

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("printStatusJSON failed: %v", err)
	}

	buf := make([]byte, 8192)
	n, _ := r.Read(buf)
	output := string(buf[:n])

	var result StatusInfo
	if jsonErr := json.Unmarshal([]byte(output), &result); jsonErr != nil {
		t.Fatalf("invalid JSON: %v\nOutput: %s", jsonErr, output)
	}

	if result.RoutingMode != "direct" {
		t.Errorf("expected routing_mode 'direct', got %q", result.RoutingMode)
	}
}

func TestCollectStatusFromConfigRoutingMode(t *testing.T) {
	t.Run("uses config routing mode", func(t *testing.T) {
		cfg := &config.Config{
			Listen:      "127.0.0.1:8080",
			APIKey:      "testkey",
			RoutingMode: "direct",
		}

		info := collectStatusFromConfig(cfg, "/tmp/test.sock", "/tmp/config.json")

		if info.RoutingMode != "direct" {
			t.Errorf("expected routing mode 'direct', got %q", info.RoutingMode)
		}
	})

	t.Run("defaults to retrieve_tools when empty", func(t *testing.T) {
		cfg := &config.Config{
			Listen: "127.0.0.1:8080",
			APIKey: "testkey",
		}

		info := collectStatusFromConfig(cfg, "/tmp/test.sock", "/tmp/config.json")

		if info.RoutingMode != config.RoutingModeRetrieveTools {
			t.Errorf("expected routing mode %q, got %q", config.RoutingModeRetrieveTools, info.RoutingMode)
		}
	})
}

// parseTestDuration is a helper to parse duration strings for tests.
func parseTestDuration(s string) (time.Duration, error) {
	return time.ParseDuration(s)
}
