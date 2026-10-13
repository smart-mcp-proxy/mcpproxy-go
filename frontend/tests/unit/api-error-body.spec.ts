import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import api, { ApiError } from '@/services/api'

// Spec 108-i I1: the Profiles v3 methods throw an ApiError that keeps the whole
// error body (code, field, used_by, bindings, fixes, skipped...), and report an
// auth error for a 401 only - a 403 means "not an administrator".

function respond(status: number, body: unknown) {
  return vi.fn().mockResolvedValue({
    ok: status >= 200 && status < 300,
    status,
    statusText: 'x',
    json: () => Promise.resolve(body),
  })
}

describe('requestRaw / ApiError (Spec 108-i I1)', () => {
  const authEvents: Array<{ status: number }> = []
  let off: () => void

  beforeEach(() => {
    authEvents.length = 0
    off = api.addEventListener(event => authEvents.push({ status: event.status }))
  })
  afterEach(() => {
    off()
    vi.unstubAllGlobals()
  })

  it('throws an ApiError carrying code, field, used_by, bindings, fixes and skipped', async () => {
    const body = {
      error: 'profile in use',
      code: 'profile_in_use',
      field: 'name',
      used_by: { clients: [{ id: 'cursor', mode: 'locked' }], tokens: ['ci'], anonymous_profile: false },
      bindings: [{ client_id: 'cursor', token_name: 'client-cursor', profile: 'work', mode: 'switchable' }],
      fixes: [{ kind: 'require_mcp_auth' }, { kind: 'set_anonymous_profile', target: 'work' }],
      skipped: [{ client_id: 'codex', code: 'no_client_credential' }],
      conflicting_token: 'client-cursor',
      remediation: 'revoke it',
    }
    vi.stubGlobal('fetch', respond(409, body))
    const error = await api.deleteProfile('work').catch(err => err)
    expect(error).toBeInstanceOf(ApiError)
    expect(error.message).toBe('profile in use')
    expect(error.status).toBe(409)
    expect(error.code).toBe('profile_in_use')
    expect(error.field).toBe('name')
    expect(error.used_by.clients).toEqual([{ id: 'cursor', mode: 'locked' }])
    expect(error.bindings[0].profile).toBe('work')
    expect(error.fixes.map((fix: { kind: string }) => fix.kind)).toEqual(['require_mcp_auth', 'set_anonymous_profile'])
    expect(error.skipped[0].code).toBe('no_client_credential')
    expect(error.conflicting_token).toBe('client-cursor')
    expect(error.remediation).toBe('revoke it')
  })

  it('does not report an auth error for a 403 but does for a 401', async () => {
    vi.stubGlobal('fetch', respond(403, { error: 'Admin credentials required' }))
    await expect(api.getProfile('work')).rejects.toMatchObject({ status: 403 })
    expect(authEvents).toEqual([])

    vi.stubGlobal('fetch', respond(401, { error: 'invalid key' }))
    await expect(api.getProfile('work')).rejects.toMatchObject({ status: 401 })
    expect(authEvents).toEqual([{ status: 401 }])
  })

  it('unwraps the envelope data on success and uses the REST names verbatim', async () => {
    const fetchMock = respond(200, { success: true, data: { profiles: [], anonymous_profile: 'work' } })
    vi.stubGlobal('fetch', fetchMock)
    await expect(api.getProfiles()).resolves.toEqual({ profiles: [], anonymous_profile: 'work' })
    expect(fetchMock.mock.calls[0][0]).toBe('/api/v1/profiles')

    await api.deleteProfile('a b', { reassign_to: 'other' })
    expect(fetchMock.mock.calls[1][0]).toBe('/api/v1/profiles/a%20b?reassign_to=other')
    expect(fetchMock.mock.calls[1][1].method).toBe('DELETE')

    await api.setClientBinding('cursor', { profile: 'work' })
    expect(fetchMock.mock.calls[2][0]).toBe('/api/v1/clients/cursor/binding')
    expect(fetchMock.mock.calls[2][1].method).toBe('PUT')
    expect(JSON.parse(fetchMock.mock.calls[2][1].body)).toEqual({ profile: 'work' })
  })

  it('explainAccess sends exactly one subject and the connect calls carry the binding', async () => {
    const fetchMock = respond(200, { success: true, data: {} })
    vi.stubGlobal('fetch', fetchMock)
    await api.explainAccess({ tool: 'github:create_issue', client: 'cursor' })
    expect(fetchMock.mock.calls[0][0]).toBe('/api/v1/access/explain?tool=github%3Acreate_issue&client=cursor')

    await api.getConnectPreview('cursor', { profile: '', mode: 'switchable' })
    expect(fetchMock.mock.calls[1][0]).toBe('/api/v1/connect/cursor/preview?profile=&mode=switchable')

    await api.connectClient('cursor', 'mcpproxy', true, { profile: 'work', mode: 'locked', precondition_token: 'tok' })
    expect(JSON.parse(fetchMock.mock.calls[2][1].body)).toEqual({ server_name: 'mcpproxy', force: true, profile: 'work', mode: 'locked', precondition_token: 'tok' })

    await api.connectClient('cursor')
    expect(JSON.parse(fetchMock.mock.calls[3][1].body)).toEqual({ server_name: 'mcpproxy', force: false })
  })

  it('createAgentToken and listAgentTokens go through the same structured path', async () => {
    vi.stubGlobal('fetch', respond(400, { error: 'reserved', field: 'name' }))
    await expect(api.createAgentToken({ name: 'client-x', profile: 'work', expires_in: '720h' })).rejects.toMatchObject({ field: 'name' })
    const fetchMock = respond(200, { success: true, data: { tokens: [] } })
    vi.stubGlobal('fetch', fetchMock)
    await api.listAgentTokens({ profile: 'work', token: 'ci' })
    expect(fetchMock.mock.calls[0][0]).toBe('/api/v1/tokens?profile=work&token=ci')
  })
})
