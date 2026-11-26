package core

import (
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/hash"
)

// Spec 135 FR-019 / research R1a: mcp-go v1.3+ keeps every JSON Schema
// keyword when decoding a tool schema ($schema, oneOf, ...) and marshals it
// back. mcpproxy stores, scans and hashes the marshalled schema, so the bump
// alone would change the Spec 032 hash of every tool that declares one of
// them (re-quarantining approved tools as "changed") and feed the scanner
// text it never saw. The legacy encoder reproduces v1.0.0's bytes exactly.
func TestLegacySchemaJSON_MatchesMCPGoV100(t *testing.T) {
	cases := []struct{ in, v100 string }{
		{ // @modelcontextprotocol/server-everything "echo", as sent
			`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"message":{"type":"string","description":"Message to echo"}},"required":["message"]}`,
			`{"properties":{"message":{"description":"Message to echo","type":"string"}},"required":["message"],"type":"object"}`,
		},
		{`{"type":"object"}`, `{"properties":{},"required":[],"type":"object"}`},
		{
			`{"type":"object","properties":{"a":{"$ref":"#/definitions/X"}},"definitions":{"X":{"type":"string"}},"additionalProperties":false,"oneOf":[{"required":["a"]}]}`,
			`{"$defs":{"X":{"type":"string"}},"additionalProperties":false,"properties":{"a":{"$ref":"#/$defs/X"}},"required":[],"type":"object"}`,
		},
	}
	for _, tc := range cases {
		var s mcp.ToolInputSchema
		require.NoError(t, json.Unmarshal([]byte(tc.in), &s))
		got, err := json.Marshal(legacySchema(mcp.ToolArgumentsSchema(s)))
		require.NoError(t, err)
		assert.JSONEq(t, tc.v100, string(got), tc.in)
		assert.Equal(t, tc.v100, string(got), "byte for byte (hashes and stored schemas)")
		assert.Equal(t,
			hash.ComputeToolHashWithOutputSchema("srv", "t", "d", json.RawMessage(tc.v100), ""),
			hash.ComputeToolHashWithOutputSchema("srv", "t", "d", legacySchema(mcp.ToolArgumentsSchema(s)), ""),
			"the Spec 032 hash is the one v1.0.0 produced")
	}
}
