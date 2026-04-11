package server

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream"
)

// TestCallToolVariantSchemaAdvertisesArgsObject verifies that the
// call_tool_read/write/destructive tool schemas advertise a native
// `args` object parameter alongside the legacy `args_json` string.
// This eliminates the escaping overhead when LLM clients produce
// tool arguments (see mcp-proxy-shim README for background).
func TestCallToolVariantSchemaAdvertisesArgsObject(t *testing.T) {
	variants := []string{
		contracts.ToolVariantRead,
		contracts.ToolVariantWrite,
		contracts.ToolVariantDestructive,
	}

	for _, variant := range variants {
		t.Run(variant, func(t *testing.T) {
			tool := buildCallToolVariantTool(variant)

			// Legacy string path must remain advertised for backward compat.
			argsJSONProp, ok := tool.InputSchema.Properties["args_json"].(map[string]any)
			require.True(t, ok, "schema must still advertise 'args_json' (backward compat)")
			assert.Equal(t, "string", argsJSONProp["type"])

			// New native object path must be advertised.
			argsProp, ok := tool.InputSchema.Properties["args"].(map[string]any)
			require.True(t, ok, "schema must advertise 'args' as an object property")
			assert.Equal(t, "object", argsProp["type"])

			// `args` must not be required (args_json is still accepted).
			for _, req := range tool.InputSchema.Required {
				assert.NotEqual(t, "args", req, "'args' must not be required")
			}
		})
	}
}

// TestHandleCallToolVariantAcceptsArgsObject verifies the handler extracts a
// native `args` object from the request and routes past the argument-parsing
// phase without emitting "Invalid args_json format" — i.e. the native object
// path actually works, not just the schema.
func TestHandleCallToolVariantAcceptsArgsObject(t *testing.T) {
	mockProxy := &MCPProxyServer{
		upstreamManager: upstream.NewManager(zap.NewNop(), config.DefaultConfig(), nil, secret.NewResolver(), nil),
		logger:          zap.NewNop(),
		config:          &config.Config{},
	}
	ctx := context.Background()

	request := mcp.CallToolRequest{}
	request.Params.Name = contracts.ToolVariantRead
	request.Params.Arguments = map[string]any{
		"name": "non-existent-server:some_tool",
		"args": map[string]any{
			"files": []any{
				map[string]any{"path": "src/index.ts", "head": 20},
			},
		},
	}

	result, err := mockProxy.handleCallToolVariant(ctx, request, contracts.ToolVariantRead)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.IsError, "non-existent server should yield an error result")

	// The error must NOT be an argument-parsing error. The handler may still
	// fail later in the dispatch pipeline (because this mock proxy has no
	// real runtime), but it must get past argument extraction — proving the
	// native object path was accepted.
	require.Len(t, result.Content, 1)
	textContent, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok, "expected TextContent")
	errMsg := textContent.Text
	assert.NotContains(t, errMsg, "Invalid args_json format",
		"handler should not attempt args_json parsing when only 'args' object is provided")
	assert.NotContains(t, errMsg, "Missing required parameter",
		"required parameters are present — 'args' object must be accepted")
}

// TestHandleCallToolVariantArgsJSONWinsWhenBothProvided verifies backward
// compatibility: when a client sends BOTH args_json and args, args_json takes
// precedence. This preserves existing behavior so migration is gradual.
func TestHandleCallToolVariantArgsJSONWinsWhenBothProvided(t *testing.T) {
	mockProxy := &MCPProxyServer{
		upstreamManager: upstream.NewManager(zap.NewNop(), config.DefaultConfig(), nil, secret.NewResolver(), nil),
		logger:          zap.NewNop(),
		config:          &config.Config{},
	}
	ctx := context.Background()

	// Malformed args_json + well-formed args → must surface args_json parse
	// error, proving args_json took precedence.
	request := mcp.CallToolRequest{}
	request.Params.Name = contracts.ToolVariantRead
	request.Params.Arguments = map[string]any{
		"name":      "non-existent-server:some_tool",
		"args_json": "not valid json",
		"args":      map[string]any{"ok": true},
	}

	result, err := mockProxy.handleCallToolVariant(ctx, request, contracts.ToolVariantRead)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.IsError)

	require.Len(t, result.Content, 1)
	textContent, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, textContent.Text, "Invalid args_json format",
		"args_json must take precedence when both are provided (backward compat)")
}
