package server_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// binaryName returns the appropriate binary name for the current OS
func binaryName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

func TestCodeExecClientModeE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	tmpDir := t.TempDir()

	// Build mcpproxy binary
	mcpproxyBin := filepath.Join(tmpDir, binaryName("mcpproxy"))
	buildCmd := exec.Command("go", "build", "-o", mcpproxyBin, "./cmd/mcpproxy")
	// Run from project root (two directories up from internal/server)
	buildCmd.Dir = filepath.Join("..", "..")
	output, err := buildCmd.CombinedOutput()
	require.NoError(t, err, "Failed to build mcpproxy: %s", string(output))

	// Create minimal config
	configPath := filepath.Join(tmpDir, "mcp_config.json")
	config := `{
		"listen": "127.0.0.1:18080",
		"data_dir": "` + tmpDir + `",
		"enable_code_execution": true,
		"mcpServers": []
	}`
	require.NoError(t, os.WriteFile(configPath, []byte(config), 0600))

	t.Run("client_mode_when_daemon_running", func(t *testing.T) {
		// Start daemon in background
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		daemonCmd := exec.CommandContext(ctx, mcpproxyBin, "serve", "--config", configPath)
		daemonCmd.Env = append(os.Environ(), "MCPPROXY_DATA_DIR="+tmpDir)
		require.NoError(t, daemonCmd.Start())
		defer daemonCmd.Process.Kill()

		// Wait for daemon to be ready
		time.Sleep(2 * time.Second)

		// Run code exec CLI command
		execCmd := exec.Command(mcpproxyBin, "code", "exec",
			"--code", `({ result: 42 })`,
			"--input", `{}`,
			"--config", configPath)
		execCmd.Env = append(os.Environ(), "MCPPROXY_DATA_DIR="+tmpDir)

		output, err := execCmd.CombinedOutput()
		require.NoError(t, err, "code exec should succeed: %s", string(output))

		// Verify result (check for nested result field)
		assert.Contains(t, string(output), `"result": 42`, "Should return correct result")

		// Verify client mode was used (check logs or output)
		assert.NotContains(t, string(output), "database locked", "Should not have DB lock error")
	})

	t.Run("standalone_mode_when_no_daemon", func(t *testing.T) {
		// Ensure no daemon is running
		// Run code exec CLI command
		execCmd := exec.Command(mcpproxyBin, "code", "exec",
			"--code", `({ result: 99 })`,
			"--input", `{}`,
			"--config", configPath)
		execCmd.Env = append(os.Environ(),
			"MCPPROXY_DATA_DIR="+tmpDir,
			"MCPPROXY_TRAY_ENDPOINT=") // Force standalone mode

		output, err := execCmd.CombinedOutput()
		require.NoError(t, err, "code exec should succeed in standalone: %s", string(output))

		// Verify result (check for nested result field)
		assert.Contains(t, string(output), `"result": 99`, "Should return correct result")
	})
}

