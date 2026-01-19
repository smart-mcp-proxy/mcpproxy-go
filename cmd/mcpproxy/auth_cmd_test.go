package main

import (
	"context"
	"runtime"
	"strings"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/socket"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShouldUseAuthDaemon(t *testing.T) {
	// Test with non-existent directory
	result := shouldUseAuthDaemon("/tmp/nonexistent-mcpproxy-test-dir-auth-88888")
	assert.False(t, result, "shouldUseAuthDaemon should return false for non-existent directory")

	// Test with existing directory but no socket
	tmpDir := t.TempDir()
	result = shouldUseAuthDaemon(tmpDir)
	assert.False(t, result, "shouldUseAuthDaemon should return false when socket doesn't exist")
}

func TestAuthStatus_RequiresDaemon(t *testing.T) {
	tmpDir := t.TempDir()

	// Test that auth status requires daemon
	result := shouldUseAuthDaemon(tmpDir)
	assert.False(t, result, "Should return false when daemon not running")
}

func TestDetectSocketPath_Auth(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := socket.DetectSocketPath(tmpDir)

	assert.NotEmpty(t, socketPath, "DetectSocketPath should return non-empty path")

	// Platform-specific assertions
	if runtime.GOOS == "windows" {
		// Windows: Named pipes use global namespace with hash
		assert.True(t, strings.HasPrefix(socketPath, "npipe:////./pipe/mcpproxy-"),
			"Windows socket should be a named pipe")
	} else {
		// Unix: Socket file is within data directory
		assert.Contains(t, socketPath, tmpDir, "Socket path should be within data directory")
		assert.True(t, strings.HasPrefix(socketPath, "unix://"),
			"Unix socket should have unix:// prefix")
	}
}

func TestAuthLogin_FlagValidation(t *testing.T) {
	tests := []struct {
		name        string
		serverName  string
		allFlag     bool
		wantErr     bool
		errContains string
	}{
		{
			name:        "both server and all flags",
			serverName:  "test-server",
			allFlag:     true,
			wantErr:     true,
			errContains: "cannot use both --server and --all",
		},
		{
			name:        "neither server nor all flags",
			serverName:  "",
			allFlag:     false,
			wantErr:     true,
			errContains: "either --server or --all flag is required",
		},
		{
			name:       "only server flag - valid",
			serverName: "test-server",
			allFlag:    false,
			wantErr:    false, // validation passes, but will fail later due to no daemon
		},
		{
			name:       "only all flag - valid",
			serverName: "",
			allFlag:    true,
			wantErr:    false, // validation passes, but will fail later due to no daemon
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set up test flags
			authServerName = tt.serverName
			authAll = tt.allFlag
			authConfigPath = "" // Use default
			authTimeout = 0     // Will use command default

			// Create a mock command
			cmd := &cobra.Command{
				Use: "login",
			}

			// Run the validation logic (first part of runAuthLogin)
			err := runAuthLogin(cmd, []string{})

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
			} else {
				// For these cases, we expect failure due to no daemon/config,
				// but NOT due to flag validation
				if err != nil {
					assert.NotContains(t, err.Error(), "cannot use both")
					assert.NotContains(t, err.Error(), "either --server or --all")
				}
			}

			// Reset flags
			authServerName = ""
			authAll = false
		})
	}
}

func TestAuthLogin_SilenceUsageAfterValidation(t *testing.T) {
	// Set up valid flags
	authServerName = "test-server"
	authAll = false
	authConfigPath = "" // Use default, will fail to load but that's after validation
	defer func() {
		authServerName = ""
		authAll = false
	}()

	cmd := &cobra.Command{
		Use: "login",
	}

	// SilenceUsage should be false initially
	assert.False(t, cmd.SilenceUsage, "SilenceUsage should be false initially")

	// Run the command (it will fail due to no config, but we just check the flag)
	_ = runAuthLogin(cmd, []string{})

	// SilenceUsage should be true after flag validation
	assert.True(t, cmd.SilenceUsage, "SilenceUsage should be true after flag validation")
}

func TestRunAuthLoginAll_NoDaemon(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	err := runAuthLoginAll(ctx, tmpDir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "requires running daemon")
}

func TestAuthLogin_ForceFlagWithoutAll(t *testing.T) {
	// The --force flag should only be meaningful with --all
	// but it's not an error to use it with --server (it just has no effect)
	authServerName = "test-server"
	authAll = false
	authForce = true
	authConfigPath = ""
	defer func() {
		authServerName = ""
		authAll = false
		authForce = false
	}()

	cmd := &cobra.Command{
		Use: "login",
	}

	// This should not fail validation
	err := runAuthLogin(cmd, []string{})

	// It will fail for other reasons (no daemon/config), but not validation
	if err != nil {
		assert.NotContains(t, err.Error(), "cannot use both")
		assert.NotContains(t, err.Error(), "either --server or --all")
	}
}
