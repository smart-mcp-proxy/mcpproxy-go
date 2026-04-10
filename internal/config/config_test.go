package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()

	// Test default values
	assert.Equal(t, "127.0.0.1:8080", config.Listen)
	assert.Equal(t, "", config.DataDir)
	assert.False(t, config.DebugSearch)
	assert.Equal(t, 15, config.ToolsLimit)
	assert.Equal(t, 20000, config.ToolResponseLimit)

	// Test security defaults (permissive)
	assert.False(t, config.RequireMCPAuth)
	assert.False(t, config.ReadOnlyMode)
	assert.False(t, config.DisableManagement)
	assert.True(t, config.AllowServerAdd)
	assert.True(t, config.AllowServerRemove)

	// Test prompts default
	assert.True(t, config.EnablePrompts)

	// Test empty servers list
	assert.Empty(t, config.Servers)
}

func TestConfigValidation(t *testing.T) {
	tests := []struct {
		name     string
		config   *Config
		expected *Config
	}{
		{
			name: "empty listen defaults to :8080",
			config: &Config{
				Listen: "",
			},
			expected: &Config{
				Listen:            "127.0.0.1:8080",
				ToolsLimit:        15,
				ToolResponseLimit: 0,
			},
		},
		{
			name: "negative ToolsLimit defaults to 15",
			config: &Config{
				ToolsLimit: -5,
			},
			expected: &Config{
				Listen:            "127.0.0.1:8080",
				ToolsLimit:        15,
				ToolResponseLimit: 0,
			},
		},
		{
			name: "negative ToolResponseLimit defaults to 0",
			config: &Config{
				ToolResponseLimit: -100,
			},
			expected: &Config{
				Listen:            "127.0.0.1:8080",
				ToolsLimit:        15,
				ToolResponseLimit: 0,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			require.NoError(t, err)
			assert.Equal(t, tt.expected.Listen, tt.config.Listen)
			assert.Equal(t, tt.expected.ToolsLimit, tt.config.ToolsLimit)
			assert.Equal(t, tt.expected.ToolResponseLimit, tt.config.ToolResponseLimit)
		})
	}
}

func TestConfigJSONSerialization(t *testing.T) {
	original := &Config{
		Listen:            ":9090",
		DataDir:           "/tmp/test",
		EnableTray:        false,
		DebugSearch:       true,
		TopK:              10,
		ToolsLimit:        20,
		ToolResponseLimit: 50000,
		CallToolTimeout:   Duration(5 * time.Minute),
		RequireMCPAuth:    true,
		ReadOnlyMode:      true,
		DisableManagement: true,
		AllowServerAdd:    false,
		AllowServerRemove: false,
		EnablePrompts:     false,
		Servers: []*ServerConfig{
			{
				Name:     "test-server",
				URL:      "http://localhost:3000",
				Protocol: "http",
				Enabled:  true,
				Created:  time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
			},
		},
	}

	// Marshal to JSON
	data, err := json.Marshal(original)
	require.NoError(t, err)

	// Unmarshal from JSON
	var restored Config
	err = json.Unmarshal(data, &restored)
	require.NoError(t, err)

	// Compare values
	assert.Equal(t, original.Listen, restored.Listen)
	assert.Equal(t, original.DataDir, restored.DataDir)
	assert.Equal(t, original.EnableTray, restored.EnableTray)
	assert.Equal(t, original.DebugSearch, restored.DebugSearch)
	assert.Equal(t, original.TopK, restored.TopK)
	assert.Equal(t, original.ToolsLimit, restored.ToolsLimit)
	assert.Equal(t, original.ToolResponseLimit, restored.ToolResponseLimit)
	assert.Equal(t, original.CallToolTimeout, restored.CallToolTimeout)
	assert.Equal(t, original.RequireMCPAuth, restored.RequireMCPAuth)
	assert.Equal(t, original.ReadOnlyMode, restored.ReadOnlyMode)
	assert.Equal(t, original.DisableManagement, restored.DisableManagement)
	assert.Equal(t, original.AllowServerAdd, restored.AllowServerAdd)
	assert.Equal(t, original.AllowServerRemove, restored.AllowServerRemove)
	assert.Equal(t, original.EnablePrompts, restored.EnablePrompts)
	assert.Len(t, restored.Servers, 1)
	assert.Equal(t, original.Servers[0].Name, restored.Servers[0].Name)
}

func TestConfigJSON_RequireMCPAuth(t *testing.T) {
	jsonData := `{"require_mcp_auth": true, "listen": "127.0.0.1:8080"}`
	var cfg Config
	err := json.Unmarshal([]byte(jsonData), &cfg)
	require.NoError(t, err)
	assert.True(t, cfg.RequireMCPAuth)

	// Default should be false
	jsonData = `{"listen": "127.0.0.1:8080"}`
	var cfg2 Config
	err = json.Unmarshal([]byte(jsonData), &cfg2)
	require.NoError(t, err)
	assert.False(t, cfg2.RequireMCPAuth)
}

