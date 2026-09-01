package detect

import (
	"strings"
	"testing"
	"unicode/utf16"
)

// jsSlice reproduces JavaScript's String.prototype.slice exactly: it encodes s
// to UTF-16 code units, takes the half-open [start,end) range, and decodes back.
// Every offset assertion below is checked against it, so the tests prove the
// contract the Web UI actually relies on (description.slice(start,end)) rather
// than re-implementing Go-side arithmetic and agreeing with itself.
func jsSlice(s string, start, end int) string {
	units := utf16.Encode([]rune(s))
	if start < 0 || end > len(units) || start > end {
		return "<out of range>"
	}
	return string(utf16.Decode(units[start:end]))
}

func TestUTF16Offsets_ASCII(t *testing.T) {
	const s = "call reason now"
	start, end, ok := UTF16Offsets(s, 5, 11)
	if !ok {
		t.Fatalf("UTF16Offsets(%q, 5, 11) ok = false, want true", s)
	}
	if start != 5 || end != 11 {
		t.Errorf("offsets = (%d,%d), want (5,11)", start, end)
	}
	if got := jsSlice(s, start, end); got != "reason" {
		t.Errorf("jsSlice = %q, want %q", got, "reason")
	}
}

func TestUTF16Offsets_MultiByteBMP(t *testing.T) {
	// Every rune before the span is 2 or 3 bytes but exactly ONE UTF-16 unit,
	// so a byte-offset leak would land the span far past the real word.
	tests := []struct {
		name      string
		s         string
		want      string
		wantStart int
		wantEnd   int
	}{
		// "Привет " = 6 Cyrillic runes (2 bytes each) + space = 13 bytes, 7 units.
		{"cyrillic", "Привет reason", "reason", 7, 13},
		// "文書を翻訳 " = 5 Han/kana runes (3 bytes each) + space = 16 bytes, 6 units.
		{"cjk", "文書を翻訳 reason", "reason", 6, 12},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			byteStart := len(tc.s) - len(tc.want)
			start, end, ok := UTF16Offsets(tc.s, byteStart, len(tc.s))
			if !ok {
				t.Fatalf("ok = false, want true")
			}
			if start != tc.wantStart || end != tc.wantEnd {
				t.Errorf("offsets = (%d,%d), want (%d,%d)", start, end, tc.wantStart, tc.wantEnd)
			}
			if got := jsSlice(tc.s, start, end); got != tc.want {
				t.Errorf("jsSlice = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestUTF16Offsets_AstralRuneBeforeSpanCountsTwoUnits(t *testing.T) {
	// U+1F680 ROCKET is 4 bytes in UTF-8 and a SURROGATE PAIR (2 units) in
	// UTF-16. Counting runes instead of units would put the span one unit
	// early and highlight " reaso" instead of "reason".
	const s = "🚀 reason here"
	start, end, ok := UTF16Offsets(s, 5, 11)
	if !ok {
		t.Fatalf("ok = false, want true")
	}
	if start != 3 || end != 9 {
		t.Errorf("offsets = (%d,%d), want (3,9) — rocket is 2 UTF-16 units", start, end)
	}
	if got := jsSlice(s, start, end); got != "reason" {
		t.Errorf("jsSlice = %q, want %q", got, "reason")
	}
}

func TestUTF16Offsets_AstralRuneInsideSpan(t *testing.T) {
	const s = "a 🚀b c"
	// Span covers "🚀b": bytes 2..7 (4-byte rocket + 1-byte 'b').
	start, end, ok := UTF16Offsets(s, 2, 7)
	if !ok {
		t.Fatalf("ok = false, want true")
	}
	if start != 2 || end != 5 {
		t.Errorf("offsets = (%d,%d), want (2,5) — span spends 3 units", start, end)
	}
	if got := jsSlice(s, start, end); got != "🚀b" {
		t.Errorf("jsSlice = %q, want %q", got, "🚀b")
	}
}

func TestUTF16Offsets_WholeStringAndEmptyRange(t *testing.T) {
	const s = "Привет 🚀 reason"
	start, end, ok := UTF16Offsets(s, 0, len(s))
	if !ok {
		t.Fatalf("whole-string range must convert, ok = false")
	}
	if got := jsSlice(s, start, end); got != s {
		t.Errorf("jsSlice = %q, want the whole string %q", got, s)
	}
	// An empty range is in-bounds and on boundaries, so it converts; producers
	// simply must not emit it (the UI drops start >= end). Byte 13 is the start
	// of the rocket, i.e. a real rune boundary.
	es, ee, ok := UTF16Offsets(s, 13, 13)
	if !ok || es != ee {
		t.Errorf("empty range = (%d,%d,%v), want equal offsets and ok=true", es, ee, ok)
	}
}

func TestUTF16Offsets_RejectsInvalidRanges(t *testing.T) {
	const s = "Привет reason"
	tests := []struct {
		name             string
		byteStart, byteE int
	}{
		{"inverted", 11, 5},
		{"negative start", -1, 5},
		{"end past length", 0, len(s) + 1},
		{"start past length", len(s) + 1, len(s) + 2},
		// Byte 1 is a continuation byte of "П" — slicing there in Go would
		// produce mojibake, so the conversion must refuse rather than emit
		// offsets that point at the wrong characters.
		{"start mid-rune", 1, 12},
		{"end mid-rune", 0, 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if start, end, ok := UTF16Offsets(s, tc.byteStart, tc.byteE); ok {
				t.Errorf("ok = true (%d,%d), want false for %s", start, end, tc.name)
			}
		})
	}
}

// TestCapEvidenceSpanReportsCoveredBytes pins the contract DescriptionSpan
// relies on: the snippet, and how much of the input it speaks for.
func TestCapEvidenceSpanReportsCoveredBytes(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantCov int
	}{
		{"short ascii", "reason", len("reason")},
		{"multibyte, untruncated", "数据库 reason", len("数据库 reason")},
		{"exactly at the cap", strings.Repeat("a", MaxEvidenceLen), MaxEvidenceLen},
		// One rune past the cap: the snippet truncates and stops covering it.
		{"one past the cap", strings.Repeat("a", MaxEvidenceLen+1), MaxEvidenceLen},
		// Escaping expands, so far fewer SOURCE runes fit under the cap. 6
		// escaped runes per ZWSP => 33 whole ZWSPs, 3 bytes each.
		{"escape expansion", strings.Repeat("\u200b", 60), 33 * 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snippet, covered := CapEvidenceSpan(tt.in)
			if covered != tt.wantCov {
				t.Errorf("coveredBytes = %d, want %d", covered, tt.wantCov)
			}
			// The invariant every consumer depends on: the snippet is exactly
			// CapEvidence of the range it claims to cover (plus the ellipsis when
			// it stopped short), never a value spanning more text than that.
			body := strings.TrimSuffix(snippet, "…")
			if got := CapEvidence(tt.in[:covered]); got != body {
				t.Errorf("CapEvidence(in[:%d]) = %q, want snippet body %q", covered, got, body)
			}
			if covered < len(tt.in) && !strings.HasSuffix(snippet, "…") {
				t.Errorf("snippet %q covers only %d/%d bytes but is not marked truncated", snippet, covered, len(tt.in))
			}
		})
	}
}

