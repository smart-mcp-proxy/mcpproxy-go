package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestInfoEndpoint tests the /api/v1/info endpoint returns correct server information
func TestInfoEndpoint(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	// Build the binary first
	binaryPath, cleanup := buildTestBinary(t)
	defer cleanup()

	// Create temp directory for this test
	tempDir := t.TempDir()

	// Set secure permissions (0700) required by mcpproxy
	err := os.Chmod(tempDir, 0700)
	require.NoError(t, err, "Failed to set secure permissions on temp directory")

	// Find available port
	ln, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	// Start server
	listenAddr := fmt.Sprintf("127.0.0.1:%d", port)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, binaryPath, "serve",
		"--data-dir", tempDir,
		"--listen", listenAddr)

	// Disable API key authentication for E2E tests
	cmd.Env = append(os.Environ(), "MCPPROXY_API_KEY=")

	// Capture output for debugging
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	require.NoError(t, cmd.Start(), "Failed to start server")
	defer func() {
		if cmd.Process != nil {
			cmd.Process.Signal(syscall.SIGTERM)
			cmd.Wait()
		}
	}()

	// Wait for server to be ready
	serverURL := fmt.Sprintf("http://%s", listenAddr)
	require.True(t, waitForServer(serverURL, 10*time.Second), "Server did not become ready")

	// Test 1: GET /api/v1/info without API key (should work)
	t.Run("GET /api/v1/info returns server information", func(t *testing.T) {
		resp, err := http.Get(serverURL + "/api/v1/info")
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode, "Expected 200 OK")

		// Read the body first for debugging
		bodyBytes, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		t.Logf("Response body: %s", string(bodyBytes))

		var result map[string]interface{}
		require.NoError(t, json.Unmarshal(bodyBytes, &result))

		// Verify response structure
		assert.True(t, result["success"].(bool), "Expected success=true")

		// Check if data exists before type assertion
		require.NotNil(t, result["data"], "Expected data field to exist")
		data, ok := result["data"].(map[string]interface{})
		require.True(t, ok, "Expected data to be a map[string]interface{}")
		assert.NotEmpty(t, data["version"], "Expected version to be set")
		assert.NotEmpty(t, data["web_ui_url"], "Expected web_ui_url to be set")
		assert.Equal(t, listenAddr, data["listen_addr"], "Expected listen_addr to match")

		// Verify endpoints structure
		endpoints := data["endpoints"].(map[string]interface{})
		assert.Equal(t, listenAddr, endpoints["http"], "Expected http endpoint")

		t.Logf("Server info: version=%s, web_ui_url=%s", data["version"], data["web_ui_url"])
	})

	// Test 2: Verify web_ui_url is correctly formatted
	t.Run("web_ui_url is correctly formatted", func(t *testing.T) {
		resp, err := http.Get(serverURL + "/api/v1/info")
		require.NoError(t, err)
		defer resp.Body.Close()

		var result map[string]interface{}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))

		data := result["data"].(map[string]interface{})
		webUIURL := data["web_ui_url"].(string)

		// Should be in format http://host:port/ui/
		assert.Contains(t, webUIURL, "http://")
		assert.Contains(t, webUIURL, "/ui/")
		assert.Contains(t, webUIURL, listenAddr)
	})
}

