package socket_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/socket"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetectSocketPath_DefaultDataDir(t *testing.T) {
	// Given: Default data directory
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	dataDir := filepath.Join(home, ".mcpproxy")

	// When: Detecting socket path
	socketPath := socket.DetectSocketPath(dataDir)

	// Then: Returns platform-specific default
	if runtime.GOOS == "windows" {
		assert.Contains(t, socketPath, "npipe:////./pipe/mcpproxy-")
	} else {
		expected := filepath.Join(dataDir, "mcpproxy.sock")
		assert.Equal(t, "unix://"+expected, socketPath)
	}
}

func TestDetectSocketPath_EnvironmentOverride(t *testing.T) {
	// Given: Environment variable set
	customEndpoint := "unix:///tmp/custom.sock"
	os.Setenv("MCPPROXY_TRAY_ENDPOINT", customEndpoint)
	defer os.Unsetenv("MCPPROXY_TRAY_ENDPOINT")

	// When: Detecting socket path
	socketPath := socket.DetectSocketPath("")

	// Then: Returns environment value
	assert.Equal(t, customEndpoint, socketPath)
}

func TestIsSocketAvailable_SocketExists(t *testing.T) {
	// Given: A valid socket file
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "test.sock")

	// Create a dummy file (simulating socket)
	err := os.WriteFile(socketPath, []byte{}, 0600)
	require.NoError(t, err)

	// When: Checking availability
	available := socket.IsSocketAvailable("unix://" + socketPath)

	// Then: Returns true
	assert.True(t, available)
}

func TestIsSocketAvailable_SocketMissing(t *testing.T) {
	// Given: Non-existent socket path
	socketPath := "/nonexistent/path/test.sock"

	// When: Checking availability
	available := socket.IsSocketAvailable("unix://" + socketPath)

	// Then: Returns false
	assert.False(t, available)
}
