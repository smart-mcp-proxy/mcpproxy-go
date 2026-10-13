export interface OAuthLoginResult {
  browser_opened: boolean
  auth_url?: string
  browser_error?: string
}

export interface OAuthLoginOptions {
  // True when the daemon's host browser is NOT the user's browser (the UI is
  // served through Tailscale, a LAN address, or a container port mapping).
  remote: boolean
}

// The login endpoint opens a browser on the DAEMON's host. That is the user's
// browser only when the Web UI is served from a loopback address; anywhere
// else the page must open the sign-in URL itself.
export function uiIsRemote(loc: { hostname: string } = window.location): boolean {
  const host = loc.hostname.replace(/^\[|\]$/g, '').toLowerCase()
  return !(host === 'localhost' || host === '127.0.0.1' || host === '::1' || host.endsWith('.localhost'))
}

// A remote UI reserves a tab during the user's click, before the API request
// loses the browser's user activation. A local UI leaves the daemon's own
// browser launch alone — reserving a tab there would open the sign-in page
// twice — and only opens a tab itself when the daemon could not.
export async function openOAuthLogin(
  request: () => Promise<{ success: boolean; data?: OAuthLoginResult; error?: string }>,
  options: OAuthLoginOptions = { remote: uiIsRemote() },
): Promise<OAuthLoginResult> {
  const popup = options.remote ? window.open('about:blank', '_blank') : null
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
      if (!options.remote && result.browser_opened) return result
      if (!options.remote) {
        // HEADLESS, Docker or no xdg-open: the daemon could not open a
        // browser. The click is usually still recent enough for a popup; a
        // blocked one leaves the persistent toast link.
        const late = window.open(url.href, '_blank')
        if (late) late.opener = null
        return { ...result, browser_opened: !!late }
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
