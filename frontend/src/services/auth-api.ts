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

export const authApi = {
  // Public edition probe (Spec 107 FR-030 / FR-041). No authentication, no
  // side effects. 200 {display_name} = server edition; 404 = personal build
  // or server_edition.enabled=false. Any other failure is reported as null so
  // the UI degrades to the personal surface rather than hanging on a spinner.
  // This is called BEFORE any authenticated call: a tenant holds no API key,
  // so learning the edition from a keyed endpoint would 401 for exactly the
  // people the server edition exists for.
  async getProvider(): Promise<ProviderInfo | null> {
    try {
      const response = await fetch(`${API_BASE}/auth/provider`)
      if (response.status === 404) return null
      if (!response.ok) return null
      const body = await response.json()
      if (!body || typeof body.display_name !== 'string') return null
      return { display_name: body.display_name }
    } catch {
      return null
    }
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
