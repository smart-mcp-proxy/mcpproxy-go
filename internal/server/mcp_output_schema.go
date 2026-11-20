package server

import (
	"encoding/json"

	"github.com/mark3labs/mcp-go/mcp"
)

func applyToolOutputSchemaJSON(tool *mcp.Tool, outputSchemaJSON string) bool {
	if !outputSchemaAdvertised(outputSchemaJSON) {
		return false
	}
	raw := json.RawMessage(outputSchemaJSON)

	tool.RawOutputSchema = append(json.RawMessage(nil), raw...)
	tool.OutputSchema = mcp.ToolOutputSchema{}
	return true
}
