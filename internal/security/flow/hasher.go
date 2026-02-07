package flow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

// HashContent computes a SHA256 hash truncated to 128 bits (32 hex chars).
func HashContent(content string) string {
	h := sha256.Sum256([]byte(content))
	return hex.EncodeToString(h[:16]) // first 16 bytes = 128 bits = 32 hex chars
}

// HashContentNormalized computes a normalized hash: lowercased and trimmed.
// Catches lightly reformatted data (whitespace changes, case changes).
func HashContentNormalized(content string) string {
	normalized := strings.ToLower(strings.TrimSpace(content))
	return HashContent(normalized)
}

// ExtractFieldHashes extracts per-field hashes from JSON content.
// For each string value >= minLength characters, it produces a separate hash.
// For non-JSON content >= minLength, it returns the full content hash.
// Returns a map of hash → true for O(1) lookup.
func ExtractFieldHashes(content string, minLength int) map[string]bool {
	hashes := make(map[string]bool)

	// Try to parse as JSON
	var parsed any
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		// Not JSON — hash the full content if long enough
		if len(content) >= minLength {
			hashes[HashContent(content)] = true
		}
		return hashes
	}

	// Walk the JSON structure and extract string values
	extractStrings(parsed, minLength, hashes)
	return hashes
}

// extractStrings recursively walks a parsed JSON value, hashing all string
// values that meet the minimum length threshold.
func extractStrings(v any, minLength int, hashes map[string]bool) {
	switch val := v.(type) {
	case string:
		if len(val) >= minLength {
			hashes[HashContent(val)] = true
		}
	case map[string]any:
		for _, fieldVal := range val {
			extractStrings(fieldVal, minLength, hashes)
		}
	case []any:
		for _, elem := range val {
			extractStrings(elem, minLength, hashes)
		}
	// Numbers, booleans, nil — skip
	}
}
