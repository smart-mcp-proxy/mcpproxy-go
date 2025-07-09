package upstream

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"mcpproxy-go/internal/config"
)

func TestSecureEnvironmentIntegration(t *testing.T) {
	// Skip on Windows due to shell differences
	if runtime.GOOS == "windows" {
		t.Skip("Skipping shell test on Windows")
	}

	// Save original environment
	originalEnv := os.Environ()
	defer func() {
		os.Clearenv()
		for _, env := range originalEnv {
			parts := strings.SplitN(env, "=", 2)
			if len(parts) == 2 {
				os.Setenv(parts[0], parts[1])
			}
		}
	}()

	// Set up test environment with both safe and unsafe variables
	os.Clearenv()
	os.Setenv("PATH", "/usr/bin:/bin")
	os.Setenv("HOME", "/tmp/test-home")
	os.Setenv("SECRET_API_KEY", "secret123")        // Should be filtered out
	os.Setenv("DATABASE_PASSWORD", "dbpass123")     // Should be filtered out
	os.Setenv("SAFE_TEST_VAR", "should_be_blocked") // Should be filtered out (not in allow list)

	// Create test config with secure environment settings
	cfg := config.DefaultConfig()

	// Create server config for testing environment variable passing
	serverConfig := &config.ServerConfig{
		Name:    "test-env-server",
		Command: "sh",
		Args:    []string{"-c", "env | sort"},
		Env: map[string]string{
			"CUSTOM_SERVER_VAR": "custom_value",
		},
		Enabled: true,
	}

	// Create upstream client
	logger := zap.NewNop()
	client, err := NewClient("test-id", serverConfig, logger, nil, cfg)
	require.NoError(t, err)
	require.NotNil(t, client)
	require.NotNil(t, client.envManager)

	// Test the environment filtering
	t.Run("environment manager filters correctly", func(t *testing.T) {
		envVars := client.envManager.BuildSecureEnvironment()

		// Convert to map for easier checking
		envMap := make(map[string]string)
		for _, envVar := range envVars {
			parts := strings.SplitN(envVar, "=", 2)
			if len(parts) == 2 {
				envMap[parts[0]] = parts[1]
			}
		}

		// Should include safe system variables with enhanced PATH discovery
		assert.Contains(t, envMap["PATH"], "/usr/bin")
		assert.Contains(t, envMap["PATH"], "/bin")
		// Enhanced PATH should include additional system paths when available
		pathValue := envMap["PATH"]
		assert.True(t, strings.Contains(pathValue, "/usr/bin") && strings.Contains(pathValue, "/bin"),
			"PATH should contain basic system paths, got: %s", pathValue)
		assert.Equal(t, "/tmp/test-home", envMap["HOME"])

		// Should include custom server variables
		assert.Equal(t, "custom_value", envMap["CUSTOM_SERVER_VAR"])

		// Should NOT include secrets
		assert.NotContains(t, envMap, "SECRET_API_KEY")
		assert.NotContains(t, envMap, "DATABASE_PASSWORD")
		assert.NotContains(t, envMap, "SAFE_TEST_VAR") // Not in allow list
	})

	t.Run("filtered environment count", func(t *testing.T) {
		filteredCount, totalCount := client.envManager.GetFilteredEnvCount()

		// We set 5 environment variables (PATH, HOME, SECRET_API_KEY, DATABASE_PASSWORD, SAFE_TEST_VAR)
		assert.Equal(t, 5, totalCount)

		// At minimum PATH and HOME should be filtered through (2 safe system vars)
		// May include additional safe system variables from the allow list
		assert.GreaterOrEqual(t, filteredCount, 2, "Should filter at least PATH and HOME")
	})
}

func TestConfigurationIntegration(t *testing.T) {
	t.Run("default config includes environment settings", func(t *testing.T) {
		cfg := config.DefaultConfig()

		require.NotNil(t, cfg.Environment)
		assert.True(t, cfg.Environment.InheritSystemSafe)
		assert.NotEmpty(t, cfg.Environment.AllowedSystemVars)
		assert.NotNil(t, cfg.Environment.CustomVars)

		// Verify essential variables are in the default config
		assert.Contains(t, cfg.Environment.AllowedSystemVars, "PATH")
		assert.Contains(t, cfg.Environment.AllowedSystemVars, "HOME")
	})

	t.Run("config validation ensures environment config exists", func(t *testing.T) {
		cfg := &config.Config{
			Listen:      ":8080",
			Environment: nil, // Intentionally nil
		}

		err := cfg.Validate()
		assert.NoError(t, err)
		assert.NotNil(t, cfg.Environment) // Should be set during validation
	})

	t.Run("upstream manager uses global config", func(t *testing.T) {
		cfg := config.DefaultConfig()
		logger := zap.NewNop()

		manager := NewManager(logger, cfg)
		require.NotNil(t, manager)
		assert.Equal(t, cfg, manager.globalConfig)
	})
}

