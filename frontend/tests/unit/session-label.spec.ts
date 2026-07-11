import { describe, it, expect } from 'vitest'
import {
  prettyClientName,
  formatSessionLabel,
  buildSessionLabels,
} from '@/utils/sessionLabel'

describe('prettyClientName', () => {
  it('maps known clients to their display names', () => {
    expect(prettyClientName('claude-code')).toBe('Claude Code')
    expect(prettyClientName('Claude Desktop')).toBe('Claude Code')
    expect(prettyClientName('cursor-vscode')).toBe('Cursor')
    expect(prettyClientName('codex-cli')).toBe('Codex')
    expect(prettyClientName('windsurf')).toBe('Windsurf')
  })

  it('shows an unknown client verbatim rather than hiding it', () => {
    expect(prettyClientName('acme-agent')).toBe('acme-agent')
  })

  it('returns empty string when there is no client name', () => {
    expect(prettyClientName(undefined)).toBe('')
    expect(prettyClientName('   ')).toBe('')
  })
})

describe('formatSessionLabel', () => {
  const startTime = '2026-07-11T14:32:00Z'

  it('renders client name and start time instead of a hash', () => {
    const label = formatSessionLabel({
      sessionId: 'abc-def-139c9',
      clientName: 'claude-code',
      startTime,
    })
    expect(label).toContain('Claude Code')
    expect(label).not.toContain('139c9')
  })

  it('falls back to the id suffix when clientInfo is absent', () => {
    expect(formatSessionLabel({ sessionId: 'abc-def-139c9' })).toBe('...139c9')
  })

  it('disambiguates with the id suffix when the label would collide', () => {
    const label = formatSessionLabel(
      { sessionId: 'abc-def-139c9', clientName: 'claude-code', startTime },
      { ambiguous: true }
    )
    expect(label).toContain('Claude Code')
    expect(label).toContain('139c9')
  })
})

describe('buildSessionLabels', () => {
  it('gives every session a distinct label when two clients collide', () => {
    const labels = buildSessionLabels([
      { sessionId: 'aaaaa-11111', clientName: 'claude-code', startTime: '2026-07-11T14:32:00Z' },
      { sessionId: 'bbbbb-22222', clientName: 'claude-code', startTime: '2026-07-11T14:32:00Z' },
    ])
    const a = labels.get('aaaaa-11111')!
    const b = labels.get('bbbbb-22222')!
    expect(a).not.toBe(b)
    expect(a).toContain('11111')
    expect(b).toContain('22222')
  })

  it('leaves a unique label clean, with no id suffix', () => {
    const labels = buildSessionLabels([
      { sessionId: 'aaaaa-11111', clientName: 'claude-code', startTime: '2026-07-11T14:32:00Z' },
      { sessionId: 'bbbbb-22222', clientName: 'cursor', startTime: '2026-07-11T14:05:00Z' },
    ])
    expect(labels.get('aaaaa-11111')).toContain('Claude Code')
    expect(labels.get('aaaaa-11111')).not.toContain('11111')
    expect(labels.get('bbbbb-22222')).toContain('Cursor')
    expect(labels.get('bbbbb-22222')).not.toContain('22222')
  })

  it('keeps the hash fallback for sessions with no client name', () => {
    const labels = buildSessionLabels([{ sessionId: 'abc-def-139c9' }])
    expect(labels.get('abc-def-139c9')).toBe('...139c9')
  })

  // Regression: a fixed 5-char suffix is not guaranteed unique. Two same-client
  // sessions started in the same minute whose ids END IN THE SAME 5 CHARACTERS
  // used to produce byte-identical labels. Caught in cross-model review.
  it('grows the suffix when the last 5 chars also collide', () => {
    const labels = buildSessionLabels([
      { sessionId: 'sess-a-12345', clientName: 'claude-code', startTime: '2026-07-11T14:32:00Z' },
      { sessionId: 'sess-b-12345', clientName: 'claude-code', startTime: '2026-07-11T14:32:00Z' },
    ])
    const a = labels.get('sess-a-12345')!
    const b = labels.get('sess-b-12345')!
    expect(a).not.toBe(b)
    expect(a).toContain('Claude Code')
    expect(b).toContain('Claude Code')
  })

  // Same trap, on the no-client-name path: those labels are ONLY the id suffix.
  it('grows the suffix for unnamed sessions sharing the last 5 chars', () => {
    const labels = buildSessionLabels([
      { sessionId: 'sess-a-12345' },
      { sessionId: 'sess-b-12345' },
    ])
    expect(labels.get('sess-a-12345')).not.toBe(labels.get('sess-b-12345'))
  })

  it('never emits duplicate labels across a realistic mixed list', () => {
    const labels = buildSessionLabels([
      { sessionId: 's1-aaaaa', clientName: 'claude-code', startTime: '2026-07-11T14:32:00Z' },
      { sessionId: 's2-aaaaa', clientName: 'claude-code', startTime: '2026-07-11T14:32:00Z' },
      { sessionId: 's3-bbbbb', clientName: 'cursor', startTime: '2026-07-11T14:05:00Z' },
      { sessionId: 's4-ccccc' },
      { sessionId: 's5-ccccc' },
    ])
    const all = [...labels.values()]
    expect(new Set(all).size).toBe(all.length)
  })
})

describe('prettyClientName — untrusted input', () => {
  // clientInfo.name is attacker-controllable: any MCP client can send anything
  // at initialize and the core stores it verbatim. Vue escapes it (no XSS), but
  // an unbounded name would still be dumped into a <select> option and wreck the
  // filter layout. Caught in cross-model review.
  it('bounds the length of an unrecognised client name', () => {
    const hostile = 'A'.repeat(10_000)
    const out = prettyClientName(hostile)
    expect(out.length).toBeLessThanOrEqual(32)
    expect(out.endsWith('…')).toBe(true)
  })

  it('collapses newlines and tabs that would break the option label', () => {
    expect(prettyClientName('evil\n\nname\twith\nbreaks')).toBe('evil name with breaks')
  })

  it('does not truncate recognised clients (they map to a short display name)', () => {
    expect(prettyClientName('claude-code-' + 'x'.repeat(500))).toBe('Claude Code')
  })
})
