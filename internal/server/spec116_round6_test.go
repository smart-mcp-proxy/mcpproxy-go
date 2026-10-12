package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/types"
)

// Review round 6 (finding 2, tasks.md T029): the MCP update reports the
// connection of the instance the commit installed, never the previous one.
// The commit reconnects after it returns; while the replacement is held back
// (here: the manager's capture read section is held, so AddServerConfig
// cannot swap the entry), the old instance's error state describes the old
// settings and must not end the monitoring.
func TestMCPUpdate_R6_StatusIgnoresPreviousInstance(t *testing.T) {
	srv, _ := newSpec116Server(t, &config.ServerConfig{
		Name: "flaky", URL: "http://127.0.0.1:9/mcp", Protocol: "http", Enabled: true,
	})
	mgr := srv.runtime.UpstreamManager()

	var old = func() bool {
		c, ok := mgr.GetClient("flaky")
		return ok && c.GetConnectionInfo().State == types.StateError
	}
	require.Eventually(t, old, 30*time.Second, 10*time.Millisecond, "the old instance never reached its error state")
	oldClient, _ := mgr.GetClient("flaky")

	held := make(chan struct{})
	release := make(chan struct{})
	holdDone := make(chan struct{})
	go func() {
		defer close(holdDone)
		_, _ = mgr.WithCurrentClientOnEpoch("flaky", oldClient, oldClient.ConnectionEpoch(), func() error {
			close(held)
			<-release
			return nil
		})
	}()
	awaitSignal(t, held, "the capture read section")
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
		<-holdDone
	}()
	require.Equal(t, types.StateError, oldClient.GetConnectionInfo().State, "precondition: old instance is in error")

	ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	defer cancel()
	res, _, err := srv.mcpProxy.handleUpdateUpstream(ctx, mcpCall(map[string]interface{}{
		"name": "flaky", "url": "http://127.0.0.1:10/mcp",
	}))
	require.NoError(t, err)
	require.False(t, res.IsError, "update failed: %v", res.Content)

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(res.Content[0].(mcp.TextContent).Text), &body))
	cur, _ := mgr.GetClient("flaky")
	assert.Same(t, oldClient, cur, "the replacement must still be held back")
	assert.NotEqual(t, "error", body["connection_status"],
		"the previous instance's error was reported as the update's outcome: %v", body["connection_message"])
	assert.Equal(t, statusCancelled, body["connection_status"],
		"with the replacement held back the bounded wait must run out")

	// Released, the committed instance's own outcome is what the next update
	// reports: the bounded wait still ends on the replacement's state.
	close(release)
	<-holdDone
	// Let the held worker install its instance and finish its connect first:
	// a worker that overlaps a later commit's worker (and can connect or
	// replace the newer instance) is PR3's stale-worker case (#1562).
	require.Eventually(t, func() bool {
		c, ok := mgr.GetClient("flaky")
		return ok && c.GetConfig().URL == "http://127.0.0.1:10/mcp" &&
			c.GetConnectionInfo().State == types.StateError
	}, 20*time.Second, 10*time.Millisecond, "the held replacement never installed and failed")
	ctx2, cancel2 := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel2()
	res, _, err = srv.mcpProxy.handleUpdateUpstream(ctx2, mcpCall(map[string]interface{}{
		"name": "flaky", "url": "http://127.0.0.1:11/mcp",
	}))
	require.NoError(t, err)
	body = nil
	require.NoError(t, json.Unmarshal([]byte(res.Content[0].(mcp.TextContent).Text), &body))
	assert.Equal(t, "error", body["connection_status"])
	assert.Contains(t, body["connection_message"], "127.0.0.1:11", "the status comes from the committed instance")
}
