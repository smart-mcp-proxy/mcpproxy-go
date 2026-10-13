// TypeScript mirror of Go's `detect.CapSpanSnippet` (internal/security/detect/span.go).
//
// This exists for ONE reason: verifying that a scan span still points at the same
// words it pointed at when the scan ran. The backend ships `FindingSpan.snippet`
// as `CapSpanSnippet(rawDescription[byteStart:byteEnd])`, so the frontend must
// apply the IDENTICAL escaping to `description.slice(start, end)` before
// comparing — otherwise every description containing a control or zero-width rune
// would false-negative the comparison and silently lose its highlights.
//
// It is never used to render: marked text always reaches the DOM as the tool's
// own live description via `{{ }}` interpolation.
//
// It is deliberately NOT a mirror of Go's `detect.CapEvidence`, which used to
// live here. That function escapes a control rune to a six-character `\uXXXX`
// form but leaves a backslash the text already contained alone, so an escaped
// newline and six literal characters an attacker typed by hand encode
// identically. As a checksum that is unsound: a description edited from one form
// to the other keeps the same value, the span still verifies, and the UI marks
// characters no rule matched. CapEvidence stays as it is on the Go side (it
// feeds human-read report evidence), and no mirror of it ships here, because a
// second near-identical escaper one autocomplete away from the verification path
// is how that hole gets reopened.
//
// Go semantics being mirrored, exactly:
//
//   for i, r := range s {                       // iterate by RUNE (code point)
//     piece := spanEscapeRune(r)                // `\\`, `\uXXXX`, `\UXXXXXXXX`, or r
//     n := utf8.RuneCountInString(piece)
//     if emitted+n > MaxEvidenceLen {           // RUNE count, not UTF-16 units
//       return b.String(), i, true              // rune-aligned truncation
//     }
//     b.WriteString(piece)
//     emitted += n
//   }
//   return b.String(), len(s), false
//
// `unicode.IsControl` in Go is true only for the Latin-1 control ranges
// (U+0000–U+001F, U+007F–U+009F), which is precisely the Cc general category —
// so `\p{Cc}` is a faithful match, not an approximation. `\p{Cf}` is the format
// category and covers exactly the smuggling runes the escaping is there to
// reveal (U+200B ZWSP, U+200E/F bidi marks, U+2066–2069 isolates, U+FEFF, …).

/** Rune length at which a snippet is truncated. Mirrors `detect.MaxEvidenceLen`. */
export const MAX_EVIDENCE_LEN = 200

// Not global: `.test()` on a /g regex is stateful via lastIndex and would return
// alternating results for the same input.
const ESCAPED_RUNE_RE = /[\p{Cc}\p{Cf}]/u

/** True when Go would escape this single code point to a `\uXXXX` form. */
export function isEscapedRune(codePoint: string): boolean {
  return ESCAPED_RUNE_RE.test(codePoint)
}

/** What `capSpanSnippet` produced, and whether it stopped short of its input. */
export interface SpanSnippet {
  snippet: string
  /**
   * True when the cap cut the encoding short. The backend ships the same fact
   * as `FindingSpan.truncated`; neither side infers it from the snippet's last
   * character, because a description can legitimately end a matched passage
   * with an ellipsis of its own.
   */
  truncated: boolean
}

/**
 * Render-safe, INJECTIVE escaping identical to Go's `detect.CapSpanSnippet`.
 *
 * Injective because the per-rune encoding is prefix-free: a literal backslash
 * always doubles, so a `\u` / `\U` in the output can only have come from an
 * escape, and the fixed digit count says where that escape ends. Equal snippets
 * therefore imply equal inputs, which is the property a staleness checksum has
 * to have and the one CapEvidence lacked.
 */
export function capSpanSnippet(s: string): SpanSnippet {
  let out = ''
  let emitted = 0
  // for...of iterates by code point, which is what Go's `range` over a string does.
  for (const ch of s) {
    const piece = escapeRune(ch)
    // Code points, not UTF-16 units — an unescaped astral rune costs 1, as it
    // does to Go's utf8.RuneCountInString.
    const n = Array.from(piece).length
    if (emitted + n > MAX_EVIDENCE_LEN) return { snippet: out, truncated: true }
    out += piece
    emitted += n
  }
  return { snippet: out, truncated: false }
}

/** Mirror of Go's `spanEscapeRune`. Both sides are pinned by the same vectors. */
function escapeRune(ch: string): string {
  if (ch === '\\') return '\\\\'
  if (!ESCAPED_RUNE_RE.test(ch)) return ch
  const cp = ch.codePointAt(0) ?? 0
  // Fixed width per plane, matching Go's `%04x` / `%08x` verbs: 4 hex digits
  // below U+10000, 8 above it. Fixed, not minimum, width is what makes the
  // encoding decodable and therefore collision-free.
  return cp > 0xffff
    ? '\\U' + cp.toString(16).padStart(8, '0')
    : '\\u' + cp.toString(16).padStart(4, '0')
}