// TestGracefulShutdownNoPanic tests that server shuts down gracefully without panic
func TestGracefulShutdownNoPanic(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	// Build the binary first
	binaryPath, cleanup := buildTestBinary(t)
	defer cleanup()

	// Create temp directory for this test
	tempDir := t.TempDir()

	// Set secure permissions (0700) required by mcpproxy
	err := os.Chmod(tempDir, 0700)
	require.NoError(t, err, "Failed to set secure permissions on temp directory")

	// Find available port
	ln, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	// Start server
	listenAddr := fmt.Sprintf("127.0.0.1:%d", port)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, binaryPath, "serve",
		"--data-dir", tempDir,
		"--listen", listenAddr,
		"--log-level", "debug")

	// Disable API key authentication for E2E tests
	cmd.Env = append(os.Environ(), "MCPPROXY_API_KEY=")

	// Capture stderr to check for panic messages
	stderrPipe, err := cmd.StderrPipe()
	require.NoError(t, err)

	require.NoError(t, cmd.Start(), "Failed to start server")

	// Wait for server to be ready
	serverURL := fmt.Sprintf("http://%s", listenAddr)
	require.True(t, waitForServer(serverURL, 10*time.Second), "Server did not become ready")

	// Make a few requests to ensure server is active
	for i := 0; i < 3; i++ {
		resp, err := http.Get(serverURL + "/ready")
		require.NoError(t, err)
		resp.Body.Close()
		time.Sleep(100 * time.Millisecond)
	}

	// Send SIGINT (Ctrl+C) to the process
	t.Log("Sending SIGINT to server process...")
	require.NoError(t, cmd.Process.Signal(syscall.SIGINT))

	// Read stderr in a goroutine
	stderrOutput := make(chan string, 1)
	go func() {
		output, _ := io.ReadAll(stderrPipe)
		stderrOutput <- string(output)
	}()

	// Wait for process to exit (with timeout)
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		// Process exited
		t.Logf("Server exited with: %v", err)

		// Check stderr for panic messages
		select {
		case output := <-stderrOutput:
			assert.NotContains(t, output, "panic:", "Server should not panic during shutdown")
			assert.NotContains(t, output, "SIGSEGV", "Server should not segfault during shutdown")
			t.Logf("Shutdown completed cleanly")
		case <-time.After(1 * time.Second):
			t.Log("No stderr output captured")
		}

	case <-time.After(15 * time.Second):
		t.Fatal("Server did not shut down within 15 seconds")
		cmd.Process.Kill()
	}
}

// TestSocketInfoEndpoint tests /api/v1/info via Unix socket
func TestSocketInfoEndpoint(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	// Build the binary first
	binaryPath, cleanup := buildTestBinary(t)
	defer cleanup()

	// Create temp directory for this test
	tempDir := t.TempDir()

	// Set secure permissions (0700) required by mcpproxy
	err := os.Chmod(tempDir, 0700)
	require.NoError(t, err, "Failed to set secure permissions on temp directory")

	socketPath := filepath.Join(tempDir, "mcpproxy.sock")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start server with socket enabled
	cmd := exec.CommandContext(ctx, binaryPath, "serve",
		"--data-dir", tempDir,
		"--listen", "127.0.0.1:0", // Random port for HTTP
		"--enable-socket", "true")

	// Disable API key authentication for E2E tests
	cmd.Env = append(os.Environ(), "MCPPROXY_API_KEY=")

	require.NoError(t, cmd.Start(), "Failed to start server")
	defer func() {
		if cmd.Process != nil {
			cmd.Process.Signal(syscall.SIGTERM)
			cmd.Wait()
		}
	}()

	// Wait for socket to be created
	require.True(t, waitForSocket(socketPath, 10*time.Second), "Socket was not created")

	// Test info endpoint via socket using curl
	t.Run("GET /api/v1/info via Unix socket", func(t *testing.T) {
		// Use curl to test Unix socket communication
		curlCmd := exec.Command("curl",
			"-s", // Silent mode to suppress progress meter
			"--unix-socket", socketPath,
			"http://localhost/api/v1/info")

		output, err := curlCmd.CombinedOutput()
		require.NoError(t, err, "curl command failed: %s", string(output))

		t.Logf("Curl output: %s", string(output))

		var result map[string]interface{}
		require.NoError(t, json.Unmarshal(output, &result))

		// Verify response
		assert.True(t, result["success"].(bool))
		data := result["data"].(map[string]interface{})
		assert.NotEmpty(t, data["version"])
		assert.NotEmpty(t, data["web_ui_url"])

		t.Logf("Socket info endpoint works: %s", string(output))
	})
}

// Helper functions

func buildTestBinary(t *testing.T) (string, func()) {
	t.Helper()

	// Build binary with proper extension for Windows
	binaryName := "mcpproxy-test"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	binaryPath := filepath.Join(t.TempDir(), binaryName)

	buildCmd := exec.Command("go", "build",
		"-o", binaryPath,
		"./cmd/mcpproxy")

	// Run from project root (two directories up from internal/server)
	buildCmd.Dir = filepath.Join("..", "..")
	output, err := buildCmd.CombinedOutput()
	require.NoError(t, err, "Failed to build binary: %s", string(output))

	cleanup := func() {
		os.Remove(binaryPath)
	}

	return binaryPath, cleanup
}

func waitForSocket(socketPath string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(socketPath); err == nil {
			// Socket exists, verify it's actually a socket
			info, err := os.Stat(socketPath)
			if err == nil && info.Mode()&os.ModeSocket != 0 {
				return true
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

func waitForServer(serverURL string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 1 * time.Second}

	for time.Now().Before(deadline) {
		resp, err := client.Get(serverURL + "/ready")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return true
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}
