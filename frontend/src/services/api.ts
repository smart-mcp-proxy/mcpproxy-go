import type { APIResponse, Server, Tool, ToolApproval, SearchResult, StatusUpdate, SecretRef, MigrationAnalysis, ConfigSecretsResponse, GetToolCallsResponse, GetToolCallDetailResponse, GetServerToolCallsResponse, GetConfigResponse, ValidateConfigResponse, ConfigApplyResult, ServerTokenMetrics, GetRegistriesResponse, SearchRegistryServersResponse, RepositoryServer, GetSessionsResponse, GetSessionDetailResponse, InfoResponse, ActivityListResponse, ActivityDetailResponse, ActivitySummaryResponse, ImportResponse, AgentTokenInfo, CreateAgentTokenRequest, CreateAgentTokenResponse, RoutingInfo, ConnectStatusResponse, ConnectResult, DiagnosticFixResponse } from '@/types'

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
        // Spec 042: telemetry surface header so the daemon can attribute
        // requests to the web UI for the surface_requests counter. Version
        // is intentionally a constant string — the daemon already reports
        // its own build version separately and we don't want to leak the
        // browser/UA fingerprint into telemetry.
        'X-MCPProxy-Client': 'webui/web',
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
        // Try to extract error message from response body
        const errorData = await response.json().catch(() => ({}))
        const errorMsg = errorData.error || `HTTP ${response.status}: ${response.statusText}`
        console.error(`API request failed: ${errorMsg}`)

        // Special handling for authentication errors
        if (response.status === 401 || response.status === 403) {
          console.error('Authentication failed - API key may be invalid or missing')
          this.emitAuthError(errorMsg, response.status)
        }

        throw new Error(errorMsg)
      }

      // Handle 204 No Content (e.g., DELETE responses)
      if (response.status === 204) {
        console.log(`API request to ${endpoint} succeeded (204 No Content)`)
        return { success: true } as APIResponse<T>
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

  // Status endpoint
  async getStatus(): Promise<APIResponse<{ edition: string; running: boolean; routing_mode: string }>> {
    return this.request<{ edition: string; running: boolean; routing_mode: string }>('/api/v1/status')
  }

  // Routing mode endpoint
  async getRouting(): Promise<APIResponse<RoutingInfo>> {
    return this.request<RoutingInfo>('/api/v1/routing')
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

  async triggerOAuthLogout(serverName: string): Promise<APIResponse> {
    return this.request(`/api/v1/servers/${encodeURIComponent(serverName)}/logout`, {
      method: 'POST',
    })
  }

  async quarantineServer(serverName: string): Promise<APIResponse> {
    return this.request(`/api/v1/servers/${encodeURIComponent(serverName)}/quarantine`, {
      method: 'POST',
    })
  }

  async unquarantineServer(serverName: string): Promise<APIResponse> {
    return this.request(`/api/v1/servers/${encodeURIComponent(serverName)}/unquarantine`, {
      method: 'POST',
    })
  }

  async discoverServerTools(serverName: string): Promise<APIResponse> {
    return this.request(`/api/v1/servers/${encodeURIComponent(serverName)}/discover-tools`, {
      method: 'POST',
    })
  }

  async deleteServer(serverName: string): Promise<APIResponse> {
    return this.callTool('upstream_servers', {
      operation: 'remove',
      name: serverName
    })
  }

  async getServerTools(serverName: string): Promise<APIResponse<{ tools: Tool[] }>> {
    return this.request<{ tools: Tool[] }>(`/api/v1/servers/${encodeURIComponent(serverName)}/tools`)
  }

  // Tool-level quarantine (Spec 032)
  async getToolApprovals(serverName: string): Promise<APIResponse<{ tools: ToolApproval[], count: number }>> {
    return this.request<{ tools: ToolApproval[], count: number }>(`/api/v1/servers/${encodeURIComponent(serverName)}/tools/export`)
  }

  async getToolDiff(serverName: string, toolName: string): Promise<APIResponse<ToolApproval>> {
    return this.request<ToolApproval>(`/api/v1/servers/${encodeURIComponent(serverName)}/tools/${encodeURIComponent(toolName)}/diff`)
  }

  async approveTools(serverName: string, tools?: string[]): Promise<APIResponse<{ approved: number }>> {
    const body = tools && tools.length > 0
      ? { tools }
      : { approve_all: true }
    return this.request<{ approved: number }>(`/api/v1/servers/${encodeURIComponent(serverName)}/tools/approve`, {
      method: 'POST',
      body: JSON.stringify(body),
    })
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

  // Docker status
  async getDockerStatus(): Promise<APIResponse<{
    docker_available: boolean
    recovery_mode: boolean
    failure_count: number
    attempts_since_up: number
    last_attempt: string
    last_error: string
    last_successful_at: string
  }>> {
    return this.request('/api/v1/docker/status')
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

  // Spec 044 — per-server diagnostics.
  async getServerDiagnostic(serverName: string): Promise<APIResponse<{
    server: string
    connected: boolean
    status: string
    health: any
    diagnostic: any | null
    error_code: string | null
    catalog_size: number
  }>> {
    return this.request(`/api/v1/servers/${encodeURIComponent(serverName)}/diagnostics`)
  }

  // Spec 044 — invoke a registered fixer. Destructive fixers default to
  // dry_run unless mode='execute' is supplied by the caller.
  async invokeDiagnosticFix(params: {
    server: string
    code: string
    fixer_key: string
    mode?: 'dry_run' | 'execute'
  }): Promise<APIResponse<DiagnosticFixResponse>> {
    return this.request<DiagnosticFixResponse>('/api/v1/diagnostics/fix', {
      method: 'POST',
      body: JSON.stringify(params),
    })
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

  // Session management endpoints
  async getSessions(limit?: number): Promise<APIResponse<GetSessionsResponse>> {
    const url = `/api/v1/sessions${limit ? `?limit=${limit}` : ''}`
    return this.request<GetSessionsResponse>(url)
  }

  async getSessionDetail(sessionId: string): Promise<APIResponse<GetSessionDetailResponse>> {
    return this.request<GetSessionDetailResponse>(`/api/v1/sessions/${encodeURIComponent(sessionId)}`)
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

  // setDockerIsolationEnabled flips the global docker_isolation.enabled
  // flag without resending the full config. Mirrors the PATCH endpoint
  // added on the backend.
  async setDockerIsolationEnabled(enabled: boolean): Promise<APIResponse<ConfigApplyResult>> {
    return this.request<ConfigApplyResult>('/api/v1/config/docker-isolation', {
      method: 'PATCH',
      body: JSON.stringify({ enabled })
    })
  }

  // Token statistics endpoints
  async getTokenStats(): Promise<APIResponse<ServerTokenMetrics>> {
    return this.request<ServerTokenMetrics>('/api/v1/stats/tokens')
  }

  // Tool Call via REST API
  async callTool(toolName: string, args: Record<string, any>): Promise<APIResponse<any>> {
    return this.request<any>('/api/v1/tools/call', {
      method: 'POST',
      body: JSON.stringify({
        tool_name: toolName,
        arguments: args
      })
    })
  }

  // Registry browsing (Phase 7)
  async listRegistries(): Promise<APIResponse<GetRegistriesResponse>> {
    return this.request<GetRegistriesResponse>('/api/v1/registries')
  }

  async searchRegistryServers(
    registryId: string,
    options?: {
      query?: string
      tag?: string
      limit?: number
    }
  ): Promise<APIResponse<SearchRegistryServersResponse>> {
    const params = new URLSearchParams()
    if (options?.query) params.append('q', options.query)
    if (options?.tag) params.append('tag', options.tag)
    if (options?.limit) params.append('limit', options.limit.toString())

    const url = `/api/v1/registries/${encodeURIComponent(registryId)}/servers${params.toString() ? '?' + params.toString() : ''}`
    return this.request<SearchRegistryServersResponse>(url)
  }

  async addServerFromRepository(server: RepositoryServer): Promise<APIResponse<any>> {
    // Use the upstream_servers tool to add the server
    const args: Record<string, any> = {
      operation: 'add',
      name: server.id,
      enabled: true,
      protocol: 'stdio'
    }

    // Determine command and args from installCmd or connectUrl
    if (server.installCmd) {
      const parts = server.installCmd.split(' ')
      args.command = parts[0]
      if (parts.length > 1) {
        args.args_json = JSON.stringify(parts.slice(1))
      }
    } else if (server.url) {
      // Remote server with HTTP protocol
      args.protocol = 'http'
      args.url = server.url
    } else if (server.connectUrl) {
      args.protocol = 'http'
      args.url = server.connectUrl
    }

    return this.callTool('upstream_servers', args)
  }

  // Info endpoint (version and update information)
  async getInfo(opts?: { refresh?: boolean }): Promise<APIResponse<InfoResponse>> {
    const url = opts?.refresh ? '/api/v1/info?refresh=true' : '/api/v1/info'
    return this.request<InfoResponse>(url)
  }

  // Activity Log endpoints (RFC-003)
  async getActivities(params?: {
    type?: string
    server?: string
    tool?: string
    session_id?: string
    status?: string
    intent_type?: string
    start_time?: string
    end_time?: string
    limit?: number
    offset?: number
  }): Promise<APIResponse<ActivityListResponse>> {
    const searchParams = new URLSearchParams()
    if (params) {
      Object.entries(params).forEach(([key, value]) => {
        if (value !== undefined && value !== '') {
          searchParams.append(key, String(value))
        }
      })
    }
    const url = `/api/v1/activity${searchParams.toString() ? '?' + searchParams.toString() : ''}`
    return this.request<ActivityListResponse>(url)
  }

  async getActivityDetail(id: string): Promise<APIResponse<ActivityDetailResponse>> {
    return this.request<ActivityDetailResponse>(`/api/v1/activity/${encodeURIComponent(id)}`)
  }

  async getActivitySummary(period: string = '24h'): Promise<APIResponse<ActivitySummaryResponse>> {
    return this.request<ActivitySummaryResponse>(`/api/v1/activity/summary?period=${period}`)
  }

  getActivityExportUrl(params: {
    format: 'json' | 'csv'
    type?: string
    server?: string
    status?: string
    start_time?: string
    end_time?: string
    include_bodies?: boolean
  }): string {
    const searchParams = new URLSearchParams()
    searchParams.append('format', params.format)
    if (this.apiKey) {
      searchParams.append('apikey', this.apiKey)
    }
    Object.entries(params).forEach(([key, value]) => {
      if (key !== 'format' && value !== undefined && value !== '') {
        searchParams.append(key, String(value))
      }
    })
    return `${this.baseUrl}/api/v1/activity/export?${searchParams.toString()}`
  }

  // Import server configurations
  async importServersFromJSON(params: {
    content: string
    format?: string
    server_names?: string[]
    preview?: boolean
  }): Promise<APIResponse<ImportResponse>> {
    const url = `/api/v1/servers/import/json${params.preview ? '?preview=true' : ''}`
    return this.request<ImportResponse>(url, {
      method: 'POST',
      body: JSON.stringify({
        content: params.content,
        format: params.format,
        server_names: params.server_names
      })
    })
  }

  async importServersFromFile(file: File, params?: {
    format?: string
    server_names?: string[]
    preview?: boolean
  }): Promise<APIResponse<ImportResponse>> {
    const formData = new FormData()
    formData.append('file', file)

    const searchParams = new URLSearchParams()
    if (params?.preview) searchParams.append('preview', 'true')
    if (params?.format) searchParams.append('format', params.format)
    if (params?.server_names?.length) searchParams.append('server_names', params.server_names.join(','))

    const url = `/api/v1/servers/import${searchParams.toString() ? '?' + searchParams.toString() : ''}`

    // Use custom fetch without Content-Type header (let browser set it for FormData)
    try {
      const headers: Record<string, string> = {}
      if (this.apiKey) {
        headers['X-API-Key'] = this.apiKey
      }

      const response = await fetch(`${this.baseUrl}${url}`, {
        method: 'POST',
        headers,
        body: formData
      })

      if (!response.ok) {
        // Extract error message from response body if available
        const errorData = await response.json().catch(() => ({}))
        const errorMsg = errorData.error || `HTTP ${response.status}: ${response.statusText}`
        throw new Error(errorMsg)
      }

      const data = await response.json()
      return data as APIResponse<ImportResponse>
    } catch (error) {
      return {
        success: false,
        error: error instanceof Error ? error.message : 'Unknown error'
      }
    }
  }

  // Get canonical config paths for import hints
  async getCanonicalConfigPaths(): Promise<APIResponse<CanonicalConfigPathsResponse>> {
    return this.request<CanonicalConfigPathsResponse>('/api/v1/servers/import/paths')
  }

  // Import servers from a file path on the server's filesystem
  async importServersFromPath(params: {
    path: string
    format?: string
    server_names?: string[]
    preview?: boolean
  }): Promise<APIResponse<ImportResponse>> {
    const url = `/api/v1/servers/import/path${params.preview ? '?preview=true' : ''}`
    return this.request<ImportResponse>(url, {
      method: 'POST',
      body: JSON.stringify({
        path: params.path,
        format: params.format,
        server_names: params.server_names
      })
    })
  }

  // Agent Token Management (Spec 028)
  async listAgentTokens(): Promise<APIResponse<{ tokens: AgentTokenInfo[] }>> {
    return this.request<{ tokens: AgentTokenInfo[] }>('/api/v1/tokens')
  }

  async createAgentToken(req: CreateAgentTokenRequest): Promise<APIResponse<CreateAgentTokenResponse>> {
    return this.request<CreateAgentTokenResponse>('/api/v1/tokens', {
      method: 'POST',
      body: JSON.stringify(req),
    })
  }

  async revokeAgentToken(name: string): Promise<APIResponse<void>> {
    return this.request<void>(`/api/v1/tokens/${encodeURIComponent(name)}`, {
      method: 'DELETE',
    })
  }

  async regenerateAgentToken(name: string): Promise<APIResponse<{ name: string; token: string }>> {
    return this.request<{ name: string; token: string }>(`/api/v1/tokens/${encodeURIComponent(name)}/regenerate`, {
      method: 'POST',
    })
  }

  // Admin server management (Server edition)
  async adminEnableServer(name: string): Promise<APIResponse<any>> {
    return this.request(`/api/v1/admin/servers/${encodeURIComponent(name)}/enable`, { method: 'POST', credentials: 'include' } as RequestInit)
  }

  async adminDisableServer(name: string): Promise<APIResponse<any>> {
    return this.request(`/api/v1/admin/servers/${encodeURIComponent(name)}/disable`, { method: 'POST', credentials: 'include' } as RequestInit)
  }

  async adminRestartServer(name: string): Promise<APIResponse<any>> {
    return this.request(`/api/v1/admin/servers/${encodeURIComponent(name)}/restart`, { method: 'POST', credentials: 'include' } as RequestInit)
  }

  // User tokens (Server edition)
  async listUserTokens(): Promise<APIResponse<{ tokens: AgentTokenInfo[] }>> {
    return this.request<{ tokens: AgentTokenInfo[] }>('/api/v1/user/tokens', { credentials: 'include' } as RequestInit)
  }

  async createUserToken(data: CreateAgentTokenRequest): Promise<APIResponse<CreateAgentTokenResponse>> {
    return this.request<CreateAgentTokenResponse>('/api/v1/user/tokens', {
      method: 'POST',
      body: JSON.stringify(data),
      headers: { 'Content-Type': 'application/json' },
      credentials: 'include',
    } as RequestInit)
  }

  async revokeUserToken(name: string): Promise<APIResponse<void>> {
    return this.request<void>(`/api/v1/user/tokens/${encodeURIComponent(name)}`, {
      method: 'DELETE',
      credentials: 'include',
    } as RequestInit)
  }

  async regenerateUserToken(name: string): Promise<APIResponse<{ name: string; token: string }>> {
    return this.request<{ name: string; token: string }>(`/api/v1/user/tokens/${encodeURIComponent(name)}/regenerate`, {
      method: 'POST',
      credentials: 'include',
    } as RequestInit)
  }

  // Feedback submission
  async submitFeedback(data: { category: string; message: string; email?: string }): Promise<APIResponse<{ issue_url?: string }>> {
    return this.request<{ issue_url?: string }>('/api/v1/feedback', {
      method: 'POST',
      body: JSON.stringify(data),
    })
  }

  // Connect feature (client registration)
  async getConnectStatus(): Promise<APIResponse<ConnectStatusResponse>> {
    return this.request<ConnectStatusResponse>('/api/v1/connect')
  }

  async connectClient(clientId: string, serverName = 'mcpproxy', force = false): Promise<APIResponse<ConnectResult>> {
    return this.request<ConnectResult>(`/api/v1/connect/${encodeURIComponent(clientId)}`, {
      method: 'POST',
      body: JSON.stringify({ server_name: serverName, force })
    })
  }

  async disconnectClient(clientId: string): Promise<APIResponse<ConnectResult>> {
    return this.request<ConnectResult>(`/api/v1/connect/${encodeURIComponent(clientId)}`, {
      method: 'DELETE',
    })
  }

  // Security Scanner Management (Spec 039)
  async listScanners(): Promise<APIResponse<any[]>> {
    return this.request<any[]>('/api/v1/security/scanners')
  }

  async installScanner(id: string): Promise<APIResponse<void>> {
    return this.request<void>(`/api/v1/security/scanners/${encodeURIComponent(id)}/enable`, {
      method: 'POST',
    })
  }

  async removeScanner(id: string): Promise<APIResponse<void>> {
    return this.request<void>(`/api/v1/security/scanners/${encodeURIComponent(id)}/disable`, {
      method: 'POST',
    })
  }

  async configureScanner(id: string, env: Record<string, string>, dockerImage?: string): Promise<APIResponse<void>> {
    const payload: any = { env }
    if (dockerImage) payload.docker_image = dockerImage
    return this.request<void>(`/api/v1/security/scanners/${encodeURIComponent(id)}/config`, {
      method: 'PUT',
      body: JSON.stringify(payload),
    })
  }

  async getScannerStatus(id: string): Promise<APIResponse<any>> {
    return this.request<any>(`/api/v1/security/scanners/${encodeURIComponent(id)}/status`)
  }

  async startScan(serverName: string, dryRun = false, scannerIds: string[] = []): Promise<APIResponse<any>> {
    return this.request<any>(`/api/v1/servers/${encodeURIComponent(serverName)}/scan`, {
      method: 'POST',
      body: JSON.stringify({ dry_run: dryRun, scanner_ids: scannerIds }),
    })
  }

  async getScanStatus(serverName: string): Promise<APIResponse<any>> {
    return this.request<any>(`/api/v1/servers/${encodeURIComponent(serverName)}/scan/status`)
  }

  async getScanReport(serverName: string): Promise<APIResponse<any>> {
    return this.request<any>(`/api/v1/servers/${encodeURIComponent(serverName)}/scan/report`)
  }

  async getScanFiles(serverName: string, limit = 100, offset = 0, pass = 1): Promise<APIResponse<any>> {
    return this.request<any>(`/api/v1/servers/${encodeURIComponent(serverName)}/scan/files?limit=${limit}&offset=${offset}&pass=${pass}`)
  }

  async cancelScan(serverName: string): Promise<APIResponse<void>> {
    return this.request<void>(`/api/v1/servers/${encodeURIComponent(serverName)}/scan/cancel`, {
      method: 'POST',
    })
  }

  async securityApprove(serverName: string, force = false): Promise<APIResponse<void>> {
    return this.request<void>(`/api/v1/servers/${encodeURIComponent(serverName)}/security/approve`, {
      method: 'POST',
      body: JSON.stringify({ force }),
    })
  }

  async securityReject(serverName: string): Promise<APIResponse<void>> {
    return this.request<void>(`/api/v1/servers/${encodeURIComponent(serverName)}/security/reject`, {
      method: 'POST',
    })
  }

  async checkIntegrity(serverName: string): Promise<APIResponse<any>> {
    return this.request<any>(`/api/v1/servers/${encodeURIComponent(serverName)}/integrity`)
  }

  async getSecurityOverview(): Promise<APIResponse<any>> {
    return this.request<any>('/api/v1/security/overview')
  }

  async scanAll(scannerIds: string[] = []): Promise<APIResponse<any>> {
    return this.request<any>('/api/v1/security/scan-all', {
      method: 'POST',
      body: JSON.stringify({ scanner_ids: scannerIds }),
    })
  }

  async getQueueProgress(): Promise<APIResponse<any>> {
    return this.request<any>('/api/v1/security/queue')
  }

  async cancelAllScans(): Promise<APIResponse<void>> {
    return this.request<void>('/api/v1/security/cancel-all', { method: 'POST' })
  }

  async listScanHistory(params?: { sort?: string; order?: string; limit?: number; offset?: number; status?: string }): Promise<APIResponse<{ scans: any[]; total: number }>> {
    const q = new URLSearchParams()
    if (params?.sort) q.set('sort', params.sort)
    if (params?.order) q.set('order', params.order)
    if (params?.limit) q.set('limit', String(params.limit))
    if (params?.offset) q.set('offset', String(params.offset))
    if (params?.status) q.set('status', params.status)
    const qs = q.toString()
    return this.request<{ scans: any[]; total: number }>(`/api/v1/security/scans${qs ? '?' + qs : ''}`)
  }

  async getScanReportByJobId(jobId: string): Promise<APIResponse<any>> {
    return this.request<any>(`/api/v1/security/scans/${encodeURIComponent(jobId)}/report`)
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

// Canonical config path types
export interface CanonicalConfigPath {
  name: string
  format: string
  path: string
  exists: boolean
  os: string
  description: string
}

export interface CanonicalConfigPathsResponse {
  os: string
  paths: CanonicalConfigPath[]
}

export default new APIService()
