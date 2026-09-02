package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// bigResponseChars must exceed the test environment's ToolResponseLimit (10000)
// by enough that the truncator's own banner still leaves the payload cut.
const bigResponseChars = 60_000

// CreateMockBigResponseServer starts a mock MCP server with two tools: one that
// answers with far more text than ToolResponseLimit allows, and one that fits
// comfortably inside it.
func (env *TestEnvironment) CreateMockBigResponseServer(name string) *MockUpstreamServer {
	mcpServer := mcpserver.NewMCPServer(name, "1.0.0-test", mcpserver.WithToolCapabilities(true))
	mockServer := &MockUpstreamServer{server: mcpServer}

	bigTool := mcp.Tool{
		Name:        "dump_everything",
		Description: "Returns far more text than the proxy forwards",
		InputSchema: mcp.ToolInputSchema{Type: "object", Properties: map[string]interface{}{}},
	}
	smallTool := mcp.Tool{
		Name:        "dump_a_little",
		Description: "Returns a short answer that fits inside the response limit",
		InputSchema: mcp.ToolInputSchema{Type: "object", Properties: map[string]interface{}{}},
	}
	mockServer.tools = []mcp.Tool{bigTool, smallTool}

	mcpServer.AddTool(bigTool, func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText(strings.Repeat("payload ", bigResponseChars/8)), nil
	})
	mcpServer.AddTool(smallTool, func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("a short answer"), nil
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
			env.logger.Error("Mock big-response server error", zap.Error(err))
		}
	}()

	time.Sleep(200 * time.Millisecond)

	env.mockServers[name] = mockServer
	return mockServer
}

// awaitInternalToolCallFor polls for the internal_tool_call record a call_tool_*
// dispatch emits for a given target tool. Activity is written asynchronously off
// the event bus, so a single immediate read races the writer.
func awaitInternalToolCallFor(t *testing.T, rt interface {
	ListActivities(storage.ActivityFilter) ([]*storage.ActivityRecord, int, error)
}, targetTool string) *storage.ActivityRecord {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for {
		records, _, err := rt.ListActivities(storage.ActivityFilter{
			Types: []string{string(storage.ActivityTypeInternalToolCall)},
			Limit: 200,
		})
		require.NoError(t, err)
		for _, rec := range records {
			if strings.HasPrefix(rec.ToolName, "call_tool_") {
				if target, _ := rec.Metadata["target_tool"].(string); target == targetTool {
					return rec
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no internal call_tool_* activity record for %q within 10s", targetTool)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestE2E_CallToolInternalRecordFlagsForwardTruncation is the regression test
// for the root cause of issue #1173.
//
// handleCallToolVariant emitted its internal_tool_call record through the
// WHOLE-RESPONSE form while passing the PRE-forward upstream result — even
// though forwardContentResult had already cut the agent's copy to
// ToolResponseLimit and returned a wasTruncated it never used. So a call_tool_*
// record stored the entire upstream payload, however large, with
// response_truncated=false: the exact Spec 103 overstatement that
// emitActivityInternalToolCallTruncated exists to prevent. It is also why the
// reporter's oversized records carried no truncation flag, while the PAIRED
// tool_call record — which gets the post-forward text — looked fine.
func TestE2E_CallToolInternalRecordFlagsForwardTruncation(t *testing.T) {
	env := NewTestEnvironment(t)
	defer env.Cleanup()

	mockServer := env.CreateMockBigResponseServer("bulky")

	mcpClient := env.CreateProxyClient()
	defer mcpClient.Close()
	env.ConnectClient(mcpClient)

	ctx := context.Background()

	addRequest := mcp.CallToolRequest{}
	addRequest.Params.Name = "upstream_servers"
	addRequest.Params.Arguments = map[string]interface{}{
		"operation": "add",
		"name":      "bulky",
		"url":       mockServer.addr,
		"protocol":  "streamable-http",
		"enabled":   true,
	}
	_, err := mcpClient.CallTool(ctx, addRequest)
	require.NoError(t, err)

	rt := env.proxyServer.runtime

	serverConfig, err := rt.StorageManager().GetUpstreamServer("bulky")
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

	call := func(tool string) *mcp.CallToolResult {
		req := mcp.CallToolRequest{}
		req.Params.Name = "call_tool_read"
		req.Params.Arguments = map[string]interface{}{
			"name":   "bulky:" + tool,
			"args":   map[string]interface{}{},
			"intent": map[string]interface{}{"operation_type": "read"},
		}
		res, callErr := mcpClient.CallTool(ctx, req)
		require.NoError(t, callErr)
		require.NotNil(t, res)
		return res
	}

	forwarded := call("dump_everything")
	require.NotEmpty(t, forwarded.Content)
	delivered := forwarded.Content[0].(mcp.TextContent).Text

	rec := awaitInternalToolCallFor(t, rt, "dump_everything")

	// Premise check. If the fixture stopped exceeding ToolResponseLimit the
	// assertion below would be asserting nothing.
	require.Less(t, len(delivered), bigResponseChars,
		"fixture must actually exceed ToolResponseLimit: the agent's text has to be "+
			"shorter than what the upstream returned")

	assert.True(t, rec.ResponseTruncated,
		"a call_tool_* record holding the pre-forward upstream result must say the "+
			"agent received less; without the flag a benchmark tokenizes text nobody paid for")

	// The other half of the contract: the flag has to mean something. A blanket
	// true would make every call_tool_* record unusable to the same consumers.
	call("dump_a_little")
	small := awaitInternalToolCallFor(t, rt, "dump_a_little")
	assert.False(t, small.ResponseTruncated,
		"a response that fit inside the limit was delivered whole and must not be flagged")
}
