package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// CreateMockIsErrorServer starts a mock MCP server exposing two tools: one that
// answers normally and one that answers with isError:true — the shape an
// upstream uses to report a bad argument value, an unknown tool, or a
// server-side validation failure. The transport call succeeds in both cases.
func (env *TestEnvironment) CreateMockIsErrorServer(name string) *MockUpstreamServer {
	mcpServer := mcpserver.NewMCPServer(name, "1.0.0-test", mcpserver.WithToolCapabilities(true))
	mockServer := &MockUpstreamServer{server: mcpServer}

	okTool := mcp.Tool{
		Name:        "get_time",
		Description: "Returns a fixed time",
		InputSchema: mcp.ToolInputSchema{Type: "object", Properties: map[string]interface{}{}},
	}
	failTool := mcp.Tool{
		Name:        "bad_timezone",
		Description: "Always answers with isError:true, like a real server rejecting an argument",
		InputSchema: mcp.ToolInputSchema{Type: "object", Properties: map[string]interface{}{}},
	}
	mockServer.tools = []mcp.Tool{okTool, failTool}

	mcpServer.AddTool(okTool, func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("2026-01-01T00:00:00Z"), nil
	})
	mcpServer.AddTool(failTool, func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultError("Invalid timezone: 'Mars/Olympus'"), nil
	})

	streamableServer := mcpserver.NewStreamableHTTPServer(mcpServer)

	ln, err := net.Listen("tcp", ":0")
	require.NoError(env.t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	mockServer.addr = fmt.Sprintf("http://localhost:%d", port)
	httpServer := &http.Server{
		Addr:              fmt.Sprintf(":%d", port),
		Handler:           streamableServer,
		ReadHeaderTimeout: 5 * time.Second,
	}
	mockServer.httpServer = httpServer
	mockServer.stopFunc = httpServer.Close

	go func() {
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			env.logger.Error("Mock isError server error", zap.Error(err))
		}
	}()

	time.Sleep(200 * time.Millisecond)

	env.mockServers[name] = mockServer
	return mockServer
}

// TestE2E_UpstreamIsErrorRecordedAsActivityError is the regression test for
// issue #935.
//
// Before the fix, a call whose upstream answered isError:true landed in the
// activity log with status="success" — the transport hop had worked, so err was
// nil and the emit site hardcoded "success". That hid the most common real
// failure from the tray glance error markers and from the 24h error counts.
//
// The test drives the real dispatch path (client → /mcp → call_tool_read →
// upstream) and asserts on what is actually persisted, so it fails if any layer
// between the classifier and the activity store drops the classification.
func TestE2E_UpstreamIsErrorRecordedAsActivityError(t *testing.T) {
	env := NewTestEnvironment(t)
	defer env.Cleanup()

	mockServer := env.CreateMockIsErrorServer("flaky")

	mcpClient := env.CreateProxyClient()
	defer mcpClient.Close()
	env.ConnectClient(mcpClient)

	ctx := context.Background()

	addRequest := mcp.CallToolRequest{}
	addRequest.Params.Name = "upstream_servers"
	addRequest.Params.Arguments = map[string]interface{}{
		"operation": "add",
		"name":      "flaky",
		"url":       mockServer.addr,
		"protocol":  "streamable-http",
		"enabled":   true,
	}
	_, err := mcpClient.CallTool(ctx, addRequest)
	require.NoError(t, err)

	rt := env.proxyServer.runtime

	serverConfig, err := rt.StorageManager().GetUpstreamServer("flaky")
	require.NoError(t, err)
	serverConfig.Quarantined = false
	require.NoError(t, rt.StorageManager().SaveUpstreamServer(serverConfig))

	servers, err := rt.StorageManager().ListUpstreamServers()
	require.NoError(t, err)
	cfg := rt.Config()
	cfg.Servers = servers
	require.NoError(t, rt.LoadConfiguredServers(cfg))

	time.Sleep(3 * time.Second)
	_ = rt.DiscoverAndIndexTools(ctx)
	time.Sleep(3 * time.Second)

	callRequest := mcp.CallToolRequest{}
	callRequest.Params.Name = "call_tool_read"
	callRequest.Params.Arguments = map[string]interface{}{
		"name":   "flaky:bad_timezone",
		"args":   map[string]interface{}{},
		"intent": map[string]interface{}{"operation_type": "read"},
	}

	callResult, err := mcpClient.CallTool(ctx, callRequest)
	require.NoError(t, err)
	require.NotNil(t, callResult)

	// The caller's answer is untouched: the upstream's own isError result is
	// still forwarded verbatim. Only the activity classification changes.
	assert.True(t, callResult.IsError,
		"the upstream's isError result must still reach the caller unchanged")

	record := awaitToolCallActivity(t, rt, "flaky", "bad_timezone")
	assert.Equal(t, "error", record.Status,
		"an isError:true upstream answer must be recorded as an error (issue #935)")
	assert.Contains(t, record.ErrorMessage, "Mars/Olympus",
		"the recorded error_message must carry the upstream's own explanation")

	// And a genuinely successful call on the same server is still a success —
	// the classifier must not paint everything red.
	okRequest := mcp.CallToolRequest{}
	okRequest.Params.Name = "call_tool_read"
	okRequest.Params.Arguments = map[string]interface{}{
		"name":   "flaky:get_time",
		"args":   map[string]interface{}{},
		"intent": map[string]interface{}{"operation_type": "read"},
	}
	okResult, err := mcpClient.CallTool(ctx, okRequest)
	require.NoError(t, err)
	require.False(t, okResult.IsError)

	okRecord := awaitToolCallActivity(t, rt, "flaky", "get_time")
	assert.Equal(t, "success", okRecord.Status)
	assert.Empty(t, okRecord.ErrorMessage)
}

// awaitToolCallActivity polls the activity store for the tool_call record of a
// given server/tool. Activity is written asynchronously off the event bus, so a
// single immediate read races the writer.
func awaitToolCallActivity(t *testing.T, rt interface {
	ListActivities(storage.ActivityFilter) ([]*storage.ActivityRecord, int, error)
}, serverName, toolName string) *storage.ActivityRecord {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for {
		records, _, err := rt.ListActivities(storage.ActivityFilter{
			Types:  []string{string(storage.ActivityTypeToolCall)},
			Server: serverName,
			Tool:   toolName,
			Limit:  50,
		})
		require.NoError(t, err)
		if len(records) > 0 {
			return records[0]
		}
		if time.Now().After(deadline) {
			t.Fatalf("no tool_call activity recorded for %s:%s within 10s", serverName, toolName)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
