/**
 * Activity log utility functions
 * Extracted for reuse across Activity.vue and ActivityWidget.vue
 */

// Activity type labels
const typeLabels: Record<string, string> = {
  'tool_call': 'Tool Call',
  'policy_decision': 'Policy Decision',
  'quarantine_change': 'Quarantine Change',
  'server_change': 'Server Change',
  // Spec 098: one executed required-tools preflight.
  'preflight': 'Preflight'
}

// Activity type icons
const typeIcons: Record<string, string> = {
  'tool_call': '🔧',
  'policy_decision': '🛡️',
  'quarantine_change': '⚠️',
  'server_change': '🔄',
  'preflight': '🛫'
}

// Status labels
const statusLabels: Record<string, string> = {
  'success': 'Success',
  'error': 'Error',
  'blocked': 'Blocked',
  // Spec 093: shed by a concurrency limit before reaching the upstream.
  'rejected': 'Rejected'
}

// Status badge CSS classes (DaisyUI)
const statusClasses: Record<string, string> = {
  'success': 'badge-success',
  'error': 'badge-error',
  'blocked': 'badge-warning',
  // Distinct from both error (upstream fault) and blocked (policy): backpressure.
  'rejected': 'badge-info'
}

// Intent operation type icons
const intentIcons: Record<string, string> = {
  'read': '📖',
  'write': '✏️',
  'destructive': '⚠️'
}

// Intent badge classes
const intentClasses: Record<string, string> = {
  'read': 'badge-info',
  'write': 'badge-warning',
  'destructive': 'badge-error'
}

/**
 * Format activity type for display
 */
export const formatType = (type: string): string => {
  return typeLabels[type] || type
}

/**
 * Get icon for activity type
 */
export const getTypeIcon = (type: string): string => {
  return typeIcons[type] || '📋'
}

/**
 * Format status for display
 */
export const formatStatus = (status: string): string => {
  return statusLabels[status] || status
}

/**
 * Get badge CSS class for status
 */
export const getStatusBadgeClass = (status: string): string => {
  return statusClasses[status] || 'badge-ghost'
}

/**
 * Get icon for intent operation type
 */
export const getIntentIcon = (operationType: string): string => {
  return intentIcons[operationType] || '❓'
}

/**
 * Get badge CSS class for intent operation type
 */
export const getIntentBadgeClass = (operationType: string): string => {
  return intentClasses[operationType] || 'badge-ghost'
}

// --- Spec 098: preflight activity records -----------------------------------
//
// A preflight record is set-scoped, not server-scoped: `server_name` and
// `tool_name` are empty by construction and everything an operator wants to see
// lives in `metadata`:
//   {verdict, ids_count, reasons: {code: count}, per_tool: [{id,status,reason?}]}
// (written by runtime.ActivityService.RecordPreflight). Without the helpers
// below the row renders as three empty columns, which is exactly the
// transparency gap FR-014 exists to close.

/** One {reason code, count} pair of the metadata rollup. */
export interface PreflightReasonCount {
  reason: string
  count: number
}

/** One line of the per-tool detail. `reason` is absent for a ready tool. */
export interface PreflightPerToolEntry {
  id: string
  status: string
  reason?: string
}

/**
 * How many distinct reason codes the one-line summary names before it collapses
 * the tail into "+N more". A preflight may carry up to 100 ids across 15 reason
 * codes; an uncapped rollup would push every other column off screen.
 */
const MAX_PREFLIGHT_SUMMARY_REASONS = 3

/** True for a Spec 098 preflight activity record. */
export const isPreflightActivity = (activity?: { type?: string } | null): boolean =>
  activity?.type === 'preflight'

// --- code_execution parent / sub-call linkage ------------------------------
//
// One `code_execution` call runs a script that may invoke many upstream tools.
// The activity log records that as a TREE, not a flat list:
//
//   internal_tool_call  code_execution   request_id = <parentCallID>
//     └─ tool_call      github:create_issue   parent_id = <parentCallID>
//     └─ tool_call      slack:post_message    parent_id = <parentCallID>
//
// Both ends are recognisable from a single record, so the table, the widget and
// the detail drawer all agree on what is a parent and what is a sub-call.

/** Minimal record shape the linkage helpers need. */
interface ActivityLinkFields {
  type?: string
  tool_name?: string
  parent_id?: string
  metadata?: Record<string, any> | null
}

/**
 * True for the PARENT record of a sandboxed run: the built-in `code_execution`
 * tool. `metadata.internal_tool_name` is the authoritative field; `tool_name` is
 * the fallback for records that only carry it there. The `internal_tool_call`
 * type is required in both cases — an upstream server is free to expose a tool
 * of its own called "code_execution", and that one fans out into nothing.
 */
export const isCodeExecutionActivity = (a?: ActivityLinkFields | null): boolean =>
  a?.type === 'internal_tool_call' &&
  (a?.metadata?.internal_tool_name === 'code_execution' || a?.tool_name === 'code_execution')

/** True for a sub-call: a record that names the parent it was dispatched from. */
export const isChildCall = (a?: ActivityLinkFields | null): boolean => Boolean(a?.parent_id)

/**
 * Read `metadata.reasons` into a DETERMINISTIC order: most frequent first, ties
 * broken alphabetically. Object key order is insertion-dependent, so without the
 * sort two renders of the same record could disagree.
 */
