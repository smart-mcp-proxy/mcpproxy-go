import { describe, it, expect } from 'vitest'
import { capEvidence, isEscapedRune, MAX_EVIDENCE_LEN, EVIDENCE_ELLIPSIS } from '@/utils/capEvidence'

// Phase 1 (TPA inline findings) — `capEvidence` is a TypeScript mirror of Go's
// `detect.CapEvidence` (internal/security/detect/signal.go:126-141).
//
// It is load-bearing for correctness, not cosmetics: `FindingSpan.snippet` is
// produced by the Go function and compared against the TS output of the live
// description slice. Any divergence silently turns every highlight off on
// exactly the descriptions the feature exists for (the ones smuggling control
// and zero-width runes).
//
// The first four cases below are ported one-for-one from Go's TestCapEvidence.
// Every invisible rune is written as an explicit \u escape — a literal one in
// the source would be unreviewable.

describe('capEvidence — parity with Go detect.CapEvidence', () => {
  it('leaves plain ASCII untouched (Go: "hello world")', () => {
    expect(capEvidence('hello world')).toBe('hello world')
  })

  it('escapes a control rune to a visible \\uXXXX form (Go: "a\\x00b")', () => {
    expect(capEvidence('a\u0000b')).toBe('a\\u0000b')
  })

  it('escapes a zero-width rune rather than dropping it (Go: "a\\u200bb")', () => {
    // Revealing the smuggling is the entire point — a silent drop would hide it.
    expect(capEvidence('a\u200bb')).toBe('a\\u200bb')
  })

  it('truncates to MaxEvidenceLen runes plus an ellipsis (Go: strings.Repeat)', () => {
    const long = 'x'.repeat(MAX_EVIDENCE_LEN + 50)
    const got = capEvidence(long)
    expect(got.endsWith(EVIDENCE_ELLIPSIS)).toBe(true)
    expect(Array.from(got).length).toBe(MAX_EVIDENCE_LEN + 1)
  })

  it('does not truncate at exactly MaxEvidenceLen runes', () => {
    const exact = 'x'.repeat(MAX_EVIDENCE_LEN)
    expect(capEvidence(exact)).toBe(exact)
  })

  it('counts CODE POINTS, not UTF-16 units, when capping', () => {
    // 150 astral emoji = 150 runes but 300 UTF-16 units. Go caps at 200 runes,
    // so this must pass through untouched; a .length-based cap would truncate it.
    const emoji = '\u{1F600}'.repeat(150)
    expect(emoji.length).toBe(300)
    expect(capEvidence(emoji)).toBe(emoji)
  })

  it('counts the ESCAPED form against the cap, as Go does', () => {
    // 100 zero-width spaces escape to 600 characters, well past the cap.
    const got = capEvidence('\u200b'.repeat(100))
    expect(Array.from(got).length).toBe(MAX_EVIDENCE_LEN + 1)
    expect(got.endsWith(EVIDENCE_ELLIPSIS)).toBe(true)
  })

  it('escapes the bidi override / isolate / BOM family', () => {
    expect(capEvidence('a\u202eb')).toBe('a\\u202eb')
    expect(capEvidence('a\u2066b')).toBe('a\\u2066b')
    expect(capEvidence('a\ufeffb')).toBe('a\\ufeffb')
    expect(capEvidence('a\u00adb')).toBe('a\\u00adb')
  })

  it('escapes newlines and tabs, which are control runes to Go', () => {
    expect(capEvidence('a\nb\tc')).toBe('a\\u000ab\\u0009c')
  })

  it('lowercases hex and pads to at least four digits, like Go %04x', () => {
    expect(capEvidence('\u0007')).toBe('\\u0007')
    // U+1D173 (MUSICAL SYMBOL BEGIN BEAM) is category Cf and above the BMP, so
    // Go's minimum-width-4 verb emits five digits, not four.
    expect(capEvidence('\u{1D173}')).toBe('\\u1d173')
  })

  it('leaves ordinary whitespace, punctuation and non-ASCII letters alone', () => {
    const prose = 'For this reason, you can’t add two IAM users. é你好'
    expect(capEvidence(prose)).toBe(prose)
  })
})

describe('isEscapedRune', () => {
  it('is true for Cc and Cf runes and false for printable text', () => {
    expect(isEscapedRune('\u0000')).toBe(true)
    expect(isEscapedRune('\u200b')).toBe(true)
    expect(isEscapedRune('\u202e')).toBe(true)
    expect(isEscapedRune('a')).toBe(false)
    expect(isEscapedRune(' ')).toBe(false)
    expect(isEscapedRune('\u{1F600}')).toBe(false)
  })

  it('is stateless across repeated calls with the same input', () => {
    // A /g regex would alternate true/false here via lastIndex.
    expect(isEscapedRune('\u200b')).toBe(true)
    expect(isEscapedRune('\u200b')).toBe(true)
  })
})
