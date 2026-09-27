// Auth API client for server edition
const API_BASE = '/api/v1'

export interface UserProfile {
  id: string
  email: string
  display_name: string
  role: 'admin' | 'user'
  provider: string
  created_at: string
  last_login_at: string
}

export interface BearerTokenResponse {
  token: string
  expires_at: string
}

// Spec 107 FR-030: the whole body of the public edition probe — an
// operator-chosen label and nothing else (never issuer, client id, tenant,
// scopes, domains or provider family).
export interface ProviderInfo {
  display_name: string
}

export interface SessionStatus {
  authenticated: boolean
}

export const authApi = {
  // Public edition probe (Spec 107 FR-030 / FR-041). No authentication, no
  // side effects. 200 {display_name} = server edition; 404 = personal build
  // or server_edition.enabled=false. `undefined` is deliberately distinct
  // from null: an unavailable or malformed probe is not evidence that this is
  // a personal edition, so startup must fail closed instead of firing keyed
  // admin requests at a server that may require a session.
  // This is called BEFORE any authenticated call: a tenant holds no API key,
  // so learning the edition from a keyed endpoint would 401 for exactly the
  // people the server edition exists for.
  async getProvider(): Promise<ProviderInfo | null | undefined> {
    // A served personal-edition index explicitly says that the public route
    // does not exist. Avoiding this request prevents an expected 401 from
    // being surfaced as a browser console error. Any other value deliberately
    // falls back to the public probe for standalone development and older
    // cores. This marker is never an authorization decision.
    const editionHint = document.querySelector('meta[name="mcpproxy-server-edition"]')?.getAttribute('content')
    if (editionHint === 'false') return null

    try {
      const response = await fetch(`${API_BASE}/auth/provider`)
      if (response.status === 404) return null
      if (!response.ok) return undefined
      const body = await response.json()
      if (!body || typeof body.display_name !== 'string') return undefined
      return { display_name: body.display_name }
    } catch {
      return undefined
    }
  },

  // Public, cookie-session-only bootstrap hint. It never grants access and
  // contains no identity data; /auth/me remains the profile and role source.
  async getSessionStatus(): Promise<SessionStatus> {
    const response = await fetch(`${API_BASE}/auth/session`, { credentials: 'include' })
    if (!response.ok) throw new Error(`HTTP ${response.status}`)
    const body: unknown = await response.json()
    if (!body || typeof body !== 'object' || typeof (body as SessionStatus).authenticated !== 'boolean') {
      throw new Error('Invalid session status response')
    }
    return { authenticated: (body as SessionStatus).authenticated }
  },

  // Get current user profile (returns null if not authenticated)
  async getMe(): Promise<UserProfile | null> {
    try {
      const response = await fetch(`${API_BASE}/auth/me`, { credentials: 'include' })
      if (response.status === 401) return null
      if (!response.ok) throw new Error(`HTTP ${response.status}`)
      return await response.json()
    } catch {
      return null
    }
  },

  // Generate bearer token for MCP clients
  async generateToken(): Promise<BearerTokenResponse> {
    const response = await fetch(`${API_BASE}/auth/token`, {
      method: 'POST',
      credentials: 'include',
    })
    if (!response.ok) throw new Error(`HTTP ${response.status}`)
    return await response.json()
  },

  // Log out
  async logout(): Promise<void> {
    await fetch(`${API_BASE}/auth/logout`, {
      method: 'POST',
      credentials: 'include',
    })
  },

  // Get login URL
  getLoginUrl(redirectUri?: string): string {
    const params = new URLSearchParams()
    if (redirectUri) params.set('redirect_uri', redirectUri)
    return `${API_BASE}/auth/login${params.toString() ? '?' + params.toString() : ''}`
  },
}
