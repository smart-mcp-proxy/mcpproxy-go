package server

import (
	"context"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 085 US1 — handler-level compact serialization tests:
//   - T022 (FR-005): per-call `detail` override in both directions; unset
//     falls back to the configured tool_response_mode; the `detail` param is
//     registered on EVERY retrieve_tools definition with all existing
//     parameters preserved.
//   - T023 (FR-009): deterministic top-level `hint` line present iff the
//     effective mode is compact, and counted in the serialized bytes.

// callRetrieve invokes the retrieve_tools handler and returns the decoded
// response plus the raw serialized bytes.
func callRetrieve(t *testing.T, proxy *MCPProxyServer, args map[string]interface{}) (retrieveToolsResponse, string) {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	result, err := proxy.handleRetrieveTools(context.Background(), req)
	require.NoError(t, err)
	return decodeRetrieve(t, result), result.Content[0].(mcp.TextContent).Text
}

// assertCompactEntries asserts every returned entry has the compact shape.
func assertCompactEntries(t *testing.T, resp retrieveToolsResponse) {
	t.Helper()
	require.NotEmpty(t, resp.Tools, "fixture query must match tools")
	for _, entry := range resp.Tools {
		assert.Contains(t, entry, "id")
		assert.Contains(t, entry, "sig")
		assert.Contains(t, entry, "desc")
		assert.Contains(t, entry, "lossy")
		assert.Contains(t, entry, "score")
		assert.NotContains(t, entry, "inputSchema", "compact entries must not carry the full input schema (FR-002)")
		assert.NotContains(t, entry, "description")
		assert.NotContains(t, entry, "name")
	}
}

// assertFullEntries asserts every returned entry has today's full shape.
func assertFullEntries(t *testing.T, resp retrieveToolsResponse) {
	t.Helper()
	require.NotEmpty(t, resp.Tools, "fixture query must match tools")
	for _, entry := range resp.Tools {
		assert.Contains(t, entry, "name")
		assert.Contains(t, entry, "inputSchema")
		assert.Contains(t, entry, "description")
		assert.NotContains(t, entry, "sig")
		assert.NotContains(t, entry, "lossy")
	}
}

func TestRetrieveTools_DetailCompactOverridesConfiguredFull(t *testing.T) {
	proxy := createTestMCPProxyServer(t) // default config => full
	seedEntryBuilderFixture(t, proxy)

	resp, raw := callRetrieve(t, proxy, map[string]interface{}{
		"query": "manage", "limit": float64(10), "detail": "compact",
	})
	assertCompactEntries(t, resp)
	assert.Contains(t, raw, `"hint"`, "compact responses carry the FR-009 hint line")
}

func TestRetrieveTools_DetailFullOverridesConfiguredCompact(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	proxy.config.ToolResponseMode = config.ToolResponseModeCompact
	seedEntryBuilderFixture(t, proxy)

	resp, raw := callRetrieve(t, proxy, map[string]interface{}{
		"query": "manage", "limit": float64(10), "detail": "full",
	})
	assertFullEntries(t, resp)
	assert.NotContains(t, raw, `"hint"`, "full responses must not carry the compact hint")
}

func TestRetrieveTools_UnsetDetailUsesConfiguredMode(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	seedEntryBuilderFixture(t, proxy)

	// Configured compact, no detail => compact.
	proxy.config.ToolResponseMode = config.ToolResponseModeCompact
	resp, _ := callRetrieve(t, proxy, map[string]interface{}{
		"query": "manage", "limit": float64(10),
	})
	assertCompactEntries(t, resp)

	// Configured full (and unset => full is covered by the byte-identity
	// golden test), no detail => full.
	proxy.config.ToolResponseMode = config.ToolResponseModeFull
	resp, _ = callRetrieve(t, proxy, map[string]interface{}{
		"query": "manage", "limit": float64(10),
	})
	assertFullEntries(t, resp)
}

// T023 (FR-009): the hint is a single deterministic line explaining the
// lossy marker and describe_tool, present iff the effective mode is compact.
// Being a top-level response field, it is part of the serialized payload and
// therefore counts toward every measured response size.
func TestRetrieveTools_CompactHintLine(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	proxy.config.ToolResponseMode = config.ToolResponseModeCompact
	seedEntryBuilderFixture(t, proxy)

	_, raw1 := callRetrieve(t, proxy, map[string]interface{}{
		"query": "manage", "limit": float64(10),
	})
	_, raw2 := callRetrieve(t, proxy, map[string]interface{}{
		"query": "weather forecast", "limit": float64(5),
	})

	assert.Contains(t, raw1, `"hint":`)
	assert.Contains(t, raw1, compactModeHint, "hint must be the deterministic FR-009 line")
	assert.Contains(t, raw2, compactModeHint, "hint must be identical across queries (deterministic)")
	assert.NotContains(t, compactModeHint, "\n", "hint must be a single line")
	assert.Contains(t, compactModeHint, "~", "hint must explain the lossy marker")
	assert.Contains(t, compactModeHint, "describe_tool", "hint must point at the second stage")
}

// T022 (FR-005 / US4 FR-014 precondition): the `detail` parameter is
// registered on every retrieve_tools definition — the default server and
// both routing-mode builders — as an enum {compact, full} with NO default,
// and ALL pre-existing parameters are preserved unchanged.
func TestRetrieveTools_DetailParamRegisteredEverywhere(t *testing.T) {
	proxy := createTestMCPProxyServer(t)

	// Existing params per registration site (pre-Spec-085 surface, preserved).
	defaultParams := []string{"query", "limit", "include_stats", "debug", "explain_tool", "include_session_risk_warning", "include_disabled"}
	callToolModeParams := []string{"query", "limit", "include_stats", "debug", "explain_tool", "read_only_only", "exclude_destructive", "exclude_open_world", "include_session_risk_warning"}
	codeExecModeParams := []string{"query", "limit", "read_only_only", "exclude_destructive", "exclude_open_world", "include_session_risk_warning"}

	checkTool := func(t *testing.T, tool mcp.Tool, existing []string) {
		t.Helper()
		props := tool.InputSchema.Properties
		for _, name := range existing {
			assert.Contains(t, props, name, "pre-existing retrieve_tools param %q must be preserved (FR-014)", name)
		}
		require.Contains(t, props, "detail", "detail param must be registered (FR-005)")
		detail, ok := props["detail"].(map[string]interface{})
		require.True(t, ok, "detail schema must be an object")
		assert.Equal(t, "string", detail["type"])
		assert.ElementsMatch(t, []interface{}{"compact", "full"}, detail["enum"])
		assert.NotContains(t, detail, "default", "detail must have NO default — unset means configured mode")
		assert.NotContains(t, tool.InputSchema.Required, "detail", "detail must stay optional")
		assert.Contains(t, tool.InputSchema.Required, "query", "query must stay required")
	}

	t.Run("default server", func(t *testing.T) {
		st := proxy.server.GetTool("retrieve_tools")
		require.NotNil(t, st, "retrieve_tools must be registered on the default server")
		checkTool(t, st.Tool, defaultParams)
	})

	t.Run("call-tool routing mode", func(t *testing.T) {
		for _, st := range proxy.buildCallToolModeTools() {
			if st.Tool.Name == "retrieve_tools" {
				checkTool(t, st.Tool, callToolModeParams)
				return
			}
		}
		t.Fatal("retrieve_tools not found in buildCallToolModeTools")
	})

	t.Run("code-execution routing mode", func(t *testing.T) {
		for _, st := range proxy.buildCodeExecModeTools() {
			if st.Tool.Name == "retrieve_tools" {
				checkTool(t, st.Tool, codeExecModeParams)
				return
			}
		}
		t.Fatal("retrieve_tools not found in buildCodeExecModeTools")
	})
}

// Cross-cutting response sections survive compaction untouched (data-model
// §4): query, total, usage_instructions — compact only trims per-entry bulk.
func TestRetrieveTools_CompactKeepsCrossCuttingSections(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	proxy.config.ToolResponseMode = config.ToolResponseModeCompact
	seedEntryBuilderFixture(t, proxy)

	_, raw := callRetrieve(t, proxy, map[string]interface{}{
		"query": "manage", "limit": float64(10),
	})
	for _, section := range []string{`"query":`, `"total":`, `"usage_instructions":`, `"tools":`} {
		assert.True(t, strings.Contains(raw, section), "compact response must keep top-level section %s", section)
	}
}
