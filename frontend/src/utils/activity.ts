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
 * The glyph vocabulary the Activity table uses, as a legend. The table paints
 * the word beside each glyph, so this is a reference rather than the only key —
 * but the Sensitive column has no room for a word, and a column of bare ☢ / ⚠️
 * badges is unreadable without one (F26, #1046).
 */
export interface ActivityLegendEntry {
  icon: string
  term: string
  description: string
}

export const INTENT_LEGEND: ActivityLegendEntry[] = [
  { icon: intentIcons.read, term: 'read', description: 'the agent declared this call reads data' },
  { icon: intentIcons.write, term: 'write', description: 'it creates or modifies data' },
  { icon: intentIcons.destructive, term: 'destructive', description: 'it deletes or overwrites data' },
]

export const SENSITIVE_LEGEND: ActivityLegendEntry[] = [
  { icon: '☢️', term: 'critical', description: 'cloud credentials, private keys' },
  { icon: '⚠️', term: 'high', description: 'API tokens, connection strings' },
  { icon: '⚡', term: 'medium', description: 'card numbers, sensitive paths' },
  { icon: 'ℹ️', term: 'low', description: 'high-entropy strings worth a look' },
]

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
  /**
   * Operation type, e.g. "read". PAINTED beside the glyph, not only announced:
   * 📖 / ✏️ / ⚠️ with no key and no word left the column decodable only by
   * someone who already knew the mapping, and made colour+glyph the sole
   * encoding (audit finding F26, #1046; WCAG 1.4.1).
   */
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

// --- security_scan records ---------------------------------------------------
//
// The drawer for a security_scan row showed Status, ID, Server, Tool, Source and
// ~80% whitespace — no verdict, no findings, nowhere to go (audit finding F25,
// #1046). The record does carry the scan's outcome: `metadata.findings_summary`
// is a {severity: count} rollup written by handleSecurityScanSettled. It just
// had no renderer.

/** True for a settled security-scan record (Spec 077). */
export const isSecurityScanActivity = (a?: { type?: string } | null): boolean =>
  a?.type === 'security_scan'

/** One {severity, count} pair of a scan's findings rollup. */
export interface ScanFindingCount {
  severity: string
  count: number
}

/**
 * Whether the record actually CARRIES a findings rollup.
 *
 * An empty rollup and a missing one look identical downstream — both produce
 * zero findings — and they mean opposite things. "The scanners found nothing"
 * is a security claim; "this record has no summary" is the absence of one, and
 * a drawer that prints the first when it only knows the second is telling the
 * operator a server is clean on no evidence. Records written before the
 * producer/consumer type mismatch was fixed all fall in the second case.
 */
export const hasScanFindingsSummary = (metadata?: Record<string, any> | null): boolean => {
  const summary = metadata?.findings_summary
  return Boolean(summary) && typeof summary === 'object' && !Array.isArray(summary)
}

/** Severity order, worst first — the order an operator triages in. */
const SCAN_SEVERITY_ORDER = ['critical', 'high', 'medium', 'low', 'info']

/**
 * `metadata.findings_summary` as an ordered list: worst severity first, unknown
 * keys last in alphabetical order so two renders never disagree. Zero counts are
 * dropped — "0 low" is not a finding.
 */
export const scanFindingsRollup = (
  metadata?: Record<string, any> | null
): ScanFindingCount[] => {
  const summary = metadata?.findings_summary
  if (!summary || typeof summary !== 'object' || Array.isArray(summary)) return []

  const rank = (severity: string): number => {
    const i = SCAN_SEVERITY_ORDER.indexOf(severity)
    return i === -1 ? SCAN_SEVERITY_ORDER.length : i
  }

  return Object.entries(summary as Record<string, unknown>)
    .map(([severity, count]) => ({ severity, count: Number(count) || 0 }))
    .filter(entry => entry.count > 0)
    .sort((a, b) => rank(a.severity) - rank(b.severity) || a.severity.localeCompare(b.severity))
}

/** Total findings across every severity. 0 means the scan came back clean. */
export const scanFindingsTotal = (metadata?: Record<string, any> | null): number =>
  scanFindingsRollup(metadata).reduce((sum, entry) => sum + entry.count, 0)

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
  key: 'total' | 'calls' | 'error' | 'blocked' | 'rejected'
  label: string
  tone: StatusTone
  /** Status value this segment filters to; '' clears the status filter. */
  status: string
  /** False for segments that describe the list without narrowing it. */
  filterable: boolean
}

