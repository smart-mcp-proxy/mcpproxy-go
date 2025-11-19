package socket_test

import (
	"runtime"
	"testing"

	"mcpproxy-go/internal/socket"

	"github.com/stretchr/testify/assert"
)

func TestCreateDialer_UnixSocket(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix socket test not applicable on Windows")
	}

	// Given: Unix socket endpoint
	endpoint := "unix:///tmp/test.sock"

	// When: Creating dialer
	dialer, baseURL, err := socket.CreateDialer(endpoint)

	// Then: Returns dialer and localhost base URL
	assert.NoError(t, err)
	assert.NotNil(t, dialer)
	assert.Equal(t, "http://localhost", baseURL)
}

func TestCreateDialer_HTTPEndpoint(t *testing.T) {
	// Given: HTTP endpoint
	endpoint := "http://localhost:8080"

	// When: Creating dialer
	dialer, baseURL, err := socket.CreateDialer(endpoint)

	// Then: Returns nil dialer (use default) and original URL
	assert.NoError(t, err)
	assert.Nil(t, dialer)
	assert.Equal(t, endpoint, baseURL)
}

func TestCreateDialer_InvalidEndpoint(t *testing.T) {
	// Given: Invalid endpoint (not a recognized scheme)
	endpoint := "invalid://test"

	// When: Creating dialer
	dialer, baseURL, err := socket.CreateDialer(endpoint)

	// Then: Returns original endpoint (no error for unknown schemes)
	assert.NoError(t, err)
	assert.Nil(t, dialer)
	assert.Equal(t, endpoint, baseURL)
}
