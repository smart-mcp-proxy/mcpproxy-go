package core

import (
	"encoding/json"

	"github.com/mark3labs/mcp-go/mcp"
)

const outputSchemaMarshalErrorKey = "_mcpproxy_output_schema_marshal_error"

func captureOutputSchemaJSON(tool *mcp.Tool) string {
	if len(tool.RawOutputSchema) > 0 {
		return normalizeRawJSON(tool.RawOutputSchema)
	}

	if tool.OutputSchema.Type == "" {
		return ""
	}

	data, err := json.Marshal(tool.OutputSchema)
	if err != nil {
		return outputSchemaMarshalErrorJSON(err)
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

func outputSchemaMarshalErrorJSON(err error) string {
	data, marshalErr := json.Marshal(map[string]string{
		outputSchemaMarshalErrorKey: err.Error(),
	})
	if marshalErr != nil {
		return `{"_mcpproxy_output_schema_marshal_error":"unknown"}`
	}
	return string(data)
}
