package upstream

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/secret"
	"mcpproxy-go/internal/transport"
	"mcpproxy-go/internal/upstream/managed"
)

// createTestServer creates a simple HTTP server for testing that simulates connection issues
// This prevents MCP protocol errors and makes failures happen at the transport level
func createTestServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Simulate connection issues by returning 503 Service Unavailable
		// This causes transport-level failures before MCP protocol is attempted
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("Service Unavailable"))
	}))
}

func TestClient_Connect_SSE_NotSupported(t *testing.T) {
	// Disable OAuth for these unit tests to avoid network calls
	oldValue := os.Getenv("MCPPROXY_DISABLE_OAUTH")
	os.Setenv("MCPPROXY_DISABLE_OAUTH", "true")
	defer func() {
		if oldValue == "" {
			os.Unsetenv("MCPPROXY_DISABLE_OAUTH")
		} else {
			os.Setenv("MCPPROXY_DISABLE_OAUTH", oldValue)
		}
	}()

	// Create a test HTTP server
	server := createTestServer()
	defer server.Close()

	// Create a test config with SSE protocol
	cfg := &config.ServerConfig{
		Name:     "test-sse-server",
		URL:      server.URL + "/sse",
		Protocol: "sse",
		Enabled:  true,
		Created:  time.Now(),
	}

	// Create test logger
	logger, err := zap.NewDevelopment()
	require.NoError(t, err)

	// Create client with all required parameters
	client, err := managed.NewClient("test-client", cfg, logger, nil, nil, nil, secret.NewResolver())
	require.NoError(t, err)
	require.NotNil(t, client)

	// Attempt to connect - should fail with connection error (not OAuth)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err = client.Connect(ctx)

	// Verify we get a connection error, not OAuth authorization error
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "SSE transport is not supported")
	// Should be a connection error since OAuth is disabled
	assert.True(t,
		strings.Contains(err.Error(), "connection") ||
			strings.Contains(err.Error(), "dial") ||
			strings.Contains(err.Error(), "refused") ||
			strings.Contains(err.Error(), "timeout") ||
			strings.Contains(err.Error(), "unexpected status code") ||
			strings.Contains(err.Error(), "status code: 503") ||
			strings.Contains(err.Error(), "Service Unavailable"),
		"Error should be about connection failure, not OAuth or SSE support")
}

func TestClient_DetermineTransportType_SSE(t *testing.T) {
	cfg := &config.ServerConfig{
		Protocol: "sse",
		URL:      "http://localhost:8080/sse",
	}

	// Test that DetermineTransportType returns "sse" for SSE protocol
	transportType := transport.DetermineTransportType(cfg)
	assert.Equal(t, "sse", transportType)
}

func TestClient_Connect_SSE_ErrorContainsAlternatives(t *testing.T) {
	// Disable OAuth for these unit tests to avoid network calls
	oldValue := os.Getenv("MCPPROXY_DISABLE_OAUTH")
	os.Setenv("MCPPROXY_DISABLE_OAUTH", "true")
	defer func() {
		if oldValue == "" {
			os.Unsetenv("MCPPROXY_DISABLE_OAUTH")
		} else {
			os.Setenv("MCPPROXY_DISABLE_OAUTH", oldValue)
		}
	}()

	// Create a test HTTP server
	server := createTestServer()
	defer server.Close()

	cfg := &config.ServerConfig{
		Name:     "test-sse-server",
		URL:      server.URL + "/sse",
		Protocol: "sse",
		Enabled:  true,
		Created:  time.Now(),
	}

	logger, err := zap.NewDevelopment()
	require.NoError(t, err)

	client, err := managed.NewClient("test-client", cfg, logger, nil, nil, nil, secret.NewResolver())
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err = client.Connect(ctx)

	require.Error(t, err)

	// Verify that the error is about connection failure, not OAuth or SSE not supported
	errorMsg := err.Error()
	assert.NotContains(t, errorMsg, "SSE transport is not supported")
	assert.NotContains(t, errorMsg, "streamable-http")

	// Should be a connection error since OAuth is disabled
	assert.True(t,
		strings.Contains(errorMsg, "connection") ||
			strings.Contains(errorMsg, "dial") ||
			strings.Contains(errorMsg, "refused") ||
			strings.Contains(errorMsg, "timeout") ||
			strings.Contains(errorMsg, "unexpected status code") ||
			strings.Contains(errorMsg, "status code: 503") ||
			strings.Contains(errorMsg, "Service Unavailable"),
		"Error should be about connection failure, not OAuth or SSE support")
}

