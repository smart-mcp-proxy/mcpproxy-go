import { afterEach, describe, expect, it, vi } from 'vitest'
import api from '@/services/api'

function respond(status: number, body: unknown) {
  return vi.fn().mockResolvedValue({
    ok: status >= 200 && status < 300, status, statusText: 'x', json: () => Promise.resolve(body),
  })
}
const call = (f: any) => ({ url: f.mock.calls[0][0] as string, init: f.mock.calls[0][1] as RequestInit })

describe('skills API client', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('getServerSkills and refreshServerSkills hit the server skills routes', async () => {
    const f = respond(200, { success: true, data: { enabled: true, skills: [] } })
    vi.stubGlobal('fetch', f)
    await api.getServerSkills('my server')
    expect(call(f).url).toBe('/api/v1/servers/my%20server/skills')
    const g = respond(200, { success: true, data: {} })
    vi.stubGlobal('fetch', g)
    await api.refreshServerSkills('s')
    expect(call(g).url).toBe('/api/v1/servers/s/skills/refresh')
    expect(call(g).init.method).toBe('POST')
  })

  it('getSkillContent encodes the query and omits unset parts', async () => {
    const f = respond(200, { success: true, data: { text: '' } })
    vi.stubGlobal('fetch', f)
    await api.getSkillContent('s', { skill: 'a/b', file: 'dir/x y.md', copy: 'approved', offset: 65536, limit: 100 })
    const u = new URL(call(f).url, 'http://x')
    expect(u.pathname).toBe('/api/v1/servers/s/skills/content')
    expect(Object.fromEntries(u.searchParams)).toEqual({ skill: 'a/b', file: 'dir/x y.md', copy: 'approved', offset: '65536', limit: '100' })
    const g = respond(200, { success: true, data: { text: '' } })
    vi.stubGlobal('fetch', g)
    await api.getSkillContent('s', { skill: 'a' })
    expect(call(g).url).toBe('/api/v1/servers/s/skills/content?skill=a')
  })

  it('approveSkills sends skills, expected_hashes and the incarnation', async () => {
    const f = respond(200, { success: true, data: { approved: 1 } })
    vi.stubGlobal('fetch', f)
    await api.approveSkills('s', ['hf'], { hf: 'sk1-a' }, 3)
    expect(call(f).url).toBe('/api/v1/servers/s/skills/approve')
    expect(JSON.parse(call(f).init.body as string)).toEqual({ skills: ['hf'], expected_hashes: { hf: 'sk1-a' }, expected_incarnation: 3 })
  })

  it('omits expected_incarnation when it is unknown or 0', async () => {
    const f = respond(200, { success: true, data: {} })
    vi.stubGlobal('fetch', f)
    await api.blockSkills('s', ['hf'], 0)
    expect(JSON.parse(call(f).init.body as string)).toEqual({ skills: ['hf'] })
    const g = respond(200, { success: true, data: {} })
    vi.stubGlobal('fetch', g)
    await api.unblockSkills('s', ['hf'], 2)
    expect(call(g).url).toBe('/api/v1/servers/s/skills/unblock')
    expect(JSON.parse(call(g).init.body as string)).toEqual({ skills: ['hf'], expected_incarnation: 2 })
  })

  it('carries error_code into errorCode on a failure', async () => {
    vi.stubGlobal('fetch', respond(409, { success: false, error: 'review_out_of_date: x', error_code: 'review_out_of_date' }))
    const r: any = await api.approveSkills('s', ['hf'], { hf: 'a' })
    expect(r.success).toBe(false)
    expect(r.errorCode).toBe('review_out_of_date')
    expect(r.error).toContain('review_out_of_date')
  })

  it('leaves errorCode unset when the body has none', async () => {
    vi.stubGlobal('fetch', respond(500, { error: 'nope' }))
    const r: any = await api.getServerSkills('s')
    expect(r.success).toBe(false)
    expect(r.errorCode).toBeUndefined()
  })
})
