// API Response types
export interface APIResponse<T = any> {
  success: boolean
  data?: T
  error?: string
}

// Health Status types (unified across all interfaces)
export interface HealthStatus {
  level: 'healthy' | 'degraded' | 'unhealthy'
  admin_state: 'enabled' | 'disabled' | 'quarantined'
  summary: string
  detail?: string
  action?: 'login' | 'restart' | 'enable' | 'approve' | 'view_logs' | 'set_secret' | 'configure' | ''
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
  authenticated?: boolean
  tool_count: number
  last_error?: string
  tool_list_token_size?: number
  oauth?: {
    client_id: string
    auth_url: string
    token_url: string
  }
  oauth_status?: 'authenticated' | 'expired' | 'error' | 'none'
  token_expires_at?: string
  user_logged_out?: boolean // True if user explicitly logged out (prevents auto-reconnection)
  health?: HealthStatus // Unified health status calculated by the backend
}

// Tool Annotation types
export interface ToolAnnotation {
  title?: string
  readOnlyHint?: boolean
  destructiveHint?: boolean
  idempotentHint?: boolean
  openWorldHint?: boolean
}

// MCP Session types
export interface MCPSession {
  id: string
  client_name?: string
  client_version?: string
  status: 'active' | 'closed'
  start_time: string  // ISO 8601
  end_time?: string   // ISO 8601
  last_activity: string  // ISO 8601
  tool_call_count: number
  total_tokens: number
  // MCP Client Capabilities
  has_roots?: boolean
  has_sampling?: boolean
  experimental?: string[]
}

// Tool types
export interface Tool {
  name: string
  description: string
  server: string
  input_schema?: Record<string, any>
  annotations?: ToolAnnotation
}

// Search result types
export interface SearchResult {
  tool: {
    name: string
    description: string
    server_name: string
    input_schema?: Record<string, any>
    usage?: number
    last_used?: string
  }
  score: number
  snippet?: string
  matches: number
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

export interface EnvVarStatus {
  secret_ref: SecretRef
  is_set: boolean
}

export interface KeyringSecretStatus {
  secret_ref: SecretRef
  is_set: boolean
}

export interface ConfigSecretsResponse {
  secrets: KeyringSecretStatus[]
  environment_vars: EnvVarStatus[]
  total_secrets: number
  total_env_vars: number
}

// Tool Call History types
export interface TokenMetrics {
  input_tokens: number        // Tokens in the request
  output_tokens: number       // Tokens in the response
  total_tokens: number        // Total tokens (input + output)
  model: string               // Model used for tokenization
  encoding: string            // Encoding used (e.g., cl100k_base)
  estimated_cost?: number     // Optional cost estimate
  truncated_tokens?: number   // Tokens removed by truncation
  was_truncated: boolean      // Whether response was truncated
}

export interface ServerTokenMetrics {
  total_server_tool_list_size: number
  average_query_result_size: number
  saved_tokens: number
  saved_tokens_percentage: number
  per_server_tool_list_sizes: Record<string, number>
}

export interface ToolCallRecord {
  id: string
  server_id: string
  server_name: string
  tool_name: string
  arguments: Record<string, any>
  response?: any
  error?: string
  duration: number  // nanoseconds
  timestamp: string  // ISO 8601 date string
  config_path: string
  request_id?: string
  metrics?: TokenMetrics  // Token usage metrics (optional for older records)
  parent_call_id?: string  // Links nested calls to parent code_execution
  execution_type?: string  // "direct" or "code_execution"
  mcp_session_id?: string  // MCP session identifier
  mcp_client_name?: string  // MCP client name from InitializeRequest
  mcp_client_version?: string  // MCP client version
  annotations?: ToolAnnotation  // Tool behavior hints snapshot
}

export interface GetToolCallsResponse {
  tool_calls: ToolCallRecord[]
  total: number
  limit: number
  offset: number
}

export interface GetToolCallDetailResponse {
  tool_call: ToolCallRecord
}

export interface GetServerToolCallsResponse {
  server_name: string
  tool_calls: ToolCallRecord[]
  total: number
}

// Session response types
export interface GetSessionsResponse {
  sessions: MCPSession[]
  total: number
  limit: number
  offset: number
}

export interface GetSessionDetailResponse {
  session: MCPSession
}

// Configuration management types
export interface ValidationError {
  field: string
  message: string
}

export interface ConfigApplyResult {
  success: boolean
  applied_immediately: boolean
  requires_restart: boolean
  restart_reason?: string
  validation_errors?: ValidationError[]
  changed_fields?: string[]
}

export interface GetConfigResponse {
  config: any  // The full configuration object
  config_path: string
}

export interface ValidateConfigRequest {
  config: any
}

export interface ValidateConfigResponse {
  valid: boolean
  errors?: ValidationError[]
}

export interface ApplyConfigRequest {
  config: any
}

// Registry browsing types (Phase 7)

export interface Registry {
  id: string
  name: string
  description: string
  url: string
  servers_url?: string
  tags?: string[]
  protocol?: string
  count?: number | string
}

export interface NPMPackageInfo {
  exists: boolean
  install_cmd: string
}

export interface RepositoryInfo {
  npm?: NPMPackageInfo
  // Future: pypi, docker_hub, etc.
}

export interface RepositoryServer {
  id: string
  name: string
  description: string
  url?: string  // MCP endpoint for remote servers only
  source_code_url?: string  // Source repository URL
  installCmd?: string  // Installation command
  connectUrl?: string  // Alternative connection URL
  updatedAt?: string
  createdAt?: string
  registry?: string  // Which registry this came from
  repository_info?: RepositoryInfo  // Detected package info
}

export interface GetRegistriesResponse {
  registries: Registry[]
  total: number
}

export interface SearchRegistryServersResponse {
  registry_id: string
  servers: RepositoryServer[]
  total: number
  query?: string
  tag?: string
}

// Activity Log types (RFC-003)

export type ActivityType =
  | 'tool_call'
  | 'policy_decision'
  | 'quarantine_change'
  | 'server_change'

export type ActivitySource = 'mcp' | 'cli' | 'api'

export type ActivityStatus = 'success' | 'error' | 'blocked'

export interface ActivityRecord {
  id: string
  type: ActivityType
  source?: ActivitySource
  server_name?: string
  tool_name?: string
  arguments?: Record<string, any>
  response?: string
  response_truncated?: boolean
  status: ActivityStatus
  error_message?: string
  duration_ms?: number
  timestamp: string
  session_id?: string
  request_id?: string
  metadata?: Record<string, any>
}

export interface ActivityListResponse {
  activities: ActivityRecord[]
  total: number
  limit: number
  offset: number
}

export interface ActivityDetailResponse {
  activity: ActivityRecord
}

export interface ActivityTopServer {
  name: string
  count: number
}

export interface ActivityTopTool {
  server: string
  tool: string
  count: number
}

export interface ActivitySummaryResponse {
  period: string
  total_count: number
  success_count: number
  error_count: number
  blocked_count: number
  top_servers?: ActivityTopServer[]
  top_tools?: ActivityTopTool[]
  start_time: string
  end_time: string
}