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

// Secret management types
export interface SecretRef {
  type: string      // "env", "keyring", etc.
  name: string      // The secret name/key
  original: string  // Original reference string like "${env:API_KEY}"
}

export interface MigrationCandidate {
  field: string      // Field path in configuration
  value: string      // Masked value for display
  suggested: string  // Suggested secret reference
  confidence: number // Confidence score (0.0 to 1.0)
  migrating?: boolean // UI state for migration in progress
}

export interface MigrationAnalysis {
  candidates: MigrationCandidate[]
  total_found: number
}