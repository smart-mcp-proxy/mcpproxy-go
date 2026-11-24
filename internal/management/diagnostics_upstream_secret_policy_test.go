package management

import (
	"context"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/health"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func TestDoctor_UpstreamSecretPolicyDenialPublicOnly(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()
	lastError, public, leaks := upstreamPolicyDoctorInputs(t)

	h := health.CalculateHealth(health.HealthCalculatorInput{
		Name: "denied", Enabled: true, State: "error", Connected: false, LastError: lastError,
	}, nil)

	runtime := newMockRuntime()
	runtime.servers = []map[string]interface{}{
		{
			"name":       "denied",
			"last_error": lastError,
			"health": map[string]interface{}{
				"level":  h.Level,
				"action": h.Action,
				"detail": h.Detail,
			},
		},
	}
	svc := NewService(runtime, &config.Config{}, "", &mockEventEmitter{}, nil, logger)
	diag, err := svc.Doctor(context.Background())
	require.NoError(t, err)
	require.Len(t, diag.UpstreamErrors, 1)
	msg := diag.UpstreamErrors[0].ErrorMessage
	assert.Contains(t, msg, public)
	for _, leak := range leaks {
		assert.NotContains(t, msg, leak)
	}
}
