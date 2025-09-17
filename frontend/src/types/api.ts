// API Response types
export interface APIResponse<T = any> {
  success: boolean
  data?: T
  error?: string
}

// Server types
export interface Server {
  name: string
  url?: string
  command?: string
  protocol: 'http' | 'stdio' | 'streamable-http'
  enabled: boolean
  quarantined: boolean
  connected: boolean
  connecting: boolean
  tool_count: number
  last_error: string
}

// Tool types
export interface Tool {
  name: string
  description: string
  server: string
  input_schema?: Record<string, any>
}

// Search result types
export interface SearchResult {
  name: string
  description: string
  server: string
  score: number
  input_schema?: Record<string, any>
}

// Status types
export interface StatusUpdate {
  running: boolean
  listen_addr: string
  upstream_stats: {
    connected_servers: number
    total_servers: number
    total_tools: number
  }
  status: Record<string, any>
  timestamp: number
}

// Dashboard stats
export interface DashboardStats {
  servers: {
    total: number
    connected: number
    enabled: number
    quarantined: number
  }
  tools: {
    total: number
    available: number
  }
  system: {
    uptime: string
    version: string
    memory_usage?: string
  }
}