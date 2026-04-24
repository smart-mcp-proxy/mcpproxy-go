// Re-export common types from contracts (generated from Go constants)
export type { APIResponse, HealthStatus, HealthLevel, AdminState, HealthAction } from './contracts'
export {
  HealthLevelHealthy,
  HealthLevelDegraded,
  HealthLevelUnhealthy,
  AdminStateEnabled,
  AdminStateDisabled,
  AdminStateQuarantined,
  HealthActionNone,
  HealthActionLogin,
  HealthActionRestart,
  HealthActionEnable,
  HealthActionApprove,
  HealthActionViewLogs,
  HealthActionSetSecret,
  HealthActionConfigure,
} from './contracts'

// Import HealthStatus for use in this file
import type { HealthStatus } from './contracts'

// Quarantine stats for tool-level quarantine (Spec 032)
export interface QuarantineStats {
  pending_count: number
  changed_count: number
}

// Security scan summary (Spec 039)
export type SecurityScanStatus = 'clean' | 'warnings' | 'dangerous' | 'failed' | 'not_scanned' | 'scanning'

export interface SecurityScanFindingCounts {
  dangerous: number
  warning: number
  info: number
  total: number
}

export interface SecurityScanSummary {
  last_scan_at?: string
  risk_score: number
  status: SecurityScanStatus
  finding_counts?: SecurityScanFindingCounts
}

// Security scan finding (Spec 039)
export type ThreatType = 'tool_poisoning' | 'prompt_injection' | 'rug_pull' | 'supply_chain' | 'malicious_code' | 'uncategorized'
export type ThreatLevel = 'dangerous' | 'warning' | 'info'

export interface SecurityScanFinding {
  rule_id?: string
  severity?: string             // critical, high, medium, low, info
  category?: string
  threat_type: ThreatType
  threat_level: ThreatLevel
  title: string
  description: string
  location?: string
  scanner?: string              // Scanner that found this
  help_uri?: string             // Link to CVE/advisory
  cvss_score?: number           // CVSS score (0-10)
  package_name?: string
  installed_version?: string
  fixed_version?: string        // Version with fix
  scan_pass?: number            // 1 = security scan, 2 = supply chain audit
  evidence?: string             // Text/content that triggered the finding
  supply_chain_audit?: boolean  // True for real CVE/package findings — routes to the Supply Chain (CVEs) section regardless of scan_pass
}

export interface SecurityScanReport {
  job_id?: string
  server_name: string
  status: SecurityScanStatus
  risk_score: number
  findings: SecurityScanFinding[]
  finding_counts: SecurityScanFindingCounts
  summary: SecurityScanReportSummary
  scanned_at: string
  duration_ms?: number
  scanners_used?: string[]
  // Scan completion tracking
  scanners_run?: number     // How many scanners actually produced results
  scanners_failed?: number  // How many scanners failed
  scanners_total?: number   // Total scanners attempted
  scan_complete?: boolean   // True only if at least one scanner succeeded
  empty_scan?: boolean      // True when scanners ran but had no files to analyze
  // Two-pass scan tracking
  pass1_complete?: boolean  // Security scan (fast) done
  pass2_complete?: boolean  // Supply chain audit done
  pass2_running?: boolean   // Supply chain audit in progress
}

// Scan job summary for history listing
export interface ScanJobSummary {
  id: string
  server_name: string
  status: string
  scan_pass: number
  started_at: string
  completed_at?: string
  findings_count: number
  risk_score: number
  scanners: string[]
}

// Summary from the aggregated report API (matches Go ReportSummary)
export interface SecurityScanReportSummary {
  critical: number
  high: number
  medium: number
  low: number
  info: number
  total: number
  dangerous: number   // Threat level counts
  warnings: number
  info_level: number
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
  quarantine?: QuarantineStats // Tool-level quarantine stats (Spec 032)
  security_scan?: SecurityScanSummary // Security scan summary (Spec 039)
  // Spec 044: structured diagnostic error + stable error code
  error_code?: string
  diagnostic?: Diagnostic | null
}

// Spec 044 — diagnostics & error taxonomy types.
export type DiagnosticSeverity = 'info' | 'warn' | 'error'
export type DiagnosticFixStepType = 'link' | 'command' | 'button'

export interface DiagnosticFixStep {
  type: DiagnosticFixStepType
  label: string
  command?: string
  url?: string
  fixer_key?: string
  destructive?: boolean
}

export interface Diagnostic {
  code: string
  severity: DiagnosticSeverity
  cause?: string
  detected_at?: string
  user_message?: string
  fix_steps?: DiagnosticFixStep[]
  docs_url?: string
}

export interface DiagnosticFixResponse {
  outcome: 'success' | 'failed' | 'blocked'
  duration_ms: number
  mode: 'dry_run' | 'execute'
  preview?: string
  failure_msg?: string
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

// Tool approval types (Spec 032)
export interface ToolApproval {
  server_name: string
  tool_name: string
  status: 'pending' | 'approved' | 'changed'
  hash: string
  description: string
  schema?: string
  approved_hash?: string
  current_hash?: string
  previous_description?: string
  current_description?: string
  previous_schema?: string
  current_schema?: string
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
  routing_mode?: string
  upstream_stats: {
    connected_servers: number
    total_servers: number
    total_tools: number
  }
  status: Record<string, any>
  timestamp: number
}

// Routing mode types
export interface RoutingInfo {
  routing_mode: string
  description: string
  endpoints: {
    default: string
    direct: string
    code_execution: string
    retrieve_tools: string
  }
  available_modes: string[]
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
  // Spec 026: Sensitive data detection fields
  has_sensitive_data?: boolean
  detection_types?: string[]
  max_severity?: 'critical' | 'high' | 'medium' | 'low'
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

// Agent Token types (Spec 028)

export interface AgentTokenInfo {
  name: string
  token_prefix: string
  allowed_servers: string[]
  permissions: string[]
  expires_at: string
  created_at: string
  last_used_at: string | null
  revoked: boolean
}

export interface CreateAgentTokenRequest {
  name: string
  allowed_servers: string[]
  permissions: string[]
  expires_in?: string
}

export interface CreateAgentTokenResponse {
  name: string
  token: string
  allowed_servers: string[]
  permissions: string[]
  expires_at: string
  created_at: string
}

// Import server configuration types

export interface ImportSummary {
  total: number
  imported: number
  skipped: number
  failed: number
}

export interface ImportedServer {
  name: string
  protocol: string
  url?: string
  command?: string
  args?: string[]
  source_format: string
  original_name: string
  fields_skipped?: string[]
  warnings?: string[]
}

export interface SkippedServer {
  name: string
  reason: string
}

export interface FailedServer {
  name: string
  error: string
}

export interface ImportResponse {
  format: string
  format_name: string
  summary: ImportSummary
  imported: ImportedServer[]
  skipped: SkippedServer[]
  failed: FailedServer[]
  warnings: string[]
}

// Connect feature types (client registration)

// API returns a flat array of ClientStatus objects in the data field
export type ConnectStatusResponse = ClientStatus[]

export interface ClientStatus {
  id: string
  name: string
  config_path: string
  exists: boolean
  connected: boolean
  supported: boolean
  reason?: string
  icon: string
}

export interface ConnectResult {
  success: boolean
  client: string
  config_path: string
  backup_path?: string
  server_name: string
  action: string
  message: string
  error?: string
}
