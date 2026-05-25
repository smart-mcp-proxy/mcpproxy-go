// Command outputschema is a minimal stdio MCP server used by the Spec 056
// output-schema-validation E2E test. It exposes three tools, all declaring the
// same output schema (an object with a required integer "id"):
//
//   - conforming:   returns structured {"id": 7}            -> passes validation
//   - bad_output:   returns structured {"id": "not-an-int"} -> violates the schema
//   - text_only:    returns only text content (no structuredContent) -> the
//     ContextForge #4042 case (declared schema, nothing to validate)
//
// It is intentionally dependency-light and deterministic so the proxy's
// validation behaviour can be asserted from curl/JSON-RPC.
package main

import (
	"context"
	"encoding/json"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const outputSchema = `{
  "type": "object",
  "properties": { "id": { "type": "integer" } },
  "required": ["id"],
  "additionalProperties": true
}`

func main() {
	s := server.NewMCPServer("outputschema-stub", "1.0.0")

	rawSchema := json.RawMessage(outputSchema)

	conforming := mcp.NewTool("conforming",
		mcp.WithDescription("Returns a structured response that conforms to its output schema."),
		mcp.WithRawOutputSchema(rawSchema),
	)
	s.AddTool(conforming, func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultStructured(map[string]any{"id": 7}, `{"id":7}`), nil
	})

	badOutput := mcp.NewTool("bad_output",
		mcp.WithDescription("Returns a structured response that VIOLATES its output schema (id is a string)."),
		mcp.WithRawOutputSchema(rawSchema),
	)
	s.AddTool(badOutput, func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultStructured(map[string]any{"id": "not-an-int"}, `{"id":"not-an-int"}`), nil
	})

	textOnly := mcp.NewTool("text_only",
		mcp.WithDescription("Declares an output schema but returns only text content (no structuredContent)."),
		mcp.WithRawOutputSchema(rawSchema),
	)
	s.AddTool(textOnly, func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("plain text, no structured content"), nil
	})

	if err := server.ServeStdio(s); err != nil {
		panic(err)
	}
}
