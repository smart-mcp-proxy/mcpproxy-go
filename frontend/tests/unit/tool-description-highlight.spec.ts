import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import ToolDescription from '@/components/ToolDescription.vue'
import { capSpanSnippet } from '@/utils/spanSnippet'
import { MAX_RENDERED_SPANS, LONG_DESCRIPTION_CHARS } from '@/utils/highlightSpans'
import type { FindingSpan, SecurityScanFinding } from '@/types/api'

// Phase 1 (TPA inline findings) — ToolDescription renders an ATTACKER-AUTHORED
// string. The description IS the Tool Poisoning Attack payload, so the first
// group of tests is the threat model itself: markup in a description must reach
// the DOM as text, on the marked path and the unmarked path alike. There is no
// v-html anywhere in the component, and tool-description-no-v-html.spec.ts reads
// the source to keep it that way (an ESLint override would enforce nothing —
// `npm run lint` is a no-op echo and eslint.config.cjs is eslintrc-shaped while
// the installed ESLint reads only flat config).
//
// The rest pins the fallback ladder: a description whose spans no longer verify
// degrades to the exact plain paragraph the Available Tools card rendered
// before this feature, plus a note explaining why it is not annotated. The
// description text itself never disappears behind a scan state.

// Pass `text` and the span carries the checksum the backend would really have
// shipped for that range. isSpanUsable refuses a span it cannot verify, so a
// helper defaulting to NO snippet would make these tests assert against the
// rejection path while reading as though they covered the rendered one. An
// explicit `snippet` always wins, so the staleness tests still say what they mean.
function span(overrides: Partial<FindingSpan> = {}, text?: string): FindingSpan {
  const base: FindingSpan = {
    field: 'description',
    start: 0,
    end: 1,
    check_id: 'shadowing.cross_server',
    tier: 'hard',
    ...overrides,
  }
  if (base.snippet === undefined && text !== undefined) {
    const { snippet, truncated } = capSpanSnippet(text.slice(base.start, base.end))
    return { ...base, snippet, truncated }
  }
  return base
}

function finding(spans: FindingSpan[], overrides: Partial<SecurityScanFinding> = {}): SecurityScanFinding {
  return {
    rule_id: 'detect.shadowing.cross_server',
    threat_type: 'tool_poisoning',
    threat_level: 'dangerous',
    title: 'Tool shadowing',
    description: 'description references a cross-server tool',
    spans,
    ...overrides,
  }
}

function mountDescription(description: string, findings: SecurityScanFinding[] = []) {
  return mount(ToolDescription, { props: { description, findings } })
}

/**
 * The description exactly as a sighted reader sees it: everything the component
 * adds for assistive tech (the visually-hidden severity prefix) or for sighted
 * severity encoding (the glyph, the superscript) is aria-hidden or .sr-only and
 * is stripped here. What remains must be the upstream string, byte for byte.
 */
function visibleText(wrapper: ReturnType<typeof mountDescription>): string {
  const el = wrapper.get('[data-test="tool-description-text"]').element.cloneNode(true) as HTMLElement
  el.querySelectorAll('.sr-only, [aria-hidden="true"]').forEach((node) => node.remove())
  return el.textContent ?? ''
}

const MARKUP = '<img src=x onerror=alert(1)> click javascript:alert(2) now'

