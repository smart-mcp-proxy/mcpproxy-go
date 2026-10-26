package registries

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

// PulseMCP descriptions are upstream-authored free text, so a non-Latin
// description longer than the 300-byte display cap put multi-byte runes
// astride the cut. The raw byte slice left ServerEntry.Description invalid
// UTF-8, which the REST/Web UI JSON encoder rewrites to U+FFFD (round 3 of
// #1647/#1664/#1665).
//
// The 1-byte padding covers all three offsets inside a 3-byte rune.
func TestPulseDescriptionTruncationIsValidUTF8(t *testing.T) {
	for _, field := range []string{"short_description", "EXPERIMENTAL_ai_generated_description"} {
		for pad := 0; pad < 3; pad++ {
			desc := strings.Repeat("a", pad) + strings.Repeat("語", 200)
			payload := fmt.Sprintf(`{"servers":[{"name":"srv",%q:%q}]}`, field, desc)

			var raw map[string]interface{}
			if err := json.Unmarshal([]byte(payload), &raw); err != nil {
				t.Fatalf("fixture is not JSON: %v", err)
			}
			entries := parsePulseWithoutGuesser(raw)
			if len(entries) != 1 {
				t.Fatalf("%s pad=%d: parsed %d entries, want 1", field, pad, len(entries))
			}
			got := entries[0].Description
			if len(got) > 300 {
				t.Errorf("%s pad=%d: description is %d bytes, over the 300-byte cap", field, pad, len(got))
			}
			if !utf8.ValidString(got) {
				t.Errorf("%s pad=%d: description is not valid UTF-8 (cut inside a rune)", field, pad)
			}
		}
	}
}
