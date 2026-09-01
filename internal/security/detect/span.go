package detect

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// SpanField names the raw tool text field a Span is measured against. A span is
// always relative to ONE field: checks that match over a concatenation of
// description + schemas must attribute each hit to the field it fell in and
// subtract that field's origin before emitting.
type SpanField string

const (
	// SpanFieldDescription is ToolView.Description, verbatim.
	SpanFieldDescription SpanField = "description"
	// SpanFieldInputSchema is the raw ToolView.InputSchema JSON text.
	SpanFieldInputSchema SpanField = "input_schema"
	// SpanFieldOutputSchema is the raw ToolView.OutputSchema JSON text.
	SpanFieldOutputSchema SpanField = "output_schema"
)

// MaxSpansPerFinding bounds the payload for pathological descriptions. A
// 4,500-character description tripping a dozen rules must not turn one finding
// into an unbounded array in every scan-report response.
const MaxSpansPerFinding = 32

// Span locates one check's match inside one RAW (un-normalized) tool text field.
//
// Start/End are half-open [Start, End) offsets in UTF-16 CODE UNITS — i.e.
// JavaScript string indices — because the only consumer that renders them is the
// Web UI, where description.slice(Start, End) is then exactly the matched text
// including across surrogate pairs. Producers hold Go BYTE offsets and convert
// them with UTF16Offsets; a span that survives that conversion is provably the
// location of the text the check matched, and one that does not is dropped
// rather than approximated (a mark on the wrong words is worse than no mark).
//
// Snippet is CapSpanSnippet(raw[byteStart:byteEnd]): render-SAFE (control, Cf
// and bidi runes escaped to a visible \uXXXX form, capped at MaxEvidenceLen).
// It is NOT the render source — the UI renders the tool's own LIVE description
// sliced by the offsets, so a description edited since the scan cannot smuggle
// stale text back onto the page. Snippet exists solely as a staleness checksum,
// and it is escaped for the reason tpa_bundle.go already documents: a dot-all
// bundle regex can match bidi/zero-width runes from an attacker-controlled
// description, and the raw span must never land verbatim in a JSON payload.
//
// Being a checksum is exactly why Snippet is NOT CapEvidence: see CapSpanSnippet
// for why that function's escaping is ambiguous and what an ambiguous checksum
// costs here.
//
// Truncated says, explicitly, that Snippet stopped short of the matched text —
// it is never inferred from the snippet's last character. An ordinary tool
// description can legitimately end a matched passage with an ellipsis, so a
// consumer sniffing for one would prefix-compare a snippet that was never
// truncated and accept any later description sharing the prefix. The flag is
// the declaration; the snippet carries no marker at all.
//
// CheckID and Tier are carried PER SPAN because aggregate() emits exactly one
// Finding per tool: the finding-level RuleID/ThreatLevel describe the primary
// signal only, so per-mark labelling has to come from the span itself.
type Span struct {
	Field     SpanField `json:"field"`
	Start     int       `json:"start"`
	End       int       `json:"end"`
	CheckID   string    `json:"check_id"`
	Tier      string    `json:"tier"` // "hard" | "soft" — per-span severity
	Snippet   string    `json:"snippet,omitempty"`
	Truncated bool      `json:"truncated,omitempty"`
}

// valid reports whether a span is structurally well-formed: a field the render
// side knows, and a non-empty, non-negative, non-inverted range.
//
// It deliberately does NOT check the offsets against the text, because the text
// is long gone at the one seam that enforces this — unionSpans merges spans out
// of every signal, with no ToolView in hand. DescriptionSpan remains where the
// real guarantee is made (bounds, rune alignment, snippet coverage); this is the
// cheap structural backstop at the single choke point every span passes through
// on its way into a JSON payload, and it exists because Span is an exported
// struct that any future check in a sibling package can fill in by hand.
func (s Span) valid() bool {
	switch s.Field {
	case SpanFieldDescription, SpanFieldInputSchema, SpanFieldOutputSchema:
	default:
		return false
	}
	return s.Start >= 0 && s.Start < s.End
}

// UTF16Offsets converts a byte range in s to UTF-16 code-unit offsets.
//
// ok=false when the range is inverted, out of bounds, or does not start and end
// on rune boundaries — all three would otherwise yield offsets pointing at
// characters the check never matched. Callers MUST drop the span on !ok; there
// is deliberately no best-effort fallback.
//
// An empty in-bounds range (byteStart == byteEnd) converts successfully; it is
// a legal conversion, and producers simply do not emit zero-width spans.
func UTF16Offsets(s string, byteStart, byteEnd int) (start, end int, ok bool) {
	if byteStart < 0 || byteEnd < byteStart || byteEnd > len(s) {
		return 0, 0, false
	}
	if !onRuneBoundary(s, byteStart) || !onRuneBoundary(s, byteEnd) {
		return 0, 0, false
	}
	start = utf16Len(s[:byteStart])
	return start, start + utf16Len(s[byteStart:byteEnd]), true
}

// onRuneBoundary reports whether index i splits s between runes. len(s) is a
// boundary; any other index must not sit on a UTF-8 continuation byte.
func onRuneBoundary(s string, i int) bool {
	return i == len(s) || utf8.RuneStart(s[i])
}

