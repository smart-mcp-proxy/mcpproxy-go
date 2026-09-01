import { describe, it, expect } from 'vitest'
import { capSpanSnippet, isEscapedRune, MAX_EVIDENCE_LEN } from '@/utils/spanSnippet'

// Phase 1 (TPA inline findings) — `capSpanSnippet` is a TypeScript mirror of
// Go's `detect.CapSpanSnippet` (internal/security/detect/span.go).
//
// It is load-bearing for correctness, not cosmetics: `FindingSpan.snippet` is
// produced by the Go function and compared against the TS output of the live
// description slice. Any divergence silently turns every highlight off on
// exactly the descriptions the feature exists for (the ones smuggling control
// and zero-width runes).
//
// The vectors below are ported ONE FOR ONE from Go's
// TestCapSpanSnippetParityVectors. The two lists move together or not at all.
// Every invisible rune is written as an explicit escape — a literal one in the
// source would be unreviewable — and every EXPECTED value spells its backslash
// as '\\', because these tests are about the difference between a backslash the
// escaper wrote and one the description already contained.

describe('capSpanSnippet — parity with Go detect.CapSpanSnippet', () => {
  const vectors: Array<[name: string, input: string, want: string]> = [
    ['plain ascii', 'hello world', 'hello world'],
    ['control rune', 'a\x00b', 'a\\u0000b'],
    ['zero width', 'a\u{200b}b', 'a\\u200bb'],
    ['bidi override', 'a\u{202e}b', 'a\\u202eb'],
    ['isolate', 'a\u{2066}b', 'a\\u2066b'],
    ['bom', 'a\u{feff}b', 'a\\ufeffb'],
    ['soft hyphen', 'a\u{ad}b', 'a\\u00adb'],
    ['newline and tab', 'a\nb\tc', 'a\\u000ab\\u0009c'],
    ['bell', '\x07', '\\u0007'],
    // U+1D173 is category Cf and above the BMP, so it takes the FIXED-width
    // 8-digit form. Fixed width is what makes the encoding decodable, and
    // therefore collision-free.
    ['astral format rune', '\u{1d173}', '\\U0001d173'],
    ['lone backslash', '\\', '\\\\'],
    // The C1 shape in miniature: a literal backslash-u sequence already in the
    // description must not encode like an escape this function produced.
    ['literal backslash-u text', '\\uffff', '\\\\uffff'],
    ['escaped rune beside a literal backslash', '\u{200b}\\', '\\u200b\\\\'],
    ['emoji passes through', 'a\u{1f600}b', 'a\u{1f600}b'],
    [
      'prose untouched',
      'For this reason, you can’t add two IAM users. é你好',
      'For this reason, you can’t add two IAM users. é你好',
    ],
    ['ellipsis is ordinary text', 'read the docs…', 'read the docs…'],
  ]

  for (const [name, input, want] of vectors) {
    it(name, () => {
      expect(capSpanSnippet(input)).toEqual({ snippet: want, truncated: false })
    })
  }
})

