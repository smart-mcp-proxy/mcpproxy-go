package config

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/spf13/viper"
)

const (
	DefaultDataDir = ".mcpproxy"
	ConfigFileName = "mcp_config.json"
	trueValue      = "true"
	falseValue     = "false"
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

	// Expand secret/env refs in DataDir before creating it
	expandDataDir(cfg)

	// Create data directory if it doesn't exist.
	// Skip if the path still contains unresolved ${...} refs (e.g., missing env var) —
	// these are invalid path characters on Windows and the directory can't be created anyway.
	if !strings.Contains(cfg.DataDir, "${") {
		if err := os.MkdirAll(cfg.DataDir, 0700); err != nil {
			return nil, fmt.Errorf("failed to create data directory %s: %w", cfg.DataDir, err)
		}
	}

	// Apply environment variable overrides for TLS configuration
	applyTLSEnvOverrides(cfg)

	// Migrate an unrecognized per-server trust_mode to the fail-closed tier
	// BEFORE validating: a bogus value must not brick an existing install.
	warnNormalizedTrustModes(cfg)

	// Validate configuration
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	// Initialize registries from config
	initializeRegistries(cfg)

	return cfg, nil
}

// warnNormalizedTrustModes applies NormalizeTrustModes and reports each rewrite
// on stderr (the loader has no injected logger; this matches the other
// load-time WARN lines here). The daemon starts, the runtime behaves exactly as
// it already did for the unrecognized value (manual), and the operator is told
// instead of being left with a proxy that refuses to boot (GH #938).
func warnNormalizedTrustModes(cfg *Config) {
	for _, n := range NormalizeTrustModes(cfg) {
		fmt.Fprintf(os.Stderr,
			"WARN: server %q has an unrecognized trust_mode %q; treating it as %q (valid: %s)\n",
			n.Server, n.Original, TrustModeManual, strings.Join(ValidTrustModes(), ", "))
	}
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

			fmt.Fprintf(os.Stderr, "INFO: Created default configuration file at %s\n", defaultConfigPath)
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

	// Expand secret/env refs in DataDir before creating it
	expandDataDir(cfg)

	// Create data directory if it doesn't exist.
	// Skip if the path still contains unresolved ${...} refs (e.g., missing env var) —
	// these are invalid path characters on Windows and the directory can't be created anyway.
	if !strings.Contains(cfg.DataDir, "${") {
		if err := os.MkdirAll(cfg.DataDir, 0700); err != nil {
			return nil, fmt.Errorf("failed to create data directory %s: %w", cfg.DataDir, err)
		}
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

	// Same migration as LoadFromFile: normalize-and-warn, never fail the load.
	warnNormalizedTrustModes(cfg)

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
	viper.SetDefault("aggregate-upstream-prompts", false)
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

	// Back-compat (MCP-1086): the server-edition block was renamed from the
	// legacy "teams" key to "server_edition". An existing config that still uses
	// "teams" is normalized onto ServerEdition on read. The new key always wins;
	// only fall back to the legacy key when "server_edition" is absent. This
	// compiles in both editions because ServerEditionConfig is a struct{} stub
	// in the personal build (it simply unmarshals to an empty value there).
	if _, hasNew := rawConfig["server_edition"]; !hasNew {
		if legacy, hasLegacy := rawConfig["teams"]; hasLegacy {
			if raw, err := json.Marshal(legacy); err == nil {
				var se ServerEditionConfig
				if err := json.Unmarshal(raw, &se); err == nil {
					cfg.ServerEdition = &se
				}
			}
		}
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
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// Atomic write with fsync to prevent race conditions
	// This ensures core never reads partially written config files
	if err := atomicWriteFile(path, data, 0600); err != nil {
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
		applyFirstRunDockerIsolation(cfg)
		if err := SaveConfig(cfg, configPath); err != nil {
			return nil, fmt.Errorf("failed to create initial config: %w", err)
		}
		return cfg, nil
	}

	return LoadFromFile(configPath)
}

// applyFirstRunDockerIsolation turns on DockerIsolation.Enabled for a freshly
// created config if (and only if) a Docker daemon is reachable at install
// time. Existing installs are unaffected — DefaultConfig() still returns
// Enabled=false so LoadFromFile's default-then-merge path preserves whatever
// the user has (or doesn't have) in their config file.
//
// Probing here keeps new users secure-by-default without breaking the ~75%
// of current users who don't have Docker: if `docker info` fails, we keep
// isolation off and the user can flip it on later via the Web UI toggle or
// by editing mcp_config.json.
func applyFirstRunDockerIsolation(cfg *Config) {
	if cfg == nil || cfg.DockerIsolation == nil {
		return
	}
	if !dockerDaemonProbe() {
		return
	}
	cfg.DockerIsolation.Enabled = true
}

// dockerDaemonProbe is the function used to detect Docker at first-run.
// Tests override it to return deterministic values without spawning a
// subprocess. Production code uses probeDockerDaemonAvailable.
var dockerDaemonProbe = probeDockerDaemonAvailable

// probeDockerDaemonAvailable runs `docker info` with a short timeout to check
// whether the host has a reachable Docker daemon. Returns false on any
// failure (binary missing, daemon down, permissions). Used only during
// initial config creation — not on every start.
func probeDockerDaemonAvailable() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "docker", "info", "--format", "{{.ServerVersion}}").Run() == nil
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

// EnsureConfigFile seeds a default configuration at an explicitly named path
// that does not exist yet, and reports whether it created one.
//
// It exists for the first run of an instance whose config path is named on the
// command line rather than discovered — the relocated tray instance of GH #936
// spawns its core with BOTH `--data-dir <root>` and `--config
// <root>/mcp_config.json`, and on a fresh root that file has never been
// written. LoadFromFile refuses a missing explicit path, so without this the
// core exited on the spot with "no such file or directory" and the instance
// could never come up.
//
// Why here and not inside LoadFromFile: LoadFromFile is also the HOT-RELOAD
// entry point (internal/runtime/lifecycle.go, internal/runtime/configsvc). A
// config file that disappears under a running proxy must fail the reload and
// leave the live config alone; creating a default there would replace every
// server the user has with nothing. Seeding is a startup decision, so it is
// made at startup, once.
//
// dataDir is the directory the caller will run out of (the `--data-dir` flag
// when one was given); the seeded file records it, exactly as the implicit path
// records its own. An empty dataDir leaves the field unset, so the loader's
// usual default applies.
func EnsureConfigFile(path, dataDir string) (created bool, err error) {
	if path == "" {
		return false, nil
	}
	if _, statErr := os.Stat(path); statErr == nil {
		return false, nil
	} else if !os.IsNotExist(statErr) {
		return false, fmt.Errorf("failed to inspect config file %s: %w", path, statErr)
	}

	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return false, fmt.Errorf("failed to create config directory %s: %w", dir, err)
		}
	}

	seed := &Config{DataDir: dataDir}
	if err := createDefaultConfigFile(path, seed); err != nil {
		return false, fmt.Errorf("failed to create default config file %s: %w", path, err)
	}
	// Same notice the implicit path prints, for the same reason: a typo in
	// --config would otherwise create a blank instance in silence.
	fmt.Fprintf(os.Stderr, "INFO: Created default configuration file at %s\n", path)
	return true, nil
}

