package upstream

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/transport"
)

func TestClient_Connect_SSE_NotSupported(t *testing.T) {
	// Create a test config with SSE protocol
	cfg := &config.ServerConfig{
		Name:     "test-sse-server",
		URL:      "http://localhost:8080/sse",
		Protocol: "sse",
		Enabled:  true,
		Created:  time.Now(),
	}

	// Create test logger
	logger, err := zap.NewDevelopment()
	require.NoError(t, err)

	// Create client with all required parameters
	client, err := NewClient("test-client", cfg, logger, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, client)

	// Attempt to connect - should fail with OAuth authorization required
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err = client.Connect(ctx)

	// Verify we get an OAuth authorization error, not SSE not supported
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "SSE transport is not supported")
	// Should be an OAuth authorization error since SSE with OAuth is now supported
	assert.True(t,
		strings.Contains(err.Error(), "authorization required") ||
			strings.Contains(err.Error(), "no valid token available") ||
			strings.Contains(err.Error(), "connection") ||
			strings.Contains(err.Error(), "dial") ||
			strings.Contains(err.Error(), "refused") ||
			strings.Contains(err.Error(), "timeout"),
		"Error should be about OAuth authorization or connection failure, not SSE support")
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
	cfg := &config.ServerConfig{
		Name:     "test-sse-server",
		URL:      "http://localhost:8080/sse",
		Protocol: "sse",
		Enabled:  true,
		Created:  time.Now(),
	}

	logger, err := zap.NewDevelopment()
	require.NoError(t, err)

	client, err := NewClient("test-client", cfg, logger, nil, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err = client.Connect(ctx)

	require.Error(t, err)

	// Verify that the error is about OAuth authorization or connection failure, not SSE not supported
	errorMsg := err.Error()
	assert.NotContains(t, errorMsg, "SSE transport is not supported")
	assert.NotContains(t, errorMsg, "streamable-http")

	// Should be an OAuth authorization error or connection error since SSE with OAuth is now supported
	assert.True(t,
		strings.Contains(errorMsg, "authorization required") ||
			strings.Contains(errorMsg, "no valid token available") ||
			strings.Contains(errorMsg, "connection") ||
			strings.Contains(errorMsg, "dial") ||
			strings.Contains(errorMsg, "refused") ||
			strings.Contains(errorMsg, "timeout"),
		"Error should be about OAuth authorization or connection failure, not SSE support")
}

func TestClient_Connect_WorkingTransports(t *testing.T) {
	tests := []struct {
		name          string
		protocol      string
		url           string
		command       string
		args          []string
		shouldConnect bool
		errorContains string
	}{
		{
			name:          "SSE protocol should work (until actual connection)",
			protocol:      "sse",
			url:           "http://localhost:8080/sse",
			shouldConnect: false, // Will fail at actual connection, but transport creation should work
			errorContains: "",    // Won't check error for SSE as it depends on server availability
		},
		{
			name:          "HTTP protocol should work (until actual connection)",
			protocol:      "http",
			url:           "http://localhost:8080",
			shouldConnect: false, // Will fail at actual connection, but transport creation should work
			errorContains: "",    // Won't check error for HTTP as it depends on server availability
		},
		{
			name:          "Streamable-HTTP protocol should work (until actual connection)",
			protocol:      "streamable-http",
			url:           "http://localhost:8080",
			shouldConnect: false, // Will fail at actual connection, but transport creation should work
			errorContains: "",    // Won't check error for streamable-http as it depends on server availability
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.ServerConfig{
				Name:     "test-server",
				Protocol: tt.protocol,
				URL:      tt.url,
				Command:  tt.command,
				Args:     tt.args,
				Enabled:  true,
				Created:  time.Now(),
			}

			logger, err := zap.NewDevelopment()
			require.NoError(t, err)

			client, err := NewClient("test-client", cfg, logger, nil, nil)
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
	tests := []struct {
		name      string
		protocol  string
		url       string
		headers   map[string]string
		expectErr bool
	}{
		{
			name:     "SSE with headers",
			protocol: "sse",
			url:      "http://localhost:8080/sse",
			headers: map[string]string{
				"Authorization": "Bearer token123",
				"X-Custom":      "custom-value",
			},
			expectErr: true, // Will fail at connection, but headers should be processed
		},
		{
			name:     "Streamable-HTTP with headers",
			protocol: "streamable-http",
			url:      "http://localhost:8080",
			headers: map[string]string{
				"Authorization": "Bearer token456",
				"Content-Type":  "application/json",
			},
			expectErr: true, // Will fail at connection, but headers should be processed
		},
		{
			name:      "SSE without headers",
			protocol:  "sse",
			url:       "http://localhost:8080/sse",
			headers:   nil,
			expectErr: true, // Will fail at connection
		},
		{
			name:      "Streamable-HTTP without headers",
			protocol:  "streamable-http",
			url:       "http://localhost:8080",
			headers:   nil,
			expectErr: true, // Will fail at connection
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.ServerConfig{
				Name:     "test-headers-server",
				Protocol: tt.protocol,
				URL:      tt.url,
				Headers:  tt.headers,
				Enabled:  true,
				Created:  time.Now(),
			}

			logger, err := zap.NewDevelopment()
			require.NoError(t, err)

			client, err := NewClient("test-client", cfg, logger, nil, nil)
			require.NoError(t, err)
			require.NotNil(t, client)

			// Test that headers are stored in config
			assert.Equal(t, tt.headers, client.config.Headers)

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
