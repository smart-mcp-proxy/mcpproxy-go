package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/outputvalidation"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/stateview"
)

func TestApplyToolOutputSchemaJSON_PreservesOutputSchemaInToolsListJSON(t *testing.T) {
	tool := mcp.NewTool("github__create_issue", mcp.WithDescription("create issue"))

	applied := applyToolOutputSchemaJSON(&tool, `{"type":"object","properties":{"url":{"type":"string"}}}`)

	assert.True(t, applied)
	assert.JSONEq(t, `{"type":"object","properties":{"url":{"type":"string"}}}`, string(tool.RawOutputSchema))

	toolJSON, err := json.Marshal(tool)
	require.NoError(t, err)

	var payload map[string]interface{}
	require.NoError(t, json.Unmarshal(toolJSON, &payload))
	assert.Contains(t, payload, "outputSchema")
	assert.NotContains(t, payload, "RawOutputSchema")
	assert.NotContains(t, payload, "rawOutputSchema")
}

func TestApplyToolOutputSchemaJSON_RejectsInvalidJSON(t *testing.T) {
	tool := mcp.NewTool("github__create_issue", mcp.WithDescription("create issue"))

	applied := applyToolOutputSchemaJSON(&tool, `{"type":`)

	assert.False(t, applied)
	assert.Empty(t, tool.RawOutputSchema)
}

// =============================================================================
// Spec 090 (T016): output-schema decisions carry the caller's request id
// =============================================================================

// applyOutputValidation records both blocks (strict mode) and warnings (warn
// mode) as policy decisions. Both need the dispatch's request id: a warning
// with no identity cannot be attached to the call that produced it, and the
// tray would show it as an orphan row.
func TestApplyOutputValidation_StrictBlock_CarriesRequestID(t *testing.T) {
	proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{
		{Name: "github", Enabled: true},
	})
	proxy.config.OutputValidation = &config.OutputValidationConfig{
		Mode:                     "strict",
		MissingStructuredContent: "block",
	}
	proxy.outputValidator = outputvalidation.New(
		proxy.config.OutputValidation.EffectiveMaxBytes(),
		proxy.config.OutputValidation.EffectiveMaxDepth(),
		zap.NewNop(),
	)
	seedOutputSchema(t, rt, "github", "get_repo", `{"type":"object","properties":{"url":{"type":"string"}}}`)

	probe := watchPolicyDecisions(t, rt)

	// Declares a schema but returns no structured content — the FR-A8 trap.
	forwarded := &mcp.CallToolResult{Content: []mcp.Content{
		mcp.TextContent{Type: "text", Text: "some prose"},
	}}

	block := proxy.applyOutputValidation(context.Background(), "github", "get_repo", "req-schema-1", forwarded)
	require.NotNil(t, block, "strict mode + missing structured content must block")
	require.True(t, block.IsError)

	payload := probe.awaitOne(t)
	assert.Equal(t, "blocked", payload["decision"])
	assert.Equal(t, "req-schema-1", requestIDOf(t, payload))
}

// seedOutputSchema publishes a tool with a declared output schema into the
// runtime's state view, which is where applyOutputValidation looks the schema
// up (lookupOutputSchema reads the snapshot, never storage).
func seedOutputSchema(t *testing.T, rt *runtime.Runtime, serverName, toolName, schemaJSON string) {
	t.Helper()

	supervisor := rt.Supervisor()
	require.NotNil(t, supervisor, "runtime must expose a supervisor")
	supervisor.StateView().UpdateServer(serverName, func(s *stateview.ServerStatus) {
		s.Tools = []stateview.ToolInfo{{Name: toolName, OutputSchemaJSON: schemaJSON}}
	})
}