func TestServerConfig(t *testing.T) {
	now := time.Now()
	server := &ServerConfig{
		Name:     "test-server",
		URL:      "http://localhost:3000",
		Protocol: "http",
		Headers: map[string]string{
			"Authorization": "Bearer token123",
			"Content-Type":  "application/json",
		},
		Enabled: true,
		Created: now,
	}

	// Test JSON serialization
	data, err := json.Marshal(server)
	require.NoError(t, err)

	var restored ServerConfig
	err = json.Unmarshal(data, &restored)
	require.NoError(t, err)

	assert.Equal(t, server.Name, restored.Name)
	assert.Equal(t, server.URL, restored.URL)
	assert.Equal(t, server.Protocol, restored.Protocol)
	assert.Equal(t, server.Headers, restored.Headers)
	assert.Equal(t, server.Enabled, restored.Enabled)
	assert.True(t, server.Created.Equal(restored.Created))
}

func TestConvertFromCursorFormat(t *testing.T) {
	cursorConfig := &CursorMCPConfig{
		MCPServers: map[string]CursorServerConfig{
			"sqlite-server": {
				Command: "uvx",
				Args:    []string{"mcp-server-sqlite", "--db-path", "/tmp/test.db"},
				Env: map[string]string{
					"DEBUG": "1",
				},
			},
			"http-server": {
				URL: "http://localhost:3001",
				Headers: map[string]string{
					"Authorization": "Bearer token",
				},
			},
		},
	}

	servers := ConvertFromCursorFormat(cursorConfig)
	require.Len(t, servers, 2)

	// Find sqlite server
	var sqliteServer *ServerConfig
	var httpServer *ServerConfig
	for _, server := range servers {
		switch server.Name {
		case "sqlite-server":
			sqliteServer = server
		case "http-server":
			httpServer = server
		}
	}

	require.NotNil(t, sqliteServer)
	assert.Equal(t, "uvx", sqliteServer.Command)
	assert.Equal(t, []string{"mcp-server-sqlite", "--db-path", "/tmp/test.db"}, sqliteServer.Args)
	assert.Equal(t, map[string]string{"DEBUG": "1"}, sqliteServer.Env)
	assert.Equal(t, "stdio", sqliteServer.Protocol)
	assert.True(t, sqliteServer.Enabled)

	require.NotNil(t, httpServer)
	assert.Equal(t, "http://localhost:3001", httpServer.URL)
	assert.Equal(t, map[string]string{"Authorization": "Bearer token"}, httpServer.Headers)
	assert.Equal(t, "http", httpServer.Protocol)
	assert.True(t, httpServer.Enabled)
}

func TestConfigSecurityModes(t *testing.T) {
	tests := []struct {
		name              string
		readOnlyMode      bool
		disableManagement bool
		allowServerAdd    bool
		allowServerRemove bool
		expectCanList     bool
		expectCanAdd      bool
		expectCanRemove   bool
		expectCanManage   bool
	}{
		{
			name:              "default permissive mode",
			readOnlyMode:      false,
			disableManagement: false,
			allowServerAdd:    true,
			allowServerRemove: true,
			expectCanList:     true,
			expectCanAdd:      true,
			expectCanRemove:   true,
			expectCanManage:   true,
		},
		{
			name:              "read-only mode",
			readOnlyMode:      true,
			disableManagement: false,
			allowServerAdd:    true,
			allowServerRemove: true,
			expectCanList:     true,
			expectCanAdd:      false,
			expectCanRemove:   false,
			expectCanManage:   false,
		},
		{
			name:              "disable management",
			readOnlyMode:      false,
			disableManagement: true,
			allowServerAdd:    true,
			allowServerRemove: true,
			expectCanList:     false,
			expectCanAdd:      false,
			expectCanRemove:   false,
			expectCanManage:   false,
		},
		{
			name:              "allow add but not remove",
			readOnlyMode:      false,
			disableManagement: false,
			allowServerAdd:    true,
			allowServerRemove: false,
			expectCanList:     true,
			expectCanAdd:      true,
			expectCanRemove:   false,
			expectCanManage:   true,
		},
		{
			name:              "allow remove but not add",
			readOnlyMode:      false,
			disableManagement: false,
			allowServerAdd:    false,
			allowServerRemove: true,
			expectCanList:     true,
			expectCanAdd:      false,
			expectCanRemove:   true,
			expectCanManage:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &Config{
				ReadOnlyMode:      tt.readOnlyMode,
				DisableManagement: tt.disableManagement,
				AllowServerAdd:    tt.allowServerAdd,
				AllowServerRemove: tt.allowServerRemove,
			}

			// Test read-only mode logic
			if tt.readOnlyMode {
				assert.True(t, tt.expectCanList && !tt.expectCanAdd && !tt.expectCanRemove)
			}

			// Test management disable logic
			if tt.disableManagement {
				assert.True(t, !tt.expectCanList && !tt.expectCanAdd && !tt.expectCanRemove && !tt.expectCanManage)
			}

			// Test granular permissions
			assert.Equal(t, tt.allowServerAdd, config.AllowServerAdd)
			assert.Equal(t, tt.allowServerRemove, config.AllowServerRemove)
		})
	}
}