// utf16Len counts the UTF-16 code units s encodes to: one per BMP rune, two per
// astral rune (surrogate pair). Bytes that are not valid UTF-8 decode to
// RuneError one byte at a time and count as one unit each, which matches what a
// JSON decoder hands the browser for the same input.
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r > 0xFFFF {
			n += 2
			continue
		}
		n++
	}
	return n
}

// CapSpanSnippet encodes s for use as Span.Snippet, and reports how much of s
// the result covers and whether it stopped short.
//
// It is NOT CapEvidence, and the difference is the whole point. CapEvidence
// escapes a control or Cf rune to a six-character `\uXXXX` form but does NOT
// escape a backslash the text already contained, so an escaped rune and the six
// literal characters spelling that same escape, typed into a description by
// hand, encode identically. That ambiguity is harmless for Signal.Evidence,
// which a human reads in a report, and fatal here, because Snippet is the ONLY
// staleness check the frontend has: a description edited from one form to the
// other keeps its checksum, the span still "verifies", and the UI confidently
// marks characters no rule matched — the "a mark on the wrong words is worse
// than no mark" failure this package exists to avoid. CapEvidence itself is
// deliberately untouched: it feeds user-facing evidence throughout the package
// and its exact output is pinned by existing tests.
//
// The encoding here is injective because it is PREFIX-FREE per rune: a literal
// backslash always doubles, so a `\u` or `\U` in the output can only have come
// from an escape, and the fixed digit count (4 below U+10000, 8 above it) says
// where that escape ends. Equal snippets therefore imply equal inputs — no
// argument about which code points happen to be escapable is needed, which
// matters because that set grows with each Unicode revision. CapEvidence's
// minimum-width `%04x` is not fixed-width and cannot make that promise.
//
// The render-safety property CapEvidence was originally chosen for is kept
// intact: control, zero-width and bidi runes still never land verbatim in the
// JSON payload. Doubling a backslash costs nothing there — it is printable
// ASCII either way.
//
// coveredBytes exists because a snippet is a checksum, not a quote. The cap is
// MaxEvidenceLen runes, and the bundle's dot-all `<important>…` rules routinely
// match far more, so a snippet that verified only a 200-rune PREFIX would say
// nothing whatsoever about the rest of the range: edit only the tail of such a
// description after the scan and the span still verifies. Producers therefore
// CLAMP the span to coveredBytes, so a mark can never extend past text the
// snippet actually pins down.
//
// The truncation point is rune-aligned, unlike CapEvidence's, which slices the
// escaped string and can cut through a `\uXXXX` escape. Alignment is required
// here and not there: the frontend re-escapes the LIVE slice of the clamped
// range and compares, so a snippet ending in half an escape sequence would never
// match and would silently lose the highlight.
//
// truncated is RETURNED rather than written into the snippet as a trailing
// ellipsis. See Span.Truncated for why sniffing for one is unsound.
func CapSpanSnippet(s string) (snippet string, coveredBytes int, truncated bool) {
	var b strings.Builder
	b.Grow(len(s))
	emitted := 0 // escaped-rune count, mirroring CapEvidence's cap unit
	for i, r := range s {
		piece := spanEscapeRune(r)
		n := utf8.RuneCountInString(piece)
		if emitted+n > MaxEvidenceLen {
			return b.String(), i, true
		}
		b.WriteString(piece)
		emitted += n
	}
	return b.String(), len(s), false
}

// spanEscapeRune is the per-rune half of CapSpanSnippet's encoding, and the
// mirror of escapeRune() in frontend/src/utils/spanSnippet.ts. The two are a
// pair: any divergence turns every highlight off on exactly the descriptions
// this feature exists for, so both sides are pinned by the same vectors
// (TestCapSpanSnippetParityVectors / span-snippet.spec.ts).
func spanEscapeRune(r rune) string {
	switch {
	case r == '\\':
		return `\\`
	case unicode.IsControl(r) || unicode.Is(unicode.Cf, r):
		if r > 0xFFFF {
			return fmt.Sprintf(`\U%08x`, r)
		}
		return fmt.Sprintf(`\u%04x`, r)
	default:
		return string(r)
	}
}

// DescriptionSpan builds the highlight span for a check that matched
// description[byteStart:byteEnd], or nil when no honest span can be produced.
//
// Two ways it returns nil, both deliberate: a byte range that will not convert
// exactly to UTF-16 (see UTF16Offsets), and a range the snippet cannot cover at
// all. Everything else is clamped to the snippet's extent rather than trimmed
// by the caller, so every producer gets the same guarantee — the marked range is
// exactly the range the checksum verifies.
func DescriptionSpan(checkID string, tier Tier, description string, byteStart, byteEnd int) []Span {
	if byteStart < 0 || byteEnd > len(description) || byteStart >= byteEnd {
		return nil
	}
	snippet, covered, truncated := CapSpanSnippet(description[byteStart:byteEnd])
	if covered <= 0 {
		return nil
	}
	start, end, ok := UTF16Offsets(description, byteStart, byteStart+covered)
	if !ok {
		return nil
	}
	return []Span{{
		Field:     SpanFieldDescription,
		Start:     start,
		End:       end,
		CheckID:   checkID,
		Tier:      tier.String(),
		Snippet:   snippet,
		Truncated: truncated,
	}}
}
