package server

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

func TestHandleCallToolVariant_PanicRecovery(t *testing.T) {
	// Issue #318: When handleCallToolVariant panics (e.g., nil tokenizer dereference),
	// the recover() block must return a proper error result, not (nil, nil).
	// Returning (nil, nil) causes a second unrecovered panic in mcp-go's HandleMessage
	// when it dereferences the nil *CallToolResult.

	t.Run("normal error path returns error result", func(t *testing.T) {
		proxy := createTestMCPProxyServer(t)

		request := mcp.CallToolRequest{}
		request.Params.Arguments = map[string]interface{}{
			"name": "nonexistent-server:some_tool",
		}

		result, err := proxy.handleCallToolVariant(context.Background(), request, contracts.ToolVariantRead)

		// Must never return (nil, nil)
		assert.True(t, result != nil || err != nil,
			"handleCallToolVariant must never return (nil, nil)")
		if result != nil {
			assert.True(t, result.IsError, "error results should have IsError=true")
		}
	})

	t.Run("panic in handler returns error result via recover", func(t *testing.T) {
		proxy := createTestMCPProxyServer(t)

		// Nil out storage to trigger a guaranteed nil pointer panic inside
		// handleCallToolVariant at p.storage.GetUpstreamServer(). This exercises
		// the actual recover() path — the function panics, the deferred recover
		// catches it, and sets callResult via named return values.
		proxy.storage = nil

		request := mcp.CallToolRequest{}
		request.Params.Arguments = map[string]interface{}{
			"name": "panic-server:panic_tool",
		}

		result, err := proxy.handleCallToolVariant(context.Background(), request, contracts.ToolVariantRead)

		// Before fix: would return (nil, nil), crashing mcp-go.
		// After fix: must return a proper error result.
		require.NoError(t, err, "recovered panic should not return an error")
		require.NotNil(t, result, "recovered panic must return a non-nil CallToolResult, not (nil, nil)")
		assert.True(t, result.IsError, "recovered panic should be an error result")

		// Verify the error message mentions the panic
		if len(result.Content) > 0 {
			if text, ok := result.Content[0].(mcp.TextContent); ok {
				assert.Contains(t, text.Text, "Internal proxy error")
				assert.Contains(t, text.Text, "recovered from panic")
			}
		}
	})

	t.Run("panic in legacy handleCallTool returns error result via recover", func(t *testing.T) {
		proxy := createTestMCPProxyServer(t)

		// Same approach: nil storage triggers panic in handleCallTool
		proxy.storage = nil

		request := mcp.CallToolRequest{}
		request.Params.Arguments = map[string]interface{}{
			"name": "panic-server:panic_tool",
		}

		result, err := proxy.handleCallTool(context.Background(), request)

		require.NoError(t, err, "recovered panic should not return an error")
		require.NotNil(t, result, "recovered panic must return a non-nil CallToolResult, not (nil, nil)")
		assert.True(t, result.IsError, "recovered panic should be an error result")

		if len(result.Content) > 0 {
			if text, ok := result.Content[0].(mcp.TextContent); ok {
				assert.Contains(t, text.Text, "Internal proxy error")
			}
		}
	})
}
