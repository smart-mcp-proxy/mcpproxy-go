import type { SkillEntry, SkillFileInfo, SkillsListResponse } from '@/types/api'

export const skillFile = (path: string, digest = `sha256:${path}`, size = 20, text = true): SkillFileInfo => ({ path, size, digest, text })

export const makeSkill = (path: string, over: Partial<SkillEntry> = {}): SkillEntry => ({
  id: `srv:${path}`, uri: `skill://${path}/SKILL.md`, path, name: path, description: `desc of ${path}`,
  status: 'pending', blocked: false, listed: true, available: false, unusable_reason: '',
  current_hash: `sk1-${path}`, approved_hash: '', approved_by: '',
  files: [skillFile('SKILL.md')], total_size: 20,
  scan: { verdict: 'clean', complete: true, incomplete: '', findings_truncated: false, findings: [] },
  ...over,
})

export const makeListing = (skills: SkillEntry[], over: Partial<SkillsListResponse> = {}): SkillsListResponse => ({
  enabled: true, incarnation: 7, server: 'srv', supported: true, expose_skills: null,
  last_refresh: '2026-11-16T19:20:00Z', error: '', truncated: false, skills, ...over,
})

export const ok = <T,>(data: T) => ({ success: true, data })

/**
 * Connects the real system store to a fake EventSource and returns a function
 * that delivers a skills.changed frame exactly as the core writes it
 * ({"payload": {server, counts}, "timestamp": n}, internal/httpapi/server.go)
 * through the store's own re-dispatch to `mcpproxy:skills-changed`. Call it with
 * a Pinia active and the spec's mocked api object (it sets createEventSource).
 */
export async function connectSkillsSSE(apiMock: object): Promise<(server: string | string[], counts?: Record<string, number>) => void> {
  const { vi } = await import('vitest')
  const { useSystemStore } = await import('@/stores/system')
  const g = globalThis as any
  if (!g.EventSource) g.EventSource = { CONNECTING: 0, OPEN: 1, CLOSED: 2 }
  const listeners: Record<string, (ev: { data: string }) => void> = {}
  const a = apiMock as Record<string, unknown>
  a.createEventSource = vi.fn(() => ({
    onopen: null, onmessage: null, onerror: null, readyState: 1, close: vi.fn(),
    addEventListener: (type: string, cb: (ev: { data: string }) => void) => { listeners[type] = cb },
  }))
  if (typeof a.hasAPIKey !== 'function') a.hasAPIKey = vi.fn(() => true)
  useSystemStore().connectEventSource()
  return (server, counts = {}) => listeners['skills.changed']({ data: JSON.stringify({ payload: Array.isArray(server) ? { servers: server } : { server, counts }, timestamp: 1763300000 }) })
}
