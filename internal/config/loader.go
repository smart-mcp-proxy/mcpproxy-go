package config

import (
	"crypto/rand"
	"encoding/hex"
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
	trueValue      = "true"
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
	if err := os.MkdirAll(cfg.DataDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create data directory %s: %w", cfg.DataDir, err)
	}

	// Apply environment variable overrides for TLS configuration
	applyTLSEnvOverrides(cfg)

	// Validate configuration
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	// Initialize registries from config
	initializeRegistries(cfg)

	return cfg, nil
}

// Load loads configuration from file, environment, and defaults
func Load() (*Config, error) {
	cfg := DefaultConfig()

	// Set up viper
	setupViper()

	// Load from config file if specified
	configPath := viper.GetString("config")
	configFileAutoLoaded := false
	if configPath != "" {
		if err := loadConfigFile(configPath, cfg); err != nil {
			return nil, fmt.Errorf("failed to load config file %s: %w", configPath, err)
		}
	} else {
		// Try to find config file in common locations
		configFound, _, err := findAndLoadConfigFile(cfg)
		if err != nil && configFound {
			return nil, err // Only return error if config was found but couldn't be loaded
		}
		configFileAutoLoaded = configFound

		// If no config file was found, create a default one
		if !configFound {
			// Set data directory first to know where to create the config
			if cfg.DataDir == "" {
				homeDir, err := os.UserHomeDir()
				if err != nil {
					return nil, fmt.Errorf("failed to get user home directory: %w", err)
				}
				cfg.DataDir = filepath.Join(homeDir, DefaultDataDir)
			}

			// Create data directory if it doesn't exist
			if err := os.MkdirAll(cfg.DataDir, 0700); err != nil {
				return nil, fmt.Errorf("failed to create data directory %s: %w", cfg.DataDir, err)
			}

			// Create default config file
			defaultConfigPath := filepath.Join(cfg.DataDir, ConfigFileName)
			if err := createDefaultConfigFile(defaultConfigPath, cfg); err != nil {
				return nil, fmt.Errorf("failed to create default config file: %w", err)
			}

			fmt.Printf("INFO: Created default configuration file at %s\n", defaultConfigPath)
		}
	}

	// Only use viper.Unmarshal if no config file was auto-loaded
	// When config file is auto-loaded, CLI flags are handled in main.go
	if !configFileAutoLoaded {
		// Override with viper (CLI flags and env vars)
		if err := viper.Unmarshal(cfg); err != nil {
			return nil, fmt.Errorf("failed to unmarshal config: %w", err)
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
	if err := os.MkdirAll(cfg.DataDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create data directory %s: %w", cfg.DataDir, err)
	}

	// Parse upstream servers from CLI
	upstreamList := viper.GetStringSlice("upstream")
	for _, upstream := range upstreamList {
		if err := parseUpstreamServer(upstream, cfg); err != nil {
			return nil, fmt.Errorf("failed to parse upstream server %s: %w", upstream, err)
		}
	}

	// Apply environment variable overrides for TLS configuration
	applyTLSEnvOverrides(cfg)

	// Validate configuration
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	// Initialize registries from config
	initializeRegistries(cfg)

	return cfg, nil
}

// setupViper configures viper with environment variable handling
func setupViper() {
	viper.SetEnvPrefix("MCPP")
	viper.AutomaticEnv()

	// Replace - with _ for environment variables
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))

	// Set defaults
	viper.SetDefault("listen", "127.0.0.1:8080")
	viper.SetDefault("top-k", 5)
	viper.SetDefault("tools-limit", 15)
	viper.SetDefault("config", "")

	// Security defaults
	viper.SetDefault("read-only-mode", false)
	viper.SetDefault("disable-management", false)
	viper.SetDefault("allow-server-add", true)
	viper.SetDefault("allow-server-remove", true)
	viper.SetDefault("enable-prompts", true)
	viper.SetDefault("check-server-repo", true)

	// TLS defaults
	viper.SetDefault("tls.enabled", false)
	viper.SetDefault("tls.require_client_cert", false)
	viper.SetDefault("tls.hsts", true)
}

// findAndLoadConfigFile tries to find config file in common locations
func findAndLoadConfigFile(cfg *Config) (found bool, path string, err error) {
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
			return true, location, loadConfigFile(location, cfg)
		}
	}
	return false, "", nil
}

