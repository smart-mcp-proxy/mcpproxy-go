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

// TestCapSpanSnippetReportsCoveredBytes pins the contract DescriptionSpan
// relies on: the snippet, how much of the input it speaks for, and whether it
// stopped short.
func TestCapSpanSnippetReportsCoveredBytes(t *testing.T) {
	tests := []struct {
		name          string
		in            string
		wantCov       int
		wantTruncated bool
	}{
		{"short ascii", "reason", len("reason"), false},
		{"multibyte, untruncated", "数据库 reason", len("数据库 reason"), false},
		{"exactly at the cap", strings.Repeat("a", MaxEvidenceLen), MaxEvidenceLen, false},
		// One rune past the cap: the snippet truncates and stops covering it.
		{"one past the cap", strings.Repeat("a", MaxEvidenceLen+1), MaxEvidenceLen, true},
		// Escaping expands, so far fewer SOURCE runes fit under the cap. 6
		// escaped runes per ZWSP => 33 whole ZWSPs, 3 bytes each.
		{"escape expansion", strings.Repeat("\U0000200B", 60), 33 * 3, true},
		// A backslash doubles, costing 2 of the cap, so exactly 100 fit.
		{"backslash expansion", strings.Repeat("\\", 120), 100, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snippet, covered, truncated := CapSpanSnippet(tt.in)
			if covered != tt.wantCov {
				t.Errorf("coveredBytes = %d, want %d", covered, tt.wantCov)
			}
			if truncated != tt.wantTruncated {
				t.Errorf("truncated = %v, want %v", truncated, tt.wantTruncated)
			}
			// The invariant every consumer depends on: the snippet is exactly
			// the encoding of the range it claims to cover — never a value
			// speaking for more text than that, and never carrying a marker of
			// its own that a consumer would have to strip back off.
			whole, _, _ := CapSpanSnippet(tt.in[:covered])
			if whole != snippet {
				t.Errorf("CapSpanSnippet(in[:%d]) = %q, want the snippet %q", covered, whole, snippet)
			}
			if strings.HasSuffix(snippet, "…") {
				t.Errorf("snippet %q carries a truncation marker; that is the flag's job", snippet)
			}
			if covered < len(tt.in) && !truncated {
				t.Errorf("snippet covers only %d/%d bytes but truncated = false", covered, len(tt.in))
			}
		})
	}
}

// TestCapSpanSnippetIsInjective is the C1 regression.
//
// CapEvidence escapes a control rune but not a backslash the text already
// contained, so a real newline followed by six literal characters, and those
// same six literal characters followed by a real vertical tab, produce
// IDENTICAL evidence. Both texts are the same UTF-16 length, so a span recorded
// against one verifies against the other at the same offsets and the UI marks
// characters no rule ever matched. The span snippet has to separate them.
func TestCapSpanSnippetIsInjective(t *testing.T) {
	// A real newline, then the six literal characters backslash u 0 0 0 b.
	scanned := "\n" + "\\u000b"
	// The six literal characters backslash u 0 0 0 a, then a real vertical tab.
	live := "\\u000a" + "\v"

	if CapEvidence(scanned) != CapEvidence(live) {
		t.Fatalf("premise changed: CapEvidence no longer collides on these inputs (%q vs %q) — "+
			"the C1 vectors need rebuilding, not deleting",
			CapEvidence(scanned), CapEvidence(live))
	}
	a, _, _ := CapSpanSnippet(scanned)
	b, _, _ := CapSpanSnippet(live)
	if a == b {
		t.Errorf("CapSpanSnippet collides: %q == %q for inputs %q vs %q", a, b, scanned, live)
	}
}

// TestCapSpanSnippetEscapesBackslash pins the one difference from CapEvidence
// that buys injectivity, in isolation.
func TestCapSpanSnippetEscapesBackslash(t *testing.T) {
	got, _, _ := CapSpanSnippet("a\\b")
	if want := "a\\\\b"; got != want {
		t.Errorf("CapSpanSnippet(%q) = %q, want %q", "a\\b", got, want)
	}
	// CapEvidence is deliberately untouched and still passes a backslash
	// through. Asserting the asymmetry means a well-meaning "fix" over there
	// trips here and has to be argued rather than absorbed.
	if got := CapEvidence("a\\b"); got != "a\\b" {
		t.Errorf("CapEvidence(%q) = %q, want it unchanged", "a\\b", got)
	}
}