func TestServerSpecificEnvironmentVariables(t *testing.T) {
	// Test that server-specific environment variables are properly combined with global settings

	globalConfig := config.DefaultConfig()
	globalConfig.Environment.CustomVars = map[string]string{
		"GLOBAL_VAR": "global_value",
	}

	serverConfig := &config.ServerConfig{
		Name:    "test-server",
		Command: "echo",
		Args:    []string{"test"},
		Env: map[string]string{
			"SERVER_VAR":   "server_value",
			"OVERRIDE_VAR": "server_override",
		},
		Enabled: true,
	}

	// Also add the override var to global config to test precedence
	globalConfig.Environment.CustomVars["OVERRIDE_VAR"] = "global_value"

	logger := zap.NewNop()
	client, err := NewClient("test-id", serverConfig, logger, nil, globalConfig)
	require.NoError(t, err)

	envVars := client.envManager.BuildSecureEnvironment()

	// Convert to map for checking
	envMap := make(map[string]string)
	for _, envVar := range envVars {
		parts := strings.SplitN(envVar, "=", 2)
		if len(parts) == 2 {
			envMap[parts[0]] = parts[1]
		}
	}

	// Should have global custom variables
	assert.Equal(t, "global_value", envMap["GLOBAL_VAR"])

	// Should have server-specific variables
	assert.Equal(t, "server_value", envMap["SERVER_VAR"])

	// Server-specific variables should override global ones
	assert.Equal(t, "server_override", envMap["OVERRIDE_VAR"])
}

func TestEnvironmentInheritanceDisabled(t *testing.T) {
	// Test scenario where system environment inheritance is disabled

	// Save original environment
	originalPath := os.Getenv("PATH")
	defer func() {
		if originalPath != "" {
			os.Setenv("PATH", originalPath)
		}
	}()

	// Set up system environment
	os.Setenv("PATH", "/usr/bin:/bin")

	// Create config with inheritance disabled
	cfg := config.DefaultConfig()
	cfg.Environment.InheritSystemSafe = false
	cfg.Environment.CustomVars = map[string]string{
		"CUSTOM_ONLY": "custom_value",
	}

	serverConfig := &config.ServerConfig{
		Name:    "test-server",
		Command: "echo",
		Args:    []string{"test"},
		Enabled: true,
	}

	logger := zap.NewNop()
	client, err := NewClient("test-id", serverConfig, logger, nil, cfg)
	require.NoError(t, err)

	envVars := client.envManager.BuildSecureEnvironment()

	// Convert to map for checking
	envMap := make(map[string]string)
	for _, envVar := range envVars {
		parts := strings.SplitN(envVar, "=", 2)
		if len(parts) == 2 {
			envMap[parts[0]] = parts[1]
		}
	}

	// Should NOT include system PATH since inheritance is disabled
	assert.NotContains(t, envMap, "PATH")

	// Should include custom variables
	assert.Equal(t, "custom_value", envMap["CUSTOM_ONLY"])
}

func TestRealWorldNpxScenario(t *testing.T) {
	// Test the exact scenario that prompted this feature: npx commands failing due to missing PATH

	// Skip if npx is not available
	if _, err := exec.LookPath("npx"); err != nil {
		t.Skip("npx not available in PATH, skipping real-world test")
	}

	// Save original environment
	originalPath := os.Getenv("PATH")
	defer func() {
		if originalPath != "" {
			os.Setenv("PATH", originalPath)
		}
	}()

	// Set a minimal PATH for testing
	os.Setenv("PATH", originalPath) // Use the real PATH so npx can be found

	cfg := config.DefaultConfig()

	// Create server config for npx command
	serverConfig := &config.ServerConfig{
		Name:    "npx-test-server",
		Command: "npx",
		Args:    []string{"--version"}, // Simple npx command
		Enabled: true,
	}

	logger := zap.NewNop()
	client, err := NewClient("test-id", serverConfig, logger, nil, cfg)
	require.NoError(t, err)

	// Verify that PATH is available in the secure environment
	envVars := client.envManager.BuildSecureEnvironment()

	foundPath := false
	for _, envVar := range envVars {
		if strings.HasPrefix(envVar, "PATH=") {
			foundPath = true
			break
		}
	}

	assert.True(t, foundPath, "PATH should be available for npx to find Node.js/npm")

	// Test that we can determine the transport type (should be stdio for command-based configs)
	transportType := client.determineTransportType()
	assert.Equal(t, "stdio", transportType)
}

