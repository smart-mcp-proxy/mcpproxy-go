export interface OAuthLoginResult {
  browser_opened: boolean
  auth_url?: string
  browser_error?: string
}

// Reserve a tab during the user's click, before the API request loses the
// browser's user activation. The remote daemon cannot open this browser.
export async function openOAuthLogin(
  request: () => Promise<{ success: boolean; data?: OAuthLoginResult; error?: string }>,
): Promise<OAuthLoginResult> {
  const popup = window.open('about:blank', '_blank')
  if (popup) popup.opener = null
  try {
    const response = await request()
    if (!response.success) throw new Error(response.error || 'Failed to start sign-in')
    const result = response.data
    if (!result) throw new Error('The server returned no sign-in details')
    if (result.auth_url) {
      const url = new URL(result.auth_url)
      if (!['https:', 'http:'].includes(url.protocol)) throw new Error('The server returned an invalid sign-in URL')
      if (popup && !popup.closed) {
        popup.location.replace(url.href)
        return { ...result, browser_opened: true }
      }
      return { ...result, browser_opened: false }
    }
    popup?.close()
    if (!result.browser_opened) throw new Error(result.browser_error || 'No sign-in URL was returned')
    return result
  } catch (error) {
    popup?.close()
    throw error
  }
}
