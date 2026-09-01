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
  blocked_count: number
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
  // Scanner coverage for the primary (baseline) scan pass — informational only.
  // Spec 077 US3 (FR-008/FR-014): status derives SOLELY from baseline findings;
  // a failed Docker deep scanner never downgrades the verdict.
  scanners_run?: number
  scanners_failed?: number
  scanners_total?: number
  // Opt-in deep-scan layer status (Spec 077 US3), always emitted on a computed
  // summary (enabled=false when off). Informational — never influences status.
  deep_scan?: DeepScanDescriptor
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
  // Spec 077 unified report — additive fields.
  sources?: string[]            // Contributing scanner ids; ≥2 means scanners agreed (consensus)
  tier?: string                 // "hard" (gates approval) | "soft" (review-only)
  confidence?: number           // 0.0–1.0; raised when independent sources agree
  signals?: string[]            // Deterministic detect check ids that fired
}

export interface SecurityScanReport {
  job_id?: string
  server_name: string
  status: SecurityScanStatus
  risk_score: number
  findings: SecurityScanFinding[]
  // Tier-driven, baseline-only verdict (Spec 077 FR-014): 'dangerous' only for
  // hard-tier baseline findings; tierless deep-scan/external findings never
  // move it. Verdict-bearing UI must read this, NOT summary (raw counts).
  verdict?: 'clean' | 'warnings' | 'dangerous'
  // Tier-driven buckets matching SecurityScanSummary.finding_counts (a tierless
  // 'dangerous' finding buckets as warning — informs, never gates).
  finding_counts?: SecurityScanFindingCounts
  // Raw threat-level/severity counts across ALL findings — transparency only.
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
  // Opt-in deep-scan availability (Spec 077 US3). Informational only — a failed
  // or unavailable deep scanner never changes the baseline verdict/status.
  deep_scan?: DeepScanDescriptor
}

