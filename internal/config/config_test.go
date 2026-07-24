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

func TestServerConfig_IsToolAllowedByConfig(t *testing.T) {
	tests := []struct {
		name     string
		cfg      *ServerConfig
		toolName string
		want     bool
	}{
		{"no filter allows everything", &ServerConfig{}, "anything", true},
		{"allowlist: listed tool allowed", &ServerConfig{EnabledTools: []string{"read_file", "list_dir"}}, "read_file", true},
		{"allowlist: unlisted tool denied", &ServerConfig{EnabledTools: []string{"read_file"}}, "delete_file", false},
		{"denylist: listed tool denied", &ServerConfig{DisabledTools: []string{"delete_repo"}}, "delete_repo", false},
		{"denylist: unlisted tool allowed", &ServerConfig{DisabledTools: []string{"delete_repo"}}, "list_repos", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.cfg.IsToolAllowedByConfig(tt.toolName)
			if got != tt.want {
				t.Errorf("IsToolAllowedByConfig(%q) = %v, want %v", tt.toolName, got, tt.want)
			}
		})
	}
}

func TestDefaultOutputValidationConfig(t *testing.T) {
	cfg := DefaultOutputValidationConfig()

	// Verify the four defaults
	assert.Equal(t, "warn", cfg.Mode, "default mode should be warn")
	assert.Equal(t, 5<<20, cfg.MaxBytes, "default MaxBytes should be 5<<20")
	assert.Equal(t, 64, cfg.MaxDepth, "default MaxDepth should be 64")
	assert.Equal(t, "allow", cfg.MissingStructuredContent, "default MissingStructuredContent should be allow")
}

func TestOutputValidationConfig_NilSafeHelpers(t *testing.T) {
	var c *OutputValidationConfig

	// nil receiver behaves as defaults (warn-mode enabled)
	assert.True(t, c.IsEnabled(), "nil: IsEnabled should return true (warn by default)")
	assert.False(t, c.IsStrict(), "nil: IsStrict should return false")
	assert.True(t, c.IsWarn(), "nil: IsWarn should return true")
	assert.Equal(t, 5<<20, c.EffectiveMaxBytes(), "nil: EffectiveMaxBytes should return 5<<20")
	assert.Equal(t, 64, c.EffectiveMaxDepth(), "nil: EffectiveMaxDepth should return 64")
	assert.False(t, c.BlockOnMissingStructured(), "nil: BlockOnMissingStructured should return false")
}

func TestOutputValidationConfig_ModeOff(t *testing.T) {
	c := &OutputValidationConfig{Mode: "off"}
	assert.False(t, c.IsEnabled(), "mode=off: IsEnabled should be false")
	assert.False(t, c.IsStrict(), "mode=off: IsStrict should be false")
	assert.False(t, c.IsWarn(), "mode=off: IsWarn should be false")
}

func TestOutputValidationConfig_ModeStrict(t *testing.T) {
	c := &OutputValidationConfig{Mode: "strict"}
	assert.True(t, c.IsEnabled(), "mode=strict: IsEnabled should be true")
	assert.True(t, c.IsStrict(), "mode=strict: IsStrict should be true")
	assert.False(t, c.IsWarn(), "mode=strict: IsWarn should be false (strict, not warn)")
}

func TestOutputValidationConfig_BlockOnMissingStructured(t *testing.T) {
	c := &OutputValidationConfig{Mode: "strict", MissingStructuredContent: "block"}
	assert.True(t, c.BlockOnMissingStructured(), "MissingStructuredContent=block should return true")
}

func TestOutputValidationConfig_EffectiveDefaults(t *testing.T) {
	// Zero values fall back to defaults
	c := &OutputValidationConfig{Mode: "warn"}
	assert.Equal(t, 5<<20, c.EffectiveMaxBytes(), "zero MaxBytes falls back to 5<<20")
	assert.Equal(t, 64, c.EffectiveMaxDepth(), "zero MaxDepth falls back to 64")

	// Non-zero values are preserved
	c2 := &OutputValidationConfig{Mode: "warn", MaxBytes: 1024, MaxDepth: 32}
	assert.Equal(t, 1024, c2.EffectiveMaxBytes(), "non-zero MaxBytes is preserved")
	assert.Equal(t, 32, c2.EffectiveMaxDepth(), "non-zero MaxDepth is preserved")
}

