package server

import (
	"encoding/json"

	"github.com/mark3labs/mcp-go/mcp"
)

func applyToolOutputSchemaJSON(tool *mcp.Tool, outputSchemaJSON string) bool {
	if outputSchemaJSON == "" {
		return false
	}

	raw := json.RawMessage(outputSchemaJSON)
	if !json.Valid(raw) {
		return false
	}

	tool.RawOutputSchema = append(json.RawMessage(nil), raw...)
	tool.OutputSchema = mcp.ToolOutputSchema{}
	return true
}
