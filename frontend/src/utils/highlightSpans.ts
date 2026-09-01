// Turning scan spans into renderable segments of a tool description.
//
// The contract with the backend (`detect.Span`, internal/security/detect/span.go):
// `start`/`end` are half-open UTF-16 code-unit offsets into the tool's RAW
// description, so `text.slice(start, end)` is exactly the matched words. Nothing
// here searches the text — a stale span degrades to plain text rather than
// guessing, because a six-character token appearing four times in a 4,500-char
// description makes "find it again" a coin flip that confidently marks innocuous
// prose. Verify, or degrade honestly. There is deliberately NO indexOf fallback.
//
// The segmentation is a BOUNDARY SWEEP, not interval nesting: every span
// start/end goes into one sorted, de-duplicated boundary set and one segment is
// emitted per interval, carrying every span that covers it. Two overlapping
// spans therefore become ONE segment with two sources — never nested <mark>
// elements, never duplicated or dropped characters. The invariant
//
//     segments.map(s => s.text).join('') === text
//
// holds for every input and is asserted by tests/unit/highlight-spans.spec.ts.

import type { FindingSpan } from '@/types/api'
import { capEvidence, isEscapedRune, EVIDENCE_ELLIPSIS } from '@/utils/capEvidence'

/** One verified span, plus the presentation metadata a mark needs. */
export interface SpanSource {
  span: FindingSpan
  /** 1-based marker rendered as the mark's superscript and used in its label. */
  index: number
  /** Finding-level rule id, for the mark's accessible name. */
  ruleId?: string
}

export interface Segment {
  text: string
  /** Every span covering this segment; empty for unmarked text. */
  sources: SpanSource[]
  /** 'dangerous' when any covering span is hard-tier, 'warning' when soft-only. */
  level: 'dangerous' | 'warning' | null
}

/**
 * Upper bound on verified SPANS carried into the render per description. Past
 * this the highlighting stops being an annotation and becomes the noise it was
 * meant to cut through; the remainder is reported as a count instead.
 *
 * It bounds spans, not `<mark>` elements, and the difference is deliberate: the
 * boundary sweep splits overlapping spans, so N spans can render as up to 2N-1
 * marks. Capping marks instead would mean dropping part of a span mid-word,
 * which is exactly the "showed half the evidence" failure the excerpting rules
 * exist to prevent. The rendered mark count therefore stays bounded (≤ 2N-1),
 * just not equal to N.
 */
export const MAX_RENDERED_SPANS = 20

/**
 * Whether a span can still be trusted to point at the words it was recorded
 * against.
 *
 * Phase 1 only marks the description: `input_schema` / `output_schema` spans are
 * real but have no render surface here yet, and marking them against the
 * description text would point at arbitrary prose.
 *
 * When `snippet` is present it is a checksum, not a locator: the backend shipped
 * `CapEvidence(raw[byteStart:byteEnd])`, so the SAME escaping must be applied to
 * the live slice before comparing — otherwise any description carrying a control
 * or zero-width rune (exactly the descriptions this feature exists for) would
 * fail the comparison and lose its highlights. A snippet the backend truncated
 * ends with '…' and can only be compared as a prefix.
 */
export function isSpanUsable(text: string, span: FindingSpan | null | undefined): boolean {
  if (!span) return false
  if (span.field !== 'description') return false
  if (!Number.isInteger(span.start) || !Number.isInteger(span.end)) return false
  if (span.start < 0 || span.end > text.length || span.start >= span.end) return false
  if (!span.snippet) return true

  const actual = capEvidence(text.slice(span.start, span.end))
  if (span.snippet.endsWith(EVIDENCE_ELLIPSIS)) {
    return actual.startsWith(span.snippet.slice(0, -EVIDENCE_ELLIPSIS.length))
  }
  return actual === span.snippet
}

/**
 * Filter to the spans that still verify, de-duplicate identical marks, order
 * them by position, and cap the count.
 *
 * `index` is re-assigned here so the superscript numbering a reader sees runs
 * 1..n in reading order regardless of the order findings arrived in.
 *
 * Two DIFFERENT losses are reported separately, because they mean opposite
 * things to the reader and the caller has to say so:
 *
 *   * `hidden` — verified spans left out by the cap. There is more of the same
 *     kind of evidence, and it is trustworthy.
 *   * `unverified` — description spans whose recorded words are no longer where
 *     they were when the scan ran. Nothing is drawn for these, and the reader
 *     must be told, otherwise a description that changed since the scan renders
 *     as confidently and completely annotated while a finding silently vanishes.
 */
