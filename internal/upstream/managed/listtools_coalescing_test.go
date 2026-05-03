package managed

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/types"
)

// TestListTools_CoalescesWaiters verifies that when a ListTools operation is already
// in progress, additional callers wait for and receive the shared result instead of
// failing with an in-progress error.
func TestListTools_CoalescesWaiters(t *testing.T) {
	mc := &Client{
		Config: &config.ServerConfig{Name: "test-server"},
		logger: zap.NewNop(),
	}

	mc.StateManager = types.NewStateManager()
	mc.StateManager.TransitionTo(types.StateConnecting)
	mc.StateManager.TransitionTo(types.StateReady)

	shared := []*config.ToolMetadata{
		{ServerName: "test-server", Name: "tool_a"},
		{ServerName: "test-server", Name: "tool_b"},
	}

	mc.listToolsInProgress = true
	mc.listToolsWaitCh = make(chan struct{})
	mc.listToolsLastResult = shared
	mc.listToolsLastErr = nil

	close(mc.listToolsWaitCh)

	tools, err := mc.ListTools(context.Background())
	require.NoError(t, err)
	assert.Equal(t, shared, tools)
}

// TestListTools_CoalescesWaitersError verifies waiting callers get the same error
// returned by the in-flight ListTools operation.
func TestListTools_CoalescesWaitersError(t *testing.T) {
	mc := &Client{
		Config: &config.ServerConfig{Name: "test-server"},
		logger: zap.NewNop(),
	}

	mc.StateManager = types.NewStateManager()
	mc.StateManager.TransitionTo(types.StateConnecting)
	mc.StateManager.TransitionTo(types.StateReady)

	mc.listToolsInProgress = true
	mc.listToolsWaitCh = make(chan struct{})
	mc.listToolsLastErr = assert.AnError

	close(mc.listToolsWaitCh)

	tools, err := mc.ListTools(context.Background())
	require.Error(t, err)
	assert.Nil(t, tools)
	assert.Contains(t, err.Error(), "ListTools failed")
}
