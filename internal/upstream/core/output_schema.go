package core

import (
	"encoding/json"

	"github.com/mark3labs/mcp-go/mcp"
)

func captureOutputSchemaJSON(tool *mcp.Tool) string {
	if len(tool.RawOutputSchema) > 0 {
		return normalizeRawJSON(tool.RawOutputSchema)
	}

	if tool.OutputSchema.Type == "" {
		return ""
	}

	data, err := json.Marshal(tool.OutputSchema)
	if err != nil {
		return ""
	}
	return normalizeRawJSON(data)
}

func normalizeRawJSON(data []byte) string {
	var parsed interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return string(data)
	}

	normalized, err := json.Marshal(parsed)
	if err != nil {
		return string(data)
	}
	return string(normalized)
}