// loadConfigFile loads configuration from a JSON file
func loadConfigFile(path string, cfg *Config) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read config file: %w", err)
	}

	// Empty file (including /dev/null) is treated as no configuration
	// This allows --config=/dev/null to work as "use defaults only"
	if len(data) == 0 {
		return nil
	}

	// First check if api_key is present in the JSON to distinguish between
	// "not set" vs "explicitly set to empty"
	var rawConfig map[string]interface{}
	if err := json.Unmarshal(data, &rawConfig); err != nil {
		return fmt.Errorf("failed to parse config file for api_key detection: %w", err)
	}

	// Check if api_key is explicitly set in the config file
	if _, exists := rawConfig["api_key"]; exists {
		cfg.apiKeyExplicitlySet = true
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

// atomicWriteFile writes data to path atomically using temp file + rename pattern.
// This prevents race conditions where readers might see partially written files.
//
// The atomic write pattern:
// 1. Write to temporary file with random suffix
// 2. Sync to disk (fsync)
// 3. Atomic rename over target file
//
// This ensures readers always see either the old complete file or new complete file,
// never a partially written file.
//
// Note: On POSIX systems (Linux, macOS), rename() is guaranteed to be atomic.
// On Windows, rename atomicity is not guaranteed when target exists, but this
// approach is still much safer than truncate+write (reduces race window from
// ~50ms to <1ms).
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	// Generate random suffix for temp file
	randBytes := make([]byte, 8)
	if _, err := rand.Read(randBytes); err != nil {
		return fmt.Errorf("failed to generate random suffix: %w", err)
	}
	suffix := hex.EncodeToString(randBytes)

	// Create temp file in same directory (required for atomic rename)
	dir := filepath.Dir(path)
	tmpPath := filepath.Join(dir, filepath.Base(path)+".tmp."+suffix)

	// Write to temp file
	tmpFile, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}

	// Clean up temp file on error
	defer func() {
		if tmpFile != nil {
			tmpFile.Close()
			os.Remove(tmpPath)
		}
	}()

	// Write data
	if _, err := tmpFile.Write(data); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	// Fsync to ensure data is on disk
	if err := tmpFile.Sync(); err != nil {
		return fmt.Errorf("failed to sync temp file: %w", err)
	}

	// Close temp file
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}
	tmpFile = nil // Prevent deferred cleanup

	// Atomic rename (POSIX guarantees atomicity, Windows is best-effort)
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath) // Clean up on rename failure
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	return nil
}

// SaveConfig saves configuration to file
func SaveConfig(cfg *Config, path string) error {
	fmt.Printf("[DEBUG] SaveConfig called with path: %s\n", path)
	fmt.Printf("[DEBUG] SaveConfig - server count: %d\n", len(cfg.Servers))

	// Log server states for debugging
	for _, server := range cfg.Servers {
		fmt.Printf("[DEBUG] SaveConfig - server %s: enabled=%v, quarantined=%v\n",
			server.Name, server.Enabled, server.Quarantined)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		fmt.Printf("[DEBUG] SaveConfig - JSON marshal failed: %v\n", err)
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		fmt.Printf("[DEBUG] SaveConfig - MkdirAll failed: %v\n", err)
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// Atomic write with fsync to prevent race conditions
	// This ensures core never reads partially written config files
	fmt.Printf("[DEBUG] SaveConfig - about to write file atomically: %s\n", path)
	if err := atomicWriteFile(path, data, 0600); err != nil {
		fmt.Printf("[DEBUG] SaveConfig - atomicWriteFile failed: %v\n", err)
		return fmt.Errorf("failed to write config file: %w", err)
	}

	fmt.Printf("[DEBUG] SaveConfig - successfully wrote file: %s\n", path)
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
	cfg := DefaultConfig()
	cfg.Servers = []*ServerConfig{
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
	}

	return SaveConfig(cfg, path)
}

// Helper function to get current time (useful for testing)
var now = time.Now

// createDefaultConfigFile creates a default configuration file with default settings
func createDefaultConfigFile(path string, cfg *Config) error {
	// Use the default config with empty servers list
	defaultCfg := DefaultConfig()
	defaultCfg.DataDir = cfg.DataDir
	defaultCfg.Servers = []*ServerConfig{} // Empty servers list

	return SaveConfig(defaultCfg, path)
}

// initializeRegistries initializes the registries package with config data
func initializeRegistries(cfg *Config) {
	// This function will be implemented to avoid circular imports
	// For now, we'll create a callback mechanism
	if registriesInitCallback != nil {
		registriesInitCallback(cfg)
	}
}

// registriesInitCallback is set by main.go to avoid circular import
var registriesInitCallback func(*Config)

// SetRegistriesInitCallback sets the callback function for registries initialization
func SetRegistriesInitCallback(callback func(*Config)) {
	registriesInitCallback = callback
}

// applyTLSEnvOverrides applies environment variable overrides for TLS configuration
func applyTLSEnvOverrides(cfg *Config) {
	// Ensure TLS config is initialized
	if cfg.TLS == nil {
		cfg.TLS = &TLSConfig{
			Enabled:           true,
			RequireClientCert: false,
			CertsDir:          "",
			HSTS:              true,
		}
	}

	// Override listen address from environment
	if value := os.Getenv("MCPPROXY_LISTEN"); value != "" {
		cfg.Listen = value
	}

	// Override TLS enabled from environment
	if value := os.Getenv("MCPPROXY_TLS_ENABLED"); value != "" {
		cfg.TLS.Enabled = (value == trueValue || value == "1")
	}

	// Override TLS client cert requirement from environment
	if value := os.Getenv("MCPPROXY_TLS_REQUIRE_CLIENT_CERT"); value != "" {
		cfg.TLS.RequireClientCert = (value == trueValue || value == "1")
	}

	// Override TLS certificates directory from environment
	if value := os.Getenv("MCPPROXY_CERTS_DIR"); value != "" {
		cfg.TLS.CertsDir = value
	}

	// Override data directory from environment (for backward compatibility)
	if value := os.Getenv("MCPPROXY_DATA"); value != "" {
		cfg.DataDir = value
	}
}
