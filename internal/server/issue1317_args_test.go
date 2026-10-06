package server

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

// Regression coverage for GH #1317's second bug: call_tool_write/read
// silently resolved to an empty args map for calls that didn't nest the
// upstream tool's parameters under the documented 'args' object, or that
// paired a populated 'args' with an empty 'args_json' — in both cases the
// pre-dispatch validator then reported the caller's own property as
// "missing", even though it was supplied.
const stubURLSchema = `{"type":"object","properties":{"url":{"type":"string"}},"required":["url"]}`

func newTestNavigateStub(t *testing.T, proxy *MCPProxyServer) {
	t.Helper()
	startCountingStubUpstream(t, proxy, map[string]mcp.ToolInputSchema{
		"navigate_page": {
			Type:       "object",
			Properties: map[string]interface{}{"url": map[string]interface{}{"type": "string"}},
			Required:   []string{"url"},
		},
	})
	indexStubTool(t, proxy, "navigate_page", stubURLSchema)
}

// TestCallToolVariant_FlattenedArgs_Dispatches is mechanism A: the caller
// places the upstream tool's parameters as top-level siblings of 'name'
// instead of nesting them under 'args'. Before the fix this silently
// resolved to zero args and failed pre-dispatch validation claiming 'url'
// was missing. It must now be recovered and dispatch successfully.
func TestCallToolVariant_FlattenedArgs_Dispatches(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	newTestNavigateStub(t, proxy)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"name": "stub:navigate_page",
		"url":  "https://example.com",
	}
	result, err := proxy.handleCallToolVariant(context.Background(), req, contracts.ToolVariantRead)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.IsError, "flattened args must be recovered, not rejected: %v", result.Content)
}

// TestCallToolVariant_ArgsJSONEmptyDoesNotShadowPopulatedArgs is mechanism B:
// an empty/no-op args_json ("{}") must not win over a correctly populated
// 'args' object supplied alongside it.
func TestCallToolVariant_ArgsJSONEmptyDoesNotShadowPopulatedArgs(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	newTestNavigateStub(t, proxy)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"name":      "stub:navigate_page",
		"args":      map[string]interface{}{"url": "https://example.com"},
		"args_json": "{}",
	}
	result, err := proxy.handleCallToolVariant(context.Background(), req, contracts.ToolVariantRead)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.IsError, "a populated 'args' must win over an empty 'args_json': %v", result.Content)
}

// TestCallToolVariant_NestedArgsStillWorks is the no-regression control:
// the documented, correctly-nested shape must keep working unchanged.
func TestCallToolVariant_NestedArgsStillWorks(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	newTestNavigateStub(t, proxy)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"name": "stub:navigate_page",
		"args": map[string]interface{}{"url": "https://example.com"},
	}
	result, err := proxy.handleCallToolVariant(context.Background(), req, contracts.ToolVariantRead)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.IsError, "correctly-nested args must dispatch: %v", result.Content)
}

// TestCallToolMetaKeys_MatchesRegisteredSchema is a review-round guard
// against drift: callToolMetaKeys (mcp.go) is a hand-maintained denylist of
// call_tool_read/write/destructive's own top-level parameters, used to tell
// them apart from an upstream tool's arguments in flattenedCallToolArgs. If
// a future change adds a parameter to buildCallToolVariantTool's schema
// without updating callToolMetaKeys, that new parameter would be
// misclassified as an upstream argument by the flattened-args fallback --
// this test fails loudly instead of that happening silently.
func TestCallToolMetaKeys_MatchesRegisteredSchema(t *testing.T) {
	for _, variant := range []string{contracts.ToolVariantRead, contracts.ToolVariantWrite, contracts.ToolVariantDestructive} {
		tool := buildCallToolVariantTool(variant)
		for propName := range tool.InputSchema.Properties {
			_, known := callToolMetaKeys[propName]
			assert.True(t, known, "variant %s: schema property %q is not in callToolMetaKeys -- add it, or flattenedCallToolArgs will misclassify it as an upstream tool argument", variant, propName)
		}
	}
	for metaKey := range callToolMetaKeys {
		if metaKey == "name" {
			continue // present on every variant's schema; checked implicitly above
		}
		found := false
		for _, variant := range []string{contracts.ToolVariantRead, contracts.ToolVariantWrite, contracts.ToolVariantDestructive} {
			if _, ok := buildCallToolVariantTool(variant).InputSchema.Properties[metaKey]; ok {
				found = true
				break
			}
		}
		assert.True(t, found, "callToolMetaKeys has %q, which no call_tool_* variant's schema actually declares -- remove it or it's dead weight", metaKey)
	}
}

// TestCallToolVariant_TrulyMissingArgStillRejected is the no-regression
// control for the OTHER direction: a call with no args anywhere (no 'args',
// no 'args_json', no extra top-level keys) for a tool with a required
// property must still be rejected pre-dispatch, not silently allowed
// through.
func TestCallToolVariant_TrulyMissingArgStillRejected(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	newTestNavigateStub(t, proxy)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"name": "stub:navigate_page",
	}
	result, err := proxy.handleCallToolVariant(context.Background(), req, contracts.ToolVariantRead)
	require.NoError(t, err)
	require.NotNil(t, result)
	body := decodeErrorBody(t, result)
	assert.Equal(t, "invalid_params", body["error_type"])
	assert.Contains(t, body["error"], "url")
}
