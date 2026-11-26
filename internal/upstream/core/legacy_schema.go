package core

import (
	"encoding/json"

	"github.com/mark3labs/mcp-go/mcp"
)

// legacySchema serialises a decoded tool schema exactly as mcp-go v1.0.0 did
// (its toolArgumentsSchemaMarshalJSON): "type", "$defs", "properties"
// (always, {} when empty), "required" (always, [] when empty) and
// "additionalProperties", and nothing else.
//
// Why (Spec 135 FR-019, research R1a): since v1.3 mcp-go keeps every other
// keyword it decodes ($schema, oneOf, descriptions, ...) and marshals it
// back. mcpproxy stores that JSON as the tool's parameters, scans it and
// hashes it for tool-level quarantine (Spec 032), so taking the new output
// would mark every approved tool that declares such a keyword as changed on
// upgrade and hand the scanner text it never reviewed. Adopting the richer
// schema is a separate change that needs a hash-formula migration.
//
// An array-valued "type" (which v1.0.0 could not decode at all) is written
// as the array.
type legacySchema mcp.ToolArgumentsSchema

func (s legacySchema) MarshalJSON() ([]byte, error) {
	m := make(map[string]any, 5)
	switch {
	case s.Type == "" && len(s.Types) > 0:
		m["type"] = s.Types
	default:
		m["type"] = s.Type
	}
	if s.Defs != nil {
		m["$defs"] = s.Defs
	}
	if s.Properties != nil {
		m["properties"] = s.Properties
	} else {
		m["properties"] = map[string]any{}
	}
	if len(s.Required) > 0 {
		m["required"] = s.Required
	} else {
		m["required"] = []string{}
	}
	if s.AdditionalProperties != nil {
		m["additionalProperties"] = s.AdditionalProperties
	}
	return json.Marshal(m)
}
