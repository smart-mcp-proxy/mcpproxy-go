package runtime

import (
	"encoding/json"
	"reflect"
	"sort"
)

// inputSchemaSerializationEquivalent reports whether two input-schema JSON
// documents describe the SAME contract and differ only in serialization (RT-01,
// red-team 2026-10-11). It is the precondition for the change gate's
// description-match fallback: a schema change that keeps the description
// byte-identical is only auto-approved when nothing about the contract moved.
//
// Equivalent means: both documents parse, and after canonicalization they are
// deeply equal — same property set, same per-property types and constraints
// (including nested schemas and every string, such as field descriptions), same
// required set. Canonicalization only absorbs differences that carry no meaning
// and that normalizeJSON (key order, whitespace) does not already absorb:
//
//   - the order of a "required" list (it is a set in JSON Schema);
//   - an empty "required" list versus an absent one.
//
// Both are absorbed ONLY where "required" is the JSON Schema keyword of a
// schema node. The walk is schema-aware (canonicalizeSchemaNode): literal JSON
// data — the value of const, enum, default, examples — and any keyword it does
// not know are compared byte-for-byte after normalization, so a "required"
// member inside a literal value is data, never a set (Sol r1 P1).
//
// Anything else — an added/removed/renamed property, a changed type, enum,
// format, default, nested schema or field description — is a structural change
// and returns false, so the caller holds the tool for review. A document that
// does not parse is never equivalent (fail closed).
func inputSchemaSerializationEquivalent(stored, current string) bool {
	if stored == current {
		return true
	}
	var a, b interface{}
	if err := json.Unmarshal([]byte(stored), &a); err != nil {
		return false
	}
	if err := json.Unmarshal([]byte(current), &b); err != nil {
		return false
	}
	return reflect.DeepEqual(canonicalizeSchemaNode(a), canonicalizeSchemaNode(b))
}

// Keywords whose value is a single subschema.
var schemaValuedKeywords = map[string]bool{
	"items": true, "additionalItems": true, "additionalProperties": true,
	"unevaluatedItems": true, "unevaluatedProperties": true,
	"contains": true, "propertyNames": true, "not": true,
	"if": true, "then": true, "else": true, "contentSchema": true,
}

// Keywords whose value is an array of subschemas (items may also be an
// array in draft-04..2019-09 tuple form; it is handled as either).
var schemaArrayKeywords = map[string]bool{
	"allOf": true, "anyOf": true, "oneOf": true, "prefixItems": true,
}

// Keywords whose value maps arbitrary names to subschemas. The NAMES are
// data (a property may be called "required"); only the values are schemas.
var schemaMapKeywords = map[string]bool{
	"properties": true, "patternProperties": true, "$defs": true,
	"definitions": true, "dependentSchemas": true,
}

// canonicalizeSchemaNode returns a copy of a parsed JSON Schema node with
// serialization-only variance removed (see inputSchemaSerializationEquivalent).
// It descends only through positions that hold subschemas; every other value
// (const/enum/default/examples literals, unknown or vendor keywords) is
// returned unchanged, so it is compared exactly.
func canonicalizeSchemaNode(node interface{}) interface{} {
	obj, ok := node.(map[string]interface{})
	if !ok {
		return node // boolean schema or not a schema: compare as-is
	}
	out := make(map[string]interface{}, len(obj))
	for key, child := range obj {
		switch {
		case key == "required":
			if names, ok := stringSet(child); ok {
				if len(names) == 0 {
					continue // [] and absent are the same constraint
				}
				out[key] = names
				continue
			}
			out[key] = child
		case schemaValuedKeywords[key]:
			if arr, isArr := child.([]interface{}); isArr && key == "items" {
				out[key] = canonicalizeSchemaArray(arr)
				continue
			}
			out[key] = canonicalizeSchemaNode(child)
		case schemaArrayKeywords[key]:
			if arr, isArr := child.([]interface{}); isArr {
				out[key] = canonicalizeSchemaArray(arr)
				continue
			}
			out[key] = child
		case schemaMapKeywords[key]:
			if m, isMap := child.(map[string]interface{}); isMap {
				sub := make(map[string]interface{}, len(m))
				for name, schema := range m {
					sub[name] = canonicalizeSchemaNode(schema)
				}
				out[key] = sub
				continue
			}
			out[key] = child
		default:
			out[key] = child
		}
	}
	return out
}

func canonicalizeSchemaArray(arr []interface{}) []interface{} {
	out := make([]interface{}, len(arr))
	for i, child := range arr {
		out[i] = canonicalizeSchemaNode(child)
	}
	return out
}

// stringSet returns a sorted, de-duplicated copy of a JSON array of strings,
// or ok=false when the value is not an array of strings.
func stringSet(value interface{}) ([]interface{}, bool) {
	arr, ok := value.([]interface{})
	if !ok {
		return nil, false
	}
	names := make([]string, 0, len(arr))
	seen := make(map[string]struct{}, len(arr))
	for _, item := range arr {
		s, ok := item.(string)
		if !ok {
			return nil, false
		}
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		names = append(names, s)
	}
	sort.Strings(names)
	out := make([]interface{}, len(names))
	for i, s := range names {
		out[i] = s
	}
	return out, true
}