// TestCapSpanSnippetTruncationIsDeclaredNotSniffed is the C2 regression: a
// matched passage may legitimately END in an ellipsis with nothing truncated,
// so the marker cannot live in the snippet.
func TestCapSpanSnippetTruncationIsDeclaredNotSniffed(t *testing.T) {
	const in = "read the docs…" // 14 runes, far below the 200-rune cap
	snippet, covered, truncated := CapSpanSnippet(in)
	if truncated {
		t.Errorf("truncated = true for a %d-rune input, want false", len([]rune(in)))
	}
	if covered != len(in) {
		t.Errorf("coveredBytes = %d, want %d", covered, len(in))
	}
	if snippet != in {
		t.Errorf("snippet = %q, want the input unchanged", snippet)
	}
	// The shape that made sniffing unsound: CapEvidence gives this untruncated
	// input exactly the trailing character it gives a truncated one.
	if !strings.HasSuffix(CapEvidence(in), "…") {
		t.Fatal("premise changed: CapEvidence no longer ends this input in an ellipsis")
	}
}

// TestCapSpanSnippetParityVectors is the Go half of the mirror pair. Every case
// here is asserted byte-for-byte by frontend/tests/unit/span-snippet.spec.ts; a
// divergence turns every highlight off on exactly the descriptions this feature
// exists for, so the two lists move together or not at all.
func TestCapSpanSnippetParityVectors(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain ascii", "hello world", "hello world"},
		{"control rune", "a\x00b", "a\\u0000b"},
		{"zero width", "a\U0000200Bb", "a\\u200bb"},
		{"bidi override", "a\U0000202Eb", "a\\u202eb"},
		{"isolate", "a\U00002066b", "a\\u2066b"},
		{"bom", "a\U0000FEFFb", "a\\ufeffb"},
		{"soft hyphen", "a\U000000ADb", "a\\u00adb"},
		{"newline and tab", "a\nb\tc", "a\\u000ab\\u0009c"},
		{"bell", "\a", "\\u0007"},
		// U+1D173 is category Cf and above the BMP, so it takes the FIXED-width
		// 8-digit form — unlike CapEvidence's minimum-width 5. Fixed width is
		// what makes the encoding decodable, and therefore collision-free.
		{"astral format rune", "\U0001D173", "\\U0001d173"},
		{"lone backslash", "\\", "\\\\"},
		// The C1 shape in miniature: a literal backslash-u sequence in the
		// source text must not encode like an escape this function produced.
		{"literal backslash-u text", "\\uffff", "\\\\uffff"},
		{"escaped rune beside a literal backslash", "\U0000200B\\", "\\u200b\\\\"},
		{"emoji passes through", "a\U0001F600b", "a\U0001F600b"},
		{"prose untouched", "For this reason, you can’t add two IAM users. é你好", "For this reason, you can’t add two IAM users. é你好"},
		{"ellipsis is ordinary text", "read the docs…", "read the docs…"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _, truncated := CapSpanSnippet(tt.in)
			if got != tt.want {
				t.Errorf("CapSpanSnippet(%q) = %q, want %q", tt.in, got, tt.want)
			}
			if truncated {
				t.Errorf("truncated = true, want false for %q", tt.in)
			}
		})
	}
}

// TestCapSpanSnippetCountsCodePointsNotUnits guards the cap unit against a
// length-in-UTF-16 regression on either side of the mirror.
func TestCapSpanSnippetCountsCodePointsNotUnits(t *testing.T) {
	// 150 astral emoji: 150 runes but 300 UTF-16 units. The cap is 200 RUNES,
	// so this passes through whole.
	in := strings.Repeat("\U0001F600", 150)
	got, covered, truncated := CapSpanSnippet(in)
	if truncated || covered != len(in) || got != in {
		t.Errorf("CapSpanSnippet(150 emoji) = (covered %d, truncated %v), want the input untouched",
			covered, truncated)
	}
}

// TestDescriptionSpanCarriesTruncationFlag pins the producer end of C2: the
// flag is set when, and only when, the snippet actually stopped short.
func TestDescriptionSpanCarriesTruncationFlag(t *testing.T) {
	t.Run("long match sets it", func(t *testing.T) {
		desc := strings.Repeat("z", 500)
		spans := DescriptionSpan("tpa.demo", TierHard, desc, 0, len(desc))
		if len(spans) != 1 {
			t.Fatalf("DescriptionSpan = %+v, want one span", spans)
		}
		if !spans[0].Truncated {
			t.Error("Truncated = false for a 500-rune match, want true")
		}
		if strings.HasSuffix(spans[0].Snippet, "…") {
			t.Errorf("Snippet %q still carries the old ellipsis marker", spans[0].Snippet)
		}
	})

	t.Run("a match that merely ENDS in an ellipsis does not", func(t *testing.T) {
		// The C2 false positive, at the producer. Nothing was truncated here.
		const desc = "please read the docs…"
		spans := DescriptionSpan("tpa.demo", TierHard, desc, 0, len(desc))
		if len(spans) != 1 {
			t.Fatalf("DescriptionSpan = %+v, want one span", spans)
		}
		if spans[0].Truncated {
			t.Error("Truncated = true for a 21-rune match, want false")
		}
		if spans[0].Snippet != desc {
			t.Errorf("Snippet = %q, want %q", spans[0].Snippet, desc)
		}
	})
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
