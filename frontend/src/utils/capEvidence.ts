// TypeScript mirror of Go's `detect.CapEvidence` (internal/security/detect/signal.go).
//
// This exists for ONE reason: verifying that a scan span still points at the same
// words it pointed at when the scan ran. The backend ships `FindingSpan.snippet`
// as `CapEvidence(rawDescription[byteStart:byteEnd])`, so the frontend must apply
// the IDENTICAL escaping to `description.slice(start, end)` before comparing —
// otherwise every description containing a control or zero-width rune would
// false-negative the comparison and silently lose its highlights.
//
// It is never used to render: marked text always reaches the DOM as the tool's
// own live description via `{{ }}` interpolation.
//
// Go semantics being mirrored, exactly:
//
//   for _, r := range s {                       // iterate by RUNE (code point)
//     if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
//       fmt.Fprintf(&b, "\\u%04x", r)           // lowercase hex, min width 4
//       continue
//     }
//     b.WriteRune(r)
//   }
//   if len([]rune(escaped)) > MaxEvidenceLen {  // RUNE count, not UTF-16 units
//     return string(runes[:MaxEvidenceLen]) + "…"
//   }
//
// `unicode.IsControl` in Go is true only for the Latin-1 control ranges
// (U+0000–U+001F, U+007F–U+009F), which is precisely the Cc general category —
// so `\p{Cc}` is a faithful match, not an approximation. `\p{Cf}` is the format
// category and covers exactly the smuggling runes the escaping is there to
// reveal (U+200B ZWSP, U+200E/F bidi marks, U+2066–2069 isolates, U+FEFF, …).

/** Rune length at which evidence is truncated. Mirrors `detect.MaxEvidenceLen`. */
export const MAX_EVIDENCE_LEN = 200

/** The character Go appends when it truncates. A snippet ending in it is a PREFIX. */
export const EVIDENCE_ELLIPSIS = '…'

// Not global: `.test()` on a /g regex is stateful via lastIndex and would return
// alternating results for the same input.
const ESCAPED_RUNE_RE = /[\p{Cc}\p{Cf}]/u

/** True when Go's CapEvidence would escape this single code point to \uXXXX. */
export function isEscapedRune(codePoint: string): boolean {
  return ESCAPED_RUNE_RE.test(codePoint)
}

/**
 * Render-safe escaping identical to Go's `detect.CapEvidence`.
 *
 * Control and format runes become a visible `\uXXXX` (lowercase, zero-padded to
 * at least four digits — astral format runes such as U+1D173 therefore render as
 * five, matching Go's `%04x` minimum-width semantics), and the result is capped
 * at MAX_EVIDENCE_LEN code points plus an ellipsis.
 */
export function capEvidence(s: string): string {
  let escaped = ''
  // for...of iterates by code point, which is what Go's `range` over a string does.
  for (const ch of s) {
    if (ESCAPED_RUNE_RE.test(ch)) {
      escaped += '\\u' + (ch.codePointAt(0) ?? 0).toString(16).padStart(4, '0')
      continue
    }
    escaped += ch
  }

  const runes = Array.from(escaped)
  if (runes.length > MAX_EVIDENCE_LEN) {
    return runes.slice(0, MAX_EVIDENCE_LEN).join('') + EVIDENCE_ELLIPSIS
  }
  return escaped
}
