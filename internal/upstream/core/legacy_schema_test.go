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
		{ // draft-07 definitions with a declared dialect (v1.6 keeps them apart)
			`{"$schema":"http://json-schema.org/draft-07/schema#","type":"object","definitions":{"X":{"type":"string"}},"properties":{"x":{"$ref":"#/definitions/X"}}}`,
			`{"$defs":{"X":{"type":"string"}},"properties":{"x":{"$ref":"#/$defs/X"}},"required":[],"type":"object"}`,
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

// Documented residual: a ref nested in a keyword value after the decoder
// moved "definitions" itself keeps its draft-07 spelling in the stored bytes,
// but the Spec 032 hash equals v1.0.0's.
func TestLegacySchemaJSON_NestedRefResidual_HashUnchanged(t *testing.T) {
	in := `{"type":"object","definitions":{"X":{"const":1.0}},"properties":{"x":{"default":{"$ref":"#/definitions/X"}}}}`
	v100 := `{"$defs":{"X":{"const":1}},"properties":{"x":{"default":{"$ref":"#/$defs/X"}}},"required":[],"type":"object"}`
	var s mcp.ToolInputSchema
	require.NoError(t, json.Unmarshal([]byte(in), &s))
	assert.Equal(t,
		hash.ComputeToolHashWithOutputSchema("srv", "t", "d", json.RawMessage(v100), ""),
		hash.ComputeToolHashWithOutputSchema("srv", "t", "d", legacySchema(mcp.ToolArgumentsSchema(s)), ""))
}

// A schema v1.0.0 could not decode has no earlier bytes: it is kept whole.
func TestLegacySchemaJSON_ArrayTypeKeptWhole(t *testing.T) {
	for _, in := range []string{
		`{"type":["object"],"patternProperties":{"^m_":{"type":"number"}},"additionalProperties":false}`,
		`{"$schema":"http://json-schema.org/draft-07/schema#","type":["object"],"definitions":{"Limit":{"const":1e400}}}`,
	} {
		var s mcp.ToolOutputSchema
		require.NoError(t, json.Unmarshal([]byte(in), &s), in)
		got, err := json.Marshal(legacySchema(mcp.ToolArgumentsSchema(s)))
		require.NoError(t, err, in)
		assert.Contains(t, string(got), `"type":["object"]`, in)
	}
}
