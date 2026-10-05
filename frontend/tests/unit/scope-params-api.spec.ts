import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import api, { ApiError } from '@/services/api'
import { pickScopeParams, scopeParamsKey } from '@/utils/scopeParams'
import { profileEditorLink } from '@/utils/profileRoute'

// Spec 108-j §2: the changed signatures of the page requests, all optional, so
// every existing caller is byte-identical and a scoped caller sends exactly the
// three names. A failed view-as /tools keeps its status and body (ApiError).

function stubFetch(body: unknown = { success: true, data: {} }, init: { ok?: boolean; status?: number } = {}) {
  const fetchMock = vi.fn().mockResolvedValue({
    ok: init.ok ?? true,
    status: init.status ?? 200,
    statusText: 'x',
    json: async () => body,
  })
  vi.stubGlobal('fetch', fetchMock)
  return fetchMock
}
const urlOf = (fetchMock: ReturnType<typeof vi.fn>) => fetchMock.mock.calls[0][0] as string

describe('scope params on the page requests (Spec 108-j)', () => {
  beforeEach(() => api.setAPIKey('k'))
  afterEach(() => {
    vi.restoreAllMocks()
    api.clearAPIKey()
  })

  it('pickScopeParams keeps only profile/client/token and tolerates null (a conflict)', () => {
    expect(pickScopeParams({ profile: 'p', client: 'c', token: 't', status: 'blocked' })).toEqual({ profile: 'p', client: 'c', token: 't' })
    expect(pickScopeParams({ status: 'blocked' })).toEqual({})
    expect(pickScopeParams(null)).toEqual({})
    expect(scopeParamsKey({ client: 'c' })).not.toBe(scopeParamsKey({ profile: 'c' }))
  })

  it('getGlobalTools() without a scope is the unchanged /tools request', async () => {
    const f = stubFetch()
    await api.getGlobalTools()
    expect(urlOf(f)).toBe('/api/v1/tools')
  })

  it('getGlobalTools({client}) and ({profile}) send exactly that parameter', async () => {
    let f = stubFetch()
    await api.getGlobalTools({ client: 'cursor' })
    expect(urlOf(f)).toBe('/api/v1/tools?client=cursor')
    f = stubFetch()
    await api.getGlobalTools({ profile: 'work-readonly' })
    expect(urlOf(f)).toBe('/api/v1/tools?profile=work-readonly')
  })

  it('a refused view-as /tools throws an ApiError with the status', async () => {
    stubFetch({ error: 'client not found' }, { ok: false, status: 404 })
    await expect(api.getGlobalTools({ client: 'ghost' })).rejects.toMatchObject({ status: 404, message: 'client not found' })
    stubFetch({ error: 'x' }, { ok: false, status: 403 })
    const err = await api.getGlobalTools({ client: 'a' }).catch(e => e)
    expect(err).toBeInstanceOf(ApiError)
    expect(err.status).toBe(403)
  })

  it('getActivities, getActivityUsage and the export URL carry the three names', async () => {
    let f = stubFetch()
    await api.getActivities({ limit: 5, client: 'cursor', profile: '-', token: 'ro-bot' })
    expect(urlOf(f)).toContain('client=cursor')
    expect(urlOf(f)).toContain('profile=-')
    expect(urlOf(f)).toContain('token=ro-bot')
    f = stubFetch()
    await api.getActivityUsage({ window: '24h', profile: 'work-readonly' })
    expect(urlOf(f)).toBe('/api/v1/activity/usage?window=24h&profile=work-readonly')
    expect(api.getActivityExportUrl({ format: 'json', client: 'cursor' })).toContain('client=cursor')
  })

  it('getActivitySummary(period) is unchanged and getActivitySummary(period, scope) adds the scope', async () => {
    let f = stubFetch()
    await api.getActivitySummary('24h')
    expect(urlOf(f)).toBe('/api/v1/activity/summary?period=24h')
    f = stubFetch()
    await api.getActivitySummary('24h', { client: 'cursor' })
    expect(urlOf(f)).toBe('/api/v1/activity/summary?period=24h&client=cursor')
  })

  it('profileEditorLink builds the named route, with the focus only when given', () => {
    expect(profileEditorLink('work-ro')).toEqual({ name: 'profile-editor', params: { name: 'work-ro' }, query: {} })
    expect(profileEditorLink('work-ro', 'github:create_issue')).toEqual({
      name: 'profile-editor',
      params: { name: 'work-ro' },
      query: { focus: 'github:create_issue' },
    })
  })
})