describe('capSpanSnippet — the cap', () => {
  it('does not truncate at exactly MAX_EVIDENCE_LEN runes', () => {
    const exact = 'x'.repeat(MAX_EVIDENCE_LEN)
    expect(capSpanSnippet(exact)).toEqual({ snippet: exact, truncated: false })
  })

  it('truncates one rune past the cap, and says so in the flag', () => {
    const long = 'x'.repeat(MAX_EVIDENCE_LEN + 50)
    const got = capSpanSnippet(long)
    expect(got.truncated).toBe(true)
    expect(Array.from(got.snippet).length).toBe(MAX_EVIDENCE_LEN)
  })

  it('never writes a truncation marker into the snippet itself', () => {
    // C2: the marker used to be a trailing ellipsis, which an ordinary
    // description can produce on its own. It lives in the flag now, nowhere else.
    const got = capSpanSnippet('x'.repeat(MAX_EVIDENCE_LEN + 50))
    expect(got.snippet.endsWith('…')).toBe(false)
    expect(got.snippet).toBe('x'.repeat(MAX_EVIDENCE_LEN))
  })

  it('counts CODE POINTS, not UTF-16 units, when capping', () => {
    // 150 astral emoji = 150 runes but 300 UTF-16 units. Go caps at 200 runes,
    // so this must pass through untouched; a .length-based cap would truncate it.
    const emoji = '\u{1f600}'.repeat(150)
    expect(emoji.length).toBe(300)
    expect(capSpanSnippet(emoji)).toEqual({ snippet: emoji, truncated: false })
  })

  it('counts the ESCAPED form against the cap, as Go does', () => {
    // 100 zero-width spaces escape to 600 characters, well past the cap. The
    // cut is rune-aligned, so it stops at the 33rd whole escape (198 chars)
    // rather than slicing the 34th in half — Go's TestCapSpanSnippet-
    // ReportsCoveredBytes asserts the same 33 from the byte side.
    const got = capSpanSnippet('\u{200b}'.repeat(100))
    expect(got.truncated).toBe(true)
    expect(Array.from(got.snippet).length).toBe(33 * 6)
    expect(33 * 6).toBeLessThanOrEqual(MAX_EVIDENCE_LEN)
    expect(got.snippet).toBe('\\u200b'.repeat(33))
  })

  it('charges a doubled backslash two of the cap, as Go does', () => {
    const got = capSpanSnippet('\\'.repeat(120))
    expect(got.truncated).toBe(true)
    expect(got.snippet).toBe('\\'.repeat(MAX_EVIDENCE_LEN))
  })
})

describe('capSpanSnippet — injectivity (C1 regression)', () => {
  // Go's CapEvidence escapes a control rune to a six-character form but leaves a
  // backslash the description already contained alone, so these two DIFFERENT
  // texts encoded identically. They are also the same UTF-16 length, so a span
  // recorded against the first verified against the second at the same offsets,
  // and the UI marked characters no rule ever matched.
  const scanned = '\n' + '\\u000b' // a real newline, then six literal characters
  const live = '\\u000a' + '\v' // six literal characters, then a real vertical tab

  it('separates an escaped rune from the literal text that spells its escape', () => {
    expect(scanned.length).toBe(live.length)
    expect(capSpanSnippet(scanned).snippet).toBe('\\u000a\\\\u000b')
    expect(capSpanSnippet(live).snippet).toBe('\\\\u000a\\u000b')
    expect(capSpanSnippet(scanned).snippet).not.toBe(capSpanSnippet(live).snippet)
  })

  it('is injective across a spread of adversarial pairs', () => {
    // Every pair here collides under a backslash-blind escaper.
    const pairs: Array<[string, string]> = [
      ['\x00', '\\u0000'],
      ['\u{200b}', '\\u200b'],
      ['a\u{202e}b', 'a\\u202eb'],
      ['\\\x00', '\\\\u0000'],
      ['\u{1d173}', '\\u1d173'],
    ]
    for (const [a, b] of pairs) {
      expect(capSpanSnippet(a).snippet).not.toBe(capSpanSnippet(b).snippet)
    }
  })
})

describe('isEscapedRune', () => {
  it('is true for Cc and Cf runes and false for printable text', () => {
    expect(isEscapedRune('\x00')).toBe(true)
    expect(isEscapedRune('\u{200b}')).toBe(true)
    expect(isEscapedRune('\u{202e}')).toBe(true)
    expect(isEscapedRune('a')).toBe(false)
    expect(isEscapedRune(' ')).toBe(false)
    expect(isEscapedRune('\u{1f600}')).toBe(false)
    // A backslash is escaped by capSpanSnippet but is NOT an invisible rune, so
    // revealInvisibles must keep drawing it as ordinary text.
    expect(isEscapedRune('\\')).toBe(false)
  })

  it('is stateless across repeated calls with the same input', () => {
    // A /g regex would alternate true/false here via lastIndex.
    expect(isEscapedRune('\u{200b}')).toBe(true)
    expect(isEscapedRune('\u{200b}')).toBe(true)
  })
})
