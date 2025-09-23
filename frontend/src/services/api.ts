import type { APIResponse, Server, Tool, SearchResult, StatusUpdate, SecretRef, MigrationAnalysis, ConfigSecretsResponse } from '@/types'

class APIService {
  private baseUrl = ''
  private apiKey = ''

  constructor() {
    // In development, Vite proxy handles API calls
    // In production, the frontend is served from the same origin as the API
    this.baseUrl = import.meta.env.DEV ? '' : ''

    // Extract API key from URL parameters on initialization
    this.initializeAPIKey()
  }

  private initializeAPIKey() {
    // Check if API key is provided in URL parameters
    const urlParams = new URLSearchParams(window.location.search)
    const apiKeyFromURL = urlParams.get('apikey')

    if (apiKeyFromURL) {
      this.apiKey = apiKeyFromURL
      // Clean the URL by removing the API key parameter for security
      urlParams.delete('apikey')
      const newURL = window.location.pathname + (urlParams.toString() ? '?' + urlParams.toString() : '')
      window.history.replaceState({}, '', newURL)
    }
  }

  private async request<T>(endpoint: string, options: RequestInit = {}): Promise<APIResponse<T>> {
    try {
      const headers: Record<string, string> = {
        'Content-Type': 'application/json',
      }

      // Merge headers from options if they exist
      if (options.headers) {
        if (options.headers instanceof Headers) {
          options.headers.forEach((value, key) => {
            headers[key] = value
          })
        } else if (Array.isArray(options.headers)) {
          options.headers.forEach(([key, value]) => {
            headers[key] = value
          })
        } else {
          Object.assign(headers, options.headers)
        }
      }

      // Add API key header if available
      if (this.apiKey) {
        headers['X-API-Key'] = this.apiKey
      }

      const response = await fetch(`${this.baseUrl}${endpoint}`, {
        headers,
        ...options,
      })

      if (!response.ok) {
        throw new Error(`HTTP ${response.status}: ${response.statusText}`)
      }

      const data = await response.json()
      return data as APIResponse<T>
    } catch (error) {
      console.error('API request failed:', error)
      return {
        success: false,
        error: error instanceof Error ? error.message : 'Unknown error',
      }
    }
  }

  // Server endpoints
  async getServers(): Promise<APIResponse<{ servers: Server[] }>> {
    return this.request<{ servers: Server[] }>('/api/v1/servers')
  }

  async enableServer(serverName: string): Promise<APIResponse> {
    return this.request(`/api/v1/servers/${encodeURIComponent(serverName)}/enable`, {
      method: 'POST',
    })
  }

  async disableServer(serverName: string): Promise<APIResponse> {
    return this.request(`/api/v1/servers/${encodeURIComponent(serverName)}/disable`, {
      method: 'POST',
    })
  }

  async restartServer(serverName: string): Promise<APIResponse> {
    return this.request(`/api/v1/servers/${encodeURIComponent(serverName)}/restart`, {
      method: 'POST',
    })
  }

  async triggerOAuthLogin(serverName: string): Promise<APIResponse> {
    return this.request(`/api/v1/servers/${encodeURIComponent(serverName)}/login`, {
      method: 'POST',
    })
  }

  async getServerTools(serverName: string): Promise<APIResponse<{ tools: Tool[] }>> {
    return this.request<{ tools: Tool[] }>(`/api/v1/servers/${encodeURIComponent(serverName)}/tools`)
  }

  async getServerLogs(serverName: string, tail?: number): Promise<APIResponse<{ logs: string[] }>> {
    const params = tail ? `?tail=${tail}` : ''
    return this.request<{ logs: string[] }>(`/api/v1/servers/${encodeURIComponent(serverName)}/logs${params}`)
  }

  // Tool search
  async searchTools(query: string, limit = 10): Promise<APIResponse<{ results: SearchResult[] }>> {
    const params = new URLSearchParams({ q: query, limit: limit.toString() })
    return this.request<{ results: SearchResult[] }>(`/api/v1/index/search?${params}`)
  }

  // Server-Sent Events
  createEventSource(): EventSource {
    const url = this.apiKey
      ? `${this.baseUrl}/events?apikey=${encodeURIComponent(this.apiKey)}`
      : `${this.baseUrl}/events`
    return new EventSource(url)
  }

  // Secret endpoints
  async getSecretRefs(): Promise<APIResponse<{ refs: SecretRef[] }>> {
    return this.request<{ refs: SecretRef[] }>('/api/v1/secrets/refs')
  }

  async getConfigSecrets(): Promise<APIResponse<ConfigSecretsResponse>> {
    return this.request<ConfigSecretsResponse>('/api/v1/secrets/config')
  }

  async runMigrationAnalysis(): Promise<APIResponse<{ analysis: MigrationAnalysis }>> {
    return this.request<{ analysis: MigrationAnalysis }>('/api/v1/secrets/migrate', {
      method: 'POST',
    })
  }

  // Utility methods
  async testConnection(): Promise<boolean> {
    try {
      const response = await this.getServers()
      return response.success
    } catch {
      return false
    }
  }
}

export default new APIService()