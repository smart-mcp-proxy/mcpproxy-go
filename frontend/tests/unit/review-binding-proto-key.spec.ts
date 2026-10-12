import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import type { ReviewTool } from '@/types'
import { expectedHashes } from '@/utils/reviewPresentation'

// Spec 117 PR-2 (#1566), Sol round 1: tool names are upstream-controlled. A tool named
// "__proto__" written into a plain {} hits the inherited setter and vanishes from the JSON, so
// the core answers 409 with that tool unreviewed and no reload can fix it. The binding must
// carry every reviewed name as an own key, through both approval requests.

const tool = (name: string, hash: string): ReviewTool => ({
  name, description: name, tier: 'read', approval_status: 'pending', disabled: false, scan_verdict: 'clean', default_allowed: true, current_hash: hash,
})
const hashesOf = (body: string) => {
  const parsed = JSON.parse(body) // JSON.parse defines "__proto__" as an own key
  return Object.fromEntries(Object.entries(parsed.expected_hashes))
}

describe('expectedHashes with hostile tool names', () => {
  it('keeps __proto__, constructor and toString as own keys in the JSON', () => {
    const binding = expectedHashes([tool('__proto__', 'rv1-p'), tool('constructor', 'rv1-c'), tool('toString', 'rv1-t')])!
    const json = JSON.stringify(binding)
    expect(json).toContain('"__proto__":"rv1-p"')
    expect(Object.entries(JSON.parse(json))).toEqual([['__proto__', 'rv1-p'], ['constructor', 'rv1-c'], ['toString', 'rv1-t']])
  })

  it('an empty review still binds to the empty snapshot', () => {
    expect(JSON.stringify(expectedHashes([]))).toBe('{}')
  })
})

describe('approval requests carry a __proto__ binding', () => {
  let fetchMock: ReturnType<typeof vi.fn>
  beforeEach(() => {
    fetchMock = vi.fn().mockResolvedValue({ ok: true, status: 200, json: async () => ({ success: true, data: { approved: 1 } }) })
    vi.stubGlobal('fetch', fetchMock)
  })
  afterEach(() => { vi.unstubAllGlobals(); vi.resetModules() })
  const freshApi = async () => { vi.resetModules(); return (await import('@/services/api')).default }

  it('server approval (security/approve)', async () => {
    const api = await freshApi()
    await api.securityApprove('evil', false, [], undefined, expectedHashes([tool('__proto__', 'rv1-p'), tool('ok', 'rv1-o')]))
    const [url, opts] = fetchMock.mock.calls[0]
    expect(url).toBe('/api/v1/servers/evil/security/approve')
    expect(hashesOf(opts.body)).toEqual(Object.fromEntries([['__proto__', 'rv1-p'], ['ok', 'rv1-o']]))
    expect(Object.keys(JSON.parse(opts.body).expected_hashes)).toEqual(['__proto__', 'ok'])
  })

  it('per-tool approval (tools/approve)', async () => {
    const api = await freshApi()
    const all = [tool('__proto__', 'rv1-p'), tool('ok', 'rv1-o')]
    await api.approveTools('evil', ['__proto__'], undefined, expectedHashes(all.filter(t => t.name === '__proto__'), all))
    const [url, opts] = fetchMock.mock.calls[0]
    expect(url).toBe('/api/v1/servers/evil/tools/approve')
    expect(JSON.parse(opts.body).tools).toEqual(['__proto__'])
    expect(Object.keys(JSON.parse(opts.body).expected_hashes)).toEqual(['__proto__'])
    expect(opts.body).toContain('"expected_hashes":{"__proto__":"rv1-p"}')
  })
})