export function usableSources(
  text: string,
  sources: SpanSource[],
): { rendered: SpanSource[]; hidden: number; unverified: number } {
  const usable = sources.filter((source) => isSpanUsable(text, source.span))
  // Schema spans are not "unverified" — they have no render surface here at all,
  // so they are not a staleness signal and must not raise the stale note.
  const unverified = sources.filter(
    (source) => source.span?.field === 'description' && !isSpanUsable(text, source.span),
  ).length

  const seen = new Set<string>()
  const deduped: SpanSource[] = []
  for (const source of usable) {
    const key = `${source.span.start}:${source.span.end}:${source.span.check_id}`
    if (seen.has(key)) continue
    seen.add(key)
    deduped.push(source)
  }

  deduped.sort((a, b) => a.span.start - b.span.start || a.span.end - b.span.end)

  const rendered = deduped.slice(0, MAX_RENDERED_SPANS).map((source, i) => ({
    ...source,
    index: i + 1,
  }))
  return { rendered, hidden: deduped.length - rendered.length, unverified }
}

/**
 * Split `text` into consecutive segments, each carrying the spans covering it.
 *
 * Sources are used as given — call `usableSources()` first to verify and cap
 * them. Unverifiable spans are dropped here too, so a caller that skips that
 * step still cannot produce a mark on unverified text.
 */
export function segmentByFindings(text: string, sources: SpanSource[]): Segment[] {
  const usable = sources.filter((source) => isSpanUsable(text, source.span))
  if (usable.length === 0) {
    // An empty description has no segment at all; the round-trip invariant still
    // holds because ''.join of nothing is ''.
    return text.length === 0 ? [] : [{ text, sources: [], level: null }]
  }

  const boundaries = new Set<number>([0, text.length])
  for (const source of usable) {
    boundaries.add(source.span.start)
    boundaries.add(source.span.end)
  }
  const points = [...boundaries].sort((a, b) => a - b)

  const segments: Segment[] = []
  for (let i = 0; i < points.length - 1; i++) {
    const start = points[i]
    const end = points[i + 1]
    if (start >= end) continue

    // A span covers this interval iff it contains it wholly — which it always
    // either does or does not, because every span edge is itself a boundary.
    const covering = usable.filter((s) => s.span.start <= start && s.span.end >= end)
    segments.push({
      text: text.slice(start, end),
      sources: covering,
      level: segmentLevel(covering),
    })
  }
  return segments
}

function segmentLevel(covering: SpanSource[]): 'dangerous' | 'warning' | null {
  if (covering.length === 0) return null
  return covering.some((s) => s.span.tier === 'hard') ? 'dangerous' : 'warning'
}

// --- Excerpting long descriptions -------------------------------------------

/** Descriptions longer than this collapse to an excerpt around their marks. */
export const LONG_DESCRIPTION_CHARS = 1200

/** Characters of context kept on each side of a marked segment in an excerpt. */
export const EXCERPT_CONTEXT_CHARS = 200

/** A rendered excerpt piece: either text to show, or an elision marker. */
export type ExcerptPart = { kind: 'text'; segment: Segment } | { kind: 'gap' }

/**
 * Move an arbitrary UTF-16 index off the middle of a surrogate pair.
 *
 * Excerpt window edges are plain arithmetic (`start - context`), so they can
 * land between the high and low half of an astral character — an emoji in a
 * description is enough. Slicing there emits a lone surrogate, which the browser
 * paints as U+FFFD, i.e. it CORRUPTS upstream text on the render path of a
 * feature whose entire claim is that it shows the description faithfully.
 * Edges are snapped outward (never inward) so the excerpt can only ever grow by
 * one code unit and no character is dropped.
 */
export function snapOffSurrogate(text: string, index: number, direction: -1 | 1): number {
  if (index <= 0 || index >= text.length) return index
  const code = text.charCodeAt(index)
  // A low surrogate at the index means the index splits a pair.
  if (code >= 0xdc00 && code <= 0xdfff) return index + direction
  return index
}

/**
 * Window a long description down to its marks plus surrounding context.
 *
 * Marked segments are emitted WHOLE and are never trimmed — truncating through a
 * mark would show half of the evidence and hide the rest, which is worse than
 * showing none of it. Only unmarked filler is clipped, and every clip is
 * announced with a gap marker so the reader knows text was elided.
 */