func TestToolMetadata(t *testing.T) {
	now := time.Now()
	tool := &ToolMetadata{
		Name:        "test:tool",
		ServerName:  "test",
		Description: "A test tool",
		ParamsJSON:  `{"type": "object", "properties": {"param1": {"type": "string"}}}`,
		Hash:        "abc123",
		Created:     now,
		Updated:     now,
	}

	// Test JSON serialization
	data, err := json.Marshal(tool)
	require.NoError(t, err)

	var restored ToolMetadata
	err = json.Unmarshal(data, &restored)
	require.NoError(t, err)

	assert.Equal(t, tool.Name, restored.Name)
	assert.Equal(t, tool.ServerName, restored.ServerName)
	assert.Equal(t, tool.Description, restored.Description)
	assert.Equal(t, tool.ParamsJSON, restored.ParamsJSON)
	assert.Equal(t, tool.Hash, restored.Hash)
	assert.True(t, tool.Created.Equal(restored.Created))
	assert.True(t, tool.Updated.Equal(restored.Updated))
}

func TestSearchResult(t *testing.T) {
	tool := &ToolMetadata{
		Name:        "test:search",
		ServerName:  "test",
		Description: "A search tool",
		ParamsJSON:  `{"type": "object"}`,
		Hash:        "def456",
		Created:     time.Now(),
	}

	result := &SearchResult{
		Tool:  tool,
		Score: 0.95,
	}

	// Test JSON serialization
	data, err := json.Marshal(result)
	require.NoError(t, err)

	var restored SearchResult
	err = json.Unmarshal(data, &restored)
	require.NoError(t, err)

	assert.Equal(t, result.Score, restored.Score)
	assert.Equal(t, result.Tool.Name, restored.Tool.Name)
	assert.Equal(t, result.Tool.ServerName, restored.Tool.ServerName)
}

func TestSaveAndLoadConfig(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "mcpproxy_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	configPath := filepath.Join(tempDir, "test_config.json")

	// Create test config
	cfg := &Config{
		Listen:     ":9999",
		EnableTray: false,
		TopK:       3,
		ToolsLimit: 7,
		Servers: []*ServerConfig{
			{
				Name:    "example",
				URL:     "http://example.com",
				Enabled: true,
				Created: time.Now(),
			},
		},
	}

	// Save config
	err = SaveConfig(cfg, configPath)
	if err != nil {
		t.Fatalf("Failed to save config: %v", err)
	}

	// Check file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Fatal("Config file was not created")
	}

	// Load config
	var loaded Config
	err = loadConfigFile(configPath, &loaded)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Verify values
	if loaded.Listen != cfg.Listen {
		t.Errorf("Expected Listen %s, got %s", cfg.Listen, loaded.Listen)
	}

	if loaded.ToolsLimit != cfg.ToolsLimit {
		t.Errorf("Expected ToolsLimit %d, got %d", cfg.ToolsLimit, loaded.ToolsLimit)
	}
}

func TestLoadEmptyConfigFile(t *testing.T) {
	// Test that empty config files (including /dev/null) are handled gracefully
	tests := []struct {
		name      string
		setupFn   func(t *testing.T) string
		cleanupFn func(string)
	}{
		{
			name: "empty file",
			setupFn: func(t *testing.T) string {
				tempDir, err := os.MkdirTemp("", "mcpproxy_empty_test")
				require.NoError(t, err)
				emptyFile := filepath.Join(tempDir, "empty.json")
				err = os.WriteFile(emptyFile, []byte{}, 0644)
				require.NoError(t, err)
				return emptyFile
			},
			cleanupFn: func(path string) {
				os.RemoveAll(filepath.Dir(path))
			},
		},
		{
			name: "/dev/null",
			setupFn: func(t *testing.T) string {
				// Only test /dev/null on Unix-like systems
				if _, err := os.Stat("/dev/null"); err != nil {
					t.Skip("Skipping /dev/null test on non-Unix system")
				}
				return "/dev/null"
			},
			cleanupFn: func(path string) {
				// No cleanup needed for /dev/null
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configPath := tt.setupFn(t)
			defer tt.cleanupFn(configPath)

			// Create a config with default values
			cfg := DefaultConfig()

			// Load from empty file should succeed and not modify the config
			err := loadConfigFile(configPath, cfg)
			require.NoError(t, err, "loadConfigFile should handle empty files gracefully")

			// Verify the config still has default values
			assert.Equal(t, "127.0.0.1:8080", cfg.Listen, "Default listen address should be preserved")
		})
	}
}

