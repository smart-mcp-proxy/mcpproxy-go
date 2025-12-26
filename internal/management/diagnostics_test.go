package management

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"mcpproxy-go/internal/config"
)

// T034: Unit test for Doctor() method
func TestDoctor(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()

	t.Run("success with no issues", func(t *testing.T) {
		cfg := &config.Config{}
		emitter := &mockEventEmitter{}
		runtime := newMockRuntime()
		runtime.servers = []map[string]interface{}{
			{
				"id":            "server1",
				"name":          "test-server-1",
				"enabled":       true,
				"connected":     true,
				"quarantined":   false,
				"last_error":    "",
				"authenticated": true,
			},
		}

		svc := NewService(runtime, cfg, emitter, nil, logger)
		diag, err := svc.Doctor(context.Background())

		require.NoError(t, err)
		assert.NotNil(t, diag)
		assert.Equal(t, 0, diag.TotalIssues)
		assert.Empty(t, diag.UpstreamErrors)
		assert.Empty(t, diag.OAuthRequired)
		assert.Empty(t, diag.MissingSecrets)
		assert.Empty(t, diag.RuntimeWarnings)
	})

	t.Run("detects upstream connection errors", func(t *testing.T) {
		cfg := &config.Config{}
		emitter := &mockEventEmitter{}
		runtime := newMockRuntime()
		runtime.servers = []map[string]interface{}{
			{
				"id":          "server1",
				"name":        "failing-server",
				"enabled":     true,
				"connected":   false,
				"last_error":  "connection refused",
				"error_time":  "2025-11-23T10:00:00Z",
			},
		}

		svc := NewService(runtime, cfg, emitter, nil, logger)
		diag, err := svc.Doctor(context.Background())

		require.NoError(t, err)
		assert.Equal(t, 1, diag.TotalIssues)
		require.Len(t, diag.UpstreamErrors, 1)
		assert.Equal(t, "failing-server", diag.UpstreamErrors[0].ServerName)
		assert.Equal(t, "connection refused", diag.UpstreamErrors[0].ErrorMessage)
	})

	t.Run("runtime error returns error", func(t *testing.T) {
		cfg := &config.Config{}
		emitter := &mockEventEmitter{}
		runtime := newMockRuntime()
		runtime.getAllError = fmt.Errorf("runtime failure")

		svc := NewService(runtime, cfg, emitter, nil, logger)
		diag, err := svc.Doctor(context.Background())

		assert.Error(t, err)
		assert.Nil(t, diag)
		assert.Contains(t, err.Error(), "failed to get servers")
	})
}

// T035: Unit test for OAuth requirements detection
// Updated to test aggregation from Health.Action
func TestDoctorOAuthDetection(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()

	t.Run("detects unauthenticated OAuth servers", func(t *testing.T) {
		cfg := &config.Config{}
		emitter := &mockEventEmitter{}
		runtime := newMockRuntime()
		runtime.servers = []map[string]interface{}{
			{
				"id":            "server1",
				"name":          "oauth-server",
				"enabled":       true,
				"connected":     false,
				"authenticated": false,
				"oauth":         map[string]interface{}{"enabled": true},
				// Health status with login action triggers OAuthRequired aggregation
				"health": map[string]interface{}{
					"level":       "unhealthy",
					"admin_state": "enabled",
					"summary":     "Authentication required",
					"action":      "login",
				},
			},
		}

		svc := NewService(runtime, cfg, emitter, nil, logger)
		diag, err := svc.Doctor(context.Background())

		require.NoError(t, err)
		assert.Equal(t, 1, diag.TotalIssues)
		require.Len(t, diag.OAuthRequired, 1)
		assert.Equal(t, "oauth-server", diag.OAuthRequired[0].ServerName)
		assert.Equal(t, "unauthenticated", diag.OAuthRequired[0].State)
		assert.Contains(t, diag.OAuthRequired[0].Message, "mcpproxy auth login")
	})

	t.Run("ignores authenticated OAuth servers", func(t *testing.T) {
		cfg := &config.Config{}
		emitter := &mockEventEmitter{}
		runtime := newMockRuntime()
		runtime.servers = []map[string]interface{}{
			{
				"id":            "server1",
				"name":          "oauth-server",
				"enabled":       true,
				"connected":     true,
				"authenticated": true,
				"oauth":         map[string]interface{}{"enabled": true},
				// Healthy server has no action
				"health": map[string]interface{}{
					"level":       "healthy",
					"admin_state": "enabled",
					"summary":     "Connected",
					"action":      "",
				},
			},
		}

		svc := NewService(runtime, cfg, emitter, nil, logger)
		diag, err := svc.Doctor(context.Background())

		require.NoError(t, err)
		assert.Equal(t, 0, diag.TotalIssues)
		assert.Empty(t, diag.OAuthRequired)
	})
}