interface ActivitySummaryCounts {
  total_count?: number
  /** Calls the user made — the population the Usage tab counts (F1, #1046). */
  call_count?: number
  success_count?: number
  error_count?: number
  blocked_count?: number
  rejected_count?: number
  /** Rows whose status is outside the tool-call vocabulary (F2, #1046). */
  other_count?: number
}

const plural = (n: number, one: string, many: string): string => `${n} ${n === 1 ? one : many}`

/**
 * Compact counts strip, e.g. "70 events · 42 calls · 6 errors · 1 blocked".
 *
 * "Events" and "calls" are two different questions and this strip used to print
 * the first under the second's name: the header read "70 calls" for a window
 * whose rows included security scans, quarantine auto-approvals and a system
 * start, while the Usage tab — counting only calls — read 42 for the same 24
 * hours (audit finding F1/F24, #1046). The event total is the paginator's
 * denominator, so it stays; it just says what it is, and the call count that
 * matches the Usage tab sits beside it.
 *
 * A zero attention count is omitted entirely rather than rendered as a
 * reassuring "0 errors" that costs a line of scanning.
 */
export const compactSummaryParts = (
  summary?: ActivitySummaryCounts | null
): CompactSummaryPart[] => {
  if (!summary) return []

  const total = summary.total_count ?? 0
  const calls = summary.call_count
  // Every row is a call: one segment, and it can say so.
  const allRowsAreCalls = calls === total

  const parts: CompactSummaryPart[] = [
    {
      key: 'total',
      label: allRowsAreCalls
        ? plural(total, 'call', 'calls')
        : plural(total, 'event', 'events'),
      tone: 'muted',
      status: '',
      filterable: true,
    },
  ]

  // No status selects "calls", so this segment describes rather than filters.
  if (calls !== undefined && !allRowsAreCalls) {
    parts.push({
      key: 'calls',
      label: plural(calls, 'call', 'calls'),
      tone: 'muted',
      status: '',
      filterable: false,
    })
  }

  if ((summary.error_count ?? 0) > 0) {
    parts.push({
      key: 'error',
      label: plural(summary.error_count as number, 'error', 'errors'),
      tone: 'error',
      status: 'error',
      filterable: true,
    })
  }
  if ((summary.blocked_count ?? 0) > 0) {
    parts.push({
      key: 'blocked',
      label: `${summary.blocked_count} blocked`,
      tone: 'warning',
      status: 'blocked',
      filterable: true,
    })
  }
  if ((summary.rejected_count ?? 0) > 0) {
    parts.push({
      key: 'rejected',
      label: `${summary.rejected_count} rejected`,
      tone: 'neutral',
      status: 'rejected',
      filterable: true,
    })
  }

  return parts
}

// --- status tiles: a partition, not a selection --------------------------------
//
// The expanded panel's stat row printed `Total 42 · Success 15 · Errors 4 ·
// Blocked 0 · Rejected 0` — four buckets adding to 19 under a denominator of 42
// (audit finding F2, #1046). The four are the tool-call status vocabulary, but
// the activity log is wider than tool calls: a quarantine change stores its
// ACTION in `status` ("tool_auto_approved"), a policy decision its verdict
// ("allow"). Those rows were in the total and in no tile.
//
// The summary endpoint now returns the residual as `other_count`, and the tiles
// render it. The row is therefore a PARTITION of the total by construction, and
// statusBucketTiles is the single place that decides what the row contains — so
// the assertion "the tiles sum to the total" is testable without a browser.

/** The pseudo-status the "Other" tile filters by. Never a stored value. */
export const OTHER_STATUS = 'other'

/** The four statuses a tool call can end in. Anything else is OTHER_STATUS. */
export const CALL_STATUSES = ['success', 'error', 'blocked', 'rejected'] as const

/** True for a record whose status is outside the tool-call vocabulary. */
export const isOtherStatus = (status?: string): boolean =>
  !CALL_STATUSES.includes((status ?? '') as (typeof CALL_STATUSES)[number])

/** One tile of the status row. */
export interface StatusBucketTile {
  /** Status this tile filters to — a real status, or OTHER_STATUS. */
  status: string
  label: string
  count: number
  tone: StatusTone
  /** Hover text; the Other tile has to explain what it holds. */
  title?: string
}