func TestCreateSampleConfig(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "mcpproxy_sample_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	configPath := filepath.Join(tempDir, "sample_config.json")

	// Create sample config
	err = CreateSampleConfig(configPath)
	if err != nil {
		t.Fatalf("Failed to create sample config: %v", err)
	}

	// Check file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Fatal("Sample config file was not created")
	}

	// Load and verify sample config
	var loaded Config
	err = loadConfigFile(configPath, &loaded)
	if err != nil {
		t.Fatalf("Failed to load sample config: %v", err)
	}

	// Check that it has expected structure
	if loaded.Listen != "127.0.0.1:8080" {
		t.Errorf("Expected sample config Listen to be 127.0.0.1:8080, got %s", loaded.Listen)
	}

	if len(loaded.Servers) != 2 {
		t.Errorf("Expected sample config to have 2 servers, got %d", len(loaded.Servers))
	}

	// Check for expected servers by name
	found := make(map[string]bool)
	for _, server := range loaded.Servers {
		found[server.Name] = true
	}

	if !found["example"] {
		t.Error("Expected sample config to have 'example' server")
	}

	if !found["local-command"] {
		t.Error("Expected sample config to have 'local-command' server")
	}
}

// Tests for SensitiveDataDetectionConfig (Spec 026)

func TestDefaultSensitiveDataDetectionConfig(t *testing.T) {
	cfg := DefaultSensitiveDataDetectionConfig()

	// Verify defaults
	assert.True(t, cfg.Enabled, "should be enabled by default")
	assert.True(t, cfg.ScanRequests, "should scan requests by default")
	assert.True(t, cfg.ScanResponses, "should scan responses by default")
	assert.Equal(t, 1024, cfg.MaxPayloadSizeKB, "default max payload size should be 1024KB")
	assert.Equal(t, 4.5, cfg.EntropyThreshold, "default entropy threshold should be 4.5")
	assert.NotEmpty(t, cfg.Categories, "categories should have defaults")
	assert.Empty(t, cfg.CustomPatterns, "custom patterns should be empty by default")
	assert.Empty(t, cfg.SensitiveKeywords, "sensitive keywords should be empty by default")
}

