package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/pkoukk/tiktoken-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 085 US2 — describe_tool (contracts/describe_tool.md):
//   - T025 (FR-010): definition FIELD-equal to the full-mode retrieve_tools
//     entry over {name, description, inputSchema, server, annotations,
//     call_with}; no score key; mixed valid/unknown ⇒ per-id errors with the
//     batch succeeding; >5 ids ⇒ single limit error, nothing processed.
//   - T026 (FR-011, Constitution IV): id resolution goes through the SAME
//     visibility predicate retrieve_tools uses (p.toolVisibleToSession) —
//     an out-of-scope/quarantined/pending/changed/disabled id yields a per-id
//     error, never a definition.
//   - T028 (FR-011): registered in the retrieve_tools routing mode only
//     (default server + buildCallToolModeTools), absent from code_execution
//     and direct mode; definition ≤150 tokens under tiktoken cl100k_base.
//   - T029 (FR-012): byte-identical output under full and compact
//     tool_response_mode.

// describeToolResponse decodes the JSON the describe_tool handler returns.
type describeToolResponse struct {
	Definitions []map[string]interface{} `json:"definitions"`
	Errors      []map[string]interface{} `json:"errors"`
}

// callDescribeRaw invokes the describe_tool handler and returns the raw
// result (which may be an error result).
func callDescribeRaw(t *testing.T, proxy *MCPProxyServer, ctx context.Context, ids []interface{}) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{"tool_ids": ids}
	result, err := proxy.handleDescribeTool(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, result)
	return result
}

// callDescribe invokes describe_tool and decodes the success response.
func callDescribe(t *testing.T, proxy *MCPProxyServer, ctx context.Context, ids []interface{}) describeToolResponse {
	t.Helper()
	result := callDescribeRaw(t, proxy, ctx, ids)
	require.False(t, result.IsError, "describe_tool returned an error result: %v", result.Content)
	text := result.Content[0].(mcp.TextContent).Text
	var resp describeToolResponse
	require.NoError(t, json.Unmarshal([]byte(text), &resp))
	return resp
}

// fullModeEntryFor captures the full-mode retrieve_tools entry for one tool.
func fullModeEntryFor(t *testing.T, proxy *MCPProxyServer, query, name string) map[string]interface{} {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"query": query, "limit": float64(20), "detail": "full",
	}
	result, err := proxy.handleRetrieveTools(context.Background(), req)
	require.NoError(t, err)
	resp := decodeRetrieve(t, result)
	for _, entry := range resp.Tools {
		if entry["name"] == name {
			return entry
		}
	}
	t.Fatalf("tool %s not in full-mode retrieve_tools results", name)
	return nil
}

// T025 (FR-010): a definition is field-equal to the full-mode retrieve_tools
// entry — the exact contract recipe: capture the full entry, delete its
// ranked-only "score" key, and the remainder must be byte-equal to the
// definition. The definition itself must never carry a score.
func TestDescribeTool_DefinitionFieldEqualToFullMode(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	seedEntryBuilderFixture(t, proxy)

	fullEntry := fullModeEntryFor(t, proxy, "manage", "github:create_issue")
	require.Contains(t, fullEntry, "score", "full-mode entries carry the ranked score")
	delete(fullEntry, "score")

	resp := callDescribe(t, proxy, context.Background(), []interface{}{"github:create_issue"})
	require.Len(t, resp.Definitions, 1)
	require.Empty(t, resp.Errors)
	def := resp.Definitions[0]

	assert.NotContains(t, def, "score", "describe_tool is a lookup, not a ranked search — no score key (FR-010)")

	// Field equality over the definition fields — asserted as whole-map
	// equality after the score strip, so the shared fields cannot drift.
	wantJSON, err := json.Marshal(fullEntry)
	require.NoError(t, err)
	gotJSON, err := json.Marshal(def)
	require.NoError(t, err)
	assert.JSONEq(t, string(wantJSON), string(gotJSON),
		"definition must be field-equal to the full-mode entry over {name, description, inputSchema, server, annotations, call_with}")

	// Belt and braces: the named contract fields individually.
	for _, field := range []string{"name", "description", "inputSchema", "server", "call_with"} {
		assert.Equal(t, fullEntry[field], def[field], "definition field %q must match the full-mode entry", field)
	}
}