/**
 * The status tiles, in display order, as a partition of `total_count`.
 *
 * The Other tile is omitted when it is zero — a zero bucket contributes nothing
 * to the sum and an always-on "Other 0" is a line of scanning for no
 * information. Every other tile is always present: they are the vocabulary, and
 * a missing "Errors" tile reads as "errors are not tracked", not as zero.
 */
export const statusBucketTiles = (summary?: ActivitySummaryCounts | null): StatusBucketTile[] => {
  if (!summary) return []

  const tiles: StatusBucketTile[] = [
    { status: 'success', label: 'Success', count: summary.success_count ?? 0, tone: 'muted' },
    { status: 'error', label: 'Errors', count: summary.error_count ?? 0, tone: 'error' },
    { status: 'blocked', label: 'Blocked', count: summary.blocked_count ?? 0, tone: 'warning' },
    { status: 'rejected', label: 'Rejected', count: summary.rejected_count ?? 0, tone: 'neutral' },
  ]

  const other = summary.other_count ?? 0
  if (other > 0) {
    tiles.push({
      status: OTHER_STATUS,
      label: 'Other / internal',
      count: other,
      tone: 'neutral',
      title:
        'Rows whose outcome is not a tool-call status: quarantine approvals, ' +
        'policy allow decisions and other bookkeeping. Counted here so the ' +
        'tiles add up to the total above them.',
    })
  }

  return tiles
}

/**
 * What the tiles add up to, so a caller (and a test) can compare it against the
 * denominator without re-deriving the bucket list.
 */
export const statusBucketSum = (summary?: ActivitySummaryCounts | null): number =>
  statusBucketTiles(summary).reduce((sum, tile) => sum + tile.count, 0)

// --- run grouping ------------------------------------------------------------
//
// Twelve consecutive `everything:echo` calls produced twelve rows, each
// repeating the same three-line timestamp, the same reason and the same word
// "Success" (audit finding F5, #1046). A real export was 1479 calls in 809
// runs, with runs up to 100×. The table is SCANNED: a hundred identical rows
// cost a hundred rows of attention and carry one row of information.
//
// Consecutive rows that agree on everything the collapsed row would print
// collapse into a RUN. A run never mixes outcomes — status is part of the key —
// so "echo ×12" can never hide the one call that failed, which is the only way
// this compression could lie.

/** Minimal record shape the grouping needs. */
export interface ActivityRunFields {
  id?: string
  type?: string
  server_name?: string
  tool_name?: string
  status?: string
  timestamp?: string
  duration_ms?: number
  parent_id?: string
  has_sensitive_data?: boolean
  max_severity?: string
  detection_types?: string[]
  metadata?: Record<string, any> | null
}

/** A run of identical consecutive rows. `count === 1` for an ordinary row. */
export interface ActivityRun<T extends ActivityRunFields> {
  /** Stable v-for key: the lead row's id. */
  key: string
  /** The row the collapsed line renders: the first member in list order. */
  lead: T
  /** Every member, lead included, in list order. */
  rows: T[]
  count: number
  /**
   * True when members declared DIFFERENT intent reasons. The collapsed line
   * shows the lead's reason, so it has to admit the others exist.
   */
  reasonsVary: boolean
}

/**
 * The identity a run is keyed on: EVERYTHING THE COLLAPSED LINE PRINTS, plus
 * the code_execution parent link. That rule is what makes the compression safe
 * — a field the lead row displays on behalf of eleven others has to be one all
 * twelve agree on, or the fold is a quiet lie.
 *
 * The intent REASON is the one displayed field deliberately left out: agents
 * reword it call to call, and keying on it would stop the compression working
 * on exactly the logs that need it most. The run reports the variation instead
 * (see reasonsVary), so the lead's reason never silently stands for the rest.
 */
