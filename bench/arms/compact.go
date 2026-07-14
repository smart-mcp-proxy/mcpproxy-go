package arms

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/bench"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// CompactName is the registry key of the compact-signature arm.
const CompactName = "compact_sig"

// compactTypeNames maps JSON Schema primitive type names to the compact
// signature vocabulary (contracts/arm-interface.md / plan.md):
// string/int/number/bool/obj/arr. Nested objects render as bare "obj" — the
// signature grammar `name(param:type, opt?:type)|description` is flat by
// design; nested property expansion would reintroduce the schema bulk the arm
// exists to remove.
var compactTypeNames = map[string]string{
	"string":  "string",
	"integer": "int",
	"number":  "number",
	"boolean": "bool",
	"object":  "obj",
	"array":   "arr",
}

// compactAnyType is the fallback for parameters whose schema declares no
// resolvable primitive type (bare enum, $ref, empty anyOf). Rendering a
// deterministic placeholder keeps the arm total (contract rule 2) without
// inventing type information.
const compactAnyType = "any"

// compactParamSchema is the subset of a property schema the compact renderer
// reads. Everything else (defaults, titles, constraints, nested properties) is
// deliberately dropped — that is the compression.
type compactParamSchema struct {
	Type  json.RawMessage       `json:"type"`
	AnyOf []compactParamVariant `json:"anyOf"`
	OneOf []compactParamVariant `json:"oneOf"`
}

// compactParamVariant is one alternative inside anyOf/oneOf.
type compactParamVariant struct {
	Type string `json:"type"`
}

// compactInputSchema is the subset of a tool input schema the compact renderer
// reads: the top-level parameter map and the required-name list.
type compactInputSchema struct {
	Properties map[string]compactParamSchema `json:"properties"`
	Required   []string                      `json:"required"`
}

// compactType resolves one parameter's compact type name.
//
// Resolution order:
//  1. "type":"<primitive>" → mapped name (unknown primitives → "any").
//  2. "type":[...] (JSON Schema type union) → mapped members minus "null",
//     joined with "|" in declared order ("null" alone → "any"). Nullability is
//     not a distinct compact type: in MCP tool schemas a nullable parameter is
//     just an omittable one, which the required/optional split already encodes.
//  3. anyOf/oneOf of typed variants → same union treatment (this is the
//     Pydantic `Optional[str]` shape corpus_v2 actually contains:
//     anyOf[{string},{null}] → "string").
//  4. Nothing resolvable → "any".
//
// Every branch is order-preserving over JSON arrays and touches no Go map, so
// the result is byte-deterministic (FR-010).
func compactType(p compactParamSchema) string {
	if len(p.Type) > 0 {
		var single string
		if err := json.Unmarshal(p.Type, &single); err == nil {
			if mapped, ok := compactTypeNames[single]; ok {
				return mapped
			}
			return compactAnyType
		}
		var union []string
		if err := json.Unmarshal(p.Type, &union); err == nil {
			return compactUnion(union)
		}
		return compactAnyType
	}
	variants := p.AnyOf
	if len(variants) == 0 {
		variants = p.OneOf
	}
	if len(variants) > 0 {
		names := make([]string, 0, len(variants))
		for _, v := range variants {
			names = append(names, v.Type)
		}
		return compactUnion(names)
	}
	return compactAnyType
}

// compactUnion renders a type union: members mapped, "null" and duplicates
// dropped, declared order preserved, "|"-joined; empty result → "any".
func compactUnion(names []string) string {
	mapped := make([]string, 0, len(names))
	seen := make(map[string]bool, len(names))
	for _, n := range names {
		if n == "null" || n == "" {
			continue
		}
		m, ok := compactTypeNames[n]
		if !ok {
			m = compactAnyType
		}
		if !seen[m] {
			seen[m] = true
			mapped = append(mapped, m)
		}
	}
	if len(mapped) == 0 {
		return compactAnyType
	}
	return strings.Join(mapped, "|")
}