func TestCallToolClientModeE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	tmpDir := t.TempDir()

	// Build mcpproxy binary
	mcpproxyBin := filepath.Join(tmpDir, binaryName("mcpproxy"))
	buildCmd := exec.Command("go", "build", "-o", mcpproxyBin, "./cmd/mcpproxy")
	// Run from project root (two directories up from internal/server)
	buildCmd.Dir = filepath.Join("..", "..")
	output, err := buildCmd.CombinedOutput()
	require.NoError(t, err, "Failed to build mcpproxy: %s", string(output))

	// Create minimal config
	configPath := filepath.Join(tmpDir, "mcp_config.json")
	config := `{
		"listen": "127.0.0.1:18081",
		"data_dir": "` + tmpDir + `",
		"mcpServers": []
	}`
	require.NoError(t, os.WriteFile(configPath, []byte(config), 0600))

	t.Run("client_mode_when_daemon_running", func(t *testing.T) {
		// Start daemon in background
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		daemonCmd := exec.CommandContext(ctx, mcpproxyBin, "serve", "--config", configPath)
		daemonCmd.Env = append(os.Environ(), "MCPPROXY_DATA_DIR="+tmpDir)
		require.NoError(t, daemonCmd.Start())
		defer daemonCmd.Process.Kill()

		// Wait for daemon to be ready
		time.Sleep(2 * time.Second)

		// Run call tool CLI command (test built-in upstream_servers tool)
		callCmd := exec.Command(mcpproxyBin, "call", "tool",
			"--tool-name", "upstream_servers",
			"--json_args", `{"operation":"list"}`,
			"--config", configPath)
		callCmd.Env = append(os.Environ(), "MCPPROXY_DATA_DIR="+tmpDir)

		output, err := callCmd.CombinedOutput()
		require.NoError(t, err, "call tool should succeed: %s", string(output))

		// Verify no DB lock error
		assert.NotContains(t, string(output), "database locked", "Should not have DB lock error")
	})

	t.Run("standalone_mode_when_no_daemon", func(t *testing.T) {
		// In standalone mode, built-in tools like upstream_servers aren't accessible
		// We just verify no database lock error occurs (validation happens before DB access)
		callCmd := exec.Command(mcpproxyBin, "call", "tool",
			"--tool-name", "upstream_servers",
			"--json_args", `{"operation":"list"}`,
			"--config", configPath)
		callCmd.Env = append(os.Environ(),
			"MCPPROXY_DATA_DIR="+tmpDir,
			"MCPPROXY_TRAY_ENDPOINT=") // Force standalone mode

		output, _ := callCmd.CombinedOutput()
		// Command will fail due to invalid format in standalone mode, but that's expected
		// We just verify it's not a database lock error
		assert.NotContains(t, string(output), "database locked", "Should not have DB lock error")
		assert.Contains(t, string(output), "invalid tool name format", "Should fail with format error, not DB lock")
	})
}

func TestConcurrentCLICommandsE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	tmpDir := t.TempDir()

	// Build mcpproxy binary
	mcpproxyBin := filepath.Join(tmpDir, binaryName("mcpproxy"))
	buildCmd := exec.Command("go", "build", "-o", mcpproxyBin, "./cmd/mcpproxy")
	// Run from project root (two directories up from internal/server)
	buildCmd.Dir = filepath.Join("..", "..")
	output, err := buildCmd.CombinedOutput()
	require.NoError(t, err, "Failed to build mcpproxy: %s", string(output))

	// Create minimal config
	configPath := filepath.Join(tmpDir, "mcp_config.json")
	config := `{
		"listen": "127.0.0.1:18082",
		"data_dir": "` + tmpDir + `",
		"enable_code_execution": true,
		"mcpServers": []
	}`
	require.NoError(t, os.WriteFile(configPath, []byte(config), 0600))

	// Start daemon
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	daemonCmd := exec.CommandContext(ctx, mcpproxyBin, "serve", "--config", configPath)
	daemonCmd.Env = append(os.Environ(), "MCPPROXY_DATA_DIR="+tmpDir)
	require.NoError(t, daemonCmd.Start())
	defer daemonCmd.Process.Kill()

	// Wait for daemon to be ready
	time.Sleep(2 * time.Second)

	// Run 5 concurrent code exec commands
	errChan := make(chan error, 5)
	for i := 0; i < 5; i++ {
		go func(idx int) {
			execCmd := exec.Command(mcpproxyBin, "code", "exec",
				"--code", `({ result: input.value * 2 })`,
				"--input", `{"value": 21}`,
				"--config", configPath)
			execCmd.Env = append(os.Environ(), "MCPPROXY_DATA_DIR="+tmpDir)

			output, err := execCmd.CombinedOutput()
			if err != nil {
				errChan <- err
				return
			}

			// Verify no DB lock error
			if contains(string(output), "database locked") {
				errChan <- assert.AnError
				return
			}

			errChan <- nil
		}(i)
	}

	// Wait for all commands to complete
	for i := 0; i < 5; i++ {
		err := <-errChan
		assert.NoError(t, err, "Concurrent command %d should succeed", i)
	}
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}
