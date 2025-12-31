/**
 * Activity API Contract
 *
 * TypeScript interfaces for Activity Log Web UI
 * Branch: 019-activity-webui
 * Date: 2025-12-29
 *
 * These interfaces define the API contract between the frontend and backend.
 * They are generated from the backend contracts in internal/contracts/activity.go
 */

// ============================================================================
// Enumerations
// ============================================================================

/**
 * Type of activity being recorded
 */
export type ActivityType =
  | 'tool_call'          // Tool execution event
  | 'policy_decision'    // Policy blocking a tool call
  | 'quarantine_change'  // Server quarantine state change
  | 'server_change'      // Server configuration change

/**
 * Source that triggered the activity
 */
export type ActivitySource =
  | 'mcp'  // MCP protocol (AI agent)
  | 'cli'  // CLI command
  | 'api'  // REST API

/**
 * Result status of the activity
 */
export type ActivityStatus = 'success' | 'error' | 'blocked'

// ============================================================================
// Core Entities
// ============================================================================

/**
 * Single activity record from the backend
 */
export interface ActivityRecord {
  /** Unique identifier (ULID format) */
  id: string
  /** Type of activity */
  type: ActivityType
  /** How activity was triggered */
  source?: ActivitySource
  /** Name of upstream MCP server */
  server_name?: string
  /** Name of tool called */
  tool_name?: string
  /** Tool call arguments */
  arguments?: Record<string, unknown>
  /** Tool response (potentially truncated) */
  response?: string
  /** True if response was truncated */
  response_truncated?: boolean
  /** Result status: "success", "error", "blocked" */
  status: ActivityStatus
  /** Error details if status is "error" */
  error_message?: string
  /** Execution duration in milliseconds */
  duration_ms?: number
  /** When activity occurred (ISO 8601) */
  timestamp: string
  /** MCP session ID for correlation */
  session_id?: string
  /** HTTP request ID for correlation */
  request_id?: string
  /** Additional context-specific data */
  metadata?: Record<string, unknown>
}

/**
 * Server activity count for summary
 */
export interface ActivityTopServer {
  /** Server name */
  name: string
  /** Activity count */
  count: number
}

/**
 * Tool activity count for summary
 */
export interface ActivityTopTool {
  /** Server name */
  server: string
  /** Tool name */
  tool: string
  /** Activity count */
  count: number
}

// ============================================================================
// API Responses
// ============================================================================

/**
 * Response for GET /api/v1/activity
 */
export interface ActivityListResponse {
  /** List of activity records */
  activities: ActivityRecord[]
  /** Total matching records (for pagination) */
  total: number
  /** Records per page */
  limit: number
  /** Current offset */
  offset: number
}

/**
 * Response for GET /api/v1/activity/{id}
 */
export interface ActivityDetailResponse {
  /** Single activity record */
  activity: ActivityRecord
}

/**
 * Response for GET /api/v1/activity/summary
 */
export interface ActivitySummaryResponse {
  /** Time period (1h, 24h, 7d, 30d) */
  period: string
  /** Total activity count */
  total_count: number
  /** Count of successful activities */
  success_count: number
  /** Count of error activities */
  error_count: number
  /** Count of blocked activities */
  blocked_count: number
  /** Top servers by activity count */
  top_servers?: ActivityTopServer[]
  /** Top tools by activity count */
  top_tools?: ActivityTopTool[]
  /** Start of the period (RFC3339) */
  start_time: string
  /** End of the period (RFC3339) */
  end_time: string
}

// ============================================================================
// API Request Parameters
// ============================================================================

/**
 * Filter parameters for GET /api/v1/activity
 */
export interface ActivityFilterParams {
  /** Filter by activity type */
  type?: ActivityType
  /** Filter by server name */
  server?: string
  /** Filter by tool name */
  tool?: string
  /** Filter by MCP session ID */
  session_id?: string
  /** Filter by status */
  status?: ActivityStatus
  /** Filter by intent operation type (Spec 018) */
  intent_type?: 'read' | 'write' | 'destructive'
  /** Filter activities after this time (RFC3339) */
  start_time?: string
  /** Filter activities before this time (RFC3339) */
  end_time?: string
  /** Maximum records to return (1-100, default 50) */
  limit?: number
  /** Pagination offset (default 0) */
  offset?: number
}

/**
 * Parameters for GET /api/v1/activity/export
 */
export interface ActivityExportParams extends ActivityFilterParams {
  /** Export format: json (default) or csv */
  format: 'json' | 'csv'
  /** Include request/response bodies in export */
  include_bodies?: boolean
}

/**
 * Parameters for GET /api/v1/activity/summary
 */
export interface ActivitySummaryParams {
  /** Time period: 1h, 24h (default), 7d, 30d */
  period?: '1h' | '24h' | '7d' | '30d'
}

// ============================================================================
// SSE Event Types
// ============================================================================

/**
 * SSE event types for real-time activity updates
 */
export type ActivitySSEEventType =
  | 'activity.tool_call.started'
  | 'activity.tool_call.completed'
  | 'activity.policy_decision'
  | 'activity.quarantine_change'

/**
 * Payload for activity.tool_call.started event
 */
export interface ActivityToolCallStartedPayload {
  server_name: string
  tool_name: string
  session_id?: string
  request_id?: string
  arguments?: Record<string, unknown>
}

/**
 * Payload for activity.tool_call.completed event
 */
export interface ActivityToolCallCompletedPayload {
  server_name: string
  tool_name: string
  session_id?: string
  request_id?: string
  status: ActivityStatus
  error_message?: string
  duration_ms: number
  response?: string
  response_truncated?: boolean
}

/**
 * Payload for activity.policy_decision event
 */
export interface ActivityPolicyDecisionPayload {
  server_name: string
  tool_name: string
  session_id?: string
  decision: 'blocked'
  reason: string
}

/**
 * Payload for activity.quarantine_change event
 */
export interface ActivityQuarantineChangePayload {
  server_name: string
  quarantined: boolean
  reason: string
}

// ============================================================================
// API Client Interface
// ============================================================================

/**
 * Activity API client interface
 *
 * To be implemented in frontend/src/services/api.ts
 */
export interface ActivityAPI {
  /**
   * List activity records with optional filtering
   * GET /api/v1/activity
   */
  getActivities(params?: ActivityFilterParams): Promise<ActivityListResponse>

  /**
   * Get single activity record details
   * GET /api/v1/activity/{id}
   */
  getActivityDetail(id: string): Promise<ActivityDetailResponse>

  /**
   * Get activity summary statistics
   * GET /api/v1/activity/summary
   */
  getActivitySummary(params?: ActivitySummaryParams): Promise<ActivitySummaryResponse>

  /**
   * Get export URL with current filters
   * Returns URL string for GET /api/v1/activity/export
   */
  getActivityExportUrl(params: ActivityExportParams): string
}
