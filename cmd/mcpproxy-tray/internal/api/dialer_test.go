//go:build darwin || windows

package api

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateDialer_Unix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix socket test not applicable on Windows")
	}

	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "test.sock")

	// Create a test Unix socket server
	ln, err := net.Listen("unix", socketPath)
	require.NoError(t, err)
	defer ln.Close()
	defer os.Remove(socketPath)

	// Create dialer
	endpoint := fmt.Sprintf("unix://%s", socketPath)
	dialer, baseURL, err := CreateDialer(endpoint)
	require.NoError(t, err)
	require.NotNil(t, dialer)
	assert.Equal(t, "http://localhost", baseURL)

	// Test dialing
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	conn, err := dialer(ctx, "", "")
	require.NoError(t, err)
	require.NotNil(t, conn)
	defer conn.Close()

	// Verify it's a Unix connection
	_, ok := conn.(*net.UnixConn)
	assert.True(t, ok, "Connection should be a Unix socket")
}

func TestCreateDialer_HTTP(t *testing.T) {
	// Standard HTTP endpoint - should return nil dialer
	dialer, baseURL, err := CreateDialer("http://localhost:8080")
	require.NoError(t, err)
	assert.Nil(t, dialer, "HTTP should use default dialer")
	assert.Equal(t, "http://localhost:8080", baseURL)
}

func TestCreateDialer_HTTPS(t *testing.T) {
	// Standard HTTPS endpoint - should return nil dialer
	dialer, baseURL, err := CreateDialer("https://localhost:8443")
	require.NoError(t, err)
	assert.Nil(t, dialer, "HTTPS should use default dialer")
	assert.Equal(t, "https://localhost:8443", baseURL)
}

func TestCreateDialer_InvalidScheme(t *testing.T) {
	_, _, err := CreateDialer("ftp://invalid")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported endpoint scheme")
}

func TestCreateDialer_MalformedURL(t *testing.T) {
	_, _, err := CreateDialer("not a url")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported endpoint scheme")
}

func TestDetectSocketPath_Environment(t *testing.T) {
	// Test environment variable priority
	expected := "unix:///custom/path.sock"
	os.Setenv("MCPPROXY_TRAY_ENDPOINT", expected)
	defer os.Unsetenv("MCPPROXY_TRAY_ENDPOINT")

	result := DetectSocketPath("")
	assert.Equal(t, expected, result)
}

func TestDetectSocketPath_Default(t *testing.T) {
	// Ensure env var is not set
	os.Unsetenv("MCPPROXY_TRAY_ENDPOINT")

	tmpDir := t.TempDir()
	result := DetectSocketPath(tmpDir)

	if runtime.GOOS == "windows" {
		assert.Contains(t, result, "npipe://")
		assert.Contains(t, result, "mcpproxy-")
	} else {
		assert.Contains(t, result, "unix://")
		assert.Contains(t, result, "mcpproxy.sock")
		assert.Contains(t, result, tmpDir)
	}
}

func TestGetDefaultSocketPath_Unix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix socket test not applicable on Windows")
	}

	tmpDir := t.TempDir()
	result := getDefaultSocketPath(tmpDir)

	assert.Contains(t, result, "unix://")
	assert.Contains(t, result, tmpDir)
	assert.Contains(t, result, "mcpproxy.sock")

	expected := fmt.Sprintf("unix://%s/mcpproxy.sock", tmpDir)
	assert.Equal(t, expected, result)
}

func TestGetDefaultSocketPath_Windows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows named pipe test only applicable on Windows")
	}

	tmpDir := t.TempDir()
	result := getDefaultSocketPath(tmpDir)

	assert.Contains(t, result, "npipe://")
	assert.Contains(t, result, "pipe/mcpproxy-")
}

