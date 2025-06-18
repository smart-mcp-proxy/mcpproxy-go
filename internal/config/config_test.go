package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Listen != ":8080" {
		t.Errorf("Expected default listen to be :8080, got %s", cfg.Listen)
	}

	if cfg.TopK != 5 {
		t.Errorf("Expected default TopK to be 5, got %d", cfg.TopK)
	}

	if cfg.ToolsLimit != 15 {
		t.Errorf("Expected default ToolsLimit to be 15, got %d", cfg.ToolsLimit)
	}

	if !cfg.EnableTray {
		t.Error("Expected default EnableTray to be true")
	}
}

func TestConfigValidation(t *testing.T) {
	cfg := &Config{
		Listen:     "",
		TopK:       0,
		ToolsLimit: 0,
	}

	err := cfg.Validate()
	if err != nil {
		t.Errorf("Unexpected validation error: %v", err)
	}

	// Check that defaults were applied
	if cfg.Listen != ":8080" {
		t.Errorf("Expected Listen to be set to default :8080, got %s", cfg.Listen)
	}

	if cfg.TopK != 5 {
		t.Errorf("Expected TopK to be set to default 5, got %d", cfg.TopK)
	}

	if cfg.ToolsLimit != 15 {
		t.Errorf("Expected ToolsLimit to be set to default 15, got %d", cfg.ToolsLimit)
	}
}

func TestConfigJSONMarshaling(t *testing.T) {
	cfg := &Config{
		Listen:     ":9090",
		EnableTray: false,
		TopK:       10,
		ToolsLimit: 20,
		Servers: []*ServerConfig{
			{
				Name:    "test",
				URL:     "http://localhost:8000",
				Enabled: true,
				Created: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			},
		},
	}

	// Test marshaling
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("Failed to marshal config: %v", err)
	}

	// Test unmarshaling
	var unmarshaled Config
	err = json.Unmarshal(data, &unmarshaled)
	if err != nil {
		t.Fatalf("Failed to unmarshal config: %v", err)
	}

	// Check values
	if unmarshaled.Listen != cfg.Listen {
		t.Errorf("Expected Listen %s, got %s", cfg.Listen, unmarshaled.Listen)
	}

	if unmarshaled.EnableTray != cfg.EnableTray {
		t.Errorf("Expected EnableTray %v, got %v", cfg.EnableTray, unmarshaled.EnableTray)
	}

	if len(unmarshaled.Servers) != 1 {
		t.Errorf("Expected 1 server, got %d", len(unmarshaled.Servers))
	}

	if len(unmarshaled.Servers) > 0 {
		server := unmarshaled.Servers[0]
		if server.URL != "http://localhost:8000" {
			t.Errorf("Expected server URL http://localhost:8000, got %s", server.URL)
		}
		if server.Name != "test" {
			t.Errorf("Expected server name test, got %s", server.Name)
		}
	}
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
	if loaded.Listen != ":8080" {
		t.Errorf("Expected sample config Listen to be :8080, got %s", loaded.Listen)
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
