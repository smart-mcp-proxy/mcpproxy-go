package secureenv

import (
	"os"
	"runtime"
	"strings"
)

const (
	osWindows = "windows"
	osDarwin  = "darwin"
)

// EnvConfig represents environment configuration for secure filtering
type EnvConfig struct {
	InheritSystemSafe bool              `json:"inherit_system_safe"`
	AllowedSystemVars []string          `json:"allowed_system_vars"`
	CustomVars        map[string]string `json:"custom_vars"`
}

// PathDiscovery contains auto-discovered paths for common tools
type PathDiscovery struct {
	HomePath        string
	BrewPaths       []string
	NodePaths       []string
	PythonPaths     []string
	RustPaths       []string
	GoPaths         []string
	ChocoPaths      []string
	ScoopPaths      []string
	SystemPaths     []string
	DiscoveredPaths []string
	AvailableTools  map[string]string
}

// DefaultEnvConfig returns default environment configuration with safe system variables
func DefaultEnvConfig() *EnvConfig {
	allowedVars := []string{
		"PATH",     // Essential for finding executables
		"HOME",     // User directory path (Unix)
		"TMPDIR",   // Temporary directory (Unix)
		"TEMP",     // Temporary directory (Windows)
		"TMP",      // Temporary directory (Windows)
		"SHELL",    // Default shell
		"TERM",     // Terminal type
		"LANG",     // Language settings
		"USER",     // Current user (Unix)
		"USERNAME", // Current user (Windows)
	}

	// Add Windows-specific variables
	if runtime.GOOS == osWindows {
		allowedVars = append(allowedVars,
			"USERPROFILE",  // User profile directory
			"APPDATA",      // Application data directory
			"LOCALAPPDATA", // Local application data directory
			"PROGRAMFILES", // Program files directory
			"SYSTEMROOT",   // System root directory
			"COMSPEC",      // Command interpreter
		)
	}

	// Add Unix-specific variables
	if runtime.GOOS != osWindows {
		allowedVars = append(allowedVars,
			"XDG_CONFIG_HOME", // XDG config directory
			"XDG_DATA_HOME",   // XDG data directory
			"XDG_CACHE_HOME",  // XDG cache directory
			"XDG_RUNTIME_DIR", // XDG runtime directory
		)
	}

	// Add locale-related variables
	localeVars := []string{
		"LC_ALL", "LC_CTYPE", "LC_NUMERIC", "LC_TIME", "LC_COLLATE",
		"LC_MONETARY", "LC_MESSAGES", "LC_PAPER", "LC_NAME", "LC_ADDRESS",
		"LC_TELEPHONE", "LC_MEASUREMENT", "LC_IDENTIFICATION",
	}
	allowedVars = append(allowedVars, localeVars...)

	return &EnvConfig{
		InheritSystemSafe: true,
		AllowedSystemVars: allowedVars,
		CustomVars:        make(map[string]string),
	}
}

// Manager handles secure environment variable filtering
type Manager struct {
	config        *EnvConfig
	pathDiscovery *PathDiscovery
}

// NewManager creates a new secure environment manager
func NewManager(config *EnvConfig) *Manager {
	if config == nil {
		config = DefaultEnvConfig()
	}

	manager := &Manager{
		config: config,
	}

	// Perform path discovery for robust PATH handling
	manager.pathDiscovery = manager.discoverPaths()

	return manager
}

// discoverPaths automatically discovers common tool installation paths
func (m *Manager) discoverPaths() *PathDiscovery {
	discovery := &PathDiscovery{
		AvailableTools: make(map[string]string),
	}
	return discovery
}

// BuildSecureEnvironment builds a secure environment variable list
func (m *Manager) BuildSecureEnvironment() []string {
	var envVars []string

	// Add safe system environment variables if enabled
	if m.config.InheritSystemSafe {
		envVars = append(envVars, m.getFilteredSystemEnv()...)
	}

	// Add custom environment variables from config
	for k, v := range m.config.CustomVars {
		envVars = append(envVars, k+"="+v)
	}

	// Ensure PATH is comprehensive by checking and enhancing it, but only if inheritance is enabled
	if m.config.InheritSystemSafe {
		envVars = m.ensureComprehensivePath(envVars)
	}

	return envVars
}

// ensureComprehensivePath ensures PATH includes all discovered tool paths
func (m *Manager) ensureComprehensivePath(envVars []string) []string {
	// Find existing PATH in environment
	var existingPath string
	var pathIndex = -1

	for i, envVar := range envVars {
		if strings.HasPrefix(envVar, "PATH=") {
			existingPath = strings.TrimPrefix(envVar, "PATH=")
			pathIndex = i
			break
		}
	}

	// Build enhanced PATH
	enhancedPath := m.buildEnhancedPath(existingPath)

	// Replace or add PATH
	pathVar := "PATH=" + enhancedPath
	if pathIndex >= 0 {
		envVars[pathIndex] = pathVar
	} else {
		envVars = append(envVars, pathVar)
	}

	return envVars
}

// buildEnhancedPath builds a comprehensive PATH by combining existing path with discovered paths
func (m *Manager) buildEnhancedPath(existingPath string) string {
	// With shell wrapping, we no longer need to build a complex path.
	// We just use the existing path from the environment.
	return existingPath
}

// getFilteredSystemEnv retrieves allowed environment variables from the system
func (m *Manager) getFilteredSystemEnv() []string {
	systemEnv := os.Environ()
	var filtered []string

	for _, envVar := range systemEnv {
		if m.isEnvVarAllowed(envVar) {
			filtered = append(filtered, envVar)
		}
	}

	return filtered
}

// isEnvVarAllowed checks if an environment variable is in the allowed list
func (m *Manager) isEnvVarAllowed(envVar string) bool {
	key := strings.Split(envVar, "=")[0]
	return m.isKeyAllowed(key)
}

// GetSystemEnvVar safely gets a system environment variable.
func (m *Manager) GetSystemEnvVar(key string) (string, bool) {
	if !m.isKeyAllowed(key) {
		return "", false
	}

	value := os.Getenv(key)
	return value, value != ""
}

// isKeyAllowed checks if a key is in the allowed list
func (m *Manager) isKeyAllowed(key string) bool {
	// Always allow custom variables defined in config
	if _, exists := m.config.CustomVars[key]; exists {
		return true
	}

	// Check against the list of allowed system variables
	for _, allowedKey := range m.config.AllowedSystemVars {
		if strings.HasSuffix(allowedKey, "*") {
			// Handle wildcard matching (e.g., "LC_*")
			prefix := strings.TrimSuffix(allowedKey, "*")
			if strings.HasPrefix(key, prefix) {
				return true
			}
		} else if strings.EqualFold(allowedKey, key) {
			// Handle exact matching
			return true
		}
	}
	return false
}

// ValidateConfig checks if the environment configuration is valid
func (m *Manager) ValidateConfig() error {
	return nil
}

// GetFilteredEnvCount returns the number of filtered and total system environment variables
func (m *Manager) GetFilteredEnvCount() (filteredCount, totalCount int) {
	systemEnv := os.Environ()
	filteredEnv := m.getFilteredSystemEnv()
	return len(filteredEnv), len(systemEnv)
}

// GetPathDiscovery returns the discovered path information
func (m *Manager) GetPathDiscovery() *PathDiscovery {
	return m.pathDiscovery
}
