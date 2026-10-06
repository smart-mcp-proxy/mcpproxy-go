package server

import (
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// assertOpenObjectSchema pins issue #1364: an open-object meta-tool parameter
// must serialise as {"type":"object","additionalProperties":true} with no empty
// "properties" map. Grammar-constrained clients (llama.cpp) compile an empty
// "properties" map as "no keys allowed", so models could only emit {}.
func assertOpenObjectSchema(t *testing.T, tool mcp.Tool, param string) {
	t.Helper()
	raw, err := json.Marshal(tool)
	require.NoError(t, err)
	var decoded struct {
		InputSchema struct {
			Properties map[string]map[string]any `json:"properties"`
		} `json:"inputSchema"`
	}
	require.NoError(t, json.Unmarshal(raw, &decoded))
	prop, ok := decoded.InputSchema.Properties[param]
	require.True(t, ok, "%s: param %q missing", tool.Name, param)
	assert.Equal(t, "object", prop["type"])
	assert.Equal(t, true, prop["additionalProperties"], "%s.%s must allow arbitrary keys (#1364)", tool.Name, param)
	_, hasProps := prop["properties"]
	assert.False(t, hasProps, "%s.%s must not carry an empty properties map (#1364)", tool.Name, param)
}

func TestOpenObjectParamsAreNotGrammarClosed_Issue1364(t *testing.T) {
	for _, variant := range []string{"call_tool_read", "call_tool_write", "call_tool_destructive"} {
		t.Run(variant, func(t *testing.T) {
			assertOpenObjectSchema(t, buildCallToolVariantTool(variant), "args")
		})
	}
	t.Run("code_execution", func(t *testing.T) {
		p := &MCPProxyServer{config: &config.Config{EnableCodeExecution: true}}
		tools := p.buildCodeExecutionTool()
		require.Len(t, tools, 1)
		assertOpenObjectSchema(t, tools[0].Tool, "input")
		assertOpenObjectSchema(t, tools[0].Tool, "options")
	})
}
