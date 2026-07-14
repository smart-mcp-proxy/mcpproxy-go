package toonenc

import (
	"sort"

	toon "github.com/toon-format/toon-go"
)

// canonicalToon rewrites a json.Number-decoded value into an ordered
// representation whose TOON marshaling is a pure function of the value
// (FR-011): every map[string]interface{} becomes a toon.Object with fields
// sorted by key, recursively; arrays recurse element-wise; scalars, null, and
// json.Number pass through untouched.
//
// This deliberately does NOT rely on toon-go's own map-key handling — Go's
// randomized map iteration must never be able to leak into the output bytes.
// It applies in both modes: always mode encodes arbitrary nested JSON, so it
// needs the recursive ordering just as much as tabular adaptive does.
func canonicalToon(v interface{}) interface{} {
	switch val := v.(type) {
	case map[string]interface{}:
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		fields := make([]toon.Field, 0, len(keys))
		for _, k := range keys {
			fields = append(fields, toon.Field{Key: k, Value: canonicalToon(val[k])})
		}
		return toon.NewObject(fields...)
	case []interface{}:
		out := make([]interface{}, len(val))
		for i, elem := range val {
			out[i] = canonicalToon(elem)
		}
		return out
	default:
		return v
	}
}
