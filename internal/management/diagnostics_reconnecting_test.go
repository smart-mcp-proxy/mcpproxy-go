package management

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/health"
)

// GH #1929: doctor must not report a network outage the client is still
// retrying on its own as an upstream connection error; a given-up one, or any
// other restart-class failure, still is.
func TestHealthStatusFromMap(t *testing.T) {
	assert.Equal(t, health.StatusConnecting, healthStatusFromMap(map[string]interface{}{
		"health": &contracts.HealthStatus{Status: health.StatusConnecting}}))
	assert.Equal(t, health.StatusError, healthStatusFromMap(map[string]interface{}{
		"health": map[string]interface{}{"status": "error"}}))
	assert.Equal(t, "", healthStatusFromMap(map[string]interface{}{}))
}

func TestDoctor_LeavesASelfResolvingOutageOut(t *testing.T) {
	rt := newMockRuntime()
	rt.servers = []map[string]interface{}{
		{"name": "reconnecting", "last_error": "dial tcp: connect: connection refused",
			"health": &contracts.HealthStatus{Level: health.LevelDegraded, Status: health.StatusConnecting,
				Summary: "Reconnecting: connection refused", Action: health.ActionRestart, Actions: []string{health.ActionRestart}}},
		{"name": "gave-up", "last_error": "dial tcp: connect: connection refused",
			"health": &contracts.HealthStatus{Level: health.LevelDegraded, Status: health.StatusError,
				Summary: "Reconnecting: connection refused", Action: health.ActionRestart, Actions: []string{health.ActionRestart}}},
	}
	svc := NewService(rt, &config.Config{}, "", &mockEventEmitter{}, nil, zaptest.NewLogger(t).Sugar())
	diag, err := svc.Doctor(context.Background())
	require.NoError(t, err)
	require.Len(t, diag.UpstreamErrors, 1, "only the server that stopped retrying is a connection error")
	assert.Equal(t, "gave-up", diag.UpstreamErrors[0].ServerName)
}