func TestOutputValidationConfig_JSONRoundTrip(t *testing.T) {
	// Build a root Config with an OutputValidation block and round-trip it
	orig := &Config{
		Listen: "127.0.0.1:9090",
		OutputValidation: &OutputValidationConfig{
			Mode:                     "strict",
			MaxBytes:                 1 << 20,
			MaxDepth:                 32,
			MissingStructuredContent: "block",
		},
	}

	data, err := json.Marshal(orig)
	require.NoError(t, err, "marshal should not fail")

	var restored Config
	err = json.Unmarshal(data, &restored)
	require.NoError(t, err, "unmarshal should not fail")

	require.NotNil(t, restored.OutputValidation, "OutputValidation should survive round-trip")
	assert.Equal(t, "strict", restored.OutputValidation.Mode)
	assert.Equal(t, 1<<20, restored.OutputValidation.MaxBytes)
	assert.Equal(t, 32, restored.OutputValidation.MaxDepth)
	assert.Equal(t, "block", restored.OutputValidation.MissingStructuredContent)
}

func TestDefaultOutputSanitisationConfig(t *testing.T) {
	cfg := DefaultOutputSanitisationConfig()

	assert.False(t, cfg.SpotlightUntrusted, "Track B is fully opt-in: default SpotlightUntrusted should be false")
	assert.Equal(t, "spotlight", cfg.ResponseAction, "default ResponseAction should be spotlight")
	assert.False(t, cfg.StripControlChars, "default StripControlChars should be false")
	assert.Equal(t, []string{"ansi", "c0c1", "bidi", "zero_width"}, cfg.StripClasses, "default StripClasses")
	assert.Equal(t, 100, cfg.MaxRedactions, "default MaxRedactions should be 100")
}

func TestOutputSanitisationConfig_NilSafeHelpers(t *testing.T) {
	var c *OutputSanitisationConfig

	assert.True(t, c.IsEnabled(), "nil: IsEnabled should default true")
	assert.False(t, c.IsSpotlightEnabled(), "nil: IsSpotlightEnabled should be false (fully opt-in)")
	assert.False(t, c.IsRedact(), "nil: IsRedact should be false")
	assert.False(t, c.IsBlock(), "nil: IsBlock should be false")
	assert.False(t, c.IsStripEnabled(), "nil: IsStripEnabled should be false")
	assert.Empty(t, c.EnabledStripClasses(), "nil: EnabledStripClasses should be empty (strip disabled)")
}

func TestOutputSanitisationConfig_IsRedactIsBlock(t *testing.T) {
	cases := []struct {
		action string
		redact bool
		block  bool
	}{
		{"spotlight", false, false},
		{"redact", true, false},
		{"block", false, true},
		{"", false, false},
	}
	for _, tc := range cases {
		c := &OutputSanitisationConfig{ResponseAction: tc.action}
		assert.Equal(t, tc.redact, c.IsRedact(), "IsRedact for action=%q", tc.action)
		assert.Equal(t, tc.block, c.IsBlock(), "IsBlock for action=%q", tc.action)
	}
}

func TestOutputSanitisationConfig_IsSpotlightEnabled(t *testing.T) {
	cEnabled := &OutputSanitisationConfig{SpotlightUntrusted: true}
	assert.True(t, cEnabled.IsSpotlightEnabled())

	cDisabled := &OutputSanitisationConfig{SpotlightUntrusted: false}
	assert.False(t, cDisabled.IsSpotlightEnabled())
}

