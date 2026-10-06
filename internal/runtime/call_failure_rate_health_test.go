package runtime

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/health"
)

// Spec 113-d SC-005: a connected server failing 6 of 10 calls is degraded at
// the next health computation. The window itself (and its expiry) is covered
// in callstats; here the manager's counts must reach the server projection.
func TestGetAllServers_HealthDegradesOnCallFailureRate(t *testing.T) {
	rt := newStaleTokenRuntime(t)
	sc := headerAuthServer()
	markConnected(t, rt, sc, 4)

	healthOf := func() *contracts.HealthStatus {
		h, ok := findServer(t, rt, sc.Name)["health"].(*contracts.HealthStatus)
		require.True(t, ok, "health must be a *contracts.HealthStatus")
		return h
	}
	require.Equal(t, health.LevelHealthy, healthOf().Level)

	um := rt.UpstreamManager()
	require.NotNil(t, um)
	for i := 0; i < 4; i++ {
		um.RecordCallOutcome(sc.Name, nil, nil)
	}
	for i := 0; i < 6; i++ {
		um.RecordCallOutcome(sc.Name, nil, errors.New("request timeout"))
	}

	h := healthOf()
	assert.Equal(t, health.LevelDegraded, h.Level)
	assert.Equal(t, "6 of 10 tool calls failed in the last 5 min", h.Summary)
	assert.Contains(t, h.Detail, "timeout")

	// Removing the server drops its window: nothing carries over to a re-add.
	um.RemoveServer(sc.Name)
	calls, _, _ := um.CallStats(sc.Name)
	assert.Zero(t, calls)
}
