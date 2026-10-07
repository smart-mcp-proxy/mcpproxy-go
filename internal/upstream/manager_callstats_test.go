package upstream

import (
	"context"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

func TestManager_RecordCallOutcome_And_RemoveDropsWindow(t *testing.T) {
	serverCfg := limitedServerConfig("stats-server")
	cfg := &config.Config{Servers: []*config.ServerConfig{serverCfg}}
	m := newConcurrencyManager(t, cfg, serverCfg)

	m.RecordCallOutcome("stats-server", &mcp.CallToolResult{}, nil)
	m.RecordCallOutcome("stats-server", &mcp.CallToolResult{IsError: true}, nil) // uncounted
	m.RecordCallOutcome("stats-server", nil, context.DeadlineExceeded)
	calls, failures, kind := m.CallStats("stats-server")
	assert.Equal(t, 2, calls)
	assert.Equal(t, 1, failures)
	assert.Equal(t, "timeout", kind)

	m.RemoveServer("stats-server")
	calls, failures, _ = m.CallStats("stats-server")
	assert.Zero(t, calls)
	assert.Zero(t, failures)
}

// callTool is a choke point: a dispatch that reaches the managed client and
// fails is recorded against the server by name.
func TestManager_CallTool_RecordsDispatchOutcome(t *testing.T) {
	serverCfg := limitedServerConfig("dispatch-server")
	serverCfg.QueueTimeout = nil
	cfg := &config.Config{Servers: []*config.ServerConfig{serverCfg}}
	m := newConcurrencyManager(t, cfg, serverCfg)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := m.CallTool(ctx, "dispatch-server:some_tool", map[string]interface{}{})
	require.Error(t, err)

	calls, failures, _ := m.CallStats("dispatch-server")
	assert.Equal(t, 1, calls, "a failed dispatch must be recorded")
	assert.Equal(t, 1, failures)

	// An unknown server never reached a client: nothing recorded.
	_, err = m.CallTool(ctx, "ghost:some_tool", nil)
	require.Error(t, err)
	calls, _, _ = m.CallStats("ghost")
	assert.Zero(t, calls)
}
