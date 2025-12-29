package server_test

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"mcpproxy-go/internal/socket"
)

// binaryName returns the appropriate binary name for the current OS
func binaryName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// waitForServerReady polls the server health endpoint and socket file until both are ready or timeout occurs
func waitForServerReady(address, dataDir string, timeout time.Duration) error {
	client := &http.Client{Timeout: 1 * time.Second}
	deadline := time.Now().Add(timeout)

	// Get the socket endpoint path using the same logic as the daemon
	socketEndpoint := socket.GetDefaultSocketPath(dataDir)

	for time.Now().Before(deadline) {
		// Check HTTP endpoint
		resp, err := client.Get(fmt.Sprintf("http://%s/healthz", address))
		httpReady := (err == nil && resp.StatusCode == http.StatusOK)
		if resp != nil {
			resp.Body.Close()
		}

		// Check socket/pipe availability using socket package
		socketReady := socket.IsSocketAvailable(socketEndpoint)

		if httpReady && socketReady {
			return nil
		}

		time.Sleep(100 * time.Millisecond)
	}

	return fmt.Errorf("server not ready after %v (http ready, socket at %s)", timeout, socketEndpoint)
}

func TestCodeExecClientModeE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	// Use /tmp with short name to avoid Unix socket path length limit (104-108 chars)
	tmpDir := filepath.Join("/tmp", "mcpproxy-test-"+t.Name())
	require.NoError(t, os.MkdirAll(tmpDir, 0700))
	defer os.RemoveAll(tmpDir)

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
		"enable_socket": true,
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

		// Wait for daemon to be ready with health check polling
		err = waitForServerReady("127.0.0.1:18080", tmpDir, 15*time.Second)
		require.NoError(t, err, "Daemon failed to become ready")

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

	// Use /tmp with short name to avoid Unix socket path length limit (104-108 chars)
	tmpDir := filepath.Join("/tmp", "mcpproxy-test-"+t.Name())
	require.NoError(t, os.MkdirAll(tmpDir, 0700))
	defer os.RemoveAll(tmpDir)

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
		"enable_socket": true,
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

		// Wait for daemon to be ready with health check polling
		err = waitForServerReady("127.0.0.1:18081", tmpDir, 15*time.Second)
		require.NoError(t, err, "Daemon failed to become ready")

		// Run call tool-read CLI command with a server:tool format
		// Note: This will fail because there's no such server, but we're testing that
		// the daemon mode works without database lock issues
		callCmd := exec.Command(mcpproxyBin, "call", "tool-read",
			"--tool-name", "test-server:test_tool",
			"--json_args", `{}`,
			"--config", configPath)
		callCmd.Env = append(os.Environ(), "MCPPROXY_DATA_DIR="+tmpDir)

		output, _ := callCmd.CombinedOutput()
		// Command will fail because server doesn't exist, but that's expected
		// We just verify there's no database lock error
		assert.NotContains(t, string(output), "database locked", "Should not have DB lock error")
	})

	t.Run("standalone_mode_when_no_daemon", func(t *testing.T) {
		// In standalone mode with no daemon, the command will fail
		// We just verify no database lock error occurs
		callCmd := exec.Command(mcpproxyBin, "call", "tool-read",
			"--tool-name", "test-server:test_tool",
			"--json_args", `{}`,
			"--config", configPath)
		callCmd.Env = append(os.Environ(),
			"MCPPROXY_DATA_DIR="+tmpDir,
			"MCPPROXY_TRAY_ENDPOINT=") // Force standalone mode

		output, _ := callCmd.CombinedOutput()
		// Command will fail because there's no daemon, but that's expected
		// We just verify it's not a database lock error
		assert.NotContains(t, string(output), "database locked", "Should not have DB lock error")
	})
}

func TestConcurrentCLICommandsE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	// Use /tmp with short name to avoid Unix socket path length limit (104-108 chars)
	tmpDir := filepath.Join("/tmp", "mcpproxy-test-"+t.Name())
	require.NoError(t, os.MkdirAll(tmpDir, 0700))
	defer os.RemoveAll(tmpDir)

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
		"enable_socket": true,
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

	// Wait for daemon to be ready with health check polling
	socketPath := socket.GetDefaultSocketPath(tmpDir)
	t.Logf("Waiting for daemon... Socket path: %s", socketPath)

	err = waitForServerReady("127.0.0.1:18082", tmpDir, 15*time.Second)
	if err != nil {
		t.Logf("Socket available after timeout: %v", socket.IsSocketAvailable(socketPath))
		// List files in tmpDir to debug
		files, _ := os.ReadDir(tmpDir)
		t.Logf("Files in tmpDir:")
		for _, f := range files {
			t.Logf("  - %s", f.Name())
		}
	}
	require.NoError(t, err, "Daemon failed to become ready")

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
				// Log the output to help diagnose the issue
				t.Logf("Command %d failed with error: %v\nOutput: %s", idx, err, string(output))
				errChan <- err
				return
			}

			// Verify no DB lock error
			if contains(string(output), "database locked") {
				t.Logf("Command %d got database locked error", idx)
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