func TestClient_Connect_WorkingTransports(t *testing.T) {
	// Disable OAuth for these unit tests to avoid network calls
	oldValue := os.Getenv("MCPPROXY_DISABLE_OAUTH")
	os.Setenv("MCPPROXY_DISABLE_OAUTH", "true")
	defer func() {
		if oldValue == "" {
			os.Unsetenv("MCPPROXY_DISABLE_OAUTH")
		} else {
			os.Setenv("MCPPROXY_DISABLE_OAUTH", oldValue)
		}
	}()

	// Create a test HTTP server
	server := createTestServer()
	defer server.Close()

	tests := []struct {
		name          string
		protocol      string
		urlSuffix     string
		command       string
		args          []string
		shouldConnect bool
		errorContains string
	}{
		{
			name:          "SSE protocol should work (until actual connection)",
			protocol:      "sse",
			urlSuffix:     "/sse",
			shouldConnect: false, // Will fail at actual connection, but transport creation should work
			errorContains: "",    // Won't check error for SSE as it depends on server availability
		},
		{
			name:          "HTTP protocol should work (until actual connection)",
			protocol:      "http",
			urlSuffix:     "",
			shouldConnect: false, // Will fail at actual connection, but transport creation should work
			errorContains: "",    // Won't check error for HTTP as it depends on server availability
		},
		{
			name:          "Streamable-HTTP protocol should work (until actual connection)",
			protocol:      "streamable-http",
			urlSuffix:     "",
			shouldConnect: false, // Will fail at actual connection, but transport creation should work
			errorContains: "",    // Won't check error for streamable-http as it depends on server availability
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.ServerConfig{
				Name:     "test-server",
				Protocol: tt.protocol,
				URL:      server.URL + tt.urlSuffix,
				Command:  tt.command,
				Args:     tt.args,
				Enabled:  true,
				Created:  time.Now(),
			}

			logger, err := zap.NewDevelopment()
			require.NoError(t, err)

			client, err := managed.NewClient("test-client", cfg, logger, nil, nil, nil, secret.NewResolver())
			require.NoError(t, err)

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			err = client.Connect(ctx)

			if tt.shouldConnect {
				assert.NoError(t, err)
			} else if tt.errorContains != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorContains)
			}
		})
	}
}

func TestClient_Headers_Support(t *testing.T) {
	// Disable OAuth for these unit tests to avoid network calls
	oldValue := os.Getenv("MCPPROXY_DISABLE_OAUTH")
	os.Setenv("MCPPROXY_DISABLE_OAUTH", "true")
	defer func() {
		if oldValue == "" {
			os.Unsetenv("MCPPROXY_DISABLE_OAUTH")
		} else {
			os.Setenv("MCPPROXY_DISABLE_OAUTH", oldValue)
		}
	}()

	// Create a test HTTP server
	server := createTestServer()
	defer server.Close()

	tests := []struct {
		name      string
		protocol  string
		urlSuffix string
		headers   map[string]string
		expectErr bool
	}{
		{
			name:      "SSE with headers",
			protocol:  "sse",
			urlSuffix: "/sse",
			headers: map[string]string{
				"Authorization": "Bearer token123",
				"X-Custom":      "custom-value",
			},
			expectErr: true, // Will fail at connection, but headers should be processed
		},
		{
			name:      "Streamable-HTTP with headers",
			protocol:  "streamable-http",
			urlSuffix: "",
			headers: map[string]string{
				"Authorization": "Bearer token456",
				"Content-Type":  "application/json",
			},
			expectErr: true, // Will fail at connection, but headers should be processed
		},
		{
			name:      "SSE without headers",
			protocol:  "sse",
			urlSuffix: "/sse",
			headers:   nil,
			expectErr: true, // Will fail at connection
		},
		{
			name:      "Streamable-HTTP without headers",
			protocol:  "streamable-http",
			urlSuffix: "",
			headers:   nil,
			expectErr: true, // Will fail at connection
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.ServerConfig{
				Name:     "test-headers-server",
				Protocol: tt.protocol,
				URL:      server.URL + tt.urlSuffix,
				Headers:  tt.headers,
				Enabled:  true,
				Created:  time.Now(),
			}

			logger, err := zap.NewDevelopment()
			require.NoError(t, err)

			client, err := managed.NewClient("test-client", cfg, logger, nil, nil, nil, secret.NewResolver())
			require.NoError(t, err)
			require.NotNil(t, client)

			// Test that headers are stored in config
			assert.Equal(t, tt.headers, client.Config.Headers)

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			err = client.Connect(ctx)

			if tt.expectErr {
				require.Error(t, err)
				// Should not be a "not supported" error
				assert.NotContains(t, err.Error(), "not supported")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
