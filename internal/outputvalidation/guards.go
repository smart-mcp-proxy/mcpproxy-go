package outputvalidation

// nestingDepth computes the maximum nesting depth of v by walking its structure.
// Scalars (string, number, bool, nil, json.Number) have depth 1.
// A map or slice contributes depth 1 + max(children depths).
// An empty map or slice has depth 1.
func nestingDepth(v any) int {
	switch val := v.(type) {
	case map[string]any:
		if len(val) == 0 {
			return 1
		}
		max := 0
		for _, child := range val {
			if d := nestingDepth(child); d > max {
				max = d
			}
		}
		return 1 + max

	case []any:
		if len(val) == 0 {
			return 1
		}
		max := 0
		for _, child := range val {
			if d := nestingDepth(child); d > max {
				max = d
			}
		}
		return 1 + max

	default:
		// scalar: string, bool, float64, json.Number, nil, int, etc.
		return 1
	}
}
