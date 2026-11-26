package core

import (
	"encoding/json"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// legacySchema serialises a decoded tool schema exactly as mcp-go v1.0.0
// would have: the library's current full encoding (close to the upstream's
// own JSON) is decoded again the way v1.0.0's toolArgumentsSchemaUnmarshalJSON
// did, then encoded the way its toolArgumentsSchemaMarshalJSON did: "type",
// "$defs" (from "definitions" when there is no "$defs", with local
// "#/definitions/" refs rewritten), "properties" ({} when empty), "required"
// ([] when empty) and "additionalProperties", and nothing else.
//
// Why (Spec 135 FR-019, research R1a): since v1.3 mcp-go keeps every other
// keyword it decodes ($schema, oneOf, ...), keeps "definitions" apart when a
// dialect is declared, and preserves number spellings. mcpproxy stores that
// JSON as the tool's parameters, scans it and hashes it for tool-level
// quarantine (Spec 032), so taking the new output would mark approved tools
// as changed on upgrade and hand the scanner text it never reviewed. Adopting
// the richer schema is a separate change that needs a hash-formula migration.
//
// A schema v1.0.0 could not decode (an array-valued "type", an out-of-range
// number) is stored in full. Residuals: a schema that already used "$defs"
// and also carries "#/definitions/" refs (dangling for v1.0.0, which left
// them, and now rewritten; an embedded resource with its own "definitions"
// is the one well-formed case), and duplicate or case-variant members the
// decoder already merged (malformed JSON Schema). Neither moves the Spec 032
// hash, which canonicalises refs (hash.NormalizeJSON).
type legacySchema mcp.ToolArgumentsSchema

func (s legacySchema) MarshalJSON() ([]byte, error) {
	full, err := json.Marshal(mcp.ToolArgumentsSchema(s))
	if err != nil {
		return nil, err
	}
	var v10 struct {
		Defs                 map[string]any  `json:"$defs,omitempty"`
		Definitions          map[string]any  `json:"definitions,omitempty"`
		Type                 json.RawMessage `json:"type"`
		Properties           map[string]any  `json:"properties"`
		Required             []string        `json:"required,omitempty"`
		AdditionalProperties any             `json:"additionalProperties,omitempty"`
	}
	if err := json.Unmarshal(full, &v10); err != nil {
		// v1.0.0 could not have decoded it either (a number out of float64
		// range, say): there are no earlier bytes to keep.
		return full, nil
	}
	if v10.Defs == nil && v10.Definitions != nil {
		v10.Defs = v10.Definitions
	}
	// v1.0.0 rewrote every local "#/definitions/" ref, structural or nested
	// in a keyword value, when it moved "definitions" to "$defs". The current
	// decoder may already have moved them, rewriting only structural refs,
	// and the two cases then look alike: rewrite whenever "$defs" is present.
	// Pre-dispatch argument validation compiles these stored bytes, so this
	// keeps v1.0.0's validation behaviour, not only its hash.
	if v10.Defs != nil {
		rewriteDraft07LocalRefsV100(v10.Defs)
		rewriteDraft07LocalRefsV100(v10.Properties)
		rewriteDraft07LocalRefsV100(v10.AdditionalProperties)
	}

	var typeString string
	if len(v10.Type) != 0 && string(v10.Type) != "null" && json.Unmarshal(v10.Type, &typeString) != nil {
		// v1.0.0 could not decode this schema (an array-valued "type"), so
		// there are no earlier bytes to keep: store the full schema.
		return full, nil
	}
	m := make(map[string]any, 5)
	m["type"] = typeString
	if v10.Defs != nil {
		m["$defs"] = v10.Defs
	}
	if v10.Properties != nil {
		m["properties"] = v10.Properties
	} else {
		m["properties"] = map[string]any{}
	}
	if len(v10.Required) > 0 {
		m["required"] = v10.Required
	} else {
		m["required"] = []string{}
	}
	if v10.AdditionalProperties != nil {
		m["additionalProperties"] = v10.AdditionalProperties
	}
	return json.Marshal(m)
}

// rewriteDraft07LocalRefsV100 is mcp-go v1.0.0's rewriteDraft07LocalRefs
// (MIT licence): local "#/definitions/..." refs become "#/$defs/...".
func rewriteDraft07LocalRefsV100(node any) {
	const draft07Prefix = "#/definitions/"
	switch v := node.(type) {
	case map[string]any:
		if ref, ok := v["$ref"].(string); ok && strings.HasPrefix(ref, draft07Prefix) {
			v["$ref"] = "#/$defs/" + ref[len(draft07Prefix):]
		}
		for _, child := range v {
			rewriteDraft07LocalRefsV100(child)
		}
	case []any:
		for _, child := range v {
			rewriteDraft07LocalRefsV100(child)
		}
	}
}
