import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import api from '@/services/api'

// Spec 070 (T015): the Web UI adds a server by *reference* through the new REST
// endpoint. The client must NOT split install_cmd or choose a protocol anymore;
// it posts (registryId, serverId, optional name/enabled/env) and surfaces the
// structured cross-surface error so the UI can drive the required-input prompt.

describe('api.addServerFromRegistry', () => {
  beforeEach(() => {
    api.setAPIKey('test-key')
  })

  afterEach(() => {
    vi.restoreAllMocks()
    api.clearAPIKey()
  })

  it('POSTs to the reference-based add endpoint with env and returns the added server', async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({
        success: true,
        data: { server: { name: 'github', protocol: 'stdio', quarantined: true } },
        request_id: 'req-1'
      })
    })
    vi.stubGlobal('fetch', fetchMock)

    const result = await api.addServerFromRegistry('pulse', 'github-server', {
      env: { GITHUB_TOKEN: 'ghp_x' }
    })

    expect(result.success).toBe(true)
    expect(result.server?.name).toBe('github')
    expect(result.server?.quarantined).toBe(true)

    const [calledUrl, calledInit] = fetchMock.mock.calls[0]
    expect(calledUrl).toBe('/api/v1/registries/pulse/servers/github-server/add')
    expect(calledInit.method).toBe('POST')
    expect((calledInit.headers as Record<string, string>)['X-API-Key']).toBe('test-key')
    expect(JSON.parse(calledInit.body)).toEqual({ env: { GITHUB_TOKEN: 'ghp_x' } })
  })

  it('does not send an env key when no env is supplied', async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ success: true, data: { server: { name: 'fs' } } })
    })
    vi.stubGlobal('fetch', fetchMock)

    await api.addServerFromRegistry('pulse', 'fs-server')

    expect(JSON.parse(fetchMock.mock.calls[0][1].body)).toEqual({})
  })

  it('surfaces missing_required_input with the missing names for the prompt', async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: false,
      status: 400,
      statusText: 'Bad Request',
      json: async () => ({
        success: false,
        error: 'missing_required_input: GITHUB_TOKEN',
        code: 'missing_required_input',
        missing_inputs: ['GITHUB_TOKEN'],
        request_id: 'req-2'
      })
    })
    vi.stubGlobal('fetch', fetchMock)

    const result = await api.addServerFromRegistry('pulse', 'github-server')

    expect(result.success).toBe(false)
    expect(result.code).toBe('missing_required_input')
    expect(result.missingInputs).toEqual(['GITHUB_TOKEN'])
  })

  it('surfaces duplicate_name / not-found codes as structured errors', async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: false,
      status: 400,
      statusText: 'Bad Request',
      json: async () => ({ success: false, error: 'duplicate_name: github', code: 'duplicate_name' })
    })
    vi.stubGlobal('fetch', fetchMock)

    const result = await api.addServerFromRegistry('pulse', 'github-server')

    expect(result.success).toBe(false)
    expect(result.code).toBe('duplicate_name')
    expect(result.error).toContain('duplicate_name')
  })

  it('url-encodes registry and server ids', async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ success: true, data: { server: { name: 's' } } })
    })
    vi.stubGlobal('fetch', fetchMock)

    await api.addServerFromRegistry('reg/with space', 'srv/id', { name: 'override' })

    expect(fetchMock.mock.calls[0][0]).toBe('/api/v1/registries/reg%2Fwith%20space/servers/srv%2Fid/add')
    expect(JSON.parse(fetchMock.mock.calls[0][1].body)).toEqual({ name: 'override' })
  })
})
