package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/viper"
)

const (
	DefaultDataDir = ".mcpproxy"
	ConfigFileName = "mcp_config.json"
)

// LoadFromFile loads configuration from a specific file
func LoadFromFile(configPath string) (*Config, error) {
	cfg := DefaultConfig()

	if configPath != "" {
		if err := loadConfigFile(configPath, cfg); err != nil {
			return nil, fmt.Errorf("failed to load config file %s: %w", configPath, err)
		}
	}

	// Set data directory if not specified
	if cfg.DataDir == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to get user home directory: %w", err)
		}
		cfg.DataDir = filepath.Join(homeDir, DefaultDataDir)
	}

	// Create data directory if it doesn't exist
	if err := os.MkdirAll(cfg.DataDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create data directory %s: %w", cfg.DataDir, err)
	}

	// Validate configuration
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return cfg, nil
}

// Load loads configuration from file, environment, and defaults
func Load() (*Config, error) {
	cfg := DefaultConfig()

	// Set up viper
	setupViper()

	// Load from config file if specified
	configPath := viper.GetString("config")
	if configPath != "" {
		if err := loadConfigFile(configPath, cfg); err != nil {
			return nil, fmt.Errorf("failed to load config file %s: %w", configPath, err)
		}
	} else {
		// Try to find config file in common locations
		if err := findAndLoadConfigFile(cfg); err != nil {
			// Config file not found, that's OK - we'll use defaults and env vars
		}
	}

	// Override with viper (CLI flags and env vars)
	if err := viper.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// Set data directory if not specified
	if cfg.DataDir == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to get user home directory: %w", err)
		}
		cfg.DataDir = filepath.Join(homeDir, DefaultDataDir)
	}

	// Create data directory if it doesn't exist
	if err := os.MkdirAll(cfg.DataDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create data directory %s: %w", cfg.DataDir, err)
	}

	// Parse upstream servers from CLI
	upstreamList := viper.GetStringSlice("upstream")
	for _, upstream := range upstreamList {
		if err := parseUpstreamServer(upstream, cfg); err != nil {
			return nil, fmt.Errorf("failed to parse upstream server %s: %w", upstream, err)
		}
	}

	// Validate configuration
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return cfg, nil
}

// setupViper configures viper with environment variable handling
func setupViper() {
	viper.SetEnvPrefix("MCPP")
	viper.AutomaticEnv()

	// Replace - with _ for environment variables
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))

	// Set defaults
	viper.SetDefault("listen", ":8080")
	viper.SetDefault("tray", true)
	viper.SetDefault("top-k", 5)
	viper.SetDefault("tools-limit", 15)
	viper.SetDefault("config", "")

	// Security defaults
	viper.SetDefault("read-only-mode", false)
	viper.SetDefault("disable-management", false)
	viper.SetDefault("allow-server-add", true)
	viper.SetDefault("allow-server-remove", true)
	viper.SetDefault("enable-prompts", true)
}

// findAndLoadConfigFile tries to find config file in common locations
func findAndLoadConfigFile(cfg *Config) error {
	// Common config file locations
	locations := []string{
		ConfigFileName,
		filepath.Join(".", ConfigFileName),
	}

	// Add home directory location
	if homeDir, err := os.UserHomeDir(); err == nil {
		locations = append(locations, filepath.Join(homeDir, DefaultDataDir, ConfigFileName))
	}

	for _, location := range locations {
		if _, err := os.Stat(location); err == nil {
			return loadConfigFile(location, cfg)
		}
	}

	return fmt.Errorf("config file not found in any location")
}

// loadConfigFile loads configuration from a JSON file
func loadConfigFile(path string, cfg *Config) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read config file: %w", err)
	}

	if err := json.Unmarshal(data, cfg); err != nil {
		return fmt.Errorf("failed to parse config file: %w", err)
	}

	// Set created time if not specified
	for _, server := range cfg.Servers {
		if server.Created.IsZero() {
			// Use a consistent time function if `now()` is not defined in this package
			server.Created = time.Now()
		}
	}

	return nil
}

// parseUpstreamServer parses upstream server specification from CLI
func parseUpstreamServer(upstream string, cfg *Config) error {
	parts := strings.SplitN(upstream, "=", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid format, expected name=url")
	}

	name := strings.TrimSpace(parts[0])
	url := strings.TrimSpace(parts[1])

	if name == "" || url == "" {
		return fmt.Errorf("both name and url must be non-empty")
	}

	serverConfig := &ServerConfig{
		Name:    name,
		URL:     url,
		Enabled: true,
		Created: now(),
	}

	cfg.Servers = append(cfg.Servers, serverConfig)

	return nil
}

// SaveConfig saves configuration to file
func SaveConfig(cfg *Config, path string) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

// SaveConfigToDataDir saves configuration to the data directory
func SaveConfigToDataDir(cfg *Config) error {
	if cfg.DataDir == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get user home directory: %w", err)
		}
		cfg.DataDir = filepath.Join(homeDir, DefaultDataDir)
	}

	configPath := filepath.Join(cfg.DataDir, ConfigFileName)
	return SaveConfig(cfg, configPath)
}

// GetConfigPath returns the path to the configuration file in the data directory
func GetConfigPath(dataDir string) string {
	if dataDir == "" {
		homeDir, _ := os.UserHomeDir()
		dataDir = filepath.Join(homeDir, DefaultDataDir)
	}
	return filepath.Join(dataDir, ConfigFileName)
}

// LoadOrCreateConfig loads configuration from the data directory or creates a new one
func LoadOrCreateConfig(dataDir string) (*Config, error) {
	configPath := GetConfigPath(dataDir)

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		// Config doesn't exist, create a new one
		cfg := DefaultConfig()
		cfg.DataDir = dataDir
		if err := SaveConfig(cfg, configPath); err != nil {
			return nil, fmt.Errorf("failed to create initial config: %w", err)
		}
		return cfg, nil
	}

	return LoadFromFile(configPath)
}

// CreateSampleConfig creates a sample configuration file
func CreateSampleConfig(path string) error {
	cfg := &Config{
		Listen:     ":8080",
		EnableTray: true,
		TopK:       5,
		ToolsLimit: 15,
		Servers: []*ServerConfig{
			{
				Name:    "example",
				URL:     "http://localhost:8000/mcp/",
				Enabled: true,
				Created: now(),
			},
			{
				Name:    "local-command",
				Command: "mcp-server-example",
				Args:    []string{"--config", "example.json"},
				Env:     map[string]string{"DEBUG": "true"},
				Enabled: true,
				Created: now(),
			},
		},
	}

	return SaveConfig(cfg, path)
}

// Helper function to get current time (useful for testing)
var now = func() time.Time {
	return time.Now()
}
