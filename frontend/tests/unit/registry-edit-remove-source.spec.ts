import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import api from '@/services/api'

// MCP-1073: custom registries can be edited (PUT /api/v1/registries/{id}) and
// removed (DELETE /api/v1/registries/{id}). Both mirror addRegistrySource's
// structured-error pattern, surfacing the stable `code`
// (registry_not_found | registry_shadows_builtin | invalid_registry_url |
// registries_locked) so the UI can render an actionable message.

describe('api.editRegistrySource', () => {
  beforeEach(() => {
    api.setAPIKey('test-key')
  })

  afterEach(() => {
    vi.restoreAllMocks()
    api.clearAPIKey()
  })

  it('PUTs only the supplied fields and returns the updated registry summary', async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({
        success: true,
        data: {
          registry: {
            id: 'acme',
            name: 'Acme Renamed',
            url: 'https://acme.example/registry',
            servers_url: 'https://acme.example/registry/v0.1/servers',
            protocol: 'modelcontextprotocol/registry',
            provenance: 'custom',
            trusted: false
          }
        },
        request_id: 'req-1'
      })
    })
    vi.stubGlobal('fetch', fetchMock)

    const result = await api.editRegistrySource('acme', { name: 'Acme Renamed' })

    expect(result.success).toBe(true)
    expect(result.registry?.name).toBe('Acme Renamed')

    const [calledUrl, calledInit] = fetchMock.mock.calls[0]
    expect(calledUrl).toBe('/api/v1/registries/acme')
    expect(calledInit.method).toBe('PUT')
    expect((calledInit.headers as Record<string, string>)['X-API-Key']).toBe('test-key')
    // Only non-empty fields are sent; empty = unchanged (backend contract).
    expect(JSON.parse(calledInit.body)).toEqual({ name: 'Acme Renamed' })
  })

  it('percent-encodes the id and sends url/servers_url when supplied', async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ success: true, data: { registry: { id: 'a/b', name: 'AB' } } })
    })
    vi.stubGlobal('fetch', fetchMock)

    await api.editRegistrySource('a/b', {
      url: 'https://acme.example/v2',
      serversUrl: 'https://acme.example/v2/servers'
    })

    const [calledUrl, calledInit] = fetchMock.mock.calls[0]
    expect(calledUrl).toBe('/api/v1/registries/a%2Fb')
    expect(JSON.parse(calledInit.body)).toEqual({
      url: 'https://acme.example/v2',
      servers_url: 'https://acme.example/v2/servers'
    })
  })

  it('surfaces registry_shadows_builtin / registry_not_found as a structured error', async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: false,
      status: 409,
      statusText: 'Conflict',
      json: async () => ({ success: false, error: 'collides with a built-in registry', code: 'registry_shadows_builtin' })
    })
    vi.stubGlobal('fetch', fetchMock)

    const result = await api.editRegistrySource('official', { name: 'x' })
    expect(result.success).toBe(false)
    expect(result.code).toBe('registry_shadows_builtin')
  })
})

describe('api.removeRegistrySource', () => {
  beforeEach(() => {
    api.setAPIKey('test-key')
  })

  afterEach(() => {
    vi.restoreAllMocks()
    api.clearAPIKey()
  })

  it('DELETEs the (encoded) id and reports success', async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ success: true, data: { registry: { id: 'acme', name: 'Acme' } } })
    })
    vi.stubGlobal('fetch', fetchMock)

    const result = await api.removeRegistrySource('acme')

    expect(result.success).toBe(true)
    const [calledUrl, calledInit] = fetchMock.mock.calls[0]
    expect(calledUrl).toBe('/api/v1/registries/acme')
    expect(calledInit.method).toBe('DELETE')
  })

  it('surfaces registries_locked as a structured error', async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: false,
      status: 403,
      statusText: 'Forbidden',
      json: async () => ({ success: false, error: 'locked', code: 'registries_locked' })
    })
    vi.stubGlobal('fetch', fetchMock)

    const result = await api.removeRegistrySource('acme')
    expect(result.success).toBe(false)
    expect(result.code).toBe('registries_locked')
  })

  it('surfaces registry_shadows_builtin when trying to remove a built-in', async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: false,
      status: 409,
      statusText: 'Conflict',
      json: async () => ({ success: false, error: 'cannot remove built-in', code: 'registry_shadows_builtin' })
    })
    vi.stubGlobal('fetch', fetchMock)

    const result = await api.removeRegistrySource('official')
    expect(result.success).toBe(false)
    expect(result.code).toBe('registry_shadows_builtin')
  })
})
