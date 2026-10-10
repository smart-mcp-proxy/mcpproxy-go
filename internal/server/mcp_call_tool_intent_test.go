package server

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

func intentReq(args map[string]interface{}) mcp.CallToolRequest {
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	return req
}

func TestExtractIntent_FlatAndLegacyNested(t *testing.T) {
	p := &MCPProxyServer{}

	t.Run("flat keys", func(t *testing.T) {
		i, err := p.extractIntent(intentReq(map[string]interface{}{
			"intent_reason": "r", "intent_data_sensitivity": "public"}))
		require.NoError(t, err)
		require.NotNil(t, i)
		assert.Equal(t, "r", i.Reason)
		assert.Equal(t, "public", i.DataSensitivity)
	})

	t.Run("legacy nested object is a fallback", func(t *testing.T) {
		i, err := p.extractIntent(intentReq(map[string]interface{}{
			"intent": map[string]interface{}{
				"operation_type": "destructive", "data_sensitivity": "private", "reason": "legacy"}}))
		require.NoError(t, err)
		require.NotNil(t, i)
		assert.Equal(t, "legacy", i.Reason)
		assert.Equal(t, "private", i.DataSensitivity)
		assert.Empty(t, i.OperationType, "operation_type is never taken from the client")
	})

	t.Run("flat wins over nested", func(t *testing.T) {
		i, err := p.extractIntent(intentReq(map[string]interface{}{
			"intent_reason": "flat",
			"intent":        map[string]interface{}{"reason": "nested", "data_sensitivity": "private"}}))
		require.NoError(t, err)
		require.NotNil(t, i)
		assert.Equal(t, "flat", i.Reason)
		assert.Empty(t, i.DataSensitivity, "nested is only used when both flat keys are empty")
	})

	t.Run("non-object intent ignored", func(t *testing.T) {
		i, err := p.extractIntent(intentReq(map[string]interface{}{"intent": "free text"}))
		require.NoError(t, err)
		assert.Nil(t, i)
	})
}

func TestFlattenedCallToolArgs_LegacyIntentNotForwarded(t *testing.T) {
	assert.Nil(t, flattenedCallToolArgs(map[string]interface{}{
		"name":   "s:t",
		"intent": map[string]interface{}{"operation_type": "read", "reason": "why"}}),
		"a legacy-shaped intent object is audit metadata, not an upstream argument")

	got := flattenedCallToolArgs(map[string]interface{}{"name": "s:t", "intent": "free text"})
	assert.Equal(t, map[string]interface{}{"intent": "free text"}, got)

	got = flattenedCallToolArgs(map[string]interface{}{"name": "s:t", "intent": map[string]interface{}{"foo": 1.0}})
	assert.Equal(t, map[string]interface{}{"intent": map[string]interface{}{"foo": 1.0}}, got)

	got = flattenedCallToolArgs(map[string]interface{}{"name": "s:t", "url": "u",
		"intent": map[string]interface{}{"reason": "x"}})
	assert.Equal(t, map[string]interface{}{"url": "u"}, got, "#1317 recovery still works")
}

// TestDocsCallToolExamples_MatchLiveContract is a docs smoke check (UX-06):
// every JSON example in the call_tool_* section of the protocol docs, and in
// the intent-declaration page, must use only keys the live call_tool_*
// schema registers, must parse as JSON, and must never carry the retired
// nested "intent" object or a client-supplied operation_type.
func TestDocsCallToolExamples_MatchLiveContract(t *testing.T) {
	files := []string{
		"../../docs/api/mcp-protocol.md",
		"../../docs/features/intent-declaration.md",
	}
	toolNameRE := regexp.MustCompile(`^[A-Za-z0-9_-]+:[A-Za-z0-9_-]+$`)
	fence := regexp.MustCompile("(?s)```json\n(.*?)```")
	checked := 0
	for _, f := range files {
		raw, err := os.ReadFile(f)
		require.NoError(t, err)
		for _, m := range fence.FindAllStringSubmatch(string(raw), -1) {
			var ex map[string]interface{}
			if json.Unmarshal([]byte(m[1]), &ex) != nil {
				// Non-object snippets or snippets with placeholders are not
				// call examples; malformed call examples are caught below.
				if strings.Contains(m[1], `"name": "`) && strings.Contains(m[1], "intent") {
					t.Fatalf("%s: call_tool example is not valid JSON:\n%s", f, m[1])
				}
				continue
			}
			name, _ := ex["name"].(string)
			if !toolNameRE.MatchString(name) {
				continue // not a call_tool_* example
			}
			if _, isCall := ex["operation"]; isCall {
				continue
			}
			checked++
			assert.NotContains(t, ex, "intent", "%s: retired nested intent in example:\n%s", f, m[1])
			assert.NotContains(t, ex, "operation_type", "%s: operation_type is inferred, not sent:\n%s", f, m[1])
			for k := range ex {
				if _, ok := callToolMetaKeys[k]; !ok {
					t.Errorf("%s: example key %q is not a call_tool_* parameter:\n%s", f, k, m[1])
				}
			}
			if aj, ok := ex["args_json"].(string); ok {
				var parsed map[string]interface{}
				assert.NoError(t, json.Unmarshal([]byte(aj), &parsed), "args_json must be a JSON object string")
			}
		}
	}
	assert.GreaterOrEqual(t, checked, 4, "expected to find the documented call_tool examples")
}

// UX-11 guard: the protocol docs must not call code_execution disabled by
// default while the shipped default is on.
func TestDocsCodeExecutionDefault_MatchesConfig(t *testing.T) {
	require.True(t, config.DefaultConfig().EnableCodeExecution, "code execution is on by default since v0.66.0")
	raw, err := os.ReadFile("../../docs/api/mcp-protocol.md")
	require.NoError(t, err)
	doc := strings.ToLower(string(raw))
	assert.NotContains(t, doc, "disabled by default")
	assert.Contains(t, doc, "enabled by default since v0.66.0")
}
