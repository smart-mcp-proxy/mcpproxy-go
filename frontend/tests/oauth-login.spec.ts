import { afterEach, describe, expect, it, vi } from 'vitest'
import { openOAuthLogin } from '../src/utils/oauthLogin'

afterEach(() => vi.restoreAllMocks())
const result = { success: true, data: { browser_opened: false, auth_url: 'https://provider.example/authorize?state=test' } }

describe('remote browser OAuth login', () => {
  it('reserves a tab before requesting the URL and removes its opener', async () => {
    const replace = vi.fn(), close = vi.fn()
    const popup = { opener: window, closed: false, location: { replace }, close }
    const open = vi.spyOn(window, 'open').mockReturnValue(popup as unknown as Window)
    const request = vi.fn(async () => { expect(open).toHaveBeenCalled(); expect(popup.opener).toBeNull(); return result })
    expect((await openOAuthLogin(request)).browser_opened).toBe(true)
    expect(replace).toHaveBeenCalledWith(result.data.auth_url)
    expect(close).not.toHaveBeenCalled()
  })
  it('retains a safe clickable URL when the popup is blocked', async () => {
    vi.spyOn(window, 'open').mockReturnValue(null)
    expect(await openOAuthLogin(async () => result)).toEqual(result.data)
  })
  it('closes the reserved tab on API failures or unsafe URLs', async () => {
    const close=vi.fn(),replace=vi.fn()
    vi.spyOn(window,'open').mockReturnValue({opener:null,closed:false,close,location:{replace}} as unknown as Window)
    await expect(openOAuthLogin(async()=>({success:false,error:'Denied'}))).rejects.toThrow('Denied')
    await expect(openOAuthLogin(async()=>({success:true,data:{browser_opened:false,auth_url:'javascript:alert(1)'}}))).rejects.toThrow('invalid sign-in URL')
    expect(close).toHaveBeenCalledTimes(2);expect(replace).not.toHaveBeenCalled()
  })
})