export const preflightReasonRollup = (
  metadata?: Record<string, any> | null
): PreflightReasonCount[] => {
  const counts = new Map<string, number>()

  const reasons = metadata?.reasons
  if (reasons && typeof reasons === 'object' && !Array.isArray(reasons)) {
    for (const [reason, count] of Object.entries(reasons as Record<string, unknown>)) {
      counts.set(reason, Number(count) || 0)
    }
  }

  // Fallback for a record whose rollup is missing: recount from the per-tool
  // detail, which carries the same codes.
  if (counts.size === 0) {
    for (const tool of preflightPerTool(metadata)) {
      if (tool.reason) counts.set(tool.reason, (counts.get(tool.reason) ?? 0) + 1)
    }
  }

  return Array.from(counts, ([reason, count]) => ({ reason, count }))
    .sort((a, b) => (b.count - a.count) || a.reason.localeCompare(b.reason))
}

/**
 * Render a rollup as "code xN, code xN". `limit <= 0` names them all; a positive
 * limit collapses the tail into "+N more".
 */
export const formatPreflightReasons = (
  rollup: PreflightReasonCount[],
  limit = 0
): string => {
  if (rollup.length === 0) return ''

  const shown = limit > 0 ? rollup.slice(0, limit) : rollup
  const remaining = rollup.length - shown.length
  const parts = shown.map(entry => `${entry.reason} x${entry.count}`)
  if (remaining > 0) parts.push(`+${remaining} more`)
  return parts.join(', ')
}

/** Ordered per-tool detail; malformed entries are dropped, never rendered. */
export const preflightPerTool = (
  metadata?: Record<string, any> | null
): PreflightPerToolEntry[] => {
  const perTool = metadata?.per_tool
  if (!Array.isArray(perTool)) return []

  return perTool
    .filter((entry): entry is Record<string, unknown> =>
      Boolean(entry) && typeof entry === 'object' && !Array.isArray(entry))
    .map(entry => {
      const line: PreflightPerToolEntry = {
        id: String(entry.id ?? ''),
        status: String(entry.status ?? ''),
      }
      if (entry.reason) line.reason = String(entry.reason)
      return line
    })
}

/**
 * Number of unique tool ids the run evaluated. `ids_count` is authoritative;
 * the per-tool length is the fallback for a partially-written record.
 */
export const preflightIdsCount = (metadata?: Record<string, any> | null): number => {
  const count = Number(metadata?.ids_count)
  if (Number.isFinite(count) && count > 0) return count
  return preflightPerTool(metadata).length
}

/**
 * One-line verdict summary for the activity table, e.g.
 * "blocked (4 tools): server_disabled x2, tool_changed x1".
 * Empty string when the record carries no readable verdict, so the caller can
 * fall back to its usual placeholder.
 */
export const formatPreflightSummary = (metadata?: Record<string, any> | null): string => {
  const verdict = metadata?.verdict
  if (!verdict || typeof verdict !== 'string') return ''

  const count = preflightIdsCount(metadata)
  const summary = `${verdict} (${count} ${count === 1 ? 'tool' : 'tools'})`
  const reasons = formatPreflightReasons(
    preflightReasonRollup(metadata),
    MAX_PREFLIGHT_SUMMARY_REASONS
  )
  return reasons ? `${summary}: ${reasons}` : summary
}

/**
 * Badge class for a set-level verdict. `ready` is the only success; the rest are
 * an operator action (blocked/unknown_ids) or a retry (degraded_retryable).
 */
export const getPreflightVerdictBadgeClass = (verdict?: string): string => {
  switch (verdict) {
    case 'ready':
      return 'badge-success'
    case 'degraded_retryable':
      return 'badge-info'
    case 'blocked':
      return 'badge-warning'
    case 'unknown_ids':
      return 'badge-error'
    default:
      return 'badge-ghost'
  }
}

/**
 * Format timestamp for display
 */
export const formatTimestamp = (timestamp: string): string => {
  return new Date(timestamp).toLocaleString()
}

/**
 * Format relative time (e.g., "5m ago")
 */
export const formatRelativeTime = (timestamp: string): string => {
  const now = Date.now()
  const time = new Date(timestamp).getTime()
  const diff = now - time

  if (diff < 1000) return 'Just now'
  if (diff < 60000) return `${Math.floor(diff / 1000)}s ago`
  if (diff < 3600000) return `${Math.floor(diff / 60000)}m ago`
  if (diff < 86400000) return `${Math.floor(diff / 3600000)}h ago`
  return `${Math.floor(diff / 86400000)}d ago`
}

/**
 * Format duration in milliseconds for display
 */
export const formatDuration = (ms: number): string => {
  if (ms < 1000) return `${Math.round(ms)}ms`
  return `${(ms / 1000).toFixed(2)}s`
}

/**
 * Filter activities based on filter criteria
 */
export interface ActivityFilterOptions {
  type?: string
  server?: string
  status?: string
}

export interface ActivityRecord {
  id: string
  type: string
  server_name?: string
  status: string
  timestamp: string
  [key: string]: unknown
}

export const filterActivities = (
  activities: ActivityRecord[],
  filters: ActivityFilterOptions
): ActivityRecord[] => {
  let result = activities

  if (filters.type) {
    result = result.filter(a => a.type === filters.type)
  }
  if (filters.server) {
    result = result.filter(a => a.server_name === filters.server)
  }
  if (filters.status) {
    result = result.filter(a => a.status === filters.status)
  }

  return result
}

/**
 * Paginate activities
 */
export const paginateActivities = (
  activities: ActivityRecord[],
  page: number,
  pageSize: number
): ActivityRecord[] => {
  const start = (page - 1) * pageSize
  return activities.slice(start, start + pageSize)
}

/**
 * Calculate total pages
 */
export const calculateTotalPages = (totalItems: number, pageSize: number): number => {
  return Math.ceil(totalItems / pageSize)
}
