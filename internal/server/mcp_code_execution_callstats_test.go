package server

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/types"
)

// Spec 113-d FR-062: code_execution dispatches straight to the managed client
// and bypasses upstream.Manager.callTool, so it must record its own outcome.
func TestCodeExecutionToolCaller_RecordsCallOutcome(t *testing.T) {
	t.Setenv("CI", "")

	serverCfg := &config.ServerConfig{
		Name: "db", URL: "http://127.0.0.1:1", Protocol: "http", Enabled: true,
		MaxConcurrentRequests: shedIntPtr(1),
		QueueSize:             shedIntPtr(0),
		QueueTimeout:          shedDurPtr(30 * time.Second),
	}
	cfg := &config.Config{Servers: []*config.ServerConfig{serverCfg}}
	um := upstream.NewManager(zap.NewNop(), cfg, nil, secret.NewResolver(), nil)
	require.NoError(t, um.AddServerConfig("db", serverCfg))
	client, ok := um.GetClient("db")
	require.True(t, ok)
	client.StateManager.TransitionTo(types.StateConnecting)
	client.StateManager.TransitionTo(types.StateReady)

	caller := &upstreamToolCaller{upstreamManager: um, logger: zap.NewNop(), executionID: "exec-stats"}

	// A limiter shed is the proxy's own refusal: it must not count.
	lim := um.Limiters().Server("db")
	require.NotNil(t, lim)
	release, err := lim.Acquire(context.Background(), time.Time{})
	require.NoError(t, err)
	_, callErr := caller.CallTool(context.Background(), "db", "query", map[string]interface{}{})
	require.Error(t, callErr)
	release()
	calls, _, _ := um.CallStats("db")
	assert.Zero(t, calls, "limiter refusal must not be recorded")

	// A dispatch that reaches the (dead) upstream and fails is a counted failure.
	_, callErr = caller.CallTool(context.Background(), "db", "query", map[string]interface{}{})
	require.Error(t, callErr)
	calls, failures, _ := um.CallStats("db")
	assert.Equal(t, 1, calls)
	assert.Equal(t, 1, failures)
}