// compactParams renders the signature's parameter list ("url:string,
// max_length?:int") from a tool's input schema: required parameters first,
// bare, in required-array order (deterministic — canonical JSON preserves
// array order); optional parameters after, "?"-suffixed, in sorted name order
// (never map-iteration order). A nil/empty schema renders no parameters. A
// malformed schema is an explicit error, never a truncated signature
// (contract rule 2).
func compactParams(t bench.Tool) (string, error) {
	if len(t.Schema) == 0 {
		return "", nil
	}
	dec := json.NewDecoder(bytes.NewReader(t.Schema))
	var s compactInputSchema
	if err := dec.Decode(&s); err != nil {
		return "", fmt.Errorf("tool %s: parse input schema: %w", t.ToolID, err)
	}
	if err := bench.RequireEOF(dec); err != nil {
		return "", fmt.Errorf("tool %s: parse input schema: %w", t.ToolID, err)
	}

	required := make([]string, 0, len(s.Required))
	isRequired := make(map[string]bool, len(s.Required))
	for _, name := range s.Required {
		if _, declared := s.Properties[name]; !declared || isRequired[name] {
			// A required name with no property declaration carries no type
			// info to render; duplicates in the required array count once.
			continue
		}
		isRequired[name] = true
		required = append(required, name)
	}
	optional := make([]string, 0, len(s.Properties))
	for name := range s.Properties {
		if !isRequired[name] {
			optional = append(optional, name)
		}
	}
	sort.Strings(optional)

	params := make([]string, 0, len(s.Properties))
	for _, name := range required {
		params = append(params, name+":"+compactType(s.Properties[name]))
	}
	for _, name := range optional {
		params = append(params, name+"?:"+compactType(s.Properties[name]))
	}
	return strings.Join(params, ", "), nil
}

// CompactArm is the compact-signature arm: each tool renders as
// `name(param:type, opt?:type)|description` — required parameters bare,
// optional "?"-suffixed, description preserved verbatim. Pre-measured at −92%
// on live retrieve_tools responses with recall unchanged (research D2).
type CompactArm struct{}

// NewCompact returns the compact_sig arm.
func NewCompact() *CompactArm { return &CompactArm{} }

// Name implements Arm.
func (*CompactArm) Name() string { return CompactName }

// IndexAltering implements Arm: the arm replaces the ParamsJSON the retrieval
// index ingests with the compact params text, so retrieval-quality scoring is
// obligatory (FR-008).
func (*CompactArm) IndexAltering() bool { return true }

// LowerBound implements Arm: descriptions are preserved verbatim, so the
// measured savings are exact, not a lower bound.
func (*CompactArm) LowerBound() bool { return false }

// EncodeTool implements Arm.
func (a *CompactArm) EncodeTool(t bench.Tool) (string, error) {
	params, err := compactParams(t)
	if err != nil {
		return "", err
	}
	return t.Name + "(" + params + ")|" + t.Description, nil
}

// EncodeListing implements Arm: per-tool signatures joined by the shared
// listing separator (no preamble to amortize), so listing totals decompose
// exactly into per-tool costs.
func (a *CompactArm) EncodeListing(ts []bench.Tool) (string, error) {
	parts := make([]string, 0, len(ts))
	for _, t := range ts {
		enc, err := a.EncodeTool(t)
		if err != nil {
			return "", err
		}
		parts = append(parts, enc)
	}
	return strings.Join(parts, listingSeparator), nil
}

// EncodeIndexMetadata implements Arm: Name, ServerName, and Description are
// ingested unchanged; ParamsJSON is replaced by the compact params text (the
// parenthesized portion of the signature) — the exact parameter text the
// index sees under this arm.
func (*CompactArm) EncodeIndexMetadata(t bench.Tool) (config.ToolMetadata, error) {
	params, err := compactParams(t)
	if err != nil {
		return config.ToolMetadata{}, err
	}
	return config.ToolMetadata{
		Name:        t.Name,
		ServerName:  t.Server,
		Description: t.Description,
		ParamsJSON:  params,
	}, nil
}

func init() {
	MustRegister(NewCompact())
}
