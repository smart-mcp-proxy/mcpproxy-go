import type { APIResponse, Server, Tool, SearchResult, StatusUpdate, SecretRef, MigrationAnalysis, ConfigSecretsResponse, GetToolCallsResponse, GetToolCallDetailResponse, GetServerToolCallsResponse, GetConfigResponse, ValidateConfigResponse, ConfigApplyResult, ServerTokenMetrics } from '@/types'

// Event types for API service
export interface APIAuthEvent {
  type: 'auth-error'
  error: string
  status: number
}

type APIEventListener = (event: APIAuthEvent) => void

class APIService {
  private baseUrl = ''
  private apiKey = ''
  private initialized = false
  private eventListeners: APIEventListener[] = []

  constructor() {
    // In development, Vite proxy handles API calls
    // In production, the frontend is served from the same origin as the API
    this.baseUrl = import.meta.env.DEV ? '' : ''

    // Extract API key from URL parameters on initialization
    this.initializeAPIKey()
  }

  private initializeAPIKey() {
    // Set initialized flag first to prevent race conditions
    this.initialized = true

    const urlParams = new URLSearchParams(window.location.search)
    const apiKeyFromURL = urlParams.get('apikey')

    if (apiKeyFromURL) {
      // URL param always takes priority (for backend restarts with new keys)
      this.apiKey = apiKeyFromURL
      // Store the new API key for future navigation/refreshes
      localStorage.setItem('mcpproxy-api-key', apiKeyFromURL)
      console.log('API key from URL (updating storage):', this.apiKey.substring(0, 8) + '...')
      // Clean the URL by removing the API key parameter for security
      urlParams.delete('apikey')
      const newURL = window.location.pathname + (urlParams.toString() ? '?' + urlParams.toString() : '')
      window.history.replaceState({}, '', newURL)
    } else {
      // No URL param - check localStorage as fallback
      const storedApiKey = localStorage.getItem('mcpproxy-api-key')
      if (storedApiKey) {
        this.apiKey = storedApiKey
        console.log('API key from localStorage:', this.apiKey.substring(0, 8) + '...')
      } else {
        console.log('No API key found in URL or localStorage')
      }
    }
  }

  // Public method to reinitialize API key if needed
  public reinitializeAPIKey() {
    this.initialized = false
    this.initializeAPIKey()
  }

  // Check if API key is available
  public hasAPIKey(): boolean {
    return !!this.apiKey
  }

  // Get API key (for debugging purposes)
  public getAPIKeyPreview(): string {
    return this.apiKey ? this.apiKey.substring(0, 8) + '...' : 'none'
  }

  // Clear API key from both memory and localStorage
  public clearAPIKey(): void {
    this.apiKey = ''
    localStorage.removeItem('mcpproxy-api-key')
    console.log('API key cleared from memory and localStorage')
  }

  // Set API key programmatically and store it
  public setAPIKey(key: string): void {
    this.apiKey = key
    if (key) {
      localStorage.setItem('mcpproxy-api-key', key)
      console.log('API key set and stored:', key.substring(0, 8) + '...')
    } else {
      localStorage.removeItem('mcpproxy-api-key')
      console.log('API key cleared')
    }
  }

  // Event system for global error handling
  public addEventListener(listener: APIEventListener): () => void {
    this.eventListeners.push(listener)
    return () => {
      const index = this.eventListeners.indexOf(listener)
      if (index > -1) {
        this.eventListeners.splice(index, 1)
      }
    }
  }

  private emitAuthError(error: string, status: number): void {
    const event: APIAuthEvent = {
      type: 'auth-error',
      error,
      status
    }
    this.eventListeners.forEach(listener => {
      try {
        listener(event)
      } catch (err) {
        console.error('Error in API event listener:', err)
      }
    })
  }

  // Validate the current API key by making a test request
  public async validateAPIKey(): Promise<boolean> {
    if (!this.apiKey) {
      return false
    }

    try {
      const response = await this.getServers()
      return response.success
    } catch (error) {
      console.warn('API key validation failed:', error)
      return false
    }
  }

  private async request<T>(endpoint: string, options: RequestInit = {}): Promise<APIResponse<T>> {
    // Ensure API key initialization is complete
    if (!this.initialized) {
      console.log('API service not initialized, initializing now...')
      this.initializeAPIKey()
    }

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
        console.log(`API request to ${endpoint} with API key: ${this.getAPIKeyPreview()}`)
      } else {
        console.log(`API request to ${endpoint} without API key - initialized: ${this.initialized}`)
        console.log('Current URL search params:', window.location.search)
        console.log('LocalStorage API key:', localStorage.getItem('mcpproxy-api-key')?.substring(0, 8) + '...')
      }

