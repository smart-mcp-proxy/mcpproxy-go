package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"mcpproxy-go/internal/config"
)

// TestEndToEnd_TrayToCore_UnixSocket tests the complete flow:
// 1. Core server creates Unix socket listener
// 2. Simulated tray client connects via socket
// 3. API requests work without API key
// 4. TCP connections still require API key
func TestE2E_TrayToCore_UnixSocket(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix socket E2E test not applicable on Windows (use named pipe test)")
	}

	logger := zap.NewNop()
	tmpDir := t.TempDir()

	// Setup configuration
	cfg := &config.Config{
		Listen:   "127.0.0.1:0", // Random TCP port
		DataDir:  tmpDir,
		APIKey:   "test-api-key-12345",
		Servers:  []*config.ServerConfig{},
		TopK:     5,
		Features: &config.FeatureFlags{},
	}

	// Create server
	srv, err := NewServerWithConfigPath(cfg, "", logger)
	require.NoError(t, err)
	require.NotNil(t, srv)

	// Start server in background
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serverReady := make(chan error, 1)
	go func() {
		err := srv.Start(ctx)
		serverReady <- err
	}()

	// Wait for server to be ready
	require.Eventually(t, func() bool {
		return srv.IsReady()
	}, 5*time.Second, 100*time.Millisecond, "Server should become ready")

	// Get actual addresses
	tcpAddr := srv.GetListenAddress()
	socketPath := filepath.Join(tmpDir, "mcpproxy.sock")

	t.Logf("Server started - TCP: %s, Socket: %s", tcpAddr, socketPath)

	// Verify socket file exists
	_, err = os.Stat(socketPath)
	require.NoError(t, err, "Socket file should exist")

	// Test 1: Unix socket connection WITHOUT API key (should succeed)
	t.Run("UnixSocket_NoAPIKey_Success", func(t *testing.T) {
		// Create HTTP client with Unix socket dialer
		transport := &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socketPath)
			},
		}

		client := &http.Client{
			Transport: transport,
			Timeout:   2 * time.Second,
		}

		// Make request WITHOUT API key
		resp, err := client.Get("http://localhost/api/v1/status")
		require.NoError(t, err, "Socket request without API key should succeed")
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Verify response
		var result map[string]interface{}
		err = json.NewDecoder(resp.Body).Decode(&result)
		require.NoError(t, err)
		assert.True(t, result["success"].(bool))
	})

	// Test 2: TCP connection WITHOUT API key (should fail)
	t.Run("TCP_NoAPIKey_Fail", func(t *testing.T) {
		client := &http.Client{Timeout: 2 * time.Second}

		resp, err := client.Get(fmt.Sprintf("http://%s/api/v1/status", tcpAddr))
		require.NoError(t, err, "Request should complete")
		defer resp.Body.Close()

		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, "TCP without API key should be unauthorized")
	})

	// Test 3: TCP connection WITH API key (should succeed)
	t.Run("TCP_WithAPIKey_Success", func(t *testing.T) {
		client := &http.Client{Timeout: 2 * time.Second}

		req, err := http.NewRequest("GET", fmt.Sprintf("http://%s/api/v1/status", tcpAddr), nil)
		require.NoError(t, err)
		req.Header.Set("X-API-Key", "test-api-key-12345")

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode, "TCP with valid API key should succeed")

		var result map[string]interface{}
		err = json.NewDecoder(resp.Body).Decode(&result)
		require.NoError(t, err)
		assert.True(t, result["success"].(bool))
	})

	// Test 4: SSE connection over Unix socket (should work without API key)
	t.Run("UnixSocket_SSE_NoAPIKey", func(t *testing.T) {
		transport := &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socketPath)
			},
		}

		client := &http.Client{
			Transport: transport,
			Timeout:   5 * time.Second,
		}

		resp, err := client.Get("http://localhost/events")
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))

		// Read initial SSE event
		reader := bufio.NewReader(resp.Body)
		line, err := reader.ReadString('\n')
		require.NoError(t, err)
		assert.Contains(t, line, "SSE connection established")
	})

	// Cleanup
	cancel()
	select {
	case err := <-serverReady:
		if err != context.Canceled && err != http.ErrServerClosed {
			t.Logf("Server stopped with error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Log("Server shutdown timeout")
	}

	// Verify socket file is cleaned up
	_, err = os.Stat(socketPath)
	assert.True(t, os.IsNotExist(err), "Socket file should be removed after server stops")
}

// TestEndToEnd_DualListener_Concurrent tests concurrent requests over both TCP and socket
func TestE2E_DualListener_Concurrent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix socket E2E test not applicable on Windows")
	}

	logger := zap.NewNop()
	tmpDir := t.TempDir()

	cfg := &config.Config{
		Listen:   "127.0.0.1:0",
		DataDir:  tmpDir,
		APIKey:   "concurrent-test-key",
		Servers:  []*config.ServerConfig{},
		Features: &config.FeatureFlags{},
	}

	srv, err := NewServerWithConfigPath(cfg, "", logger)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = srv.Start(ctx)
	}()

	require.Eventually(t, func() bool {
		return srv.IsReady()
	}, 5*time.Second, 100*time.Millisecond)

	tcpAddr := srv.GetListenAddress()
	socketPath := filepath.Join(tmpDir, "mcpproxy.sock")

	// Create socket client
	socketTransport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socketPath)
		},
	}
	socketClient := &http.Client{Transport: socketTransport, Timeout: 2 * time.Second}

	// Create TCP client
	tcpClient := &http.Client{Timeout: 2 * time.Second}

	// Make concurrent requests
	const numRequests = 10
	done := make(chan error, numRequests*2)

	// Socket requests (no API key needed)
	for i := 0; i < numRequests; i++ {
		go func(id int) {
			resp, err := socketClient.Get("http://localhost/api/v1/status")
			if err != nil {
				done <- fmt.Errorf("socket request %d failed: %w", id, err)
				return
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				done <- fmt.Errorf("socket request %d got status %d", id, resp.StatusCode)
				return
			}
			done <- nil
		}(i)
	}

	// TCP requests (API key required)
	for i := 0; i < numRequests; i++ {
		go func(id int) {
			req, _ := http.NewRequest("GET", fmt.Sprintf("http://%s/api/v1/status", tcpAddr), nil)
			req.Header.Set("X-API-Key", "concurrent-test-key")
			resp, err := tcpClient.Do(req)
			if err != nil {
				done <- fmt.Errorf("tcp request %d failed: %w", id, err)
				return
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				done <- fmt.Errorf("tcp request %d got status %d", id, resp.StatusCode)
				return
			}
			done <- nil
		}(i)
	}

	// Wait for all requests
	for i := 0; i < numRequests*2; i++ {
		select {
		case err := <-done:
			assert.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Fatal("Request timeout")
		}
	}
}