// T025 (FR-010): mixed valid + unknown ids ⇒ definitions for the valid ones,
// per-id errors for the rest; the call as a whole succeeds.
func TestDescribeTool_MixedValidAndUnknownIDs(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	seedEntryBuilderFixture(t, proxy)

	resp := callDescribe(t, proxy, context.Background(), []interface{}{
		"github:create_issue",
		"github:no_such_tool",
		"not-a-valid-id",
	})

	require.Len(t, resp.Definitions, 1, "the one valid id must resolve")
	assert.Equal(t, "github:create_issue", resp.Definitions[0]["name"])

	require.Len(t, resp.Errors, 2, "each unresolvable id gets its own error entry")
	byID := map[string]map[string]interface{}{}
	for _, e := range resp.Errors {
		byID[e["id"].(string)] = e
	}
	require.Contains(t, byID, "github:no_such_tool")
	assert.Equal(t, "not_found", byID["github:no_such_tool"]["error"])
	assert.Contains(t, byID["github:no_such_tool"]["remediation"], "retrieve_tools",
		"remediation must point the agent back at discovery")
	require.Contains(t, byID, "not-a-valid-id")
	assert.Equal(t, "not_found", byID["not-a-valid-id"]["error"])
}

// T025 (FR-010): >5 ids ⇒ one limit error naming the cap; the batch is not
// processed (anti-bulk-loophole, spec edge case).
func TestDescribeTool_TooManyIDs(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	seedEntryBuilderFixture(t, proxy)

	result := callDescribeRaw(t, proxy, context.Background(), []interface{}{
		"github:create_issue", "github:list_issues", "github:get_repo",
		"weather:get_forecast", "weather:search_city", "github:create_issue",
	})
	require.True(t, result.IsError, "6 ids must be rejected outright")
	text := result.Content[0].(mcp.TextContent).Text
	assert.Contains(t, text, "too many tool_ids: 6 (max 5)")
	assert.NotContains(t, text, "definitions", "no partial dump on a limit error")
}

// T025: missing / empty tool_ids follow the existing param-error convention.
func TestDescribeTool_MissingOrEmptyIDs(t *testing.T) {
	proxy := createTestMCPProxyServer(t)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{}
	result, err := proxy.handleDescribeTool(context.Background(), req)
	require.NoError(t, err)
	require.True(t, result.IsError)
	assert.Contains(t, result.Content[0].(mcp.TextContent).Text, "Missing required parameter 'tool_ids'")

	result = callDescribeRaw(t, proxy, context.Background(), []interface{}{})
	require.True(t, result.IsError, "an empty tool_ids list is a param error, not an empty batch")
	assert.Contains(t, result.Content[0].(mcp.TextContent).Text, "tool_ids")
}

// T026 (FR-011, Constitution IV): describe_tool must never return a definition
// the same session's retrieve_tools could not return — asserted against the
// SAME predicate (p.toolVisibleToSession) on the same session, across the
// agent-scope, quarantine, pending/changed, and disabled fixture cases.
func TestDescribeTool_VisibilityParityWithRetrieve(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	seedVisibilityFixture(t, proxy)

	// Agent token scoped to github+quarry (gitlab out of scope) — the exact
	// session the T009 retrieve-parity test drives.
	agentCtx := &auth.AuthContext{
		Type:           auth.AuthTypeAgent,
		AgentName:      "parity-bot",
		AllowedServers: []string{"github", "quarry"},
		Permissions:    []string{auth.PermRead, auth.PermWrite},
	}
	ctx := auth.WithAuthContext(context.Background(), agentCtx)

	cases := []struct {
		id        string
		wantError string // "" = definition expected
	}{
		{"github:visible_tool", ""},
		{"gitlab:scoped_tool", "invisible"},
		{"quarry:lingering_tool", "quarantined"},
		{"github:pending_tool", "pending_approval"},
		{"github:changed_tool", "changed"},
		{"github:disabled_tool", "disabled"},
		{"github:ghost_tool", "not_found"},
	}

	for _, c := range cases {
		t.Run(c.id, func(t *testing.T) {
			resp := callDescribe(t, proxy, ctx, []interface{}{c.id})

			server, tool, ok := splitServerTool(c.id)
			require.True(t, ok)
			visible, _ := proxy.toolVisibleToSession(ctx, server, tool)

			if c.wantError == "" {
				require.True(t, visible, "fixture: %s must be visible to the predicate", c.id)
				require.Len(t, resp.Definitions, 1)
				assert.Equal(t, c.id, resp.Definitions[0]["name"])
				assert.Empty(t, resp.Errors)
			} else {
				require.False(t, visible, "fixture: %s must be invisible to the predicate", c.id)
				assert.Empty(t, resp.Definitions,
					"describe_tool returned a definition retrieve_tools could not (%s)", c.id)
				require.Len(t, resp.Errors, 1)
				assert.Equal(t, c.id, resp.Errors[0]["id"])
				assert.Equal(t, c.wantError, resp.Errors[0]["error"])
				assert.NotEmpty(t, resp.Errors[0]["remediation"])
				// Never leak withheld content: no schema/description fields on
				// error entries (a quarantined description is a TPA payload).
				assert.NotContains(t, resp.Errors[0], "inputSchema")
				assert.NotContains(t, resp.Errors[0], "description")
			}
		})
	}
}