export function excerptSegments(
  segments: Segment[],
  context: number = EXCERPT_CONTEXT_CHARS,
): ExcerptPart[] {
  // The whole description, reassembled — the sweep guarantees the segments join
  // back to it exactly, and surrogate-pair snapping needs to look at code units
  // that may straddle two segments' boundary.
  const text = segments.map((segment) => segment.text).join('')
  // Absolute offsets, plus the windows the marks demand.
  const windows: Array<[number, number]> = []
  let cursor = 0
  const positioned = segments.map((segment) => {
    const start = cursor
    cursor += segment.text.length
    if (segment.level) windows.push([Math.max(0, start - context), cursor + context])
    return { segment, start, end: cursor }
  })
  if (windows.length === 0) return positioned.map(({ segment }) => ({ kind: 'text', segment }))

  windows.sort((a, b) => a[0] - b[0])
  const merged: Array<[number, number]> = [windows[0]]
  for (const [start, end] of windows.slice(1)) {
    const last = merged[merged.length - 1]
    if (start <= last[1]) last[1] = Math.max(last[1], end)
    else merged.push([start, end])
  }

  const parts: ExcerptPart[] = []
  const pushGap = () => {
    if (parts[parts.length - 1]?.kind !== 'gap') parts.push({ kind: 'gap' })
  }

  for (const { segment, start, end } of positioned) {
    if (segment.level) {
      // Always whole — a mark is contained in the window it created.
      parts.push({ kind: 'text', segment })
      continue
    }

    let covered = 0
    for (const [wStart, wEnd] of merged) {
      // Snap OUTWARD off a surrogate pair before clipping: window edges are
      // context arithmetic and know nothing about code points. A clip that lands
      // exactly on the segment's own boundary is left alone — it is a boundary
      // the sweep produced, not one this arithmetic invented, and moving it would
      // desynchronise the `covered` bookkeeping below.
      const rawFrom = Math.max(start, wStart)
      const rawTo = Math.min(end, wEnd)
      const from = rawFrom === start ? rawFrom : snapOffSurrogate(text, rawFrom, -1)
      const to = rawTo === end ? rawTo : snapOffSurrogate(text, rawTo, 1)
      if (from >= to) continue
      if (from > start + covered) pushGap()
      parts.push({
        kind: 'text',
        segment: { text: segment.text.slice(from - start, to - start), sources: [], level: null },
      })
      covered = to - start
    }
    if (covered < end - start) pushGap()
  }

  return parts
}

// --- Revealing invisible runes ----------------------------------------------

/** A run of a segment's text: either printable, or one revealed invisible rune. */
export interface RevealedPart {
  hidden: boolean
  /** Printable text (hidden=false), or the raw rune itself (hidden=true). */
  text: string
  /** Uppercase code point, e.g. "200B". Only meaningful when hidden. */
  hex: string
  /** Human name for the rune, used in the chip's accessible label. */
  name: string
}

const INVISIBLE_NAMES: Record<string, string> = {
  '00AD': 'soft hyphen',
  '200B': 'zero-width space',
  '200C': 'zero-width non-joiner',
  '200D': 'zero-width joiner',
  '200E': 'left-to-right mark',
  '200F': 'right-to-left mark',
  '202A': 'left-to-right embedding',
  '202B': 'right-to-left embedding',
  '202C': 'pop directional formatting',
  '202D': 'left-to-right override',
  '202E': 'right-to-left override',
  '2060': 'word joiner',
  '2066': 'left-to-right isolate',
  '2067': 'right-to-left isolate',
  '2068': 'first strong isolate',
  '2069': 'pop directional isolate',
  FEFF: 'zero-width no-break space',
}

/**
 * Split text into printable runs and individually revealed invisible runes.
 *
 * Without this a `unicode.hidden` highlight has literally nothing to draw: the
 * span points at characters that occupy no pixels. The rune itself is kept in
 * `text` (never dropped — hiding the smuggled character is the attack), while
 * the UI renders the `hex`/`name` chip in its place.
 */
export function revealInvisibles(text: string): RevealedPart[] {
  const parts: RevealedPart[] = []
  let buffer = ''

  const flush = () => {
    if (!buffer) return
    parts.push({ hidden: false, text: buffer, hex: '', name: '' })
    buffer = ''
  }

  for (const ch of text) {
    // Tabs and newlines are legitimate layout in a description and stay as text;
    // everything else Go would have escaped is smuggling and gets a chip.
    if (ch !== '\n' && ch !== '\t' && ch !== '\r' && isEscapedRune(ch)) {
      flush()
      const hex = (ch.codePointAt(0) ?? 0).toString(16).toUpperCase().padStart(4, '0')
      parts.push({ hidden: true, text: ch, hex, name: INVISIBLE_NAMES[hex] ?? 'invisible character' })
      continue
    }
    buffer += ch
  }
  flush()

  return parts
}