describe('ToolDescription — attacker-authored text never becomes markup', () => {
  // Only the element tags this component is allowed to build. Anything else in
  // the rendered subtree means an upstream string became markup.
  const ALLOWED_TAGS = new Set(['SPAN', 'MARK', 'SUP', 'CODE'])

  function paragraph(wrapper: ReturnType<typeof mountDescription>): HTMLElement {
    return wrapper.get('[data-test="tool-description-text"]').element as HTMLElement
  }

  function expectNoMarkup(el: HTMLElement) {
    // The payload can only be inert if it produced no elements at all. Attribute
    // VALUES may legitimately quote it (the mark's aria-label repeats the words
    // it marks), and an attribute value is not markup — hence a structural
    // assertion rather than a substring scan of the whole outerHTML.
    expect(el.querySelectorAll('img, script, a, iframe, object, embed, svg')).toHaveLength(0)
    const tags = [...el.querySelectorAll('*')].map((node) => node.tagName)
    expect(tags.filter((tag) => !ALLOWED_TAGS.has(tag))).toEqual([])
  }

  it('renders an <img onerror> payload as TEXT when the description is unmarked', () => {
    const wrapper = mountDescription(MARKUP)
    const el = paragraph(wrapper)
    // innerHTML proves the escaping: the payload is entity-escaped text, not a tag.
    expect(el.innerHTML).toContain('&lt;img src=x onerror=alert(1)&gt;')
    expect(el.innerHTML).not.toContain('<img')
    expectNoMarkup(el)
    expect(wrapper.text()).toContain('<img src=x onerror=alert(1)>')
    expect(wrapper.text()).toContain('javascript:alert(2)')
  })

  it('renders the same payload as TEXT when a span marks it', () => {
    // The marked path is the dangerous one: it builds DOM per segment.
    const s = span({ start: 0, end: 28, snippet: '<img src=x onerror=alert(1)>' })
    const wrapper = mountDescription(MARKUP, [finding([s])])
    const el = paragraph(wrapper)
    expect(wrapper.find('mark').exists()).toBe(true)
    expect(el.innerHTML).toContain('&lt;img src=x onerror=alert(1)&gt;')
    expectNoMarkup(el)
    expect(wrapper.text()).toContain('<img src=x onerror=alert(1)>')
  })

  it('creates no anchor for a javascript: URL inside a mark', () => {
    const start = MARKUP.indexOf('javascript:')
    const s = span({ start, end: start + 'javascript:alert(2)'.length }, MARKUP)
    const wrapper = mountDescription(MARKUP, [finding([s])])
    const el = paragraph(wrapper)
    expect(el.querySelector('a')).toBeNull()
    expect(el.innerHTML).not.toContain('href')
    expect(wrapper.get('mark').text()).toContain('javascript:alert(2)')
  })

  it('never renders the backend snippet or evidence, only the live description', () => {
    const wrapper = mountDescription('safe words here', [
      finding([span({ start: 0, end: 4, snippet: 'safe' })], {
        // A snippet is a checksum and evidence is a report string; neither is a
        // render source. If either were rendered this payload would be on screen.
        evidence: '<script>alert(1)</script>',
      }),
    ])
    expectNoMarkup(paragraph(wrapper))
    expect(wrapper.html()).not.toContain('alert(1)')
    expect(wrapper.text()).not.toContain('alert(1)')
    expect(wrapper.text()).toContain('words here')
  })
})

