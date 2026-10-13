import { describe, expect, it } from 'vitest'
import { bindingText, durationText, expiringSoon, issuerText, leaseText, stateBadge } from '@/utils/credentials'

// Spec 115 T060: the words the Web UI uses for worker credentials.
const NOW = Date.parse('2026-10-10T12:00:00Z')
const at = (offsetMin: number) => new Date(NOW + offsetMin * 60000).toISOString()

describe('credential lifecycle copy', () => {
  it('lease text: running, ended, and not a lease', () => {
    expect(leaseText({ lease: true, expires_at: at(45) }, NOW)).toBe('Lease ends in 45 min')
    expect(leaseText({ lease: true, expires_at: at(50) }, NOW)).toBe('Lease ends in 50 min')
    expect(leaseText({ lease: true, expires_at: at(125) }, NOW)).toBe('Lease ends in 2 h 5 min')
    expect(leaseText({ lease: true, expires_at: at(-1) }, NOW)).toBe('Lease ended')
    expect(leaseText({ lease: false, expires_at: at(45) }, NOW)).toBe('')
    // fallback from created/expires when the server omits `lease`
    expect(leaseText({ created_at: at(-10), expires_at: at(20) }, NOW)).toBe('Lease ends in 20 min')
    expect(leaseText({ created_at: at(-60 * 24 * 30), expires_at: at(20) }, NOW)).toBe('')
  })

  it('issuer text names the surface and the actor kind for MCP', () => {
    expect(issuerText({ actor_kind: 'api_key', surface: 'mcp' })).toBe('via MCP · api_key')
    expect(issuerText({ actor_kind: 'socket', surface: 'web' })).toBe('via Web UI')
    expect(issuerText({ actor_kind: 'api_key', surface: 'cli' })).toBe('via CLI')
    expect(issuerText({ actor_kind: 'api_key', surface: 'api' })).toBe('via REST')
    expect(issuerText(null)).toBe('')
  })

  it('binding text: locked, switchable, pinned', () => {
    expect(bindingText('client', 'daily-research', 'locked')).toBe('Locked to daily-research')
    expect(bindingText('client', 'daily-research', 'switchable')).toBe('Switchable from daily-research')
    expect(bindingText('token', 'daily-research')).toBe('Pinned to daily-research')
    expect(bindingText('token', '')).toBe('')
  })

  it('state badge: revoked wins over expired; an ended lease is an outcome', () => {
    expect(stateBadge({ revoked: true, revoked_at: at(-5), expires_at: at(-1) }, NOW).label).toBe('Revoked')
    expect(stateBadge({ revoked: true, revoked_at: at(-5) }, NOW).detail).toMatch(/^Revoked \d{4}-\d{2}-\d{2}/)
    expect(stateBadge({ credential_state: 'revoked' }, NOW).label).toBe('Revoked')
    expect(stateBadge({ lease: true, expires_at: at(-1) }, NOW)).toEqual({ label: 'Lease ended', tone: 'neutral', detail: 'Lease ended' })
    expect(stateBadge({ lease: false, expires_at: at(-1) }, NOW).label).toBe('Expired')
    expect(stateBadge({ lease: true, expires_at: at(30) }, NOW).label).toBe('Active')
  })

  it('a lease is never "expiring soon"; a long credential keeps the 72 h warning (UI-005, T069)', () => {
    expect(expiringSoon({ lease: true, expires_at: at(10) }, NOW)).toBe(false)
    expect(expiringSoon({ lease: false, expires_at: at(60 * 48) }, NOW)).toBe(true)
    expect(expiringSoon({ lease: false, expires_at: at(60 * 24 * 10) }, NOW)).toBe(false)
  })

  it('duration text', () => {
    expect(durationText(20_000)).toBe('less than a minute')
    expect(durationText(3 * 24 * 3600_000)).toBe('3 d')
  })
})
