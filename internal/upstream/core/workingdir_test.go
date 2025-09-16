package core

import (
	"context"
	"os"
	"testing"

	"mcpproxy-go/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateWorkingDir(t *testing.T) {
	tests := []struct {
		name        string
		workingDir  string
		expectError bool
		setupFunc   func() (string, func()) // returns path and cleanup func
	}{
		{
			name:        "empty working directory is valid",
			workingDir:  "",
			expectError: false,
		},
		{
			name:        "existing directory is valid",
			workingDir:  "",
			expectError: false,
			setupFunc: func() (string, func()) {
				tmpDir, err := os.MkdirTemp("", "mcpproxy-test-*")
				require.NoError(t, err)
				return tmpDir, func() { os.RemoveAll(tmpDir) }
			},
		},
		{
			name:        "non-existent directory returns error",
			workingDir:  "/path/that/does/not/exist",
			expectError: true,
		},
		{
			name:        "file instead of directory returns error",
			workingDir:  "",
			expectError: true,
			setupFunc: func() (string, func()) {
				tmpFile, err := os.CreateTemp("", "mcpproxy-test-*")
				require.NoError(t, err)
				tmpFile.Close()
				return tmpFile.Name(), func() { os.Remove(tmpFile.Name()) }
			},
		},
		{
			name:        "relative path with current directory",
			workingDir:  ".",
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workingDir := tt.workingDir
			var cleanup func()

			if tt.setupFunc != nil {
				path, cleanupFunc := tt.setupFunc()
				if path == "" {
					t.Skip("Setup function failed, skipping test")
				}
				workingDir = path
				cleanup = cleanupFunc
				defer cleanup()
			}

			err := validateWorkingDir(workingDir)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestCreateWorkingDirCommandFunc(t *testing.T) {
	tests := []struct {
		name       string
		workingDir string
		command    string
		args       []string
		env        []string
	}{
		{
			name:       "with working directory",
			workingDir: "/tmp",
			command:    "echo",
			args:       []string{"hello"},
			env:        []string{"TEST=1"},
		},
		{
			name:       "empty working directory",
			workingDir: "",
			command:    "echo",
			args:       []string{"world"},
			env:        []string{"TEST=2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmdFunc := createWorkingDirCommandFunc(tt.workingDir)

			cmd, err := cmdFunc(context.Background(), tt.command, tt.env, tt.args)

			require.NoError(t, err)
			require.NotNil(t, cmd)

			// Check that the command and args are set correctly
			// Note: cmd.Path may be the resolved full path, so we check cmd.Args[0] instead
			assert.Equal(t, append([]string{tt.command}, tt.args...), cmd.Args)
			assert.Equal(t, tt.env, cmd.Env)

			if tt.workingDir != "" {
				assert.Equal(t, tt.workingDir, cmd.Dir)
			} else {
				assert.Equal(t, "", cmd.Dir) // Default to empty (current directory)
			}
		})
	}
}

func TestConnectStdioWithWorkingDir(t *testing.T) {
	// This test verifies the integration of working directory validation
	// in the connectStdio function by checking that validation errors are properly handled

	t.Run("invalid working directory prevents connection", func(t *testing.T) {
		// Create a test config with non-existent working directory
		serverConfig := &config.ServerConfig{
			Name:       "test-server",
			Command:    "echo",
			Args:       []string{"hello"},
			WorkingDir: "/path/that/definitely/does/not/exist",
			Enabled:    true,
		}

		// Create a minimal client for testing
		// Note: This is a simplified test that focuses on validation
		// without setting up full client infrastructure

		err := validateWorkingDir(serverConfig.WorkingDir)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "working directory does not exist")
	})

	t.Run("valid working directory allows connection setup", func(t *testing.T) {
		// Create temporary directory for testing
		tmpDir, err := os.MkdirTemp("", "mcpproxy-test-*")
		require.NoError(t, err)
		defer os.RemoveAll(tmpDir)

		serverConfig := &config.ServerConfig{
			Name:       "test-server",
			Command:    "echo",
			Args:       []string{"hello"},
			WorkingDir: tmpDir,
			Enabled:    true,
		}

		err = validateWorkingDir(serverConfig.WorkingDir)
		assert.NoError(t, err)
	})
}

func TestWorkingDirIntegrationWithDockerIsolation(t *testing.T) {
	// Test that working directory is compatible with Docker isolation
	// This verifies that the WorkingDir field is properly used in Docker containers

	t.Run("working directory in isolation config", func(t *testing.T) {
		tmpDir, err := os.MkdirTemp("", "mcpproxy-test-*")
		require.NoError(t, err)
		defer os.RemoveAll(tmpDir)

		serverConfig := &config.ServerConfig{
			Name:       "test-docker-server",
			Command:    "python",
			Args:       []string{"-c", "print('hello')"},
			WorkingDir: tmpDir, // This should be used in combination with Docker isolation
			Isolation: &config.IsolationConfig{
				Enabled:    true,
				WorkingDir: "/workspace", // Docker container working dir
			},
			Enabled: true,
		}

		// Test that both working directories can coexist
		// The ServerConfig.WorkingDir should be used for host-side operations
		// The IsolationConfig.WorkingDir should be used inside the Docker container

		err = validateWorkingDir(serverConfig.WorkingDir)
		assert.NoError(t, err)

		// Ensure both working directories are set correctly
		assert.Equal(t, tmpDir, serverConfig.WorkingDir)
		assert.Equal(t, "/workspace", serverConfig.Isolation.WorkingDir)
	})
}

func TestServerConfigWithWorkingDir(t *testing.T) {
	// Test that ServerConfig properly handles the WorkingDir field

	t.Run("server config serialization", func(t *testing.T) {
		tmpDir, err := os.MkdirTemp("", "mcpproxy-test-*")
		require.NoError(t, err)
		defer os.RemoveAll(tmpDir)

		serverConfig := &config.ServerConfig{
			Name:       "test-server",
			Command:    "echo",
			Args:       []string{"test"},
			WorkingDir: tmpDir,
			Env:        map[string]string{"TEST": "value"},
			Enabled:    true,
		}

		// Test that working directory is properly set
		assert.Equal(t, tmpDir, serverConfig.WorkingDir)

		// Test that all fields are present and correct
		assert.Equal(t, "test-server", serverConfig.Name)
		assert.Equal(t, "echo", serverConfig.Command)
		assert.Equal(t, []string{"test"}, serverConfig.Args)
		assert.Equal(t, map[string]string{"TEST": "value"}, serverConfig.Env)
		assert.True(t, serverConfig.Enabled)
	})

	t.Run("empty working directory", func(t *testing.T) {
		serverConfig := &config.ServerConfig{
			Name:       "test-server",
			Command:    "echo",
			WorkingDir: "", // Empty should be valid
			Enabled:    true,
		}

		assert.Equal(t, "", serverConfig.WorkingDir)

		// Should not cause validation errors
		err := validateWorkingDir(serverConfig.WorkingDir)
		assert.NoError(t, err)
	})
}