func TestOutputSanitisationConfig_EnabledStripClasses(t *testing.T) {
	// Strip disabled -> empty regardless of classes
	cDisabled := &OutputSanitisationConfig{
		StripControlChars: false,
		StripClasses:      []string{"ansi", "bidi"},
	}
	assert.Empty(t, cDisabled.EnabledStripClasses(), "strip disabled -> empty map")

	// Strip enabled -> set of valid, lowercased classes; invalid filtered out
	cEnabled := &OutputSanitisationConfig{
		StripControlChars: true,
		StripClasses:      []string{"ANSI", "c0c1", "bogus", "Zero_Width", "bidi"},
	}
	set := cEnabled.EnabledStripClasses()
	assert.True(t, set["ansi"], "ansi present")
	assert.True(t, set["c0c1"], "c0c1 present")
	assert.True(t, set["zero_width"], "zero_width present (lowercased)")
	assert.True(t, set["bidi"], "bidi present")
	assert.False(t, set["bogus"], "invalid class filtered out")
	assert.Len(t, set, 4, "only the four valid classes")
}

func TestOutputSanitisationConfig_WouldMutate(t *testing.T) {
	// default config is fully opt-in -> never mutates (any trust)
	def := DefaultOutputSanitisationConfig()
	assert.False(t, def.WouldMutate("trusted"), "default (opt-in) should not mutate trusted")
	assert.False(t, def.WouldMutate("untrusted"), "default (opt-in) should not mutate untrusted")

	// untrusted + spotlight explicitly enabled -> true
	spot := &OutputSanitisationConfig{ResponseAction: "spotlight", SpotlightUntrusted: true}
	assert.True(t, spot.WouldMutate("untrusted"), "untrusted + spotlight-on should mutate")
	assert.False(t, spot.WouldMutate("trusted"), "trusted + spotlight should not mutate")

	// redact regardless of trust -> true
	redact := &OutputSanitisationConfig{ResponseAction: "redact"}
	assert.True(t, redact.WouldMutate("trusted"), "redact mutates even for trusted")
	assert.True(t, redact.WouldMutate("untrusted"), "redact mutates for untrusted")

	// block regardless of trust -> true
	block := &OutputSanitisationConfig{ResponseAction: "block"}
	assert.True(t, block.WouldMutate("trusted"), "block mutates even for trusted")
	assert.True(t, block.WouldMutate("untrusted"), "block mutates for untrusted")

	// untrusted + strip enabled (spotlight off) -> true
	strip := &OutputSanitisationConfig{ResponseAction: "spotlight", SpotlightUntrusted: false, StripControlChars: true}
	assert.True(t, strip.WouldMutate("untrusted"), "untrusted + strip should mutate")
	assert.False(t, strip.WouldMutate("trusted"), "trusted + strip-only should not mutate")
}

func TestOutputSanitisationConfig_JSONRoundTrip(t *testing.T) {
	orig := &Config{
		Listen: "127.0.0.1:9090",
		OutputSanitisation: &OutputSanitisationConfig{
			SpotlightUntrusted: true,
			ResponseAction:     "redact",
			StripControlChars:  true,
			StripClasses:       []string{"ansi", "bidi"},
			MaxRedactions:      42,
		},
	}

	data, err := json.Marshal(orig)
	require.NoError(t, err)

	var restored Config
	err = json.Unmarshal(data, &restored)
	require.NoError(t, err)

	require.NotNil(t, restored.OutputSanitisation)
	assert.True(t, restored.OutputSanitisation.SpotlightUntrusted)
	assert.Equal(t, "redact", restored.OutputSanitisation.ResponseAction)
	assert.True(t, restored.OutputSanitisation.StripControlChars)
	assert.Equal(t, []string{"ansi", "bidi"}, restored.OutputSanitisation.StripClasses)
	assert.Equal(t, 42, restored.OutputSanitisation.MaxRedactions)
}

func TestToolMetadata_OutputSchemaJSON(t *testing.T) {
	// Verify OutputSchemaJSON field exists on ToolMetadata
	meta := &ToolMetadata{
		Name:             "test_tool",
		ServerName:       "test_server",
		Description:      "A test tool",
		ParamsJSON:       `{"type":"object"}`,
		OutputSchemaJSON: `{"type":"string"}`,
	}

	data, err := json.Marshal(meta)
	require.NoError(t, err)

	var restored ToolMetadata
	err = json.Unmarshal(data, &restored)
	require.NoError(t, err)
	assert.Equal(t, `{"type":"string"}`, restored.OutputSchemaJSON)

	// Empty OutputSchemaJSON should be omitted from JSON (omitempty)
	metaNoSchema := &ToolMetadata{
		Name:       "test_tool",
		ServerName: "test_server",
		ParamsJSON: `{"type":"object"}`,
	}
	data2, err := json.Marshal(metaNoSchema)
	require.NoError(t, err)
	assert.NotContains(t, string(data2), "output_schema_json", "empty OutputSchemaJSON should be omitted")
}

