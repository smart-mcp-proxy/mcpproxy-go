package core

import (
	"encoding/json"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/hash"
)

// captureOutputSchemaJSON extracts a tool's MCP outputSchema as a canonical JSON
// string for hashing and storage. Returns "" when the tool exposes no output
// schema, or when the schema cannot be marshaled — a transient marshal failure
// must NOT bake an error payload into the contract hash (that would spuriously
// flip the tool to "changed"); treating it as "no schema" is the safe default.
func captureOutputSchemaJSON(tool *mcp.Tool) string {
	if len(tool.RawOutputSchema) > 0 {
		return hash.NormalizeJSON(string(tool.RawOutputSchema))
	}

	if tool.OutputSchema.Type == "" {
		return ""
	}

	data, err := json.Marshal(tool.OutputSchema)
	if err != nil {
		return ""
	}
	return hash.NormalizeJSON(string(data))
}
