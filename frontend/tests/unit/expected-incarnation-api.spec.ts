import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'

// Spec 116 PR2 (FR-017): the approval calls send the incarnation of the server
// the operator reviewed as `expected_incarnation`, so a decision made on one
// server can never land on a different server re-added under the same name
// (the backend answers 409 server_replaced). Unknown incarnation: the body is
// unchanged and the backend binds to the incarnation current on arrival.

describe('approval calls — expected_incarnation (Spec 116 FR-017)', () => {
  let fetchMock: ReturnType<typeof vi.fn>

  beforeEach(() => {
    fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ success: true, data: {} }),
    })
    vi.stubGlobal('fetch', fetchMock)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    vi.resetModules()
  })

  async function freshApi() {
    vi.resetModules()
    const mod = await import('@/services/api')
    return mod.default
  }

  function lastBody(): Record<string, unknown> | undefined {
    const [, opts] = fetchMock.mock.calls[fetchMock.mock.calls.length - 1]
    return opts.body ? JSON.parse(opts.body) : undefined
  }

  it('approveTools / blockTools carry it next to the tool set', async () => {
    const api = await freshApi()
    await api.approveTools('acme', ['echo'], 42)
    expect(lastBody()).toEqual({ tools: ['echo'], expected_incarnation: 42 })
    await api.approveTools('acme', undefined, 42)
    expect(lastBody()).toEqual({ approve_all: true, expected_incarnation: 42 })
    await api.blockTools('acme', ['echo'], 42)
    expect(lastBody()).toEqual({ tools: ['echo'], expected_incarnation: 42 })
  })

  it('the toggles and the security approval carry it', async () => {
    const api = await freshApi()
    await api.setToolEnabled('acme', 'echo', false, 7)
    expect(lastBody()).toEqual({ enabled: false, expected_incarnation: 7 })
    await api.setAllToolsEnabled('acme', true, 7)
    expect(lastBody()).toEqual({ expected_incarnation: 7 })
    await api.securityApprove('acme', false, ['echo'], 7)
    expect(lastBody()).toEqual({ force: false, block: ['echo'], expected_incarnation: 7 })
  })

  it('omits it when the incarnation is unknown', async () => {
    const api = await freshApi()
    await api.approveTools('acme', ['echo'])
    expect(lastBody()).toEqual({ tools: ['echo'] })
    await api.securityApprove('acme', true, [], 0)
    expect(lastBody()).toEqual({ force: true, block: [] })
    await api.setAllToolsEnabled('acme', false)
    expect(lastBody()).toBeUndefined()
  })
})
