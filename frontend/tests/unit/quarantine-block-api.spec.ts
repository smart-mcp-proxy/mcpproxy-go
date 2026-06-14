import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'

// MCP-2199 (GH #632): the Tool Quarantine view offered only "Approve"/"Approve
// All". A reviewer who decides a pending/changed tool is unwanted had no way to
// reject it from the UI. api.blockTools mirrors approveTools against
// POST .../tools/block, sending {tools:[...]} for an explicit set and
// {block_all:true} otherwise. Contract: -> { blocked: int }.

describe('api.blockTools — contract (MCP-2199)', () => {
  let fetchMock: ReturnType<typeof vi.fn>

  beforeEach(() => {
    fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ success: true, data: { blocked: 1 } }),
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

  it('POSTs an explicit tool set as {tools:[...]}', async () => {
    const api = await freshApi()
    const res = await api.blockTools('github', ['create_issue'])
    expect(res.success).toBe(true)
    expect(res.data?.blocked).toBe(1)

    const [url, opts] = fetchMock.mock.calls[0]
    expect(url).toBe('/api/v1/servers/github/tools/block')
    expect(opts.method).toBe('POST')
    expect(JSON.parse(opts.body)).toEqual({ tools: ['create_issue'] })
  })

  it('POSTs {block_all:true} when no tools are given', async () => {
    const api = await freshApi()
    await api.blockTools('github')
    const [url, opts] = fetchMock.mock.calls[0]
    expect(url).toBe('/api/v1/servers/github/tools/block')
    expect(JSON.parse(opts.body)).toEqual({ block_all: true })
  })

  it('percent-encodes a "/"-containing server name', async () => {
    const api = await freshApi()
    await api.blockTools('io.github.owner/repo', ['t'])
    const [url] = fetchMock.mock.calls[0]
    expect(url).toBe('/api/v1/servers/io.github.owner%2Frepo/tools/block')
  })
})
