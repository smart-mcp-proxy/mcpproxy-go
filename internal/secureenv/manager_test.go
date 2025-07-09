package secureenv

import (
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultEnvConfig(t *testing.T) {
	config := DefaultEnvConfig()

	require.NotNil(t, config)
	assert.True(t, config.InheritSystemSafe)
	assert.NotEmpty(t, config.AllowedSystemVars)
	assert.NotNil(t, config.CustomVars)

	// Verify essential variables are included
	allowedVars := config.AllowedSystemVars
	assert.Contains(t, allowedVars, "PATH")
	assert.Contains(t, allowedVars, "HOME")
	assert.Contains(t, allowedVars, "TMPDIR")
	assert.Contains(t, allowedVars, "TEMP")
	assert.Contains(t, allowedVars, "TMP")
	assert.Contains(t, allowedVars, "SHELL")
	assert.Contains(t, allowedVars, "TERM")
	assert.Contains(t, allowedVars, "LANG")

	// Platform-specific variables
	if runtime.GOOS == "windows" {
		assert.Contains(t, allowedVars, "USERPROFILE")
		assert.Contains(t, allowedVars, "APPDATA")
		assert.Contains(t, allowedVars, "LOCALAPPDATA")
		assert.Contains(t, allowedVars, "PROGRAMFILES")
		assert.Contains(t, allowedVars, "SYSTEMROOT")
		assert.Contains(t, allowedVars, "COMSPEC")
	} else {
		assert.Contains(t, allowedVars, "XDG_CONFIG_HOME")
		assert.Contains(t, allowedVars, "XDG_DATA_HOME")
		assert.Contains(t, allowedVars, "XDG_CACHE_HOME")
		assert.Contains(t, allowedVars, "XDG_RUNTIME_DIR")
	}

	// Locale variables
	assert.Contains(t, allowedVars, "LC_ALL")
	assert.Contains(t, allowedVars, "LC_CTYPE")
	assert.Contains(t, allowedVars, "LC_NUMERIC")
}

func TestNewManager(t *testing.T) {
	t.Run("with nil config uses default", func(t *testing.T) {
		manager := NewManager(nil)
		require.NotNil(t, manager)
		require.NotNil(t, manager.config)
		assert.True(t, manager.config.InheritSystemSafe)
	})

	t.Run("with custom config", func(t *testing.T) {
		config := &EnvConfig{
			InheritSystemSafe: false,
			AllowedSystemVars: []string{"PATH", "HOME"},
			CustomVars:        map[string]string{"TEST_VAR": "test_value"},
		}

		manager := NewManager(config)
		require.NotNil(t, manager)
		assert.Equal(t, config, manager.config)
	})
}

func TestIsEnvVarAllowed(t *testing.T) {
	manager := NewManager(&EnvConfig{
		AllowedSystemVars: []string{"PATH", "HOME", "LC_*", "XDG_CONFIG_HOME"},
		CustomVars:        make(map[string]string),
	})

	tests := []struct {
		name     string
		envVar   string
		expected bool
	}{
		{"allowed PATH", "PATH=/usr/bin:/bin", true},
		{"allowed HOME", "HOME=/home/user", true},
		{"allowed locale with wildcard", "LC_ALL=en_US.UTF-8", true},
		{"allowed locale specific", "LC_CTYPE=en_US.UTF-8", true},
		{"allowed XDG", "XDG_CONFIG_HOME=/home/user/.config", true},
		{"blocked secret", "API_KEY=secret123", false},
		{"blocked token", "AUTH_TOKEN=token123", false},
		{"blocked password", "DB_PASSWORD=password", false},
		{"blocked AWS", "AWS_ACCESS_KEY_ID=aws123", false},
		{"blocked custom secret", "CUSTOM_SECRET=secret", false},
		{"invalid format no equals", "INVALID", false},
		{"empty key", "=value", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := manager.isEnvVarAllowed(tt.envVar)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsKeyAllowed(t *testing.T) {
	manager := NewManager(&EnvConfig{
		AllowedSystemVars: []string{"PATH", "HOME", "LC_*", "XDG_*"},
		CustomVars:        make(map[string]string),
	})

	tests := []struct {
		name     string
		key      string
		expected bool
	}{
		{"exact match PATH", "PATH", true},
		{"exact match HOME", "HOME", true},
		{"wildcard match LC_ALL", "LC_ALL", true},
		{"wildcard match LC_CTYPE", "LC_CTYPE", true},
		{"wildcard match XDG_CONFIG_HOME", "XDG_CONFIG_HOME", true},
		{"wildcard no match partial", "LC", false},
		{"not allowed API_KEY", "API_KEY", false},
		{"not allowed SECRET", "SECRET", false},
		{"empty key", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := manager.isKeyAllowed(tt.key)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildSecureEnvironment(t *testing.T) {
	// Save original environment
	originalEnv := os.Environ()
	defer func() {
		os.Clearenv()
		for _, env := range originalEnv {
			parts := splitEnvVar(env)
			if len(parts) == 2 {
				os.Setenv(parts[0], parts[1])
			}
		}
	}()

	// Set up test environment
	os.Clearenv()

	// Set platform-specific test paths
	var testPath, testHome string
	if runtime.GOOS == "windows" {
		testPath = "C:\\Windows\\System32;C:\\Windows"
		testHome = "C:\\Users\\testuser"
	} else {
		testPath = "/usr/bin:/bin"
		testHome = "/home/user"
	}

	os.Setenv("PATH", testPath)
	os.Setenv("HOME", testHome)
	os.Setenv("SECRET_KEY", "secret123") // Should be filtered out
	os.Setenv("API_TOKEN", "token123")   // Should be filtered out
	os.Setenv("LC_ALL", "en_US.UTF-8")   // Should be included

	t.Run("with system inheritance enabled", func(t *testing.T) {
		manager := NewManager(&EnvConfig{
			InheritSystemSafe: true,
			AllowedSystemVars: []string{"PATH", "HOME", "LC_*"},
			CustomVars:        map[string]string{"CUSTOM_VAR": "custom_value"},
		})

		envVars := manager.BuildSecureEnvironment()

		// Convert to map for easier checking
		envMap := make(map[string]string)
		for _, envVar := range envVars {
			parts := splitEnvVar(envVar)
			if len(parts) == 2 {
				envMap[parts[0]] = parts[1]
			}
		}

		// Should include allowed system variables, but PATH should not be enhanced
		pathValue, pathExists := envMap["PATH"]
		assert.True(t, pathExists, "PATH should exist in the environment")
		assert.Equal(t, testPath, pathValue, "PATH should be inherited exactly from the environment, not enhanced")

		assert.Equal(t, testHome, envMap["HOME"])
		assert.Equal(t, "en_US.UTF-8", envMap["LC_ALL"])

		// Should include custom variables
		assert.Equal(t, "custom_value", envMap["CUSTOM_VAR"])

		// Should NOT include blocked variables
		assert.NotContains(t, envMap, "SECRET_KEY")
		assert.NotContains(t, envMap, "API_TOKEN")
	})

	t.Run("with system inheritance disabled", func(t *testing.T) {
		manager := NewManager(&EnvConfig{
			InheritSystemSafe: false,
			AllowedSystemVars: []string{"PATH", "HOME"},
			CustomVars:        map[string]string{"CUSTOM_VAR": "custom_value"},
		})

		envVars := manager.BuildSecureEnvironment()

		// Convert to map for easier checking
		envMap := make(map[string]string)
		for _, envVar := range envVars {
			parts := splitEnvVar(envVar)
			if len(parts) == 2 {
				envMap[parts[0]] = parts[1]
			}
		}

		// Should only include custom variables
		assert.Equal(t, "custom_value", envMap["CUSTOM_VAR"])

		// Should NOT include any system variables
		assert.NotContains(t, envMap, "PATH")
		assert.NotContains(t, envMap, "HOME")
		assert.NotContains(t, envMap, "LC_ALL")
	})
}

func TestGetSystemEnvVar(t *testing.T) {
	// Save original environment
	originalPath := os.Getenv("PATH")
	defer func() {
		if originalPath != "" {
			os.Setenv("PATH", originalPath)
		}
	}()

	// Set test environment
	var testPath string
	if runtime.GOOS == "windows" {
		testPath = "C:\\test\\bin;C:\\test\\usr\\bin"
	} else {
		testPath = "/test/bin:/test/usr/bin"
	}
	os.Setenv("PATH", testPath)

	manager := NewManager(&EnvConfig{
		AllowedSystemVars: []string{"PATH", "HOME"},
		CustomVars:        make(map[string]string),
	})

	t.Run("allowed variable exists", func(t *testing.T) {
		value, found := manager.GetSystemEnvVar("PATH")
		assert.True(t, found)
		assert.Equal(t, testPath, value)
	})

	t.Run("allowed variable does not exist", func(t *testing.T) {
		value, found := manager.GetSystemEnvVar("HOME")
		// HOME might or might not be set in test environment
		if found {
			assert.NotEmpty(t, value)
		} else {
			assert.Empty(t, value)
		}
	})

	t.Run("blocked variable", func(t *testing.T) {
		value, found := manager.GetSystemEnvVar("SECRET_KEY")
		assert.False(t, found)
		assert.Empty(t, value)
	})
}

func TestGetFilteredEnvCount(t *testing.T) {
	// Save original environment
	originalEnv := os.Environ()
	defer func() {
		os.Clearenv()
		for _, env := range originalEnv {
			parts := splitEnvVar(env)
			if len(parts) == 2 {
				os.Setenv(parts[0], parts[1])
			}
		}
	}()

	// Set up test environment
	os.Clearenv()

	// Set platform-specific test paths
	var testPath, testHome string
	if runtime.GOOS == "windows" {
		testPath = "C:\\Windows\\System32;C:\\Windows"
		testHome = "C:\\Users\\testuser"
	} else {
		testPath = "/usr/bin:/bin"
		testHome = "/home/user"
	}

	os.Setenv("PATH", testPath)
	os.Setenv("HOME", testHome)
	os.Setenv("SECRET_KEY", "secret123")
	os.Setenv("API_TOKEN", "token123")

	manager := NewManager(&EnvConfig{
		AllowedSystemVars: []string{"PATH", "HOME"},
		CustomVars:        make(map[string]string),
	})

	filteredCount, totalCount := manager.GetFilteredEnvCount()

	assert.Equal(t, 4, totalCount)    // All 4 env vars we set
	assert.Equal(t, 2, filteredCount) // Only PATH and HOME should be filtered through
}

func TestValidateConfig(t *testing.T) {
	manager := NewManager(nil)

	// Should always return nil with allow-list approach
	err := manager.ValidateConfig()
	assert.NoError(t, err)
}

func TestSecurityScenarios(t *testing.T) {
	t.Run("common secret patterns are blocked", func(t *testing.T) {
		manager := NewManager(DefaultEnvConfig())

		secretEnvVars := []string{
			"API_KEY=secret123",
			"SECRET_KEY=secret123",
			"AUTH_TOKEN=token123",
			"ACCESS_TOKEN=token123",
			"PASSWORD=password123",
			"DB_PASSWORD=dbpass",
			"AWS_ACCESS_KEY_ID=aws123",
			"AWS_SECRET_ACCESS_KEY=awssecret",
			"GITHUB_TOKEN=ghtoken",
			"STRIPE_SECRET_KEY=stripekey",
			"OPENAI_API_KEY=openaikey",
		}

		for _, envVar := range secretEnvVars {
			allowed := manager.isEnvVarAllowed(envVar)
			assert.False(t, allowed, "Secret environment variable should be blocked: %s", envVar)
		}
	})

	t.Run("safe system variables are allowed", func(t *testing.T) {
		manager := NewManager(DefaultEnvConfig())

		safeEnvVars := []string{
			"PATH=/usr/bin:/bin",
			"HOME=/home/user",
			"TMPDIR=/tmp",
			"SHELL=/bin/bash",
			"TERM=xterm-256color",
			"LANG=en_US.UTF-8",
			"LC_ALL=en_US.UTF-8",
			"USER=testuser",
		}

		for _, envVar := range safeEnvVars {
			allowed := manager.isEnvVarAllowed(envVar)
			assert.True(t, allowed, "Safe environment variable should be allowed: %s", envVar)
		}
	})

	t.Run("custom variables override system filtering", func(t *testing.T) {
		manager := NewManager(&EnvConfig{
			InheritSystemSafe: true,
			AllowedSystemVars: []string{"PATH"},
			CustomVars: map[string]string{
				"SAFE_CUSTOM": "value",
				// Note: Custom variables are added directly, bypassing system filtering
			},
		})

		envVars := manager.BuildSecureEnvironment()

		// Convert to map for checking
		envMap := make(map[string]string)
		for _, envVar := range envVars {
			parts := splitEnvVar(envVar)
			if len(parts) == 2 {
				envMap[parts[0]] = parts[1]
			}
		}

		assert.Contains(t, envMap, "SAFE_CUSTOM")
		assert.Equal(t, "value", envMap["SAFE_CUSTOM"])
	})
}

func TestPlatformSpecificBehavior(t *testing.T) {
	config := DefaultEnvConfig()

	if runtime.GOOS == "windows" {
		t.Run("windows specific variables", func(t *testing.T) {
			assert.Contains(t, config.AllowedSystemVars, "USERPROFILE")
			assert.Contains(t, config.AllowedSystemVars, "APPDATA")
			assert.Contains(t, config.AllowedSystemVars, "LOCALAPPDATA")
			assert.Contains(t, config.AllowedSystemVars, "PROGRAMFILES")
			assert.Contains(t, config.AllowedSystemVars, "SYSTEMROOT")
			assert.Contains(t, config.AllowedSystemVars, "COMSPEC")
		})
	} else {
		t.Run("unix specific variables", func(t *testing.T) {
			assert.Contains(t, config.AllowedSystemVars, "XDG_CONFIG_HOME")
			assert.Contains(t, config.AllowedSystemVars, "XDG_DATA_HOME")
			assert.Contains(t, config.AllowedSystemVars, "XDG_CACHE_HOME")
			assert.Contains(t, config.AllowedSystemVars, "XDG_RUNTIME_DIR")
		})
	}
}

func TestRealWorldNpxScenario(t *testing.T) {
	// Test the actual use case that prompted this feature
	// Save original environment
	originalPath := os.Getenv("PATH")
	defer func() {
		if originalPath != "" {
			os.Setenv("PATH", originalPath)
		}
	}()

	// Set up realistic environment
	var testPath string
	if runtime.GOOS == "windows" {
		testPath = "C:\\Program Files\\nodejs;C:\\Windows\\System32;C:\\Windows"
	} else {
		testPath = "/usr/local/bin:/usr/bin:/bin"
	}

	os.Setenv("PATH", testPath)

	// Create environment config for npx usage
	manager := NewManager(DefaultEnvConfig())

	envVars := manager.BuildSecureEnvironment()

	// Verify PATH is available for npx to find node/npm
	foundPath := false
	var actualPath string
	for _, envVar := range envVars {
		if strings.HasPrefix(envVar, "PATH=") {
			actualPath = envVar[5:] // Remove "PATH=" prefix
			foundPath = true
			break
		}
	}

	assert.True(t, foundPath, "PATH should be available in the environment")
	assert.Equal(t, testPath, actualPath, "PATH should be inherited exactly from the test setup")
}

// Helper function to split environment variable string
func splitEnvVar(envVar string) []string {
	parts := make([]string, 0, 2)
	if idx := findFirstEquals(envVar); idx != -1 {
		parts = append(parts, envVar[:idx], envVar[idx+1:])
	}
	return parts
}

// Helper function to find first equals sign (handles edge cases)
func findFirstEquals(s string) int {
	for i, c := range s {
		if c == '=' {
			return i
		}
	}
	return -1
}
