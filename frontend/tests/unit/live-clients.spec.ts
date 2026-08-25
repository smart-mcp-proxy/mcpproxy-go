import { describe, it, expect } from 'vitest'
import { liveClientsFromSessions } from '@/utils/sessionLabel'

/**
 * Audit F10: the Dashboard's "AI Agents" box read `ClientStatus.connected` from
 * `GET /api/v1/connect`, which is content-read-free by design (Spec 075) and so
 * reports `connected: false` for every client, always. The box therefore said
 * "no client connected" while `/sessions` listed a live one on the very same
 * data. Live sessions are the single source of truth for "connected".
 */
describe('liveClientsFromSessions (audit F10)', () => {
  it('derives one row per client from active sessions', () => {
    const clients = liveClientsFromSessions([
      { client_name: 'claude-code', status: 'active', last_activity: '2026-08-25T10:00:00Z' },
      { client_name: 'cursor', status: 'active', last_activity: '2026-08-25T10:05:00Z' },
    ])

    expect(clients.map(c => c.name)).toEqual(['Cursor', 'Claude Code'])
    expect(clients[0].lastActivity).toBe('2026-08-25T10:05:00Z')
  })

  it('ignores closed sessions', () => {
    const clients = liveClientsFromSessions([
      { client_name: 'cursor', status: 'closed', last_activity: '2026-08-25T09:00:00Z' },
    ])
    expect(clients).toEqual([])
  })

  it('collapses several sessions of one client onto its latest activity', () => {
    const clients = liveClientsFromSessions([
      { client_name: 'claude-code', status: 'active', last_activity: '2026-08-25T10:00:00Z' },
      { client_name: 'claude-desktop', status: 'active', last_activity: '2026-08-25T10:30:00Z' },
    ])

    // Both map to "Claude Code" through prettyClientName's substring table.
    expect(clients).toHaveLength(1)
    expect(clients[0]).toMatchObject({
      name: 'Claude Code',
      sessionCount: 2,
      lastActivity: '2026-08-25T10:30:00Z',
    })
  })

  it('drops sessions with no client name rather than showing a blank row', () => {
    const clients = liveClientsFromSessions([
      { status: 'active', last_activity: '2026-08-25T10:00:00Z' },
      { client_name: '   ', status: 'active', last_activity: '2026-08-25T10:00:00Z' },
    ])
    expect(clients).toEqual([])
  })

  it('sorts most recently active first, then by name for ties', () => {
    const clients = liveClientsFromSessions([
      { client_name: 'windsurf', status: 'active', last_activity: '2026-08-25T10:00:00Z' },
      { client_name: 'cursor', status: 'active', last_activity: '2026-08-25T10:00:00Z' },
      { client_name: 'codex', status: 'active', last_activity: '2026-08-25T11:00:00Z' },
    ])
    expect(clients.map(c => c.name)).toEqual(['Codex', 'Cursor', 'Windsurf'])
  })

  it('tolerates a missing or unparseable last_activity', () => {
    const clients = liveClientsFromSessions([
      { client_name: 'cursor', status: 'active' },
      { client_name: 'codex', status: 'active', last_activity: 'not-a-date' },
    ])
    expect(clients.map(c => c.name).sort()).toEqual(['Codex', 'Cursor'])
  })
})
