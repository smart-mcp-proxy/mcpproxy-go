package core

import (
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"

	"github.com/stretchr/testify/assert"
)

func TestBuildDockerArgsWithLogging(t *testing.T) {
	// Test with default global config (no log driver override but log limits always applied)
	t.Run("default logging config applies log limits without driver override", func(t *testing.T) {
		globalConfig := config.DefaultDockerIsolationConfig()
		im := NewIsolationManager(globalConfig)

		serverConfig := &config.ServerConfig{
			Name:    "test-server",
			Command: "python",
			Args:    []string{"-m", "mcp_server"},
		}

		args, err := im.BuildDockerArgs(serverConfig, "python")
		assert.NoError(t, err)

		// Verify NO log driver is specified (uses Docker default)
		assert.NotContains(t, args, "--log-driver")

		// But log limits are ALWAYS applied to prevent disk space issues
		assert.Contains(t, args, "--log-opt")
		assert.Contains(t, args, "max-size=100m")
		assert.Contains(t, args, "max-file=3")
	})

	// Test with custom global config
	t.Run("custom global logging config", func(t *testing.T) {
		globalConfig := config.DefaultDockerIsolationConfig()
		globalConfig.LogDriver = logDriverJSONFile
		globalConfig.LogMaxSize = "50m"
		globalConfig.LogMaxFiles = "5"

		im := NewIsolationManager(globalConfig)

		serverConfig := &config.ServerConfig{
			Name:    "test-server",
			Command: "python",
			Args:    []string{"-m", "mcp_server"},
		}

		args, err := im.BuildDockerArgs(serverConfig, "python")
		assert.NoError(t, err)

		// Verify custom log configuration is included
		assert.Contains(t, args, "--log-driver")
		assert.Contains(t, args, logDriverJSONFile)
		assert.Contains(t, args, "--log-opt")
		assert.Contains(t, args, "max-size=50m")
		assert.Contains(t, args, "max-file=5")
	})

	// Test with server-specific override
	t.Run("server-specific logging override", func(t *testing.T) {
		globalConfig := config.DefaultDockerIsolationConfig()
		im := NewIsolationManager(globalConfig)

		serverConfig := &config.ServerConfig{
			Name:    "test-server",
			Command: "python",
			Args:    []string{"-m", "mcp_server"},
			Isolation: &config.IsolationConfig{
				LogDriver:   logDriverJSONFile,
				LogMaxSize:  "200m",
				LogMaxFiles: "10",
			},
		}

		args, err := im.BuildDockerArgs(serverConfig, "python")
		assert.NoError(t, err)

		// Verify server-specific log configuration is used
		assert.Contains(t, args, "--log-driver")
		assert.Contains(t, args, logDriverJSONFile)
		assert.Contains(t, args, "--log-opt")
		assert.Contains(t, args, "max-size=200m")
		assert.Contains(t, args, "max-file=10")
	})

	// Test with non-json-file driver (log options still applied for disk space protection)
	t.Run("non-json-file driver with log options", func(t *testing.T) {
		globalConfig := config.DefaultDockerIsolationConfig()
		globalConfig.LogDriver = "none"

		im := NewIsolationManager(globalConfig)

		serverConfig := &config.ServerConfig{
			Name:    "test-server",
			Command: "python",
			Args:    []string{"-m", "mcp_server"},
		}

		args, err := im.BuildDockerArgs(serverConfig, "python")
		assert.NoError(t, err)

		// Verify log driver is set
		assert.Contains(t, args, "--log-driver")
		assert.Contains(t, args, "none")

		// Log options are still applied for disk space protection (even if driver ignores them)
		assert.Contains(t, args, "--log-opt")
		assert.Contains(t, args, "max-size=100m")
		assert.Contains(t, args, "max-file=3")
	})

	// Test without log driver configuration (uses Docker system default but applies log limits)
	t.Run("no log driver configuration uses Docker default with log limits", func(t *testing.T) {
		globalConfig := config.DefaultDockerIsolationConfig()
		// LogDriver is already "" by default now

		im := NewIsolationManager(globalConfig)

		serverConfig := &config.ServerConfig{
			Name:    "test-server",
			Command: "python",
			Args:    []string{"-m", "mcp_server"},
		}

		args, err := im.BuildDockerArgs(serverConfig, "python")
		assert.NoError(t, err)

		// Verify no log driver is specified (uses Docker system default)
		assert.NotContains(t, args, "--log-driver")

		// But log limits are always applied
		assert.Contains(t, args, "--log-opt")
		assert.Contains(t, args, "max-size=100m")
		assert.Contains(t, args, "max-file=3")
	})

	// Test explicit json-file driver with default log limits
	t.Run("explicit json-file driver with log limits", func(t *testing.T) {
		globalConfig := config.DefaultDockerIsolationConfig()
		globalConfig.LogDriver = logDriverJSONFile // Explicitly set

		im := NewIsolationManager(globalConfig)

		serverConfig := &config.ServerConfig{
			Name:    "test-server",
			Command: "python",
			Args:    []string{"-m", "mcp_server"},
		}

		args, err := im.BuildDockerArgs(serverConfig, "python")
		assert.NoError(t, err)

		// Verify log configuration is applied when explicitly set
		assert.Contains(t, args, "--log-driver")
		assert.Contains(t, args, logDriverJSONFile)
		assert.Contains(t, args, "--log-opt")
		assert.Contains(t, args, "max-size=100m")
		assert.Contains(t, args, "max-file=3")
	})
}

func TestDockerArgsOrderWithLogging(t *testing.T) {
	// Test that logging options come before other options in the correct order
	globalConfig := config.DefaultDockerIsolationConfig()
	globalConfig.LogDriver = logDriverJSONFile // Explicitly enable logging to test order
	im := NewIsolationManager(globalConfig)

	serverConfig := &config.ServerConfig{
		Name:    "test-server",
		Command: "python",
		Args:    []string{"-m", "mcp_server"},
		Env: map[string]string{
			"API_KEY": "test-key",
		},
	}

	args, err := im.BuildDockerArgs(serverConfig, "python")
	assert.NoError(t, err)

	// Find the positions of key arguments
	logDriverIndex, networkIndex, memoryIndex := -1, -1, -1
	for i, arg := range args {
		switch arg {
		case "--log-driver":
			logDriverIndex = i
		case "--network":
			networkIndex = i
		case "--memory":
			memoryIndex = i
		}
	}

	// Verify log driver comes before network and memory options
	assert.NotEqual(t, -1, logDriverIndex, "log-driver should be present")
	if networkIndex != -1 {
		assert.Less(t, logDriverIndex, networkIndex, "log-driver should come before network")
	}
	if memoryIndex != -1 {
		assert.Less(t, logDriverIndex, memoryIndex, "log-driver should come before memory")
	}
}