// TestDescriptionSpanClampsToVerifiedText is the unit-level twin of the bundle
// regression: a match longer than the snippet can pin down is clamped, never
// emitted whole with a prefix-only checksum.
func TestDescriptionSpanClampsToVerifiedText(t *testing.T) {
	desc := "lead-in " + strings.Repeat("z", 500) + " trailer"
	spans := DescriptionSpan("tpa.demo", TierHard, desc, 0, len(desc))
	if len(spans) != 1 {
		t.Fatalf("DescriptionSpan = %+v, want one span", spans)
	}
	sp := spans[0]
	if sp.End-sp.Start > MaxEvidenceLen {
		t.Errorf("span covers %d units, want <= %d", sp.End-sp.Start, MaxEvidenceLen)
	}
	if sp.Tier != TierHard.String() {
		t.Errorf("Tier = %q, want hard", sp.Tier)
	}
	if sp.Start != 0 {
		t.Errorf("Start = %d, want 0", sp.Start)
	}
}

// TestDescriptionSpanRejectsImpossibleRanges keeps the "no span beats a wrong
// span" rule at the only place both producers now share.
func TestDescriptionSpanRejectsImpossibleRanges(t *testing.T) {
	const desc = "数据库 reason"
	cases := []struct {
		name       string
		start, end int
	}{
		{"inverted", 5, 2},
		{"empty", 3, 3},
		{"past the end", 0, len(desc) + 1},
		{"negative", -1, 4},
		{"mid-rune start", 1, len(desc)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := DescriptionSpan("x", TierSoft, desc, c.start, c.end); got != nil {
				t.Errorf("DescriptionSpan(%d,%d) = %+v, want nil", c.start, c.end, got)
			}
		})
	}
}
