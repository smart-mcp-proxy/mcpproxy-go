import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import api from '@/services/api'

// MCP-866 / MCP-867: the Web UI adds a *registry source* (not a server) through
// POST /api/v1/registries. The client posts (url + optional protocol/id/name),
// surfaces the slim RegistrySummary on success, and exposes the stable error
// `code` (invalid_registry_url | registries_locked | registry_shadows_builtin |
// duplicate_registry) so the UI can render an actionable message. The backend
// always tags an added source custom/unverified — the client never sends a
// provenance/trusted claim.

describe('api.addRegistrySource', () => {
  beforeEach(() => {
    api.setAPIKey('test-key')
  })

  afterEach(() => {
    vi.restoreAllMocks()
    api.clearAPIKey()
  })

  it('POSTs the url (and default-free body) and returns the added registry summary', async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({
        success: true,
        data: {
          registry: {
            id: 'acme-registry',
            name: 'acme-registry',
            url: 'https://acme.example/registry',
            servers_url: 'https://acme.example/registry/v0.1/servers',
            protocol: 'modelcontextprotocol/registry',
            provenance: 'custom/unverified',
            trusted: false
          }
        },
        request_id: 'req-1'
      })
    })
    vi.stubGlobal('fetch', fetchMock)

    const result = await api.addRegistrySource('https://acme.example/registry')

    expect(result.success).toBe(true)
    expect(result.registry?.id).toBe('acme-registry')
    expect(result.registry?.provenance).toBe('custom/unverified')
    expect(result.registry?.trusted).toBe(false)

    const [calledUrl, calledInit] = fetchMock.mock.calls[0]
    expect(calledUrl).toBe('/api/v1/registries')
    expect(calledInit.method).toBe('POST')
    expect((calledInit.headers as Record<string, string>)['X-API-Key']).toBe('test-key')
    expect(JSON.parse(calledInit.body)).toEqual({ url: 'https://acme.example/registry' })
  })

  it('includes optional protocol/id/name only when supplied', async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ success: true, data: { registry: { id: 'acme', name: 'Acme' } } })
    })
    vi.stubGlobal('fetch', fetchMock)

    await api.addRegistrySource('https://acme.example/registry', {
      protocol: 'modelcontextprotocol/registry',
      id: 'acme',
      name: 'Acme'
    })

    expect(JSON.parse(fetchMock.mock.calls[0][1].body)).toEqual({
      url: 'https://acme.example/registry',
      protocol: 'modelcontextprotocol/registry',
      id: 'acme',
      name: 'Acme'
    })
  })

  it('surfaces invalid_registry_url as a structured error', async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: false,
      status: 400,
      statusText: 'Bad Request',
      json: async () => ({
        success: false,
        error: 'Registry URL must be HTTPS',
        code: 'invalid_registry_url',
        request_id: 'req-2'
      })
    })
    vi.stubGlobal('fetch', fetchMock)

    const result = await api.addRegistrySource('http://insecure.example')

    expect(result.success).toBe(false)
    expect(result.code).toBe('invalid_registry_url')
    expect(result.error).toContain('HTTPS')
  })

  it('surfaces registries_locked (admin pinned discovery sources)', async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: false,
      status: 403,
      statusText: 'Forbidden',
      json: async () => ({ success: false, error: 'Registry additions are locked', code: 'registries_locked' })
    })
    vi.stubGlobal('fetch', fetchMock)

    const result = await api.addRegistrySource('https://acme.example/registry')

    expect(result.success).toBe(false)
    expect(result.code).toBe('registries_locked')
  })

  it('surfaces duplicate_registry / registry_shadows_builtin conflicts', async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: false,
      status: 409,
      statusText: 'Conflict',
      json: async () => ({ success: false, error: 'A registry with id "official" already exists', code: 'registry_shadows_builtin' })
    })
    vi.stubGlobal('fetch', fetchMock)

    const result = await api.addRegistrySource('https://acme.example/registry', { id: 'official' })

    expect(result.success).toBe(false)
    expect(result.code).toBe('registry_shadows_builtin')
  })
})