describe('ToolDescription — marks', () => {
  const text = 'For this reason, you cannot add two IAM users.'

  it('marks exactly the flagged words, in order, and keeps the text intact', () => {
    const wrapper = mountDescription(text, [
      finding([
        span({ start: text.indexOf('reason'), end: text.indexOf('reason') + 6, snippet: 'reason' }),
        span({
          start: text.indexOf('users'),
          end: text.indexOf('users') + 5,
          snippet: 'users',
          check_id: 'tpa.bundle.exfiltration',
        }),
      ]),
    ])
    const marks = wrapper.findAll('mark')
    expect(marks).toHaveLength(2)
    expect(marks[0].text()).toContain('reason')
    expect(marks[1].text()).toContain('users')
    // The paragraph still reads exactly as the raw description does — not
    // "contains", EQUALS: nothing added, nothing lost, nothing reordered.
    expect(visibleText(wrapper)).toBe('For this reason, you cannot add two IAM users.')
  })

  it('renumbers marks in reading order regardless of finding order', () => {
    const wrapper = mountDescription(text, [
      finding([span({ start: text.indexOf('users'), end: text.indexOf('users') + 5, check_id: 'late' }, text)]),
      finding([span({ start: text.indexOf('reason'), end: text.indexOf('reason') + 6, check_id: 'early' }, text)], {
        rule_id: 'detect.early',
      }),
    ])
    const marks = wrapper.findAll('mark')
    expect(marks[0].text()).toContain('reason')
    expect(marks[0].find('sup').text()).toBe('1')
    expect(marks[1].find('sup').text()).toBe('2')
  })

  it('distinguishes severity by decoration and glyph, not colour alone', () => {
    const wrapper = mountDescription(text, [
      finding([span({ start: text.indexOf('reason'), end: text.indexOf('reason') + 6, tier: 'hard', check_id: 'hard' }, text)]),
      finding([span({ start: text.indexOf('users'), end: text.indexOf('users') + 5, tier: 'soft', check_id: 'soft' }, text)], {
        threat_level: 'warning',
      }),
    ])
    const hard = wrapper.get('[data-test="tool-description-mark-dangerous"]')
    const soft = wrapper.get('[data-test="tool-description-mark-warning"]')
    expect(hard.classes()).toContain('decoration-double')
    expect(hard.text()).toContain('▲')
    expect(soft.classes()).toContain('decoration-wavy')
    expect(soft.text()).toContain('▮')
    // The UA default <mark> yellow is illegible on the dark theme and is reset
    // before any tint is applied.
    expect(hard.classes()).toContain('bg-transparent')
  })

  // Regression, twice over. A mark used to be role="button" + tabindex="0" whose
  // accessible name ended "Activate to show the finding" — but nothing listened
  // for the emitted event and no element carried the id it named, so every mark
  // was a keyboard tab-stop that lied, and @keydown.space.prevent additionally
  // swallowed page scrolling in exchange for nothing. And because role="button"
  // is Children Presentational in ARIA, the aria-label REPLACED the flagged
  // words, truncating a 145-char exfiltration payload at 60 characters with no
  // other way to reach the rest.
  it('is emphasis, not a control: no role, no tab stop, no dead activation', () => {
    const wrapper = mountDescription(text, [finding([span({ start: 9, end: 15 }, text)])])
    const mark = wrapper.get('mark')
    expect(mark.attributes('role')).toBeUndefined()
    expect(mark.attributes('tabindex')).toBeUndefined()
    expect(mark.classes()).not.toContain('cursor-pointer')
    expect(Object.keys(wrapper.emitted())).toEqual([])
  })

  it('announces severity and rule WITHOUT hiding or truncating the flagged words', () => {
    const long = 'x'.repeat(20) + 'A'.repeat(140) + 'y'.repeat(20)
    const wrapper = mountDescription(long, [
      finding([span({ start: 20, end: 160, check_id: 'tpa.TPA-2026-0003.hidden_tag' }, long)]),
    ])
    const mark = wrapper.get('mark')
    // No aria-label at all: the accessible name comes from the content, so every
    // flagged character is announced no matter how long the payload is.
    expect(mark.attributes('aria-label')).toBeUndefined()
    expect(mark.text()).toContain('Dangerous finding tpa.TPA-2026-0003.hidden_tag:')
    expect(mark.text()).toContain('A'.repeat(140))
  })

  it('renders an overlap as ONE mark carrying both rules, never nested marks', () => {
    const wrapper = mountDescription(text, [
      finding([
        span({ start: 9, end: 15, check_id: 'a' }, text),
        span({ start: 12, end: 20, check_id: 'b' }, text),
      ]),
    ])
    expect(wrapper.findAll('mark mark')).toHaveLength(0)
    const labels = wrapper.findAll('mark').map((m) => m.text())
    expect(labels.some((l) => l.includes('a, b'))).toBe(true)
  })

  it('reveals a smuggled zero-width rune inside a mark as a visible chip', () => {
    const smuggled = 'call \u200btool now'
    const wrapper = mountDescription(smuggled, [
      finding([span({ start: 5, end: 10, check_id: 'unicode.hidden', snippet: capSpanSnippet('\u200btool').snippet })]),
    ])
    const chip = wrapper.get('[data-test="tool-description-invisible-rune"]')
    expect(chip.text()).toBe('U+200B')
    expect(chip.attributes('title')).toContain('zero-width space')
    // The rune itself is still present — hiding it is the attack.
    expect(wrapper.get('mark').text()).toContain('tool')
  })

  it('caps rendered spans and reports the remainder instead of dropping it silently', () => {
    const long = 'abcdefghij'.repeat(20)
    const spans = Array.from({ length: MAX_RENDERED_SPANS + 3 }, (_, i) =>
      span({ start: i * 5, end: i * 5 + 3, check_id: `c${i}` }, long),
    )
    const wrapper = mountDescription(long, [finding(spans)])
    // Non-overlapping spans render one mark each, so the two counts coincide
    // here. The cap bounds SPANS, not marks — see MAX_RENDERED_SPANS.
    expect(wrapper.findAll('mark')).toHaveLength(MAX_RENDERED_SPANS)
    expect(wrapper.get('[data-test="tool-description-mark-overflow"]').text()).toContain('+3 more')
  })
})