      const response = await fetch(`${this.baseUrl}${endpoint}`, {
        ...options,
        headers,
      })

      if (!response.ok) {
        const errorMsg = `HTTP ${response.status}: ${response.statusText}`
        console.error(`API request failed: ${errorMsg}`)

        // Special handling for authentication errors
        if (response.status === 401 || response.status === 403) {
          console.error('Authentication failed - API key may be invalid or missing')
          this.emitAuthError(errorMsg, response.status)
        }

        throw new Error(errorMsg)
      }

      const data = await response.json()
      console.log(`API request to ${endpoint} succeeded`)
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

    console.log('Creating EventSource:', {
      hasApiKey: !!this.apiKey,
      apiKeyPreview: this.getAPIKeyPreview(),
      url: this.apiKey ? url.replace(this.apiKey, this.getAPIKeyPreview()) : url
    })

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

  async setSecret(name: string, value: string, type: string = 'keyring'): Promise<APIResponse<{
    message: string
    name: string
    type: string
    reference: string
  }>> {
    return this.request('/api/v1/secrets', {
      method: 'POST',
      body: JSON.stringify({ name, value, type })
    })
  }

  async deleteSecret(name: string, type: string = 'keyring'): Promise<APIResponse<{
    message: string
    name: string
    type: string
  }>> {
    const url = `/api/v1/secrets/${encodeURIComponent(name)}?type=${encodeURIComponent(type)}`
    return this.request(url, {
      method: 'DELETE'
    })
  }

  // Diagnostics
  async getDiagnostics(): Promise<APIResponse<{
    upstream_errors: Array<{
      type: string
      category: string
      server?: string
      title: string
      message: string
      timestamp: string
      severity: string
      metadata?: Record<string, any>
    }>
    oauth_required: string[]
    missing_secrets: Array<{
      name: string
      reference: string
      server: string
      type: string
    }>
    runtime_warnings: Array<{
      type: string
      category: string
      server?: string
      title: string
      message: string
      timestamp: string
      severity: string
      metadata?: Record<string, any>
    }>
    total_issues: number
    last_updated: string
  }>> {
    return this.request('/api/v1/diagnostics')
  }

  // Tool Call History endpoints
  async getToolCalls(params?: { limit?: number; offset?: number }): Promise<APIResponse<GetToolCallsResponse>> {
    const searchParams = new URLSearchParams()
    if (params?.limit) searchParams.set('limit', params.limit.toString())
    if (params?.offset) searchParams.set('offset', params.offset.toString())

    const url = `/api/v1/tool-calls${searchParams.toString() ? '?' + searchParams.toString() : ''}`
    return this.request<GetToolCallsResponse>(url)
  }

  async getToolCallDetail(id: string): Promise<APIResponse<GetToolCallDetailResponse>> {
    return this.request<GetToolCallDetailResponse>(`/api/v1/tool-calls/${encodeURIComponent(id)}`)
  }

  async getServerToolCalls(serverName: string, limit?: number): Promise<APIResponse<GetServerToolCallsResponse>> {
    const url = `/api/v1/servers/${encodeURIComponent(serverName)}/tool-calls${limit ? `?limit=${limit}` : ''}`
    return this.request<GetServerToolCallsResponse>(url)
  }

  async replayToolCall(id: string, args: Record<string, any>): Promise<APIResponse<any>> {
    return this.request(`/api/v1/tool-calls/${encodeURIComponent(id)}/replay`, {
      method: 'POST',
      body: JSON.stringify({ arguments: args })
    })
  }

  // Configuration management endpoints
  async getConfig(): Promise<APIResponse<GetConfigResponse>> {
    return this.request<GetConfigResponse>('/api/v1/config')
  }

  async validateConfig(config: any): Promise<APIResponse<ValidateConfigResponse>> {
    return this.request<ValidateConfigResponse>('/api/v1/config/validate', {
      method: 'POST',
      body: JSON.stringify(config)
    })
  }

  async applyConfig(config: any): Promise<APIResponse<ConfigApplyResult>> {
    return this.request<ConfigApplyResult>('/api/v1/config/apply', {
      method: 'POST',
      body: JSON.stringify(config)
    })
  }

  // Token statistics endpoints
  async getTokenStats(): Promise<APIResponse<ServerTokenMetrics>> {
    return this.request<ServerTokenMetrics>('/api/v1/stats/tokens')
  }

  // MCP Tool Call
  async callTool(toolName: string, args: Record<string, any>): Promise<APIResponse<any>> {
    return this.request('/mcp', {
      method: 'POST',
      body: JSON.stringify({
        jsonrpc: '2.0',
        method: 'tools/call',
        params: {
          name: toolName,
          arguments: args
        },
        id: Date.now()
      })
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