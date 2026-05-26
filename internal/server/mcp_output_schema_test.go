package server

import (
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
