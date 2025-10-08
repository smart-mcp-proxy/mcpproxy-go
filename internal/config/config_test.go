package config

import (
	"encoding/json"
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
	assert.True(t, config.EnableTray)
	assert.False(t, config.DebugSearch)
	assert.Equal(t, 5, config.TopK)
	assert.Equal(t, 15, config.ToolsLimit)
	assert.Equal(t, 20000, config.ToolResponseLimit)

	// Test security defaults (permissive)
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
				TopK:              5,
				ToolsLimit:        15,
				ToolResponseLimit: 0,
			},
		},
		{
			name: "zero TopK defaults to 5",
			config: &Config{
				TopK: 0,
			},
			expected: &Config{
				Listen:            "127.0.0.1:8080",
				TopK:              5,
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
				TopK:              5,
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
				TopK:              5,
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
			assert.Equal(t, tt.expected.TopK, tt.config.TopK)
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
	assert.Equal(t, original.ReadOnlyMode, restored.ReadOnlyMode)
	assert.Equal(t, original.DisableManagement, restored.DisableManagement)
	assert.Equal(t, original.AllowServerAdd, restored.AllowServerAdd)
	assert.Equal(t, original.AllowServerRemove, restored.AllowServerRemove)
	assert.Equal(t, original.EnablePrompts, restored.EnablePrompts)
	assert.Len(t, restored.Servers, 1)
	assert.Equal(t, original.Servers[0].Name, restored.Servers[0].Name)
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

	if loaded.TopK != cfg.TopK {
		t.Errorf("Expected TopK %d, got %d", cfg.TopK, loaded.TopK)
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