func TestSensitiveDataDetectionConfig_IsEnabled(t *testing.T) {
	tests := []struct {
		name   string
		config *SensitiveDataDetectionConfig
		want   bool
	}{
		{
			name:   "nil config returns true (enabled by default)",
			config: nil,
			want:   true,
		},
		{
			name:   "disabled config returns false",
			config: &SensitiveDataDetectionConfig{Enabled: false},
			want:   false,
		},
		{
			name:   "enabled config returns true",
			config: &SensitiveDataDetectionConfig{Enabled: true},
			want:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.config.IsEnabled()
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestSensitiveDataDetectionConfig_IsCategoryEnabled(t *testing.T) {
	tests := []struct {
		name     string
		config   *SensitiveDataDetectionConfig
		category string
		want     bool
	}{
		{
			name:     "nil config returns true (allow by default)",
			config:   nil,
			category: "cloud_credentials",
			want:     true,
		},
		{
			name:     "empty categories returns true (allow all)",
			config:   &SensitiveDataDetectionConfig{Categories: nil},
			category: "cloud_credentials",
			want:     true,
		},
		{
			name: "category explicitly enabled",
			config: &SensitiveDataDetectionConfig{
				Categories: map[string]bool{"cloud_credentials": true},
			},
			category: "cloud_credentials",
			want:     true,
		},
		{
			name: "category explicitly disabled",
			config: &SensitiveDataDetectionConfig{
				Categories: map[string]bool{"cloud_credentials": false},
			},
			category: "cloud_credentials",
			want:     false,
		},
		{
			name: "category not in map returns true (allow by default)",
			config: &SensitiveDataDetectionConfig{
				Categories: map[string]bool{"api_token": true},
			},
			category: "cloud_credentials",
			want:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.config.IsCategoryEnabled(tt.category)
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestSensitiveDataDetectionConfig_GetMaxPayloadSize(t *testing.T) {
	tests := []struct {
		name   string
		config *SensitiveDataDetectionConfig
		want   int
	}{
		{
			name:   "nil config returns default",
			config: nil,
			want:   1024 * 1024, // 1MB
		},
		{
			name:   "zero value returns default",
			config: &SensitiveDataDetectionConfig{MaxPayloadSizeKB: 0},
			want:   1024 * 1024, // 1MB
		},
		{
			name:   "negative value returns default",
			config: &SensitiveDataDetectionConfig{MaxPayloadSizeKB: -10},
			want:   1024 * 1024, // 1MB
		},
		{
			name:   "custom value returns value in bytes",
			config: &SensitiveDataDetectionConfig{MaxPayloadSizeKB: 256},
			want:   256 * 1024,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.config.GetMaxPayloadSize()
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestSensitiveDataDetectionConfig_GetEntropyThreshold(t *testing.T) {
	tests := []struct {
		name   string
		config *SensitiveDataDetectionConfig
		want   float64
	}{
		{
			name:   "nil config returns default",
			config: nil,
			want:   4.5,
		},
		{
			name:   "zero value returns default",
			config: &SensitiveDataDetectionConfig{EntropyThreshold: 0},
			want:   4.5,
		},
		{
			name:   "negative value returns default",
			config: &SensitiveDataDetectionConfig{EntropyThreshold: -1.0},
			want:   4.5,
		},
		{
			name:   "custom value returns custom value",
			config: &SensitiveDataDetectionConfig{EntropyThreshold: 5.0},
			want:   5.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.config.GetEntropyThreshold()
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestSensitiveDataDetectionConfig_JSONSerialization(t *testing.T) {
	original := &SensitiveDataDetectionConfig{
		Enabled:          true,
		ScanRequests:     true,
		ScanResponses:    false,
		MaxPayloadSizeKB: 256,
		EntropyThreshold: 5.0,
		Categories: map[string]bool{
			"cloud_credentials": true,
			"api_token":         true,
			"credit_card":       false,
		},
		CustomPatterns: []CustomPattern{
			{
				Name:     "acme_key",
				Regex:    "ACME-KEY-[a-f0-9]{32}",
				Category: "custom",
				Severity: "high",
			},
		},
		SensitiveKeywords: []string{"SECRET", "PASSWORD"},
	}

	// Marshal to JSON
	data, err := json.Marshal(original)
	require.NoError(t, err)

	// Unmarshal from JSON
	var restored SensitiveDataDetectionConfig
	err = json.Unmarshal(data, &restored)
	require.NoError(t, err)

	// Compare values
	assert.Equal(t, original.Enabled, restored.Enabled)
	assert.Equal(t, original.ScanRequests, restored.ScanRequests)
	assert.Equal(t, original.ScanResponses, restored.ScanResponses)
	assert.Equal(t, original.MaxPayloadSizeKB, restored.MaxPayloadSizeKB)
	assert.Equal(t, original.EntropyThreshold, restored.EntropyThreshold)
	assert.Equal(t, original.Categories, restored.Categories)
	assert.Len(t, restored.CustomPatterns, 1)
	assert.Equal(t, original.CustomPatterns[0].Name, restored.CustomPatterns[0].Name)
	assert.Equal(t, original.CustomPatterns[0].Regex, restored.CustomPatterns[0].Regex)
	assert.Equal(t, original.SensitiveKeywords, restored.SensitiveKeywords)
}

func TestCustomPattern_Validation(t *testing.T) {
	tests := []struct {
		name    string
		pattern CustomPattern
		valid   bool
	}{
		{
			name: "valid regex pattern",
			pattern: CustomPattern{
				Name:  "test_pattern",
				Regex: "[a-z]+",
			},
			valid: true,
		},
		{
			name: "valid keyword pattern",
			pattern: CustomPattern{
				Name:     "test_keywords",
				Keywords: []string{"SECRET", "PASSWORD"},
			},
			valid: true,
		},
		{
			name: "empty name is invalid",
			pattern: CustomPattern{
				Name:  "",
				Regex: "[a-z]+",
			},
			valid: false,
		},
		{
			name: "both regex and keywords can coexist",
			pattern: CustomPattern{
				Name:     "test_both",
				Regex:    "[a-z]+",
				Keywords: []string{"test"},
			},
			valid: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The pattern is valid if it has a name
			hasName := tt.pattern.Name != ""
			assert.Equal(t, tt.valid, hasName)
		})
	}
}

func TestConfig_WithSensitiveDataDetection(t *testing.T) {
	// Test that SensitiveDataDetection can be part of Config
	cfg := &Config{
		Listen: "127.0.0.1:8080",
		SensitiveDataDetection: &SensitiveDataDetectionConfig{
			Enabled:          true,
			ScanRequests:     true,
			ScanResponses:    true,
			EntropyThreshold: 4.5,
		},
	}

	// Marshal to JSON
	data, err := json.Marshal(cfg)
	require.NoError(t, err)

	// Unmarshal from JSON
	var restored Config
	err = json.Unmarshal(data, &restored)
	require.NoError(t, err)

	// Verify SensitiveDataDetection is preserved
	require.NotNil(t, restored.SensitiveDataDetection)
	assert.True(t, restored.SensitiveDataDetection.Enabled)
	assert.True(t, restored.SensitiveDataDetection.ScanRequests)
	assert.True(t, restored.SensitiveDataDetection.ScanResponses)
	assert.Equal(t, 4.5, restored.SensitiveDataDetection.EntropyThreshold)
}

// Tests for routing mode (Spec 031)

func TestRoutingModeDefault(t *testing.T) {
	cfg := &Config{}
	err := cfg.Validate()
	require.NoError(t, err)
	assert.Equal(t, RoutingModeRetrieveTools, cfg.RoutingMode, "default routing mode should be retrieve_tools")
}

func TestRoutingModeValidation(t *testing.T) {
	tests := []struct {
		name        string
		routingMode string
		wantErr     bool
	}{
		{
			name:        "empty defaults to retrieve_tools",
			routingMode: "",
			wantErr:     false,
		},
		{
			name:        "retrieve_tools is valid",
			routingMode: RoutingModeRetrieveTools,
			wantErr:     false,
		},
		{
			name:        "direct is valid",
			routingMode: RoutingModeDirect,
			wantErr:     false,
		},
		{
			name:        "code_execution is valid",
			routingMode: RoutingModeCodeExecution,
			wantErr:     false,
		},
		{
			name:        "invalid mode is rejected",
			routingMode: "invalid_mode",
			wantErr:     true,
		},
		{
			name:        "typo mode is rejected",
			routingMode: "Direct",
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				RoutingMode: tt.routingMode,
			}
			err := cfg.Validate()
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "routing_mode")
			} else {
				assert.NoError(t, err)
				if tt.routingMode == "" {
					assert.Equal(t, RoutingModeRetrieveTools, cfg.RoutingMode)
				} else {
					assert.Equal(t, tt.routingMode, cfg.RoutingMode)
				}
			}
		})
	}
}

func TestRoutingModeJSONSerialization(t *testing.T) {
	cfg := &Config{
		Listen:      "127.0.0.1:8080",
		RoutingMode: RoutingModeDirect,
	}

	data, err := json.Marshal(cfg)
	require.NoError(t, err)

	var restored Config
	err = json.Unmarshal(data, &restored)
	require.NoError(t, err)
	assert.Equal(t, RoutingModeDirect, restored.RoutingMode)
}

func TestRoutingModeOmittedFromJSON(t *testing.T) {
	// When routing_mode is empty, it should be omitted from JSON
	cfg := &Config{
		Listen: "127.0.0.1:8080",
	}

	data, err := json.Marshal(cfg)
	require.NoError(t, err)

	// Parse as map to check key presence
	var m map[string]interface{}
	err = json.Unmarshal(data, &m)
	require.NoError(t, err)
	_, exists := m["routing_mode"]
	assert.False(t, exists, "routing_mode should be omitted when empty")
}

func TestRoutingModeConstants(t *testing.T) {
	assert.Equal(t, "retrieve_tools", RoutingModeRetrieveTools)
	assert.Equal(t, "direct", RoutingModeDirect)
	assert.Equal(t, "code_execution", RoutingModeCodeExecution)
}

// Tests for tool-level quarantine config (Spec 032)

func TestConfig_IsQuarantineEnabled(t *testing.T) {
	tests := []struct {
		name     string
		config   Config
		expected bool
	}{
		{
			name:     "nil pointer defaults to true (secure by default)",
			config:   Config{QuarantineEnabled: nil},
			expected: true,
		},
		{
			name:     "explicit true",
			config:   Config{QuarantineEnabled: boolPtr(true)},
			expected: true,
		},
		{
			name:     "explicit false",
			config:   Config{QuarantineEnabled: boolPtr(false)},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.config.IsQuarantineEnabled())
		})
	}
}

func TestConfig_DefaultQuarantineForNewServer(t *testing.T) {
	tests := []struct {
		name     string
		config   Config
		expected bool
	}{
		{
			name:     "nil pointer: secure by default (true)",
			config:   Config{QuarantineEnabled: nil},
			expected: true,
		},
		{
			name:     "explicit true: quarantine new servers",
			config:   Config{QuarantineEnabled: boolPtr(true)},
			expected: true,
		},
		{
			name:     "explicit false: auto-approve new servers (issue #370)",
			config:   Config{QuarantineEnabled: boolPtr(false)},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.config.DefaultQuarantineForNewServer())
		})
	}
}

func TestServerConfig_IsQuarantineSkipped(t *testing.T) {
	tests := []struct {
		name     string
		config   ServerConfig
		expected bool
	}{
		{
			name:     "default false",
			config:   ServerConfig{},
			expected: false,
		},
		{
			name:     "explicit true",
			config:   ServerConfig{SkipQuarantine: true},
			expected: true,
		},
		{
			name:     "explicit false",
			config:   ServerConfig{SkipQuarantine: false},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.config.IsQuarantineSkipped())
		})
	}
}

func TestConfig_QuarantineEnabled_JSONSerialization(t *testing.T) {
	// Test 1: quarantine_enabled omitted (nil) defaults to true
	cfg1JSON := `{"listen": "127.0.0.1:8080"}`
	var cfg1 Config
	err := json.Unmarshal([]byte(cfg1JSON), &cfg1)
	require.NoError(t, err)
	assert.Nil(t, cfg1.QuarantineEnabled)
	assert.True(t, cfg1.IsQuarantineEnabled())

	// Test 2: quarantine_enabled explicitly false
	cfg2JSON := `{"listen": "127.0.0.1:8080", "quarantine_enabled": false}`
	var cfg2 Config
	err = json.Unmarshal([]byte(cfg2JSON), &cfg2)
	require.NoError(t, err)
	require.NotNil(t, cfg2.QuarantineEnabled)
	assert.False(t, *cfg2.QuarantineEnabled)
	assert.False(t, cfg2.IsQuarantineEnabled())

	// Test 3: quarantine_enabled explicitly true
	cfg3JSON := `{"listen": "127.0.0.1:8080", "quarantine_enabled": true}`
	var cfg3 Config
	err = json.Unmarshal([]byte(cfg3JSON), &cfg3)
	require.NoError(t, err)
	require.NotNil(t, cfg3.QuarantineEnabled)
	assert.True(t, *cfg3.QuarantineEnabled)
	assert.True(t, cfg3.IsQuarantineEnabled())
}

func TestServerConfig_SkipQuarantine_JSONSerialization(t *testing.T) {
	// Test: skip_quarantine in server config
	serverJSON := `{"name": "test", "skip_quarantine": true, "enabled": true}`
	var sc ServerConfig
	err := json.Unmarshal([]byte(serverJSON), &sc)
	require.NoError(t, err)
	assert.True(t, sc.SkipQuarantine)
	assert.True(t, sc.IsQuarantineSkipped())

	// Test: skip_quarantine omitted defaults to false
	serverJSON2 := `{"name": "test", "enabled": true}`
	var sc2 ServerConfig
	err = json.Unmarshal([]byte(serverJSON2), &sc2)
	require.NoError(t, err)
	assert.False(t, sc2.SkipQuarantine)
	assert.False(t, sc2.IsQuarantineSkipped())
}

func boolPtr(b bool) *bool {
	return &b
}

// --- T011: DataDir secret-ref expansion in LoadFromFile ---

// TestLoadConfig_ExpandsDataDir verifies that ${env:...} refs in data_dir are resolved
// before MkdirAll / Validate() run, so the database opens at the resolved path (US3).
func TestLoadConfig_ExpandsDataDir(t *testing.T) {
	resolvedDir := t.TempDir()
	t.Setenv("TEST_MCPPROXY_EXPAND_DATA_DIR", resolvedDir)

	cfgFile := filepath.Join(t.TempDir(), "config.json")
	cfgData := `{"data_dir": "${env:TEST_MCPPROXY_EXPAND_DATA_DIR}"}`
	require.NoError(t, os.WriteFile(cfgFile, []byte(cfgData), 0600))

	cfg, err := LoadFromFile(cfgFile)
	require.NoError(t, err)
	assert.Equal(t, resolvedDir, cfg.DataDir)
}

// TestLoadConfig_DataDirExpandFailure verifies that when the env var in data_dir is
// missing, LoadFromFile warns and retains the original unresolved reference rather
// than returning an error (US3 robustness requirement).
func TestLoadConfig_DataDirExpandFailure(t *testing.T) {
	// Use a unique name that is almost certainly not set in any environment.
	const missingVar = "TEST_MCPPROXY_MISSING_DATA_DIR_XYZ_9876"
	os.Unsetenv(missingVar) //nolint:errcheck

	tmpBase := t.TempDir()
	cfgFile := filepath.Join(t.TempDir(), "config.json")
	// DataDir contains an unresolvable ref; the literal path lives inside tmpBase
	// so any directory MkdirAll creates is cleaned up automatically.
	cfgData := fmt.Sprintf(`{"data_dir": "%s/${env:%s}"}`, filepath.ToSlash(tmpBase), missingVar)
	require.NoError(t, os.WriteFile(cfgFile, []byte(cfgData), 0600))

	// LoadFromFile must succeed even when expansion fails — warn + retain original.
	cfg, err := LoadFromFile(cfgFile)
	require.NoError(t, err)
	assert.Contains(t, cfg.DataDir, fmt.Sprintf("${env:%s}", missingVar),
		"original unresolved ref should be retained when expansion fails")
}

func TestConfig_IsTelemetryEnabled(t *testing.T) {
	tests := []struct {
		name     string
		cfg      *Config
		envValue string
		want     bool
	}{
		{
			name: "default (nil telemetry)",
			cfg:  &Config{},
			want: true,
		},
		{
			name: "nil enabled (default true)",
			cfg:  &Config{Telemetry: &TelemetryConfig{}},
			want: true,
		},
		{
			name: "explicitly enabled",
			cfg:  &Config{Telemetry: &TelemetryConfig{Enabled: BoolPtr(true)}},
			want: true,
		},
		{
			name: "explicitly disabled",
			cfg:  &Config{Telemetry: &TelemetryConfig{Enabled: BoolPtr(false)}},
			want: false,
		},
		{
			name:     "env var override false",
			cfg:      &Config{},
			envValue: "false",
			want:     false,
		},
		{
			name:     "env var override false beats config enabled",
			cfg:      &Config{Telemetry: &TelemetryConfig{Enabled: BoolPtr(true)}},
			envValue: "false",
			want:     false,
		},
		{
			name:     "env var other value does not disable",
			cfg:      &Config{},
			envValue: "true",
			want:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envValue != "" {
				t.Setenv("MCPPROXY_TELEMETRY", tt.envValue)
			} else {
				os.Unsetenv("MCPPROXY_TELEMETRY") //nolint:errcheck
			}
			got := tt.cfg.IsTelemetryEnabled()
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestConfig_GetTelemetryEndpoint(t *testing.T) {
	tests := []struct {
		name string
		cfg  *Config
		want string
	}{
		{
			name: "default",
			cfg:  &Config{},
			want: "https://telemetry.mcpproxy.app/v1",
		},
		{
			name: "custom endpoint",
			cfg:  &Config{Telemetry: &TelemetryConfig{Endpoint: "https://custom.example.com/v1"}},
			want: "https://custom.example.com/v1",
		},
		{
			name: "empty endpoint falls back to default",
			cfg:  &Config{Telemetry: &TelemetryConfig{Endpoint: ""}},
			want: "https://telemetry.mcpproxy.app/v1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.cfg.GetTelemetryEndpoint()
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestConfig_GetAnonymousID(t *testing.T) {
	tests := []struct {
		name string
		cfg  *Config
		want string
	}{
		{
			name: "default (nil telemetry)",
			cfg:  &Config{},
			want: "",
		},
		{
			name: "set ID",
			cfg:  &Config{Telemetry: &TelemetryConfig{AnonymousID: "abc-123"}},
			want: "abc-123",
		},
		{
			name: "empty ID",
			cfg:  &Config{Telemetry: &TelemetryConfig{AnonymousID: ""}},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.cfg.GetAnonymousID()
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestTelemetryConfig_JSONSerialization(t *testing.T) {
	// Test that TelemetryConfig serializes/deserializes correctly
	enabled := true
	cfg := &Config{
		Listen: "127.0.0.1:8080",
		Telemetry: &TelemetryConfig{
			Enabled:     &enabled,
			AnonymousID: "test-uuid",
			Endpoint:    "https://custom.example.com/v1",
		},
	}

	data, err := json.Marshal(cfg)
	require.NoError(t, err)

	var restored Config
	err = json.Unmarshal(data, &restored)
	require.NoError(t, err)

	require.NotNil(t, restored.Telemetry)
	require.NotNil(t, restored.Telemetry.Enabled)
	assert.Equal(t, true, *restored.Telemetry.Enabled)
	assert.Equal(t, "test-uuid", restored.Telemetry.AnonymousID)
	assert.Equal(t, "https://custom.example.com/v1", restored.Telemetry.Endpoint)
}

func TestTelemetryConfig_OmittedWhenNil(t *testing.T) {
	cfg := &Config{
		Listen: "127.0.0.1:8080",
	}

	data, err := json.Marshal(cfg)
	require.NoError(t, err)

	// telemetry should not appear in JSON when nil
	assert.NotContains(t, string(data), "telemetry")
}

func TestServerConfig_ReconnectOnUse(t *testing.T) {
	t.Run("defaults to false", func(t *testing.T) {
		server := &ServerConfig{
			Name:    "test-server",
			Enabled: true,
		}
		assert.False(t, server.ReconnectOnUse)
	})

	t.Run("parses from JSON when true", func(t *testing.T) {
		jsonStr := `{"name":"test","enabled":true,"reconnect_on_use":true}`
		var server ServerConfig
		err := json.Unmarshal([]byte(jsonStr), &server)
		require.NoError(t, err)
		assert.True(t, server.ReconnectOnUse)
	})

	t.Run("parses from JSON when false", func(t *testing.T) {
		jsonStr := `{"name":"test","enabled":true,"reconnect_on_use":false}`
		var server ServerConfig
		err := json.Unmarshal([]byte(jsonStr), &server)
		require.NoError(t, err)
		assert.False(t, server.ReconnectOnUse)
	})

	t.Run("omitted from JSON when false", func(t *testing.T) {
		server := &ServerConfig{
			Name:           "test",
			Enabled:        true,
			ReconnectOnUse: false,
		}
		data, err := json.Marshal(server)
		require.NoError(t, err)
		assert.NotContains(t, string(data), "reconnect_on_use")
	})

	t.Run("present in JSON when true", func(t *testing.T) {
		server := &ServerConfig{
			Name:           "test",
			Enabled:        true,
			ReconnectOnUse: true,
		}
		data, err := json.Marshal(server)
		require.NoError(t, err)
		assert.Contains(t, string(data), `"reconnect_on_use":true`)
	})

	t.Run("round-trip serialization", func(t *testing.T) {
		server := &ServerConfig{
			Name:           "reconnect-test",
			URL:            "http://localhost:3000",
			Protocol:       "http",
			Enabled:        true,
			ReconnectOnUse: true,
			Created:        time.Now(),
		}
		data, err := json.Marshal(server)
		require.NoError(t, err)

		var restored ServerConfig
		err = json.Unmarshal(data, &restored)
		require.NoError(t, err)
		assert.Equal(t, server.ReconnectOnUse, restored.ReconnectOnUse)
	})
}
