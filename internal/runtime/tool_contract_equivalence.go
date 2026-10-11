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

// canonicalizeSchemaNode returns a copy of a parsed JSON value with
// serialization-only variance removed (see inputSchemaSerializationEquivalent).
func canonicalizeSchemaNode(node interface{}) interface{} {
	switch v := node.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(v))
		for key, child := range v {
			if key == "required" {
				if names, ok := stringSet(child); ok {
					if len(names) == 0 {
						continue // [] and absent are the same constraint
					}
					out[key] = names
					continue
				}
			}
			out[key] = canonicalizeSchemaNode(child)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(v))
		for i, child := range v {
			out[i] = canonicalizeSchemaNode(child)
		}
		return out
	default:
		return v
	}
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
