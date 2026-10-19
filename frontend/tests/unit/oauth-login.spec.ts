import { afterEach, describe, expect, it, vi } from 'vitest'
import { openOAuthLogin, uiIsRemote } from '@/utils/oauthLogin'

afterEach(() => vi.restoreAllMocks())
const url = 'https://provider.example/authorize?state=test'
const result = { success: true, data: { browser_opened: false, auth_url: url } }
const remote = { remote: true }
const local = { remote: false }

describe('remote browser OAuth login', () => {
  it('reserves a tab before requesting the URL and removes its opener', async () => {
    const replace = vi.fn(), close = vi.fn()
    const popup = { opener: window, closed: false, location: { replace }, close }
    const open = vi.spyOn(window, 'open').mockReturnValue(popup as unknown as Window)
    const request = vi.fn(async () => { expect(open).toHaveBeenCalled(); expect(popup.opener).toBeNull(); return result })
    expect((await openOAuthLogin(request, remote)).browser_opened).toBe(true)
    expect(replace).toHaveBeenCalledWith(url)
    expect(close).not.toHaveBeenCalled()
  })
  it('retains a safe clickable URL when the popup is blocked', async () => {
    vi.spyOn(window, 'open').mockReturnValue(null)
    expect(await openOAuthLogin(async () => result, remote)).toEqual(result.data)
  })
  it('does not report the daemon host browser as opened here when no URL comes back', async () => {
    const close = vi.fn()
    vi.spyOn(window, 'open').mockReturnValue({ opener: null, closed: false, close, location: { replace: vi.fn() } } as unknown as Window)
    await expect(openOAuthLogin(async () => ({ success: true, data: { browser_opened: true } }), remote)).rejects.toThrow('sign-in URL')
    expect(close).toHaveBeenCalledTimes(1)
  })
  it('closes the reserved tab on API failures or unsafe URLs', async () => {
    const close = vi.fn(), replace = vi.fn()
    vi.spyOn(window, 'open').mockReturnValue({ opener: null, closed: false, close, location: { replace } } as unknown as Window)
    await expect(openOAuthLogin(async () => ({ success: false, error: 'Denied' }), remote)).rejects.toThrow('Denied')
    await expect(openOAuthLogin(async () => ({ success: true, data: { browser_opened: false, auth_url: 'javascript:alert(1)' } }), remote)).rejects.toThrow('invalid sign-in URL')
    expect(close).toHaveBeenCalledTimes(2); expect(replace).not.toHaveBeenCalled()
  })
})

describe('local browser OAuth login', () => {
  it('leaves the daemon-opened browser alone instead of opening the page twice', async () => {
    const open = vi.spyOn(window, 'open')
    const opened = { success: true, data: { browser_opened: true, auth_url: url } }
    expect(await openOAuthLogin(async () => opened, local)).toEqual(opened.data)
    expect(open).not.toHaveBeenCalled()
  })
  it('opens the page itself only when the daemon could not (HEADLESS, Docker)', async () => {
    const late = { opener: window }
    const open = vi.spyOn(window, 'open').mockReturnValue(late as unknown as Window)
    expect((await openOAuthLogin(async () => result, local)).browser_opened).toBe(true)
    expect(open).toHaveBeenCalledTimes(1)
    expect(open).toHaveBeenCalledWith(url, '_blank')
    expect(late.opener).toBeNull()
  })
  it('falls back to the toast link when the late popup is blocked', async () => {
    vi.spyOn(window, 'open').mockReturnValue(null)
    expect(await openOAuthLogin(async () => result, local)).toEqual(result.data)
  })
})

describe('uiIsRemote', () => {
  it('treats loopback hosts as local and everything else as remote', () => {
    for (const hostname of ['localhost', '127.0.0.1', '[::1]', '::1', 'app.localhost', 'LOCALHOST']) {
      expect(uiIsRemote({ hostname })).toBe(false)
    }
    for (const hostname of ['proxy.tailnet.ts.net', '192.168.1.20', 'kubic.home.arpa', 'mcpproxy.homelab.de']) {
      expect(uiIsRemote({ hostname })).toBe(true)
    }
  })
})
