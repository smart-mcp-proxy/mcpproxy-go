import type { ActivityRecord, WorkSessionTimelineResponse } from '@/types/api'

// Spec 124 PR-3: pure helpers of the work-session list and timeline.

/** Rows the timeline door returns on each side of the position by default. */
export const TIMELINE_CONTEXT_DEFAULT = 20
/** The most rows the door accepts on one side. */
export const TIMELINE_CONTEXT_MAX = 250
/** Rows one "load earlier / later" adds. */
export const TIMELINE_CONTEXT_STEP = 50

export const TIMELINE_AT_DEFAULT = 'first_failure'

/**
 * The `at` value the timeline door accepts: `first_failure`, `failure:<n>`
 * (n >= 1), `row:<activity id>` or `end`. Anything else, including an absent
 * value, is the default, so a hand-edited address never produces a 400.
 */
export function normalizeTimelineAt(raw: string | null | undefined): string {
  const value = (raw ?? '').trim()
  if (value === 'end' || value === TIMELINE_AT_DEFAULT) return value
  if (/^failure:[1-9][0-9]*$/.test(value)) return value
  if (/^row:.+$/.test(value)) return value
  return TIMELINE_AT_DEFAULT
}

/** Quotes a word for a POSIX shell when it needs it. */
function shellWord(word: string): string {
  if (/^[A-Za-z0-9_.:@/+=-]+$/.test(word)) return word
  return `'${word.replace(/'/g, `'\\''`)}'`
}

export interface TimelineCliView {
  id: string
  at: string
  before: number
  after: number
  includeBodies?: boolean
}

/** The `mcpproxy activity session` command that prints the same view (FR-064). */
export function timelineCliCommand(view: TimelineCliView): string {
  const words = ['mcpproxy', 'activity', 'session', shellWord(view.id)]
  if (view.at && view.at !== TIMELINE_AT_DEFAULT) words.push('--at', shellWord(view.at))
  if (view.before !== TIMELINE_CONTEXT_DEFAULT) words.push('--before', String(view.before))
  if (view.after !== TIMELINE_CONTEXT_DEFAULT) words.push('--after', String(view.after))
  if (view.includeBodies) words.push('--include-bodies')
  return words.join(' ')
}

const two = (n: number) => String(n).padStart(2, '0')

/** `850ms`, `12s`, `41m 05s`, `2h 03m`, `1d 02h`. */
export function formatSessionDuration(ms: number | null | undefined): string {
  const value = typeof ms === 'number' && Number.isFinite(ms) && ms > 0 ? Math.floor(ms) : 0
  if (value < 1000) return `${value}ms`
  const seconds = Math.floor(value / 1000)
  if (seconds < 60) return `${seconds}s`
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes}m ${two(seconds % 60)}s`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h ${two(minutes % 60)}m`
  return `${Math.floor(hours / 24)}d ${two(hours % 24)}h`
}

export interface FailureNavigation {
  label: string
  /** The `at` value of the previous failure, when there is one. */
  prev?: string
  next?: string
}

/**
 * "failure N of M" and the two steps, from one page. `failure_index` is the
 * number of the failure at or before the position, so on a row that is not a
 * failure "previous" is that failure itself.
 */
export function failureNavigation(
  page: Pick<WorkSessionTimelineResponse, 'rows' | 'position' | 'failure_index' | 'failure_total' | 'lifetime_failures'>,
): FailureNavigation {
  const total = page.failure_total
  const index = page.failure_index
  if (total <= 0) {
    return {
      label: page.lifetime_failures > 0 ? 'No failures left in the log' : 'No failures in this session',
      prev: undefined,
      next: undefined,
    }
  }
  const onFailure = page.rows.some(row => row.index === page.position && row.failed === true)
  const next = index < total ? `failure:${index + 1}` : undefined
  if (onFailure) {
    return { label: `failure ${index} of ${total}`, prev: index > 1 ? `failure:${index - 1}` : undefined, next }
  }
  if (index < 1) return { label: `before failure 1 of ${total}`, prev: undefined, next }
  return { label: `after failure ${index} of ${total}`, prev: `failure:${index}`, next }
}

/**
 * The agent token of a session, for display. A client's own credential
 * (`client-<id>`) is the client itself, already named in the client cell, so it
 * is not shown as a token (the rule the connections table follows).
 */
export function sessionTokenName(session: { token_name?: string; client_id?: string }): string {
  const token = session.token_name ?? ''
  if (!token) return ''
  return session.client_id && token === `client-${session.client_id}` ? '' : token
}

const UPSTREAM_EVENT_LABELS: Record<string, string> = {
  connection_lost: 'Connection lost',
  process_exited: 'Process exited',
  oauth_refresh_failed: 'Token refresh failed',
  login_required: 'Login required',
  connection_restored: 'Connection restored',
}

/** The short name of an upstream event kind; an unknown kind keeps its value. */
export function upstreamEventLabel(activity: ActivityRecord | undefined): string {
  const kind = typeof activity?.metadata?.kind === 'string' ? activity.metadata.kind : ''
  return UPSTREAM_EVENT_LABELS[kind] ?? (kind || 'Upstream event')
}

/** True for the one event kind that reports a recovery, not a problem. */
export function isRecoveryEvent(activity: ActivityRecord | undefined): boolean {
  return activity?.metadata?.kind === 'connection_restored'
}
