import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import api from '@/services/api'

// MCP-1064 (follows MCP-1057 backend remove path): the Web UI removes a
// *custom/unverified* registry source through DELETE /api/v1/registries/{id}.
// The client surfaces the slim RegistrySummary on success and exposes the
// stable cross-surface error `code` (registry_not_found | registry_shadows_builtin
// | registries_locked) so the UI can render an actionable message. Removing a
// source does NOT touch upstream servers already added from it.

describe('api.removeRegistrySource', () => {
  beforeEach(() => {
    api.setAPIKey('test-key')
  })

  afterEach(() => {
    vi.restoreAllMocks()
    api.clearAPIKey()
  })

  it('DELETEs the encoded registry id and returns the removed registry summary', async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({
        success: true,
        data: {
          registry: {
            id: 'acme-registry',
            name: 'Acme Registry',
            url: 'https://acme.example/registry',
            provenance: 'custom/unverified',
            trusted: false
          }
        },
        request_id: 'req-1'
      })
    })
    vi.stubGlobal('fetch', fetchMock)

    const result = await api.removeRegistrySource('acme-registry')

    expect(result.success).toBe(true)
    expect(result.registry?.id).toBe('acme-registry')
    expect(result.registry?.provenance).toBe('custom/unverified')

    const [calledUrl, calledInit] = fetchMock.mock.calls[0]
    expect(calledUrl).toBe('/api/v1/registries/acme-registry')
    expect(calledInit.method).toBe('DELETE')
    expect((calledInit.headers as Record<string, string>)['X-API-Key']).toBe('test-key')
  })

  it('percent-encodes a namespaced registry id in the path', async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ success: true, data: { registry: { id: 'acme/inc' } } })
    })
    vi.stubGlobal('fetch', fetchMock)

    await api.removeRegistrySource('acme/inc')

    expect(fetchMock.mock.calls[0][0]).toBe('/api/v1/registries/acme%2Finc')
  })

  it('surfaces registry_not_found (404) as a structured error', async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: false,
      status: 404,
      statusText: 'Not Found',
      json: async () => ({ success: false, error: 'no such registry', code: 'registry_not_found' })
    })
    vi.stubGlobal('fetch', fetchMock)

    const result = await api.removeRegistrySource('ghost')

    expect(result.success).toBe(false)
    expect(result.code).toBe('registry_not_found')
  })

  it('surfaces registry_shadows_builtin (409 — built-in cannot be removed)', async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: false,
      status: 409,
      statusText: 'Conflict',
      json: async () => ({ success: false, error: 'built-in registry', code: 'registry_shadows_builtin' })
    })
    vi.stubGlobal('fetch', fetchMock)

    const result = await api.removeRegistrySource('official')

    expect(result.success).toBe(false)
    expect(result.code).toBe('registry_shadows_builtin')
  })

  it('surfaces registries_locked (403) without emitting an auth error', async () => {
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
})
