package server

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// renderFullInputSchema renders one full-mode direct tool from paramsJSON and
// returns its wire inputSchema decoded as a generic map.
func renderFullInputSchema(t *testing.T, paramsJSON string) map[string]any {
	t.Helper()
	tool := renderFullDirectTool(&directCatalogEntry{DisplayName: "srv__tool", ParamsJSON: paramsJSON}, "d")
	raw, err := json.Marshal(tool)
	require.NoError(t, err)
	var wire struct {
		InputSchema map[string]any `json:"inputSchema"`
	}
	require.NoError(t, json.Unmarshal(raw, &wire))
	return wire.InputSchema
}

// An open-object upstream schema must stay open on the direct surface.
// Dropping additionalProperties advertises {"properties":{}}, which
// grammar-constrained clients (llama.cpp) compile to "no keys allowed" — the
// #1364 failure class, here on /mcp/all instead of call_tool_* args.
func TestRenderFullDirectTool_PreservesAdditionalProperties(t *testing.T) {
	t.Run("open object", func(t *testing.T) {
		s := renderFullInputSchema(t, `{"type":"object","properties":{},"additionalProperties":true}`)
		assert.Equal(t, true, s["additionalProperties"])
	})
	t.Run("schema-valued", func(t *testing.T) {
		s := renderFullInputSchema(t, `{"type":"object","properties":{"a":{"type":"string"}},"additionalProperties":{"type":"number"}}`)
		assert.Equal(t, map[string]any{"type": "number"}, s["additionalProperties"])
	})
	t.Run("closed object stays closed", func(t *testing.T) {
		s := renderFullInputSchema(t, `{"type":"object","properties":{"a":{"type":"string"}},"additionalProperties":false}`)
		assert.Equal(t, false, s["additionalProperties"])
	})
	t.Run("absent stays absent", func(t *testing.T) {
		s := renderFullInputSchema(t, `{"type":"object","properties":{"a":{"type":"string"}},"required":["a"]}`)
		assert.NotContains(t, s, "additionalProperties")
		assert.NotContains(t, s, "$defs")
	})
}

// $ref targets must survive too, or a property pointing at #/$defs/X dangles.
func TestRenderFullDirectTool_PreservesDefs(t *testing.T) {
	s := renderFullInputSchema(t, `{"type":"object","properties":{"p":{"$ref":"#/$defs/Pt"}},"$defs":{"Pt":{"type":"object"}}}`)
	assert.Equal(t, map[string]any{"Pt": map[string]any{"type": "object"}}, s["$defs"])
}
