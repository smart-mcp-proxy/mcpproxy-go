package server

import (
	"context"
	"encoding/json"
	"sync"
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
// (here: the commit's connect worker is parked at its Spec 116 PR3 seam), the
// old instance's error state describes the old settings and must not end the
// monitoring.
//
// Spec 116 PR3: the commit itself unregisters the old instance (its
// connection settings changed), and a commit waits for a capture section in
// progress instead of publishing past it, so the replacement is held back at
// the worker, not by holding the capture section across the commit.
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

	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseWorkers := func() { releaseOnce.Do(func() { close(release) }) }
	srv.runtime.SetWorkerActHooksForTest(func(name string, _ int64) {
		if name == "flaky" {
			<-release
		}
	}, nil)
	defer func() {
		releaseWorkers()
		srv.runtime.SetWorkerActHooksForTest(nil, nil)
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
	if cur, registered := mgr.GetClient("flaky"); registered {
		assert.NotSame(t, oldClient, cur, "the commit unregistered the previous instance")
	}
	assert.True(t, oldClient.Unregistered(), "the previous instance never serves again")
	assert.NotEqual(t, "error", body["connection_status"],
		"the previous instance's error was reported as the update's outcome: %v", body["connection_message"])
	assert.Equal(t, statusCancelled, body["connection_status"],
		"with the replacement held back the bounded wait must run out")

	// Review round 7 (finding B): an IDENTICAL update submitted while the
	// replacement is still held back changes nothing, yet must still wait for
	// the instance at the server's committed generation instead of reporting
	// the previous instance's error.
	ctxSame, cancelSame := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	defer cancelSame()
	res, _, err = srv.mcpProxy.handleUpdateUpstream(ctxSame, mcpCall(map[string]interface{}{
		"name": "flaky", "url": "http://127.0.0.1:10/mcp",
	}))
	require.NoError(t, err)
	require.False(t, res.IsError, "identical update failed: %v", res.Content)
	body = nil
	require.NoError(t, json.Unmarshal([]byte(res.Content[0].(mcp.TextContent).Text), &body))
	assert.NotEqual(t, "error", body["connection_status"],
		"an unchanged update reported the previous instance's error: %v", body["connection_message"])
	assert.Equal(t, statusCancelled, body["connection_status"],
		"an unchanged update must wait for the committed instance too")

	// Released, the committed instance's own outcome is what the next update
	// reports: the bounded wait still ends on the replacement's state.
	releaseWorkers()
	// Let the held worker install its instance and finish its connect first.
	require.Eventually(t, func() bool {
		c, ok := mgr.GetClient("flaky")
		return ok && c.GetConfig().URL == "http://127.0.0.1:10/mcp" &&
			c.GetConnectionInfo().State == types.StateError
	}, 20*time.Second, 10*time.Millisecond, "the held replacement never installed and failed")
	// The identical update, once the replacement is installed, reports the
	// replacement's own outcome (round 7, finding B).
	ctxSame2, cancelSame2 := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelSame2()
	res, _, err = srv.mcpProxy.handleUpdateUpstream(ctxSame2, mcpCall(map[string]interface{}{
		"name": "flaky", "url": "http://127.0.0.1:10/mcp",
	}))
	require.NoError(t, err)
	body = nil
	require.NoError(t, json.Unmarshal([]byte(res.Content[0].(mcp.TextContent).Text), &body))
	assert.Equal(t, "error", body["connection_status"])
	assert.Contains(t, body["connection_message"], "127.0.0.1:10", "the unchanged update reports the committed instance")
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