// DeepScanDescriptor reports the informational status of the opt-in "deep scan"
// layer (Docker-based scanners + source extraction) separately from the
// baseline verdict (Spec 077 US3). Rendered as a quiet info note, never an error.
export interface DeepScanDescriptor {
  enabled: boolean
  ran: boolean
  available: boolean
  scanners_failed?: { id: string; reason: string }[]
  // Docker scanners the user enabled that are skipped because deep scan is off.
  // Only populated when enabled=false (the descriptor is always emitted).
  skipped_scanners?: string[]
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
export interface ServerIsolationConfig {
  // EFFECTIVE isolation state, after global + per-server + structural
  // resolution. NOT the raw per-server override — read enabled_override for
  // that. Always present on stdio servers.
  enabled: boolean
  // RAW per-server override. Absent = inherit the global setting, which is a
  // distinct state from an explicit false.
  enabled_override?: boolean
  // RAW per-server mode override. Absent = inherit.
  mode_override?: string
  image?: string
  network_mode?: string
  extra_args?: string[]
  memory_limit?: string
  cpu_limit?: string
  working_dir?: string
  timeout?: string
}

// IsolationDefaults reports the resolved baseline Docker isolation
// values the backend will apply when no per-server override is set.
// Used as placeholders so "empty = inherit" is discoverable in the UI.
// IsolationEffective reports the resolved isolation state of a server and the
// rule that produced it, so the UI can explain "inherits global: docker"
// instead of rendering an ambiguous toggle. Read-only; never sent on PATCH.
export interface ServerIsolationEffective {
  mode: string // 'docker' | 'sandbox' | 'none' — what the spawn path branches on
  isolated: boolean // actually CONFINED; not simply mode !== 'none' (see 'sandbox-unavailable')
  global_mode?: string
  inherited: boolean
  // 'global' | 'server-mode' | 'server-opt-out' | 'server-opt-in-ignored' |
  // 'not-stdio' | 'already-docker' | 'sandbox-unavailable' | 'unsupported-mode'.
  // Treat unknown values as 'global'.
  source?: string
}

export interface ServerIsolationDefaults {
  runtime_type?: string
  image?: string
  network_mode?: string
  extra_args?: string[]
  working_dir?: string
}

export interface Server {
  name: string
  // Human-friendly display label from the source registry (MCP-1112). When
  // present it is preferred over `name` for display; `name` stays the stable
  // identifier used for routing and API calls (it may be a reverse-DNS id such
  // as "io.github.owner/repo").
  title?: string
  url?: string
  command?: string
  args?: string[]
  working_dir?: string
  env?: Record<string, string>
  // Static headers sent with every request to HTTP / streamable-http
  // servers. Server-side redaction replaces sensitive values with
  // `***REDACTED***` unless `reveal_secret_headers: true` is set on the
  // loaded config (see internal/httpapi/server.go:redactServerHeaders).
  headers?: Record<string, string>
  protocol: 'http' | 'stdio' | 'streamable-http'
  enabled: boolean
  quarantined: boolean
  // Per-server intent to auto-approve tool changes/additions (MCP-2930).
  // When true, rug-pull protection is disabled for this server: changed and
  // newly-added tools are trusted automatically instead of held for review.
  // Optional because the REST status payload only includes it once the
  // backend exposes the flag; absent/undefined is treated as OFF (protected).
  auto_approve_tool_changes?: boolean
  // Per-server approval trust mode (spec 086: 'auto' | 'scan' | 'manual').
  // Raw configured value exactly as delivered — absent when unset, possibly a
  // value migrated from the legacy flags above, possibly an unrecognized
  // hand-edited string. Resolve with utils/trustMode.ts (fail-closed to
  // 'manual') before making any UI decision (spec 088 FR-001).
  trust_mode?: string
  connected: boolean
  connecting: boolean
  authenticated?: boolean
  tool_count: number
  last_error?: string
  tool_list_token_size?: number
  connected_at?: string // ISO 8601 timestamp of last successful connect
  last_reconnect_at?: string // ISO 8601 timestamp of last reconnect attempt
  reconnect_count?: number
  isolation?: ServerIsolationConfig // Per-server Docker isolation override
  isolation_defaults?: ServerIsolationDefaults // Resolved baseline values (read-only)
  isolation_effective?: ServerIsolationEffective // Resolved isolation state + why (read-only)
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

// Global Tools response types (Spec 050)
export interface GlobalTool {
  name: string
  server_name: string
  description: string
  approval_status: string  // "pending" | "changed" | "approved" | ""
  disabled: boolean        // per-tool user toggle
  config_denied: boolean   // layered config (read-only)
  usage: number
  last_used?: string       // ISO 8601; omitted if never used in window
  annotations?: ToolAnnotation
  // Hold evidence (Spec 086 FR-018, surfaced by Spec 088 FR-008): present only
  // on tools the trust gate refused to auto-approve. GET /api/v1/tools emits
  // these alongside the approval status (internal/httpapi/server.go); records
  // predating Spec 086 omit them entirely, which must render unchanged.
  held_reason?: string     // "scan_findings" (threat) | "scan_coverage" (precaution)
  held_verdict?: string    // "dangerous" | "warnings" | "clean"
  held_signals?: string[]  // matched deterministic check ids, producer order, ≤16
  // derived locally: enabled = !disabled && !config_denied
}

export interface GlobalToolsStats {
  total: number
  enabled: number
  disabled: number
  pending_approval: number
}

export interface GlobalToolsResponse {
  tools: GlobalTool[]
  stats: GlobalToolsStats
  partial: boolean
  failed_servers: string[]
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
  // Spec 082: the project the client is working in (basename only — the full
  // local path never leaves the machine), and the work session this connection
  // belongs to.
  workspace_name?: string
  work_session_id?: string
}

// Tool types
export interface Tool {
  name: string
  description: string
  server: string
  server_name?: string
  input_schema?: Record<string, any>
  schema?: Record<string, any>
  annotations?: ToolAnnotation
  usage?: number
  last_used?: string
  approval_status?: string
  disabled?: boolean
  config_denied?: boolean
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
  // Output schema diff fields (MCP-2096 / PR #638): exposed by
  // GET /api/v1/servers/{id}/tools/{tool}/diff so the approval UI can render
  // an Output-Schema diff section, not just description/input-schema.
  previous_output_schema?: string
  current_output_schema?: string
  enabled?: boolean
  disabled?: boolean
  // Scan-gate hold evidence (spec 086 FR-018, surfaced by spec 088). The
  // durable export payload does NOT carry these — api.getToolApprovals joins
  // them on from GET /api/v1/servers/{id}/tools; the diff endpoint returns them
  // directly. Absent on records that are not currently held by the scan gate
  // and on every record written before spec 086, which must render unchanged.
  //   held_reason:  'scan_findings' | 'scan_coverage'
  //   held_verdict: 'dangerous' | 'warnings' | 'clean'
  //   held_signals: matched check ids, e.g. 'tpa.TPA-2026-0001.hidden_instruction'
  held_reason?: string
  held_verdict?: string
  held_signals?: string[]
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
  // Unix seconds at which the core process started. Absent on older cores and
  // on SSE status frames that don't carry it — treat it as optional.
  started_at?: number
}

// Routing mode types
export interface RoutingInfo {
  /** The mode /mcp is ACTUALLY serving (in-memory config), not the configured intent. */
  routing_mode: string
  description: string
  endpoints: {
    default: string
    direct: string
    code_execution: string
    retrieve_tools: string
  }
  available_modes: string[]
  /** Spec 085 serialization of retrieve_tools results — resolved, never empty. */
  tool_response_mode?: string
  /** Spec 102 serialization of direct-surface listings — resolved, never empty. */
  direct_tool_response_mode?: string
  /**
   * Routing mode persisted on disk when it differs from the served one, i.e.
   * the mode a restart would adopt. Empty when there is nothing pending.
   */
  pending_routing_mode?: string
  /** True when pending_routing_mode is set — a restart is needed to apply it. */
  restart_required?: boolean
  /**
   * Whether the code_execution tool is enabled. The code-execution surface has
   * no other tool-calling path, so this gates whether that routing mode can
   * work at all. Absent on daemons that predate the field.
   */
  code_execution_enabled?: boolean
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

// Usage statistics aggregate — GET /api/v1/activity/usage (Spec 069).
// Mirrors contracts.UsageAggregateResponse. Per-tool metrics are
// lifetime-cumulative; `window` scopes the timeline + the tool-list membership.
export interface UsageToolStat {
  server: string
  tool: string
  calls: number
  errors: number
  error_rate: number
  blocked: number
  rejected: number                // Spec 093: shed by a concurrency limit; never executed
  total_resp_bytes: number
  avg_resp_bytes: number | null   // null when only legacy 0-byte calls exist
  total_req_bytes: number
  avg_req_bytes: number | null
  sized_calls: number
  // Bucket BOUNDS, not measurements: the true percentile is at or below the
  // value, except in the unbounded overflow bucket, where *_exceeds flips it to
  // a floor. Render through formatLatencyBound (audit finding F22, #1046).
  p50_ms: number
  p50_exceeds: boolean
  p95_ms: number
  p95_exceeds: boolean
  last_used: string
}

export interface UsageOtherBucket {
  tools_folded: number
  calls: number
  total_resp_bytes: number
}

export interface UsageTimeBucket {
  start: string
  calls: number
  errors: number
  total_resp_bytes: number
}

export interface UsageAggregateResponse {
  window: string
  generated_at: string
  freshness_ms: number
  token_source: string              // "bytes" — size-based proxy (FR-006)
  tokens_saved: number              // echoed from ServerTokenMetrics (FR-007)
  tokens_saved_percentage: number
  tools: UsageToolStat[]
  other?: UsageOtherBucket | null   // present only when list truncated to top-N
  timeline: UsageTimeBucket[]
  // Headline counts for the window, computed server-side as the sum of the
  // timeline above. NOT a sum of `tools`: that list is lifetime-cumulative,
  // upstream-only and truncated to top-N. Same population as
  // ActivitySummaryResponse.call_count (audit finding F1, #1046).
  total_calls: number
  total_errors: number
}

export type UsageWindow = '24h' | '7d' | 'all'
export type UsageSort = 'calls' | 'resp_bytes' | 'error_rate' | 'p95'
export type UsageStatus = 'success' | 'error' | 'blocked' | 'rejected'

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
  // MCP-866: trust tag — "official/trusted" for built-in defaults,
  // "custom/unverified" for user-added sources. Derived server-side from
  // membership in the default set, never from self-assertion in config.
  provenance?: string
  // Convenience boolean mirror of provenance === "official/trusted".
  trusted?: boolean
}

// MCP-866 trust-tag constants (mirror config.RegistryProvenance*).
export const REGISTRY_PROVENANCE_OFFICIAL = 'official/trusted'
export const REGISTRY_PROVENANCE_CUSTOM = 'custom/unverified'

// RegistrySummary is the slim projection returned by POST /api/v1/registries
// (add-source). Mirrors contracts.RegistrySummary.
export interface RegistrySummary {
  id: string
  name: string
  url?: string
  servers_url?: string
  protocol?: string
  provenance?: string
  trusted?: boolean
}

export interface NPMPackageInfo {
  exists: boolean
  install_cmd: string
}

export interface RepositoryInfo {
  npm?: NPMPackageInfo
  // Future: pypi, docker_hub, etc.
}

// RequiredInput declares an env var / key a server needs before it can run.
// Spec 070: detected server-side (explicit registry fields + ${VAR} heuristic)
// and used by the Web UI to prompt the user before adding. Mirrors
// registries.RequiredInput.
export interface RequiredInput {
  name: string
  description?: string
  secret?: boolean
}

export interface RepositoryServer {
  id: string
  name: string
  description: string
  url?: string  // MCP endpoint for remote servers only
  source_code_url?: string  // Source repository URL
  install_cmd?: string  // Installation command (matches backend contracts.RepositoryServer)
  connect_url?: string  // Alternative connection URL (matches backend contracts.RepositoryServer)
  updatedAt?: string
  createdAt?: string
  registry?: string  // Which registry this came from
  repository_info?: RepositoryInfo  // Detected package info
  required_inputs?: RequiredInput[]  // Spec 070: env/keys the user must supply before add
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
  /**
   * Spec 098: one executed required-tools preflight. Set-scoped, not
   * server-scoped — server_name/tool_name are empty and the verdict,
   * requested-id count and per-tool reason codes live in `metadata`
   * ({verdict, ids_count, reasons{code:count}, per_tool[{id,status,reason?}]}).
   */
  | 'preflight'

export type ActivitySource = 'mcp' | 'cli' | 'api'

/**
 * Closed activity-status vocabulary. 'rejected' (Spec 093) means the call was
 * shed by a concurrency limit before it reached the upstream — proxy
 * backpressure, not an upstream failure.
 */
export type ActivityStatus = 'success' | 'error' | 'blocked' | 'rejected'

/** Spec 093: machine-readable cause of a rejection (activity metadata). */
export type RejectionReason = 'queue_full' | 'queue_timeout'
/** Spec 093: which limiter tier shed the call (activity metadata). */
export type RejectionScope = 'server' | 'global'

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
  /** Spec 082: one client, one project, across reconnects. Absent on pre-082 records. */
  work_session_id?: string
  request_id?: string
  /**
   * Correlation id of the parent `code_execution` call this record was
   * dispatched from — equal to the parent record's `request_id`. Present only on
   * sandboxed sub-calls; a top-level call omits it. Navigate parent → children
   * with `?parent_id=<parent request_id>`, child → parent with
   * `?request_id=<child parent_id>`.
   */
  parent_id?: string
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
  rejected_count: number
  // Rows whose status is outside the tool-call vocabulary — a quarantine
  // change's action, a policy decision's verdict. Present so that
  // success + error + blocked + rejected + other == total_count, which is what
  // makes the status tiles a partition instead of four numbers that add up to
  // less than the denominator printed beside them (audit finding F2, #1046).
  other_count: number
  // total_count is how many ROWS the log has in the period; call_count is how
  // many of them are calls the user made (the rest — system starts, security
  // scans, quarantine auto-approvals, management chatter — are events). The
  // Usage tab counts the second population, so the header must label the two
  // apart or the same instance reports different totals on two screens
  // (audit finding F1/F24, #1046).
  call_count: number
  call_error_count: number
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

// AccessState classifies a per-client config content access (Spec 075). The
// stat-only overall listing leaves it 'unknown' (no eager read); the on-demand
// per-client GET / connect / disconnect paths resolve it to one of the others.
export type AccessState = 'unknown' | 'accessible' | 'absent' | 'denied' | 'malformed'

export interface ClientStatus {
  id: string
  name: string
  config_path: string
  exists: boolean
  connected: boolean
  supported: boolean
  reason?: string
  note?: string
  bridge?: boolean
  icon: string
  server_name?: string
  // Spec 075 (additive): per-client content access classification and, when
  // access_state === 'denied', actionable remediation text (the macOS App-Data
  // privacy fix including the exact tccutil reset command).
  access_state?: AccessState
  remediation?: string
  // Every config location the existence check consults, highest precedence
  // first (e.g. OpenCode's opencode.jsonc then opencode.json).
  checked_paths?: string[]
  // This instance's own MCP endpoint. Config-derived, so it is present on both
  // the stat-only listing and the on-demand per-client read.
  proxy_url?: string
  // The endpoint the client's existing entry actually points at, projected
  // through the same sanitizer as a connect preview's entry summary: scheme,
  // host and path only (query — the ?apikey= carrier — userinfo and fragment
  // are dropped). Resolved only by the on-demand read.
  registered_url?: string
  // How registered_url relates to proxy_url. `connected` only ever meant "an
  // mcpproxy-shaped entry exists", and an entry merely NAMED mcpproxy counts —
  // so a row can be connected to a different instance entirely (audit F18).
  endpoint_match?: EndpointMatch
}

// How a client's registered endpoint relates to this instance (audit F18).
export type EndpointMatch = 'this' | 'other' | 'unknown'

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

// Spec 078 US1: the exact change a connect would make, returned WITHOUT writing
// the file or creating a backup. entry/entry_text carry a MASKED apikey;
// contains_api_key flags that a credential is written; entry_exists marks the
// overwrite (force) case; access_state degrades per Spec 075.
export interface ConnectPreview {
  client: string
  config_path: string
  format: 'json' | 'toml'
  server_key: string
  server_name: string
  entry: Record<string, unknown>
  entry_text: string
  entry_exists: boolean
  contains_api_key: boolean
  bridge?: boolean
  access_state?: AccessState
}

// Onboarding wizard types (Spec 046)
export interface OnboardingState {
  engaged: boolean
  first_shown_at?: string
  engaged_at?: string
  connect_step_status?: '' | 'completed' | 'skipped'
  server_step_status?: '' | 'completed' | 'skipped'
}

export interface OnboardingStateResponse {
  has_connected_client: boolean
  has_configured_server: boolean
  connected_client_count: number
  connected_client_ids: string[]
  configured_server_count: number
  state: OnboardingState
  should_show_wizard: boolean
  // Spec 046 v2 — passive Verify tab + sidebar badge
  first_mcp_client_ever: boolean
  mcp_clients_seen_ever: string[]
  incomplete_tab_count: number
}

export interface OnboardingMarkRequest {
  engaged?: boolean
  connect_step_status?: '' | 'completed' | 'skipped'
  server_step_status?: '' | 'completed' | 'skipped'
  mark_shown?: boolean
}

// Profiles v2 (MCP-3243 / T4): a profile scopes tool discovery + calls to a
// named subset of upstream servers. Mirrors httpapi.ProfileSummary from the
// GET /api/v1/profiles listing (MCP-3241).
export interface ProfileSummary {
  name: string
  servers: string[]
  tool_count: number
}

export interface ListProfilesResponse {
  profiles: ProfileSummary[]
}

// Server-level default active profile used by UI surfaces (Web UI / tray).
// Empty string means "all servers". A live MCP session's set_profile selection
// takes precedence over this default.
export interface ActiveProfileResponse {
  active_profile: string
}
