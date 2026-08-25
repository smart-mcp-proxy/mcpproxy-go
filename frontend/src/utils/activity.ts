/**
 * Activity log utility functions
 * Extracted for reuse across Activity.vue and ActivityWidget.vue
 */

import { formatDateTime } from './datetime'

// Activity type labels
const typeLabels: Record<string, string> = {
  'tool_call': 'Tool Call',
  'system_start': 'System Start',
  'system_stop': 'System Stop',
  'internal_tool_call': 'Internal Tool Call',
  'config_change': 'Config Change',
  'policy_decision': 'Policy Decision',
  'quarantine_change': 'Quarantine Change',
  'server_change': 'Server Change',
  // Spec 098: one executed required-tools preflight.
  'preflight': 'Preflight'
}

// Activity type icons
const typeIcons: Record<string, string> = {
  'tool_call': '🔧',
  'system_start': '🚀',
  'system_stop': '🛑',
  'internal_tool_call': '⚙️',
  'config_change': '⚡',
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
 *
 * @deprecated Prefer {@link statusPresentation}, which also decides whether the
 * status deserves a pill at all. Kept for the few places that still want a raw
 * DaisyUI badge modifier.
 */
export const getStatusBadgeClass = (status: string): string => {
  return statusClasses[status] || 'badge-ghost'
}

// --- status presentation ----------------------------------------------------
//
// The activity table is SCANNED, not read. Success is the norm — roughly 90% of
// rows — so painting it green makes the whole column shout and buries the two
// rows that actually need a human. Semantic colour is therefore spent only on
// states that need attention:
//
//   success            plain muted text, no pill      ("this is fine, move on")
//   error              red pill                       (upstream fault)
//   blocked            amber pill                     (policy stopped it)
//   rejected / other   neutral grey pill              (odd, but not an alarm)
//
// Both the table column and the dashboard widget render from this one mapping so
// they cannot drift apart.

/** How loudly a status is allowed to speak. */
export type StatusTone = 'muted' | 'error' | 'warning' | 'neutral'

export interface StatusPresentation {
  /** Human label, e.g. "Success". Unknown statuses keep their raw value. */
  label: string
  tone: StatusTone
  /** False = render as plain text (success only); true = render as a pill. */
  pill: boolean
  /** Full class list for the rendered element, pill or text. */
  className: string
}

const statusPresentations: Record<string, StatusPresentation> = {
  // No pill, no colour: the default outcome must not compete for attention.
  success: {
    label: 'Success',
    tone: 'muted',
    pill: false,
    className: 'text-sm text-base-content/60',
  },
  error: {
    label: 'Error',
    tone: 'error',
    pill: true,
    className: 'badge badge-sm badge-error',
  },
  blocked: {
    label: 'Blocked',
    tone: 'warning',
    pill: true,
    className: 'badge badge-sm badge-warning',
  },
  // Spec 093: shed by a concurrency limit. Worth noticing, not worth alarming —
  // badge-ghost is a neutral chip that reads in both light and dark themes.
  rejected: {
    label: 'Rejected',
    tone: 'neutral',
    pill: true,
    className: 'badge badge-sm badge-ghost',
  },
}

/**
 * Pure status -> presentation mapping. Anything unrecognised is an oddity, not
 * an alarm: neutral grey pill carrying the raw status value.
 */
export const statusPresentation = (status: string): StatusPresentation => {
  const known = statusPresentations[status]
  if (known) return known
  return {
    label: statusLabels[status] || status,
    tone: 'neutral',
    pill: true,
    className: 'badge badge-sm badge-ghost',
  }
}

/**
 * Get icon for intent operation type
 */
export const getIntentIcon = (operationType: string): string => {
  return intentIcons[operationType] || '❓'
}

/**
 * Get badge CSS class for intent operation type
 *
 * @deprecated The table renders intent through {@link intentPresentation} (glyph
 * + reason, no coloured pill). Still used by the detail drawer, where a single
 * badge has room to be explicit.
 */
export const getIntentBadgeClass = (operationType: string): string => {
  return intentClasses[operationType] || 'badge-ghost'
}

// --- intent presentation ----------------------------------------------------
//
// Spec 018/024 intent: {operation_type, reason, data_sensitivity}. The old table
// cell rendered a coloured `read` pill with the glyph crammed inside it (the
// glyph overflowed the badge box) and hid the REASON — the only part an operator
// actually wants — behind a tooltip. The reason is the payload; the operation is
// a one-character prefix.

/** The intent object as it is stored on `metadata.intent`. */
export interface ActivityIntent {
  operation_type?: string
  reason?: string
  data_sensitivity?: string
}

export interface IntentPresentation {
  /** False when the record declared no operation type — render a muted dash. */
  present: boolean
  /** Operation glyph: book / pencil / warning. Empty when absent. */
  icon: string
  /** Operation type, e.g. "read". Exposed for screen readers, not painted. */
  label: string
  /** Declared reason, single line in the cell. Empty when none was given. */
  reason: string
  /** Full hover text (`title`), so a truncated reason is still readable. */
  title: string
}

const EMPTY_INTENT: IntentPresentation = { present: false, icon: '', label: '', reason: '', title: '' }

/**
 * Pure intent -> presentation mapping: glyph + reason text, never a colour pill.
 * A missing operation type means "no intent declared", which the caller renders
 * as a muted dash rather than a `❓` chip.
 */
export const intentPresentation = (intent?: ActivityIntent | null): IntentPresentation => {
  const operationType = intent?.operation_type
  if (!operationType) return EMPTY_INTENT

  const reason = (intent?.reason ?? '').trim()
  return {
    present: true,
    icon: getIntentIcon(operationType),
    label: operationType,
    reason,
    // Hover shows the operation too — the glyph alone is ambiguous, and the
    // reason may be ellipsised in the cell.
    title: reason ? `${operationType} — ${reason}` : operationType,
  }
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
 * What the table's Details column will print for a record. Extracted so the Type
 * column can tell whether its `code_execution` marker would be a second copy of
 * the same six characters on the same row.
 */
export const activityDetailsText = (
  a?: (ActivityLinkFields & { metadata?: Record<string, any> | null }) | null
): string => {
  if (!a) return ''
  if (a.tool_name) return a.tool_name
  if (isPreflightActivity(a as { type?: string })) {
    const summary = formatPreflightSummary(a.metadata)
    if (summary) return summary
  }
  const action = a.metadata?.action
  return action ? String(action) : ''
}

/**
 * True when the Type column should still carry the `code_execution` badge: the
 * record IS a sandbox parent and the Details column is not already saying so.
 * When this is false the row shows a single quiet puzzle glyph beside the
 * Details text instead — one marker per row, never the same word twice.
 */
export const showParentBadgeInTypeColumn = (
  a?: (ActivityLinkFields & { metadata?: Record<string, any> | null }) | null
): boolean => {
  if (!isCodeExecutionActivity(a)) return false
  return !activityDetailsText(a).includes('code_execution')
}

/** Last 8 chars of a correlation id — enough to recognise, short enough to fit. */
export const shortId = (id: string): string => (id.length > 8 ? `…${id.slice(-8)}` : id)

// --- compact header: counts strip + active-filter chips ---------------------
//
// Five stat cards plus an always-open nine-control filter grid pushed the first
// activity row below the fold. The default view is now one line: the total plus
// only the counts that want attention, and a Filters button carrying a count of
// what is currently narrowing the list. The cards and the controls still exist —
// they just wait until they are asked for.

/** One segment of the compact counts strip. */
export interface CompactSummaryPart {
  key: 'total' | 'error' | 'blocked' | 'rejected'
  label: string
  tone: StatusTone
  /** Status value this segment filters to; '' clears the status filter. */
  status: string
}

interface ActivitySummaryCounts {
  total_count?: number
  error_count?: number
  blocked_count?: number
  rejected_count?: number
}

const plural = (n: number, one: string, many: string): string => `${n} ${n === 1 ? one : many}`

/**
 * Compact counts strip, e.g. "54 calls · 6 errors · 1 blocked". The total is
 * always present and muted; a zero attention count is omitted entirely rather
 * than rendered as a reassuring "0 errors" that costs a line of scanning.
 */
export const compactSummaryParts = (
  summary?: ActivitySummaryCounts | null
): CompactSummaryPart[] => {
  if (!summary) return []

  const parts: CompactSummaryPart[] = [
    {
      key: 'total',
      label: plural(summary.total_count ?? 0, 'call', 'calls'),
      tone: 'muted',
      status: '',
    },
  ]

  if ((summary.error_count ?? 0) > 0) {
    parts.push({
      key: 'error',
      label: plural(summary.error_count as number, 'error', 'errors'),
      tone: 'error',
      status: 'error',
    })
  }
  if ((summary.blocked_count ?? 0) > 0) {
    parts.push({
      key: 'blocked',
      label: `${summary.blocked_count} blocked`,
      tone: 'warning',
      status: 'blocked',
    })
  }
  if ((summary.rejected_count ?? 0) > 0) {
    parts.push({
      key: 'rejected',
      label: `${summary.rejected_count} rejected`,
      tone: 'neutral',
      status: 'rejected',
    })
  }

  return parts
}

/** Every filter the Activity Log can apply, in one plain object. */
export interface ActivityFilterState {
  types?: string[]
  parentId?: string
  server?: string
  status?: string
  authType?: string
  agentName?: string
  sensitiveData?: string
  severity?: string
  session?: string
  /** Resolved display label for `session`; falls back to a shortened id. */
  sessionLabel?: string
  startDate?: string
  endDate?: string
}

/** A dismissable chip in the compact strip. `kind` selects the clear action. */
export interface ActiveFilterChip {
  kind:
    | 'type'
    | 'parent'
    | 'server'
    | 'status'
    | 'auth'
    | 'agent'
    | 'sensitive'
    | 'severity'
    | 'session'
    | 'start'
    | 'end'
  /** Unique within one render, so it can key a v-for. */
  key: string
  label: string
  /** Payload the clear action needs (the type value for a type chip). */
  value?: string
  title?: string
}

const localDateLabel = (value: string): string => formatDateTime(value, value)

/**
 * The active filters, as chips. Pure and order-stable: types first (they are
 * multi-select), then the sub-call view, then the single-value filters in the
 * order the expanded controls present them.
 */
export const activeFilterChips = (state: ActivityFilterState): ActiveFilterChip[] => {
  const chips: ActiveFilterChip[] = []

  for (const type of state.types ?? []) {
    chips.push({
      kind: 'type',
      key: `type:${type}`,
      label: `${getTypeIcon(type)} ${formatType(type)}`,
      value: type,
    })
  }

  if (state.parentId) {
    chips.push({
      kind: 'parent',
      key: 'parent',
      label: `↳ Sub-calls of ${shortId(state.parentId)}`,
      value: state.parentId,
      title: `Sub-calls of code_execution ${state.parentId}`,
    })
  }
  if (state.server) {
    chips.push({ kind: 'server', key: 'server', label: `Server: ${state.server}` })
  }
  if (state.status) {
    chips.push({ kind: 'status', key: 'status', label: `Status: ${state.status}` })
  }
  if (state.authType) {
    chips.push({
      kind: 'auth',
      key: 'auth',
      label: `Auth: ${state.authType === 'admin' ? '🔑 Admin' : '🤖 Agent'}`,
    })
  }
  if (state.agentName) {
    chips.push({ kind: 'agent', key: 'agent', label: `Agent: ${state.agentName}` })
  }
  if (state.sensitiveData) {
    chips.push({
      kind: 'sensitive',
      key: 'sensitive',
      label: `Sensitive: ${state.sensitiveData === 'true' ? '⚠️ Detected' : 'Clean'}`,
    })
  }
  // Severity only narrows the list while the sensitive-data filter is
  // "Detected" (the view applies it under that same condition) — a severity
  // value left behind after switching sensitive-data off must not read as an
  // active filter.
  if (state.severity && state.sensitiveData === 'true') {
    chips.push({ kind: 'severity', key: 'severity', label: `Severity: ${state.severity}` })
  }
  if (state.session) {
    chips.push({
      kind: 'session',
      key: 'session',
      label: `Session: ${state.sessionLabel || shortId(state.session)}`,
    })
  }
  if (state.startDate) {
    chips.push({ kind: 'start', key: 'start', label: `From: ${localDateLabel(state.startDate)}` })
  }
  if (state.endDate) {
    chips.push({ kind: 'end', key: 'end', label: `To: ${localDateLabel(state.endDate)}` })
  }

  return chips
}

/** Badge count for the Filters toggle: how many filters are narrowing the list. */
export const activeFilterCount = (state: ActivityFilterState): number =>
  activeFilterChips(state).length

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
 * Format timestamp for display, in the one house format (UX audit F35).
 */
export const formatTimestamp = (timestamp: string): string => formatDateTime(timestamp, timestamp)

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
