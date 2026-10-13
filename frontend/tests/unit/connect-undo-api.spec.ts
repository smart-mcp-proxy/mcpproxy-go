import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'

// Spec 078 US3 / CodeQL hardening: the undo request must carry only the backup's
// bare FILENAME (backup_name), never a path. The server resolves the full path
// inside the client's own config dir and never trusts a client-supplied path, so
// api.undoConnectClient strips any directory component before sending.

describe('api.undoConnectClient — sends a bare backup_name (not a path)', () => {
  let fetchMock: ReturnType<typeof vi.fn>

  beforeEach(() => {
    fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ success: true, data: { success: true, action: 'restored' } }),
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

  it('POSTs the basename of a POSIX backup path', async () => {
    const api = await freshApi()
    await api.undoConnectClient('cursor', 'mcpproxy', '/Users/test/.cursor/mcp.json.bak.20260703-101530')

    const [url, opts] = fetchMock.mock.calls[0]
    expect(url).toBe('/api/v1/connect/cursor/undo')
    expect(opts.method).toBe('POST')
    expect(JSON.parse(opts.body)).toEqual({
      server_name: 'mcpproxy',
      backup_name: 'mcp.json.bak.20260703-101530',
    })
  })

  it('strips a Windows backslash directory component too', async () => {
    const api = await freshApi()
    await api.undoConnectClient(
      'claude-desktop',
      'mcpproxy',
      'C:\\Users\\test\\AppData\\claude_desktop_config.json.bak.20260703-101530'
    )
    const [, opts] = fetchMock.mock.calls[0]
    expect(JSON.parse(opts.body).backup_name).toBe('claude_desktop_config.json.bak.20260703-101530')
  })

  it('sends an empty backup_name for the no-prior-file (null) case', async () => {
    const api = await freshApi()
    await api.undoConnectClient('cursor', 'mcpproxy', null)
    const [, opts] = fetchMock.mock.calls[0]
    expect(JSON.parse(opts.body)).toEqual({ server_name: 'mcpproxy', backup_name: '' })
  })

  it('percent-encodes a "/"-containing client id', async () => {
    const api = await freshApi()
    await api.undoConnectClient('weird/client', 'mcpproxy', null)
    const [url] = fetchMock.mock.calls[0]
    expect(url).toBe('/api/v1/connect/weird%2Fclient/undo')
  })
})