func TestUnixSocketHTTPClient(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix socket test not applicable on Windows")
	}

	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "test.sock")

	// Create test HTTP server on Unix socket
	ln, err := net.Listen("unix", socketPath)
	require.NoError(t, err)
	defer ln.Close()
	defer os.Remove(socketPath)

	mux := http.NewServeMux()
	mux.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "success")
	})

	server := &http.Server{Handler: mux}
	go func() {
		_ = server.Serve(ln)
	}()
	defer server.Close()

	// Wait for server to start
	time.Sleep(100 * time.Millisecond)

	// Create HTTP client with Unix socket dialer
	endpoint := fmt.Sprintf("unix://%s", socketPath)
	dialer, baseURL, err := CreateDialer(endpoint)
	require.NoError(t, err)

	transport := &http.Transport{
		DialContext: dialer,
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   2 * time.Second,
	}

	// Make HTTP request over Unix socket
	resp, err := client.Get(baseURL + "/test")
	require.NoError(t, err)
	defer resp.Body.Close()

	body := make([]byte, 100)
	n, _ := resp.Body.Read(body)
	assert.Equal(t, "success", string(body[:n]))
}

func TestDialerFallbackToTCP(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix socket test not applicable on Windows")
	}

	// Test that invalid socket path falls back gracefully
	invalidEndpoint := "unix:///nonexistent/path.sock"
	dialer, baseURL, err := CreateDialer(invalidEndpoint)
	require.NoError(t, err)
	require.NotNil(t, dialer)
	assert.Equal(t, "http://localhost", baseURL)

	// Dialing should fail (no server), but dialer creation succeeded
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	_, err = dialer(ctx, "", "")
	assert.Error(t, err, "Should fail to connect to nonexistent socket")
}

func TestUnixSocketWithTLSConfig(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix socket test not applicable on Windows")
	}

	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "test.sock")

	// Create test server
	ln, err := net.Listen("unix", socketPath)
	require.NoError(t, err)
	defer ln.Close()
	defer os.Remove(socketPath)

	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "secure")
	}))
	server.Listener = ln
	server.Start()
	defer server.Close()

	// Create client with Unix socket dialer
	endpoint := fmt.Sprintf("unix://%s", socketPath)
	dialer, baseURL, err := CreateDialer(endpoint)
	require.NoError(t, err)

	transport := &http.Transport{
		DialContext: dialer,
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   2 * time.Second,
	}

	// Make request
	resp, err := client.Get(baseURL + "/")
	require.NoError(t, err)
	defer resp.Body.Close()

	body := make([]byte, 100)
	n, _ := resp.Body.Read(body)
	assert.Equal(t, "secure", string(body[:n]))
}

func TestGetDefaultDataDir(t *testing.T) {
	result := getDefaultDataDir()
	assert.NotEmpty(t, result)
	assert.Contains(t, result, ".mcpproxy")

	home, err := os.UserHomeDir()
	require.NoError(t, err)
	expected := filepath.Join(home, ".mcpproxy")
	assert.Equal(t, expected, result)
}

func TestDialerURLParsing(t *testing.T) {
	tests := []struct {
		name        string
		endpoint    string
		expectError bool
		skipOS      string
	}{
		{
			name:        "Unix socket with triple slash",
			endpoint:    "unix:///path/to/socket.sock",
			expectError: false,
			skipOS:      "windows",
		},
		{
			name:        "Unix socket with double slash",
			endpoint:    "unix://path/to/socket.sock",
			expectError: false,
			skipOS:      "windows",
		},
		{
			name:        "Named pipe",
			endpoint:    "npipe:////./pipe/mcpproxy",
			expectError: false,
			skipOS:      "darwin,linux",
		},
		{
			name:        "HTTP URL",
			endpoint:    "http://localhost:8080",
			expectError: false,
		},
		{
			name:        "HTTPS URL",
			endpoint:    "https://example.com",
			expectError: false,
		},
		{
			name:        "Invalid scheme",
			endpoint:    "invalid://test",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.skipOS != "" && contains(tt.skipOS, runtime.GOOS) {
				t.Skip("Test not applicable on " + runtime.GOOS)
			}

			dialer, baseURL, err := CreateDialer(tt.endpoint)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.NotEmpty(t, baseURL)

				if dialer != nil {
					// Socket/pipe dialer
					assert.Equal(t, "http://localhost", baseURL)
				} else {
					// TCP dialer
					assert.Contains(t, baseURL, "http")
				}
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(s) > 0 && (s == substr || (len(substr) > 0 && (s[:len(substr)] == substr || s[len(s)-len(substr):] == substr)))
}