// TestEndToEnd_SocketPermissions tests that socket has correct permissions
func TestE2E_SocketPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission test not applicable on Windows")
	}

	logger := zap.NewNop()
	tmpDir := t.TempDir()

	cfg := &config.Config{
		Listen:   "127.0.0.1:0",
		DataDir:  tmpDir,
		Servers:  []*config.ServerConfig{},
		Features: &config.FeatureFlags{},
	}

	srv, err := NewServerWithConfigPath(cfg, "", logger)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = srv.Start(ctx)
	}()

	require.Eventually(t, func() bool {
		return srv.IsReady()
	}, 5*time.Second, 100*time.Millisecond)

	socketPath := filepath.Join(tmpDir, "mcpproxy.sock")

	// Check socket file permissions
	info, err := os.Stat(socketPath)
	require.NoError(t, err)

	// Verify it's a socket
	assert.Equal(t, os.ModeSocket, info.Mode()&os.ModeSocket, "Should be a socket file")

	// Verify permissions are 0600 (user read/write only)
	perm := info.Mode().Perm()
	assert.Equal(t, os.FileMode(0600), perm, "Socket should have 0600 permissions")

	// Check data directory permissions
	dirInfo, err := os.Stat(tmpDir)
	require.NoError(t, err)
	dirPerm := dirInfo.Mode().Perm()
	assert.Equal(t, os.FileMode(0700), dirPerm, "Data directory should have 0700 permissions")
}
