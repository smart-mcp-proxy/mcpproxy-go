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
    const label = formatSessionLabel({
      sessionId: 'abc-def-139c9',
      clientName: 'claude-code',
      startTime,
      ambiguous: true,
    })
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
})