describe('ToolDescription — the fallback ladder', () => {
  const text = 'For this reason, you cannot add two IAM users.'

  it('renders the plain paragraph, with no note, when there are no findings', () => {
    const wrapper = mountDescription(text)
    expect(wrapper.findAll('mark')).toHaveLength(0)
    expect(wrapper.find('[data-test="tool-description-stale-note"]').exists()).toBe(false)
    expect(wrapper.text()).toBe(text)
  })

  it('renders plain text plus a "changed since scan" note when spans no longer verify', () => {
    const stale = span({ start: 0, end: 6, snippet: 'reason' }) // text there is "For th"
    const wrapper = mountDescription(text, [finding([stale])])
    expect(wrapper.findAll('mark')).toHaveLength(0)
    // The description is still fully readable — it never disappears behind a
    // scan state — and the note explains the missing highlights.
    expect(wrapper.get('[data-test="tool-description-text"]').text()).toBe(text)
    expect(wrapper.get('[data-test="tool-description-stale-note"]').text()).toContain(
      'Description changed since the last scan',
    )
  })

  it('does not guess a new location for a stale span whose words moved', () => {
    // "reason" is present at 9..15; an indexOf fallback would relocate the span
    // and mark prose the scanner never flagged.
    const wrapper = mountDescription(text, [
      finding([span({ start: 21, end: 27, snippet: 'reason' })]),
    ])
    expect(wrapper.findAll('mark')).toHaveLength(0)
  })

  it('shows no stale note for a finding that simply carries no spans', () => {
    // A normalized-text check (phrase.injection) has no span in phase 1. That is
    // not staleness and must not be reported as it.
    const wrapper = mountDescription(text, [finding([])])
    expect(wrapper.find('[data-test="tool-description-stale-note"]').exists()).toBe(false)
    expect(wrapper.text()).toBe(text)
  })

  it('shows no stale note when the only spans target a schema field', () => {
    const wrapper = mountDescription(text, [
      finding([span({ field: 'input_schema', start: 0, end: 4 }, text)]),
    ])
    expect(wrapper.find('[data-test="tool-description-stale-note"]').exists()).toBe(false)
    expect(wrapper.findAll('mark')).toHaveLength(0)
  })

  // Regression: the note used to require that ALL spans fail, and the only loss
  // the component reported (`hidden`) is counted after the verification filter.
  // So a tool tripping two checks where one span still verified rendered as a
  // confidently and completely annotated description, while the second finding
  // vanished with no mark, no count and no note — and the flagged list still
  // offered "Show in description" for it.
  it('announces PARTIAL staleness, not only the all-spans-failed case', () => {
    const good = span({ start: 9, end: 15, snippet: capSpanSnippet('reason').snippet, check_id: 'good' })
    const moved = span({ start: 0, end: 6, snippet: 'reason', check_id: 'moved' })
    const wrapper = mountDescription(text, [finding([good]), finding([moved], { rule_id: 'detect.moved' })])

    expect(wrapper.findAll('mark')).toHaveLength(1)
    const note = wrapper.get('[data-test="tool-description-stale-note"]')
    expect(note.text()).toContain('Description changed since the last scan')
    expect(note.text()).toContain('1 flagged passage could not be located')
    // The description itself is untouched, as always.
    expect(visibleText(wrapper)).toBe(text)
  })

  it('pluralises the partial-staleness count', () => {
    const good = span({ start: 9, end: 15, snippet: capSpanSnippet('reason').snippet, check_id: 'good' })
    const movedA = span({ start: 0, end: 6, snippet: 'reason', check_id: 'a' })
    const movedB = span({ start: 1, end: 7, snippet: 'reason', check_id: 'b' })
    const wrapper = mountDescription(text, [finding([good, movedA, movedB])])
    expect(wrapper.get('[data-test="tool-description-stale-note"]').text()).toContain(
      '2 flagged passages could not be located',
    )
  })

  it('falls back to the empty-description copy the tool card used before', () => {
    expect(mountDescription('').text()).toBe('No description available')
  })
})

describe('ToolDescription — long descriptions', () => {
  it('excerpts around the mark and offers the full text, never truncating a mark', () => {
    const filler = 'a'.repeat(LONG_DESCRIPTION_CHARS)
    const description = `${filler}POISONED INSTRUCTION${filler}`
    const wrapper = mountDescription(description, [
      finding([
        span({
          start: filler.length,
          end: filler.length + 'POISONED INSTRUCTION'.length,
          snippet: 'POISONED INSTRUCTION',
        }),
      ]),
    ])

    const mark = wrapper.get('mark')
    // The whole match is on screen — showing half the evidence is worse than
    // showing none of it.
    expect(mark.text()).toContain('POISONED INSTRUCTION')
    const shown = wrapper.get('[data-test="tool-description-text"]').text()
    expect(shown.length).toBeLessThan(description.length)

    expect(wrapper.get('[data-test="tool-description-expand"]').text()).toBe(
      'Show full description',
    )
  })

  it('restores the entire description when the operator expands it', async () => {
    const filler = 'b'.repeat(LONG_DESCRIPTION_CHARS)
    const description = `${filler}FLAGGED${filler}`
    const wrapper = mountDescription(description, [
      finding([span({ start: filler.length, end: filler.length + 7, snippet: 'FLAGGED' })]),
    ])
    await wrapper.get('[data-test="tool-description-expand"]').trigger('click')
    const shown = wrapper.get('[data-test="tool-description-text"]').text()
    expect(shown).toContain(filler)
    expect(shown.length).toBeGreaterThan(description.length - 10)
  })

  it('does not excerpt a long description that has no marks', () => {
    const description = 'c'.repeat(LONG_DESCRIPTION_CHARS + 500)
    const wrapper = mountDescription(description)
    expect(wrapper.find('[data-test="tool-description-expand"]').exists()).toBe(false)
    expect(wrapper.text()).toBe(description)
  })
})
