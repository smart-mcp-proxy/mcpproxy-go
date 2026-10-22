package outputvalidation

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

// A schema-violation Reason is capped at 500 bytes, and jsonschema's message
// quotes the offending instance location — so a non-ASCII property name or
// value puts multi-byte runes astride the cut. The raw byte slice left the
// Reason invalid UTF-8, which JSON encoding rewrites to U+FFFD on the way to
// the agent and the audit line (round 3 of #1647/#1664/#1665).
//
// The 1-byte padding shifts every later byte, so the loop covers all three
// offsets inside a 3-byte rune; two of the three are mid-rune.
func TestViolationReasonIsValidUTF8(t *testing.T) {
	v := New(0, 0, nil)
	for pad := 0; pad < 3; pad++ {
		name := strings.Repeat("a", pad) + strings.Repeat("語", 200)
		schema := fmt.Sprintf(`{"type":"object","properties":{%q:{"type":"integer"}}}`, name)

		verdict := v.Validate(fmt.Sprintf("srv:tool%d", pad), schema, map[string]any{name: "not-an-integer"})
		if !verdict.IsViolation() {
			t.Fatalf("pad=%d: expected a violation", pad)
		}
		if len(verdict.Reason) > 500 {
			t.Errorf("pad=%d: reason is %d bytes, over the 500-byte cap", pad, len(verdict.Reason))
		}
		if !utf8.ValidString(verdict.Reason) {
			t.Errorf("pad=%d: reason is not valid UTF-8 (cut inside a rune)", pad)
		}
	}
}

// ASCII behaviour must not move.
func TestTruncateASCIIUnchanged(t *testing.T) {
	if got, want := truncate(strings.Repeat("x", 10), 20), strings.Repeat("x", 10); got != want {
		t.Errorf("under the cap: got %q, want %q", got, want)
	}
	if got, want := truncate(strings.Repeat("x", 30), 20), strings.Repeat("x", 17)+"..."; got != want {
		t.Errorf("over the cap: got %q, want %q", got, want)
	}
}
