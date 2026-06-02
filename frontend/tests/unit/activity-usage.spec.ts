import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import api from '@/services/api'

// Spec 069 B1 (T017): the Web UI consumes the actor-owned usage aggregate via
// GET /api/v1/activity/usage. The client must forward only the supplied query
// params (window/server/tool/status/top/sort) and surface the standard
// {success,data} envelope so the Usage panel can render the contract response.

describe('api.getActivityUsage', () => {
  beforeEach(() => {
    api.setAPIKey('test-key')
  })

  afterEach(() => {
    vi.restoreAllMocks()
    api.clearAPIKey()
  })

  it('GETs the usage endpoint with the window param and returns the aggregate', async () => {
    const payload = {
      window: '7d',
      generated_at: '2026-05-31T12:00:00Z',
      freshness_ms: 1200,
      token_source: 'bytes',
      tokens_saved: 184320,
      tokens_saved_percentage: 92.4,
      tools: [
        {
          server: 'github',
          tool: 'search_issues',
          calls: 142,
          errors: 3,
          error_rate: 0.021,
          blocked: 0,
          total_resp_bytes: 5872013,
          avg_resp_bytes: 41352,
          total_req_bytes: 28400,
          avg_req_bytes: 200,
          sized_calls: 142,
          p50_ms: 120,
          p95_ms: 480,
          last_used: '2026-05-31T11:58:00Z',
        },
      ],
      timeline: [
        { start: '2026-05-31T11:00:00Z', calls: 40, errors: 1, total_resp_bytes: 1200000 },
      ],
    }
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ success: true, data: payload, request_id: 'req-1' }),
    })
    vi.stubGlobal('fetch', fetchMock)

    const result = await api.getActivityUsage({ window: '7d' })

    expect(result.success).toBe(true)
    expect(result.data?.window).toBe('7d')
    expect(result.data?.tools[0].tool).toBe('search_issues')
    expect(result.data?.tokens_saved).toBe(184320)

    const [calledUrl, calledInit] = fetchMock.mock.calls[0]
    expect(calledUrl).toBe('/api/v1/activity/usage?window=7d')
    expect((calledInit.headers as Record<string, string>)['X-API-Key']).toBe('test-key')
  })

  it('omits unset params and only appends provided filters', async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ success: true, data: { window: '24h', tools: [], timeline: [] } }),
    })
    vi.stubGlobal('fetch', fetchMock)

    await api.getActivityUsage({ window: '24h', server: 'github', sort: 'calls' })

    const calledUrl = fetchMock.mock.calls[0][0] as string
    expect(calledUrl).toContain('window=24h')
    expect(calledUrl).toContain('server=github')
    expect(calledUrl).toContain('sort=calls')
    expect(calledUrl).not.toContain('tool=')
    expect(calledUrl).not.toContain('status=')
  })

  it('defaults to no query string when called with no params', async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ success: true, data: { window: '24h', tools: [], timeline: [] } }),
    })
    vi.stubGlobal('fetch', fetchMock)

    await api.getActivityUsage()

    expect(fetchMock.mock.calls[0][0]).toBe('/api/v1/activity/usage')
  })
})