// T028 (FR-011): describe_tool is registered in the retrieve_tools routing
// mode only — the default server and buildCallToolModeTools — and absent from
// code_execution and direct mode.
func TestDescribeTool_RegisteredInRetrieveToolsModeOnly(t *testing.T) {
	proxy := createTestMCPProxyServer(t)

	t.Run("default server", func(t *testing.T) {
		st := proxy.server.GetTool("describe_tool")
		require.NotNil(t, st, "describe_tool must be registered on the default (retrieve_tools mode) server")
		assert.NotNil(t, proxy.server.GetTool("retrieve_tools"), "describe_tool sits beside retrieve_tools")
	})

	t.Run("call-tool routing mode", func(t *testing.T) {
		var found bool
		for _, st := range proxy.buildCallToolModeTools() {
			if st.Tool.Name == "describe_tool" {
				found = true
			}
		}
		assert.True(t, found, "describe_tool must be in buildCallToolModeTools")
	})

	t.Run("code-execution routing mode", func(t *testing.T) {
		for _, st := range proxy.buildCodeExecModeTools() {
			assert.NotEqual(t, "describe_tool", st.Tool.Name,
				"describe_tool must NOT be exposed in code_execution mode (v1)")
		}
	})

	t.Run("direct routing mode", func(t *testing.T) {
		for _, st := range proxy.buildDirectModeTools() {
			assert.NotEqual(t, "describe_tool", st.Tool.Name,
				"describe_tool must NOT be exposed in direct mode (v1)")
		}
	})
}

// T028 (FR-011): the describe_tool definition costs ≤150 tokens counted with
// tiktoken cl100k_base — the same pinned encoder the spec-083 profiler uses,
// so the budget and the profiler agree.
func TestDescribeTool_DefinitionTokenBudget(t *testing.T) {
	tool := buildDescribeToolTool()
	serialized, err := json.Marshal(tool)
	require.NoError(t, err)

	enc, err := tiktoken.GetEncoding("cl100k_base")
	require.NoError(t, err, "cl100k_base encoding must be loadable (bench pins the same encoder)")

	tokens := len(enc.Encode(string(serialized), nil, nil))
	assert.LessOrEqual(t, tokens, 150,
		"describe_tool definition must stay within the 150-token budget (FR-011); got %d tokens for %s",
		tokens, serialized)

	// Schema sanity: single required array-of-strings param.
	assert.Equal(t, "describe_tool", tool.Name)
	require.Contains(t, tool.InputSchema.Properties, "tool_ids")
	assert.Equal(t, []string{"tool_ids"}, tool.InputSchema.Required)
	idsSchema := tool.InputSchema.Properties["tool_ids"].(map[string]any)
	assert.Equal(t, "array", idsSchema["type"])
}

// T029 (FR-012): describe_tool output is byte-identical whether the configured
// tool_response_mode is full or compact — it ignores the mode entirely.
func TestDescribeTool_ModeIndependent(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	seedEntryBuilderFixture(t, proxy)
	ids := []interface{}{"github:create_issue", "weather:get_forecast", "github:no_such_tool"}

	proxy.config.ToolResponseMode = config.ToolResponseModeFull
	fullResult := callDescribeRaw(t, proxy, context.Background(), ids)
	require.False(t, fullResult.IsError)

	proxy.config.ToolResponseMode = config.ToolResponseModeCompact
	compactResult := callDescribeRaw(t, proxy, context.Background(), ids)
	require.False(t, compactResult.IsError)

	assert.Equal(t,
		fullResult.Content[0].(mcp.TextContent).Text,
		compactResult.Content[0].(mcp.TextContent).Text,
		"describe_tool must return identical bytes in both response modes (FR-012)")
}
