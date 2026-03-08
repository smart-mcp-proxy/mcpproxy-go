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
	proxy := createTestMCPProxyServer(t)

	// Create a request that will reach upstream call and trigger a panic.
	// We use a server:tool format with a server that doesn't exist in upstream manager.
	// The upstream manager's CallTool will be called, which may panic or error.
	// To reliably trigger a panic, we'll test the recovery mechanism directly.
	t.Run("panic returns error result not nil", func(t *testing.T) {
		// Use a tool name with ":" to skip the "no server prefix" error path
		// and reach the upstream call code. The upstream manager has no servers,
		// so CallTool will error — but we want to test the panic path.
		//
		// We'll craft a scenario that causes a nil pointer dereference.
		// An empty serverConfig from storage + GenerateServerID panics on nil.
		request := mcp.CallToolRequest{}
		request.Params.Arguments = map[string]interface{}{
			"name": "nonexistent-server:some_tool",
		}

		result, err := proxy.handleCallToolVariant(context.Background(), request, contracts.ToolVariantRead)

		// After fix: must never return (nil, nil). Either result or err must be set.
		if err != nil {
			// An error is acceptable
			return
		}
		require.NotNil(t, result, "panic recovery must return a non-nil result, not (nil, nil)")
		assert.True(t, result.IsError, "recovered panic should be an error result")
	})
}

func TestHandleCallToolVariant_PanicRecoveryReturnsErrorResult(t *testing.T) {
	// Directly test that the named return values mechanism works by verifying
	// the function signature contract: on any internal panic, we get back a
	// usable *CallToolResult (never nil).
	proxy := createTestMCPProxyServer(t)

	// Request a tool on a valid-looking but nonexistent server
	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]interface{}{
		"name": "fake:tool",
	}

	result, err := proxy.handleCallToolVariant(context.Background(), request, contracts.ToolVariantWrite)

	// The function must NEVER return (nil, nil)
	assert.True(t, result != nil || err != nil,
		"handleCallToolVariant must never return (nil, nil) — panic recovery must set named return values")

	if result != nil && err == nil {
		// If we got a result without error, it should be marked as an error result
		// (either from normal error handling or from panic recovery)
		assert.True(t, result.IsError, "error results should have IsError=true")
	}
}