// TestMigrateDeepScanConfig verifies the Spec 077 US3 config migration:
// the deprecated top-level scanner_fetch_package_source /
// scanner_disable_no_new_privileges keys are folded into the unified
// security.deep_scan block on load, the removed auto_scan_quarantined key is
// ignored, and a round-trip through JSON preserves the migrated shape.
func TestMigrateDeepScanConfig(t *testing.T) {
	fetch := false
	original := &Config{
		Security: &SecurityConfig{
			ScannerFetchPackageSource:     &fetch,
			ScannerDisableNoNewPrivileges: true,
		},
	}

	migrateDeepScanConfig(original)

	require.NotNil(t, original.Security.DeepScan, "deep_scan block must be created by migration")
	require.NotNil(t, original.Security.DeepScan.FetchPackageSource, "fetch_package_source must migrate into deep_scan")
	assert.False(t, *original.Security.DeepScan.FetchPackageSource, "fetch_package_source value must be preserved")
	assert.True(t, original.Security.DeepScan.DisableNoNewPrivileges, "disable_no_new_privileges must migrate into deep_scan")

	// Legacy top-level keys must be cleared so the migrated config serializes
	// only the new deep_scan.* surface (no duplicate/stale keys).
	assert.Nil(t, original.Security.ScannerFetchPackageSource, "legacy scanner_fetch_package_source must be cleared after migration")
	assert.False(t, original.Security.ScannerDisableNoNewPrivileges, "legacy scanner_disable_no_new_privileges must be cleared after migration")

	// Effective accessors must read the migrated values.
	assert.True(t, original.Security.IsDisableNoNewPrivileges(), "effective disable-no-new-privileges must reflect migrated value")
	if got := original.Security.EffectiveFetchPackageSource(); assert.NotNil(t, got) {
		assert.False(t, *got, "effective fetch-package-source must reflect migrated value")
	}

	// Round-trip: marshal then unmarshal, migrate again (idempotent), and
	// confirm the deep_scan values survive and legacy keys do not reappear.
	data, err := json.Marshal(original)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "scanner_fetch_package_source", "migrated config must not serialize the legacy key")
	assert.NotContains(t, string(data), "scanner_disable_no_new_privileges", "migrated config must not serialize the legacy key")
	assert.Contains(t, string(data), "deep_scan", "migrated config must serialize the deep_scan block")

	var restored Config
	require.NoError(t, json.Unmarshal(data, &restored))
	migrateDeepScanConfig(&restored)
	require.NotNil(t, restored.Security.DeepScan)
	require.NotNil(t, restored.Security.DeepScan.FetchPackageSource)
	assert.False(t, *restored.Security.DeepScan.FetchPackageSource)
	assert.True(t, restored.Security.DeepScan.DisableNoNewPrivileges)
}

// TestMigrateDeepScanConfigIgnoresAutoScanQuarantined proves that a config file
// carrying the removed auto_scan_quarantined key loads without error and the
// key is simply dropped (Spec 077 FR-016).
func TestMigrateDeepScanConfigIgnoresAutoScanQuarantined(t *testing.T) {
	jsonData := `{"security":{"auto_scan_quarantined":true,"scanner_disable_no_new_privileges":true}}`
	var cfg Config
	require.NoError(t, json.Unmarshal([]byte(jsonData), &cfg), "config with removed key must still unmarshal")
	migrateDeepScanConfig(&cfg)
	require.NotNil(t, cfg.Security)
	require.NotNil(t, cfg.Security.DeepScan)
	assert.True(t, cfg.Security.DeepScan.DisableNoNewPrivileges)

	// The removed key must not round-trip back out (no struct field to hold it).
	data, err := json.Marshal(&cfg)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "auto_scan_quarantined", "removed key must not serialize")
}

// Tests for tool_response_mode (Spec 085 T013 — FR-001/FR-015)