const runIdentity = (a: ActivityRunFields): string =>
  // JSON.stringify rather than a delimiter join: server names, tool names and
  // the details text are free-form, so any separator character could appear
  // inside a field and let two different rows agree on one joined string.
  JSON.stringify([
    a.type ?? '',
    a.server_name ?? '',
    a.tool_name ?? '',
    a.status ?? '',
    a.parent_id ?? '',
    // The Intent column prints this word on the lead row's authority.
    // call_tool_read and call_tool_write against the same tool produce records
    // identical in type, server, tool and status, so without it a run of writes
    // could fold under a "read" lead.
    intentOperationOf(a),
    // Not just the boolean: the badge prints the severity glyph AND the count,
    // so a critical detection must not fold under a low-severity lead.
    a.has_sensitive_data ? '1' : '0',
    a.max_severity ?? '',
    a.detection_types?.length ?? 0,
    // A preflight or config change says everything in metadata.action / verdict;
    // two of them are only "the same row twice" if that text matches too.
    activityDetailsText(a as Parameters<typeof activityDetailsText>[0]),
  ])

const intentOperationOf = (a: ActivityRunFields): string =>
  String((a.metadata?.intent as ActivityIntent | undefined)?.operation_type ?? '')

const intentReasonOf = (a: ActivityRunFields): string =>
  String((a.metadata?.intent as ActivityIntent | undefined)?.reason ?? '')

/**
 * Fold consecutive identical rows into runs. Pure and order-preserving: run i
 * holds the rows that were at that position in the input, so the caller can
 * paginate runs and still render the underlying rows in order.
 *
 * `enabled: false` returns one run per row — the escape hatch for an operator
 * who wants the raw log, and the behaviour whenever the table is sorted by
 * something other than time (adjacency is only meaningful in time order; two
 * rows next to each other in a duration sort were not "repeated").
 */
export const groupActivityRuns = <T extends ActivityRunFields>(
  rows: T[],
  enabled = true
): ActivityRun<T>[] => {
  const runs: ActivityRun<T>[] = []
  let currentKey: string | null = null

  for (const row of rows) {
    const identity = enabled ? runIdentity(row) : null
    const current = runs.length > 0 ? runs[runs.length - 1] : undefined

    if (current && identity !== null && identity === currentKey) {
      current.rows.push(row)
      current.count++
      if (!current.reasonsVary && intentReasonOf(row) !== intentReasonOf(current.lead)) {
        current.reasonsVary = true
      }
      continue
    }

    currentKey = identity
    runs.push({
      // Ids are unique per record; fall back to the position so a record
      // without one still keys a stable row.
      key: row.id ?? `row-${runs.length}`,
      lead: row,
      rows: [row],
      count: 1,
      reasonsVary: false,
    })
  }

  return runs
}

/** Inclusive duration span of a run, in ms. Null when no member timed itself. */
export interface DurationRange {
  min: number
  max: number
}

export const runDurationRange = (rows: ActivityRunFields[]): DurationRange | null => {
  let min: number | null = null
  let max: number | null = null
  for (const row of rows) {
    const ms = row.duration_ms
    if (ms === undefined || ms === null || !Number.isFinite(ms)) continue
    if (min === null || ms < min) min = ms
    if (max === null || ms > max) max = ms
  }
  if (min === null || max === null) return null
  return { min, max }
}

/**
 * How a run's Duration cell reads: one value when every member agreed, a range
 * otherwise. An empty string means "nothing to show" — the caller renders its
 * usual dash.
 */
export const formatRunDuration = (rows: ActivityRunFields[]): string => {
  const range = runDurationRange(rows)
  if (!range) return ''
  if (range.min === range.max) return formatDuration(range.min)
  return `${formatDuration(range.min)}–${formatDuration(range.max)}`
}

/**
 * The time a run covers, e.g. "over 4m". Empty for a single row or an
 * instantaneous run — "over 0s" is noise.
 */
export const formatRunSpan = (rows: ActivityRunFields[]): string => {
  if (rows.length < 2) return ''
  let earliest = Infinity
  let latest = -Infinity
  for (const row of rows) {
    const t = row.timestamp ? new Date(row.timestamp).getTime() : NaN
    if (Number.isNaN(t)) continue
    if (t < earliest) earliest = t
    if (t > latest) latest = t
  }
  if (!Number.isFinite(earliest) || !Number.isFinite(latest)) return ''

  const diff = latest - earliest
  if (diff < 1000) return ''
  if (diff < 60_000) return `over ${Math.round(diff / 1000)}s`
  if (diff < 3_600_000) return `over ${Math.round(diff / 60_000)}m`
  return `over ${Math.round(diff / 3_600_000)}h`
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
    chips.push({
      kind: 'status',
      key: 'status',
      label:
        state.status === OTHER_STATUS
          ? 'Status: other / internal'
          : `Status: ${state.status}`,
    })
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
