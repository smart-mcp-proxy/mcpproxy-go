// Auth API client for teams edition
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

export const authApi = {
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
