import { describe, it, expect } from 'vitest'
import { countServers, matchesFilter, resolveFilter, sortServers, isOnline } from '@/utils/serversList'
import type { Server } from '@/types'

function srv(name: string, status: string | null, over: Partial<Server> = {}): Server {
  const enabled = status !== 'disabled'
  return {
    name, protocol: 'stdio', enabled, quarantined: status === 'needs_review', connected: status === 'ready', connecting: status === 'connecting', tool_count: 1,
    health: status === null ? undefined : { level: 'healthy', admin_state: status === 'disabled' ? 'disabled' : status === 'needs_review' ? 'quarantined' : 'enabled', summary: '', status, usable: status === 'ready', actions: [] },
    ...over,
  } as Server
}

describe('FR-024 order', () => {
  it('error tones, review, unknown, busy, ready, disabled; then title; then id', () => {
    const list = [
      srv('z-ready', 'ready'), srv('disabled-1', 'disabled'), srv('conn', 'connecting'), srv('weird', 'zzz'),
      srv('review', 'needs_review'), srv('err-b', 'error'), srv('err-a', 'sign_in_required'), srv('Ready-a', 'ready'),
    ]
    expect(sortServers(list).map(s => s.name)).toEqual(['err-a', 'err-b', 'review', 'weird', 'conn', 'Ready-a', 'z-ready', 'disabled-1'])
  })
  it('same title falls to canonical id', () => {
    const a = srv('b-id', 'ready', { title: 'Same' }); const b = srv('a-id', 'ready', { title: 'Same' })
    expect(sortServers([a, b]).map(s => s.name)).toEqual(['a-id', 'b-id'])
  })
  it('legacy servers without health are placed by their fields', () => {
    const down = srv('down', null); const up = srv('up', null, { connected: true }); const off = srv('off', null, { enabled: false })
    const held = srv('held', null, { connected: true, quarantined: true })
    expect(sortServers([off, up, down, held]).map(s => s.name)).toEqual(['down', 'held', 'up', 'off'])
  })
  it('does not mutate its input', () => {
    const list = [srv('b', 'ready'), srv('a', 'error')]
    sortServers(list)
    expect(list[0].name).toBe('b')
  })
})

describe('counts and predicates', () => {
  const list = [srv('a', 'ready'), srv('b', 'error'), srv('c', 'disabled'), srv('d', 'needs_review'), srv('e', 'connecting')]
  it('chips partition the list', () => {
    const c = countServers(list)
    expect(c).toMatchObject({ total: 5, online: 1, notOnline: 3, disabled: 1, quarantined: 1, needsReview: 1, connected: 1 })
    expect(c.online + c.notOnline + c.disabled).toBe(c.total)
    for (const f of ['online', 'not-online', 'disabled'] as const) expect(list.filter(s => matchesFilter(s, f)).length).toBe({ online: 1, 'not-online': 3, disabled: 1 }[f])
  })
  it('online equals health.usable, as the header', () => {
    expect(isOnline(srv('a', 'ready'))).toBe(true)
    expect(isOnline(srv('a', 'error', { connected: true }))).toBe(false)
  })
})

describe('FR-023 resolveFilter', () => {
  const base = { hasQ: false, hasProfile: false, wide: true, notOnlineCount: 3 }
  it('bare visit: All wide, Not online narrow when not empty', () => {
    expect(resolveFilter({ ...base })).toBe('all')
    expect(resolveFilter({ ...base, wide: false })).toBe('not-online')
    expect(resolveFilter({ ...base, wide: false, notOnlineCount: 0 })).toBe('all')
  })
  it('q or profile only: All at every width', () => {
    expect(resolveFilter({ ...base, wide: false, hasQ: true })).toBe('all')
    expect(resolveFilter({ ...base, wide: false, hasProfile: true })).toBe('all')
  })
  it('explicit filter, including all, wins over the phone default', () => {
    expect(resolveFilter({ ...base, wide: false, filter: 'all' })).toBe('all')
    expect(resolveFilter({ ...base, filter: 'disabled' })).toBe('disabled')
    expect(resolveFilter({ ...base, wide: false, filter: 'bogus' })).toBe('all')
  })
  it.each(['connected', 'enabled', 'quarantined', 'needs_review'])('legacy status=%s keeps its meaning', s => {
    expect(resolveFilter({ ...base, status: s })).toBe(s)
    expect(resolveFilter({ ...base, status: s, filter: 'online' })).toBe(s)
  })
  it('a valueless ?filter or ?status is not a bare visit', () => {
    expect(resolveFilter({ ...base, wide: false, filter: null })).toBe('all')
    expect(resolveFilter({ ...base, wide: false, status: null })).toBe('all')
  })
  it('status=all is All; unknown status is ignored', () => {
    expect(resolveFilter({ ...base, wide: false, status: 'all' })).toBe('all')
    expect(resolveFilter({ ...base, status: 'nope', filter: 'online' })).toBe('online')
  })
})
