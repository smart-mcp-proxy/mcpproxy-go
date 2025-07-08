package secureenv

import (
	"os"
	"runtime"
	"strings"
)

const (
	osWindows = "windows"
)

// EnvConfig represents environment configuration for secure filtering
type EnvConfig struct {
	InheritSystemSafe bool              `json:"inherit_system_safe"`
	AllowedSystemVars []string          `json:"allowed_system_vars"`
	CustomVars        map[string]string `json:"custom_vars"`
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
	config *EnvConfig
}

// NewManager creates a new secure environment manager
func NewManager(config *EnvConfig) *Manager {
	if config == nil {
		config = DefaultEnvConfig()
	}
	return &Manager{config: config}
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

	return envVars
}

// getFilteredSystemEnv returns filtered system environment variables
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

// isEnvVarAllowed checks if an environment variable is allowed based on the allow-list
func (m *Manager) isEnvVarAllowed(envVar string) bool {
	parts := strings.SplitN(envVar, "=", 2)
	if len(parts) != 2 {
		return false
	}

	key := parts[0]

	// Check against allow-list
	for _, allowedVar := range m.config.AllowedSystemVars {
		if key == allowedVar {
			return true
		}

		// Support wildcard matching for locale variables (LC_*)
		if strings.HasSuffix(allowedVar, "*") {
			prefix := strings.TrimSuffix(allowedVar, "*")
			if strings.HasPrefix(key, prefix) {
				return true
			}
		}
	}

	return false
}

// GetSystemEnvVar gets a specific system environment variable if allowed
func (m *Manager) GetSystemEnvVar(key string) (string, bool) {
	if !m.isKeyAllowed(key) {
		return "", false
	}

	value := os.Getenv(key)
	return value, value != ""
}

// isKeyAllowed checks if a key is in the allow-list
func (m *Manager) isKeyAllowed(key string) bool {
	for _, allowedVar := range m.config.AllowedSystemVars {
		if key == allowedVar {
			return true
		}

		// Support wildcard matching
		if strings.HasSuffix(allowedVar, "*") {
			prefix := strings.TrimSuffix(allowedVar, "*")
			if strings.HasPrefix(key, prefix) {
				return true
			}
		}
	}

	return false
}

// ValidateConfig validates the environment configuration
func (m *Manager) ValidateConfig() error {
	// Environment configuration is always valid with our allow-list approach
	// The worst case is an empty environment, which is still secure
	return nil
}

// GetFilteredEnvCount returns the number of filtered environment variables
func (m *Manager) GetFilteredEnvCount() (filteredCount, totalCount int) {
	systemEnv := os.Environ()
	filteredEnv := m.getFilteredSystemEnv()
	return len(filteredEnv), len(systemEnv)
}