// T036: Unit test for missing secrets detection
// Updated to test aggregation from Health.Action
func TestDoctorMissingSecrets(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()

	t.Run("detects missing environment variable secrets", func(t *testing.T) {
		cfg := &config.Config{}
		emitter := &mockEventEmitter{}
		runtime := newMockRuntime()
		runtime.servers = []map[string]interface{}{
			{
				"id":   "server1",
				"name": "server-with-secret",
				"env": map[string]interface{}{
					"API_KEY": "${env:MISSING_API_KEY}",
				},
				// Health status with set_secret action triggers MissingSecrets aggregation
				"health": map[string]interface{}{
					"level":       "unhealthy",
					"admin_state": "enabled",
					"summary":     "Missing secret",
					"detail":      "MISSING_API_KEY",
					"action":      "set_secret",
				},
			},
		}

		svc := NewService(runtime, cfg, emitter, nil, logger)
		diag, err := svc.Doctor(context.Background())

		require.NoError(t, err)
		assert.Equal(t, 1, diag.TotalIssues)
		require.Len(t, diag.MissingSecrets, 1)
		assert.Equal(t, "MISSING_API_KEY", diag.MissingSecrets[0].SecretName)
		assert.Contains(t, diag.MissingSecrets[0].UsedBy, "server-with-secret")
	})

	t.Run("no issues when secrets are available", func(t *testing.T) {
		cfg := &config.Config{}
		emitter := &mockEventEmitter{}
		runtime := newMockRuntime()
		runtime.servers = []map[string]interface{}{
			{
				"id":   "server1",
				"name": "server-ok",
				"env": map[string]interface{}{
					"API_KEY": "direct-value",
				},
				// Healthy server has no action
				"health": map[string]interface{}{
					"level":       "healthy",
					"admin_state": "enabled",
					"summary":     "Connected",
					"action":      "",
				},
			},
		}

		svc := NewService(runtime, cfg, emitter, nil, logger)
		diag, err := svc.Doctor(context.Background())

		require.NoError(t, err)
		assert.Equal(t, 0, diag.TotalIssues)
		assert.Empty(t, diag.MissingSecrets)
	})
}

// T037: Unit test for Docker status check
func TestDoctorDockerStatus(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()

	t.Run("includes Docker status when isolation enabled", func(t *testing.T) {
		cfg := &config.Config{
			DockerIsolation: &config.DockerIsolationConfig{
				Enabled: true,
			},
		}
		emitter := &mockEventEmitter{}
		runtime := newMockRuntime()
		runtime.servers = []map[string]interface{}{}

		svc := NewService(runtime, cfg, emitter, nil, logger)
		diag, err := svc.Doctor(context.Background())

		require.NoError(t, err)
		assert.NotNil(t, diag.DockerStatus)
	})

	t.Run("omits Docker status when isolation disabled", func(t *testing.T) {
		cfg := &config.Config{
			DockerIsolation: nil,
		}
		emitter := &mockEventEmitter{}
		runtime := newMockRuntime()
		runtime.servers = []map[string]interface{}{}

		svc := NewService(runtime, cfg, emitter, nil, logger)
		diag, err := svc.Doctor(context.Background())

		require.NoError(t, err)
		assert.Nil(t, diag.DockerStatus)
	})
}