// Helper function to get current time (useful for testing)
var now = time.Now

// createDefaultConfigFile creates a default configuration file with default settings
func createDefaultConfigFile(path string, cfg *Config) error {
	// Use the default config with empty servers list
	defaultCfg := DefaultConfig()
	defaultCfg.DataDir = cfg.DataDir
	defaultCfg.Servers = []*ServerConfig{} // Empty servers list
	applyFirstRunDockerIsolation(defaultCfg)

	return SaveConfig(defaultCfg, path)
}

// initializeRegistries initializes the registries package with config data
func initializeRegistries(cfg *Config) {
	// One-time migration (MCP-1049): drop former-default registries that were
	// trimmed from the shipped set so an existing config converges to the current
	// defaults instead of resurrecting them on every load. Idempotent and only
	// touches the known former-default id set, never user-added customs.
	PruneDeprecatedRegistries(cfg)

	// One-time migration (MCP-1072): map legacy provenance strings
	// ("official/trusted" / "custom/unverified") persisted by earlier builds onto
	// the current two-value vocabulary so existing installs don't break on read.
	normalizeRegistryProvenanceValues(cfg)

	// One-time migration (MCP-2930): map the deprecated per-server skip_quarantine
	// flag onto auto_approve_tool_changes so existing configs converge on the new
	// field. Runs on initial load and every hot-reload (LoadFromFile path).
	normalizeServerQuarantineFlags(cfg)

	// One-time migration (Spec 077 US3): fold the deprecated top-level
	// scanner_fetch_package_source / scanner_disable_no_new_privileges keys into
	// the unified security.deep_scan block, and drop the removed
	// auto_scan_quarantined key. Existing configs load unchanged.
	migrateDeepScanConfig(cfg)

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

// expandDataDir expands secret/env refs in cfg.DataDir in place.
// Failures are logged to stderr and the original value is kept.
func expandDataDir(cfg *Config) {
	if cfg.DataDir == "" {
		return
	}
	resolver := secret.NewResolver()
	resolved, err := resolver.ExpandSecretRefs(context.Background(), cfg.DataDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARN: Failed to resolve secret ref in data_dir, using original value: reference=%s err=%v\n", cfg.DataDir, err)
		return
	}
	cfg.DataDir = resolved
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

	// Override trusted hosts for reverse-proxy deployments (GH #898).
	// Comma-separated list of Host header values accepted on loopback listeners.
	if value := os.Getenv("MCPPROXY_TRUSTED_HOSTS"); value != "" {
		var hosts []string
		for _, h := range strings.Split(value, ",") {
			if h = strings.TrimSpace(h); h != "" {
				hosts = append(hosts, h)
			}
		}
		cfg.TrustedHosts = hosts
	}

	// Override the offline TPA signature-bundle path from environment
	// (spec 086 FR-019). Explicit MCPPROXY_* alias per the loader convention;
	// the env value wins over the file value, and materializes the security
	// block so a config with no `security` key can still point at a corpus.
	if value := os.Getenv(EnvTPABundlePath); value != "" {
		if cfg.Security == nil {
			cfg.Security = &SecurityConfig{}
		}
		cfg.Security.TPABundlePath = value
	}

	// Override the automatic informational baseline-scan kill switch from
	// environment. Materializes the security block so an install with no
	// `security` key can still be opted out (or explicitly back in).
	// IsAutoBaselineScanEnabled re-reads the same variable, so the env value
	// also wins on paths that never pass through the loader.
	// Only the documented vocabulary overrides. An unrecognized value (typo,
	// "yes", "maybe") must be IGNORED, matching IsAutoBaselineScanEnabled — a
	// bare `value != ""` check would have materialized `false` here and silently
	// turned automatic scanning off for a config that had explicitly enabled it,
	// because the accessor then reads the overwritten field rather than the env.
	switch os.Getenv(EnvAutoBaselineScan) {
	case trueValue, "1":
		enabled := true
		if cfg.Security == nil {
			cfg.Security = &SecurityConfig{}
		}
		cfg.Security.AutoBaselineScan = &enabled
	case falseValue, "0":
		enabled := false
		if cfg.Security == nil {
			cfg.Security = &SecurityConfig{}
		}
		cfg.Security.AutoBaselineScan = &enabled
	}

	// Override retrieve_tools serialization mode from environment (Spec 085).
	// Explicit MCPPROXY_* alias per the established loader convention; the
	// value is validated by cfg.Validate() right after these overrides apply.
	if value := os.Getenv("MCPPROXY_TOOL_RESPONSE_MODE"); value != "" {
		cfg.ToolResponseMode = value
	}

	// Override DIRECT-surface serialization mode from environment (Spec 102).
	// A separate variable from MCPPROXY_TOOL_RESPONSE_MODE above, matching the
	// separate config axis: that one governs retrieve_tools, this one governs
	// the direct enumeration surface. Setting one must never move the other.
	if value := os.Getenv("MCPPROXY_DIRECT_TOOL_RESPONSE_MODE"); value != "" {
		cfg.DirectToolResponseMode = value
	}

	// Override the GLOBAL aggregate concurrency limiter from environment
	// (spec 093 FR-022, GH #955). Only this scope has an env scheme: the
	// per-server default set and per-server overrides are file/API-configured.
	// An explicit 0 is meaningful (it disables the limiter), so the value is
	// materialized as a pointer; malformed values are warned about and ignored
	// so a typo cannot silently reshape the proxy's admission behavior.
	if value := os.Getenv("MCPPROXY_MAX_CONCURRENT_REQUESTS"); value != "" {
		if n, err := strconv.Atoi(value); err == nil && n >= 0 {
			cfg.MaxConcurrentRequests = &n
		} else {
			fmt.Fprintf(os.Stderr, "WARN: Ignoring invalid MCPPROXY_MAX_CONCURRENT_REQUESTS=%q (want a non-negative integer)\n", value)
		}
	}
	if value := os.Getenv("MCPPROXY_QUEUE_SIZE"); value != "" {
		if n, err := strconv.Atoi(value); err == nil && n >= 0 {
			cfg.QueueSize = &n
		} else {
			fmt.Fprintf(os.Stderr, "WARN: Ignoring invalid MCPPROXY_QUEUE_SIZE=%q (want a non-negative integer)\n", value)
		}
	}
	if value := os.Getenv("MCPPROXY_QUEUE_TIMEOUT"); value != "" {
		if d, err := time.ParseDuration(value); err == nil && d >= 0 {
			qt := Duration(d)
			cfg.QueueTimeout = &qt
		} else {
			fmt.Fprintf(os.Stderr, "WARN: Ignoring invalid MCPPROXY_QUEUE_TIMEOUT=%q (want a duration such as \"30s\")\n", value)
		}
	}

	// Override the HTTP server's request deadlines from environment (GH #965)
	// — the escape hatch for operators who cannot edit the config file. An
	// explicit "0s" is meaningful (it disables the deadline — except idle,
	// where net/http falls back to the read timeout; see
	// ResolveHTTPIdleTimeout), so the value is materialized as a pointer;
	// malformed values are warned about and ignored so a typo cannot silently
	// reintroduce a response-truncating deadline.
	if value := os.Getenv("MCPPROXY_HTTP_READ_TIMEOUT"); value != "" {
		if d, err := time.ParseDuration(value); err == nil && d >= 0 {
			rt := Duration(d)
			cfg.HTTPReadTimeout = &rt
		} else {
			fmt.Fprintf(os.Stderr, "WARN: Ignoring invalid MCPPROXY_HTTP_READ_TIMEOUT=%q (want a duration such as \"120s\", or \"0s\" to disable)\n", value)
		}
	}
	if value := os.Getenv("MCPPROXY_HTTP_WRITE_TIMEOUT"); value != "" {
		if d, err := time.ParseDuration(value); err == nil && d >= 0 {
			wt := Duration(d)
			cfg.HTTPWriteTimeout = &wt
		} else {
			fmt.Fprintf(os.Stderr, "WARN: Ignoring invalid MCPPROXY_HTTP_WRITE_TIMEOUT=%q (want a duration such as \"300s\", or \"0s\" to disable)\n", value)
		}
	}
	if value := os.Getenv("MCPPROXY_HTTP_IDLE_TIMEOUT"); value != "" {
		if d, err := time.ParseDuration(value); err == nil && d >= 0 {
			it := Duration(d)
			cfg.HTTPIdleTimeout = &it
		} else {
			fmt.Fprintf(os.Stderr, "WARN: Ignoring invalid MCPPROXY_HTTP_IDLE_TIMEOUT=%q (want a duration such as \"180s\"; \"0s\" falls back to the read timeout)\n", value)
		}
	}
}