func TestSecurityCompliance(t *testing.T) {
	// Test that common security vulnerabilities are prevented

	// Set up environment with various secret patterns
	secretEnvVars := map[string]string{
		"API_KEY":               "secret123",
		"SECRET_KEY":            "topsecret",
		"AUTH_TOKEN":            "token123",
		"PASSWORD":              "password123",
		"DB_PASSWORD":           "dbpass",
		"AWS_ACCESS_KEY_ID":     "AKIA123",
		"AWS_SECRET_ACCESS_KEY": "aws_secret",
		"GITHUB_TOKEN":          "ghp_token",
		"STRIPE_SECRET_KEY":     "sk_test_123",
		"OPENAI_API_KEY":        "sk-openai123",
		"ANTHROPIC_API_KEY":     "claude-key",
	}

	// Save and restore environment
	originalEnv := make(map[string]string)
	for key := range secretEnvVars {
		originalEnv[key] = os.Getenv(key)
		os.Setenv(key, secretEnvVars[key])
	}
	defer func() {
		for key, value := range originalEnv {
			if value == "" {
				os.Unsetenv(key)
			} else {
				os.Setenv(key, value)
			}
		}
	}()

	cfg := config.DefaultConfig()
	serverConfig := &config.ServerConfig{
		Name:    "security-test-server",
		Command: "env",
		Enabled: true,
	}

	logger := zap.NewNop()
	client, err := NewClient("test-id", serverConfig, logger, nil, cfg)
	require.NoError(t, err)

	envVars := client.envManager.BuildSecureEnvironment()

	// Convert to map for checking
	envMap := make(map[string]string)
	for _, envVar := range envVars {
		parts := strings.SplitN(envVar, "=", 2)
		if len(parts) == 2 {
			envMap[parts[0]] = parts[1]
		}
	}

	// Verify that NO secret environment variables are included
	for secretKey := range secretEnvVars {
		assert.NotContains(t, envMap, secretKey, "Secret environment variable should be filtered: %s", secretKey)
	}

	// Verify that safe variables are still included
	if originalPath := os.Getenv("PATH"); originalPath != "" {
		assert.Contains(t, envMap, "PATH", "PATH should be included for process execution")
	}
}

func TestWildcardMatching(t *testing.T) {
	// Test that wildcard patterns work correctly for locale variables

	// Save original environment
	localeVars := []string{"LC_ALL", "LC_CTYPE", "LC_NUMERIC", "LC_TIME", "LC_COLLATE"}
	originalValues := make(map[string]string)
	for _, key := range localeVars {
		originalValues[key] = os.Getenv(key)
		os.Setenv(key, "en_US.UTF-8")
	}
	defer func() {
		for key, value := range originalValues {
			if value == "" {
				os.Unsetenv(key)
			} else {
				os.Setenv(key, value)
			}
		}
	}()

	cfg := config.DefaultConfig()
	serverConfig := &config.ServerConfig{
		Name:    "locale-test-server",
		Command: "locale",
		Enabled: true,
	}

	logger := zap.NewNop()
	client, err := NewClient("test-id", serverConfig, logger, nil, cfg)
	require.NoError(t, err)

	envVars := client.envManager.BuildSecureEnvironment()

	// Convert to map for checking
	envMap := make(map[string]string)
	for _, envVar := range envVars {
		parts := strings.SplitN(envVar, "=", 2)
		if len(parts) == 2 {
			envMap[parts[0]] = parts[1]
		}
	}

	// Verify that locale variables are included (they should match LC_* pattern)
	for _, localeVar := range localeVars {
		if os.Getenv(localeVar) != "" {
			assert.Contains(t, envMap, localeVar, "Locale variable should be included: %s", localeVar)
			assert.Equal(t, "en_US.UTF-8", envMap[localeVar])
		}
	}
}
