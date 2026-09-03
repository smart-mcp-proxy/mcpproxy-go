package hash

import (
	"encoding/json"
	"sort"
	"testing"
)

// A tool's approval hash (Spec 032) is computed over the LIBRARY-DECODED input
// schema: internal/upstream/core/client.go marshals mcp.Tool.InputSchema, and
// mcp.Tool.RawInputSchema is `json:"-"`, so the bytes the upstream actually sent
// are discarded on decode and cannot be recovered.
//
// That couples a security signal to the library version. Given one draft-07
// schema on the wire, the two decoders disagree about the ref spelling they
// emit (captured by running each version against the same input):
//
//	v0.57.0: {"$defs":{...},"properties":{"filter":{"$ref":"#/definitions/Filter"}},...}
//	v1.0.0:  {"$defs":{...},"properties":{"filter":{"$ref":"#/$defs/Filter"}},...}
//
// v0.57.0 hoisted `definitions` into `$defs` but left the pointers dangling;
// v1.0.0 added rewriteDraft07LocalRefs and fixes them. So a hash stored by the
// old decoder cannot be reproduced by the new one, the formula-migration hatch
// in tool_quarantine.go does not absorb it, and every affected tool is reported
// as "changed" — a rug-pull signal — after an upgrade that changed nothing
// upstream.
//
// The fixtures below are therefore written as the OLD decoder's literal output,
// not produced by decoding. Decoding here would only exercise the currently
// linked library and pass whatever it happens to emit, proving nothing.
const (
	// decodedByV057 is what the v0.57.0 decoder emitted for a draft-07 schema
	// with a `definitions` block: `$defs` hoisted, pointers left dangling.
	decodedByV057 = `{"$defs":{"Filter":{"type":"string"}},"properties":{"filter":{"$ref":"#/definitions/Filter"},"list":{"items":{"$ref":"#/definitions/Filter"},"type":"array"}},"required":[],"type":"object"}`

	// decodedByV100 is what v1.0.0 emits for the same wire input.
	decodedByV100 = `{"$defs":{"Filter":{"type":"string"}},"properties":{"filter":{"$ref":"#/$defs/Filter"},"list":{"items":{"$ref":"#/$defs/Filter"},"type":"array"}},"required":[],"type":"object"}`
)

// The load-bearing assertion: a hash stored under the old decoder must still be
// reproducible under the new one. Without ref canonicalization in NormalizeJSON
// this fails, and every tool whose upstream sends draft-07 local refs is
// re-quarantined on the first start after the library bump.
func TestNormalizeJSONSurvivesDecoderRefRewrite(t *testing.T) {
	old := NormalizeJSON(decodedByV057)
	current := NormalizeJSON(decodedByV100)

	if old != current {
		t.Errorf("a schema hash stored by the pre-bump decoder must survive the bump,\n"+
			"or Spec 032 reports every affected tool as changed after an upgrade that changed nothing\n"+
			"stored:  %s\ncurrent: %s", old, current)
	}
}

// The common case: no refs at all. Canonicalization must not touch it, or the
// normalizer would move every tool's hash the moment it shipped.
func TestNormalizeJSONLeavesPlainSchemaAlone(t *testing.T) {
	plain := `{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}`

	if got, want := NormalizeJSON(plain), `{"properties":{"query":{"type":"string"}},"required":["query"],"type":"object"}`; got != want {
		t.Errorf("a schema with no refs must only have its keys sorted\ngot:  %s\nwant: %s", got, want)
	}
}

// Refs that are not draft-07 local pointers must survive verbatim. Rewriting a
// "#/properties/..." self-reference or an absolute URL would corrupt the schema
// and change what the validator enforces.
func TestNormalizeJSONPreservesNonDraft07Refs(t *testing.T) {
	cases := map[string]string{
		"self reference":  `{"type":"object","properties":{"a":{"$ref":"#/properties/b"},"b":{"type":"string"}}}`,
		"absolute url":    `{"type":"object","properties":{"a":{"$ref":"https://example.com/s.json#/definitions/X"}}}`,
		"already modern":  `{"type":"object","properties":{"a":{"$ref":"#/$defs/X"}},"$defs":{"X":{"type":"string"}}}`,
		"no defs sibling": `{"type":"object","properties":{"a":{"$ref":"#/definitions/Absent"}}}`,

		// The hazard that decided the narrow rule. NormalizeJSON is also applied
		// to a tool's RAW output schema, which reaches the Spec 056 validator
		// (captureOutputSchemaJSON -> OutputSchemaJSON -> applyToolOutputSchemaJSON).
		// Raw bytes keep their `definitions` block, so rewriting the pointer to
		// "#/$defs/" there would aim it at a member that does not exist and break
		// validation for a schema that was correct.
		"definitions block intact": `{"type":"object","properties":{"a":{"$ref":"#/definitions/X"}},"definitions":{"X":{"type":"string"}}}`,
	}

	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			var before, after interface{}
			if err := json.Unmarshal([]byte(raw), &before); err != nil {
				t.Fatalf("fixture is not valid JSON: %v", err)
			}
			normalized := NormalizeJSON(raw)
			if err := json.Unmarshal([]byte(normalized), &after); err != nil {
				t.Fatalf("normalizer produced invalid JSON: %v", err)
			}

			if got, want := findRef(after), findRef(before); got != want {
				t.Errorf("a non-draft-07 $ref must survive normalization unchanged: got %q, want %q", got, want)
			}
		})
	}
}

// A malformed or non-JSON input must still be returned unchanged, the contract
// NormalizeJSON already documents.
func TestNormalizeJSONPassesThroughNonJSON(t *testing.T) {
	for _, raw := range []string{"", "not json", `{"unterminated":`} {
		if got := NormalizeJSON(raw); got != raw {
			t.Errorf("non-JSON input must be returned unchanged: got %q, want %q", got, raw)
		}
	}
}

// findRef returns the first "$ref" value found anywhere in the decoded document,
// walking keys in sorted order so the result is deterministic.
func findRef(node interface{}) string {
	switch v := node.(type) {
	case map[string]interface{}:
		if ref, ok := v["$ref"].(string); ok {
			return ref
		}
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if got := findRef(v[key]); got != "" {
				return got
			}
		}
	case []interface{}:
		for _, item := range v {
			if got := findRef(item); got != "" {
				return got
			}
		}
	}
	return ""
}