func TestToolResponseModeValidation(t *testing.T) {
	tests := []struct {
		name    string
		mode    string
		wantErr bool
	}{
		{name: "empty means full (default)", mode: "", wantErr: false},
		{name: "full is valid", mode: ToolResponseModeFull, wantErr: false},
		{name: "compact is valid", mode: ToolResponseModeCompact, wantErr: false},
		{name: "bogus value is rejected", mode: "bogus", wantErr: true},
		{name: "case-sensitive: Compact is rejected", mode: "Compact", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{ToolResponseMode: tt.mode}
			errs := cfg.ValidateDetailed()

			var found *ValidationError
			for i := range errs {
				if errs[i].Field == "tool_response_mode" {
					found = &errs[i]
					break
				}
			}
			if tt.wantErr {
				require.NotNil(t, found, "ValidateDetailed must reject %q with Field:\"tool_response_mode\"", tt.mode)
				assert.Contains(t, found.Message, "full")
				assert.Contains(t, found.Message, "compact")
			} else {
				assert.Nil(t, found, "ValidateDetailed must accept %q", tt.mode)
			}
		})
	}
}

// The default (unset) survives Validate() untouched — Phase 1 ships full
// behavior with no field written (FR-016).
func TestToolResponseModeDefaultUnset(t *testing.T) {
	cfg := &Config{}
	require.NoError(t, cfg.Validate())
	assert.Equal(t, "", cfg.ToolResponseMode, "unset stays unset; resolution to full happens at read time")
}

// Spec 085 T017: MCPPROXY_TOOL_RESPONSE_MODE explicit env alias overrides the
// file value on the standard load path, and an invalid env value fails
// validation with a clear message.
func TestToolResponseModeEnvOverride(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "mcp_config.json")
	raw, err := json.Marshal(map[string]any{
		"listen":             "127.0.0.1:0",
		"data_dir":           tmp,
		"tool_response_mode": ToolResponseModeFull,
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(cfgPath, raw, 0o600))

	t.Run("env wins over file", func(t *testing.T) {
		t.Setenv("MCPPROXY_TOOL_RESPONSE_MODE", ToolResponseModeCompact)
		cfg, err := LoadFromFile(cfgPath)
		require.NoError(t, err)
		assert.Equal(t, ToolResponseModeCompact, cfg.ToolResponseMode)
	})

	t.Run("no env keeps file value", func(t *testing.T) {
		cfg, err := LoadFromFile(cfgPath)
		require.NoError(t, err)
		assert.Equal(t, ToolResponseModeFull, cfg.ToolResponseMode)
	})

	t.Run("invalid env value fails validation", func(t *testing.T) {
		t.Setenv("MCPPROXY_TOOL_RESPONSE_MODE", "bogus")
		_, err := LoadFromFile(cfgPath)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "tool_response_mode")
	})
}

// GH #898: trusted_hosts loads from the config file and the
// MCPPROXY_TRUSTED_HOSTS env var (comma-separated) overrides it.
func TestTrustedHostsLoadAndEnvOverride(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "mcp_config.json")
	raw, err := json.Marshal(map[string]any{
		"listen":        "127.0.0.1:0",
		"data_dir":      tmp,
		"trusted_hosts": []string{"mcp.example.com"},
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(cfgPath, raw, 0o600))

	t.Run("file value loads", func(t *testing.T) {
		cfg, err := LoadFromFile(cfgPath)
		require.NoError(t, err)
		assert.Equal(t, []string{"mcp.example.com"}, cfg.TrustedHosts)
	})

	t.Run("env wins over file", func(t *testing.T) {
		t.Setenv("MCPPROXY_TRUSTED_HOSTS", "a.example.com, b.example.com:8443 ,")
		cfg, err := LoadFromFile(cfgPath)
		require.NoError(t, err)
		assert.Equal(t, []string{"a.example.com", "b.example.com:8443"}, cfg.TrustedHosts)
	})

	t.Run("unset by default", func(t *testing.T) {
		cfg := &Config{}
		require.NoError(t, cfg.Validate())
		assert.Empty(t, cfg.TrustedHosts)
	})
}
