import type { ActivityRecord, MCPSession } from '@/types/api'

/**
 * Spec 082 — which session an activity row belongs to.
 *
 * The row we want to group by is the WORK session: one client, one project,
 * across reconnects. But not every row carries one. A record written before the
 * connection was attributed — or by an older build, or by a path that never
 * marked the session as worked — has only its TRANSPORT session id.
 *
 * Falling back to the transport id per-row (`work_session_id || session_id`) is
 * what produced the original bug: the orphaned rows of a connection landed in a
 * bucket keyed by the transport id while the rest of that same connection landed
 * in its work-session bucket, and one client showed up in the picker twice.
 *
 * So resolve the fallback through an index instead: a transport session belongs
 * to whatever work session ANY of its rows (or the sessions API) names. Only a
 * transport session with no known work session anywhere keys on itself.
 */
export type WorkSessionIndex = Map<string, string>

export function buildWorkSessionIndex(
  activities: readonly ActivityRecord[],
  sessions: readonly MCPSession[] = [],
): WorkSessionIndex {
  const index: WorkSessionIndex = new Map()

  // The sessions API is the more authoritative of the two: it is the connection's
  // own record of the work session it was attributed to.
  for (const s of sessions) {
    if (s.id && s.work_session_id) index.set(s.id, s.work_session_id)
  }

  // Sibling rows fill the gaps — activity outlives sessions (90 days vs the 100
  // most recent), so for older rows this is the only surviving evidence.
  for (const a of activities) {
    if (a.session_id && a.work_session_id && !index.has(a.session_id)) {
      index.set(a.session_id, a.work_session_id)
    }
  }

  return index
}

export function groupKeyOf(a: ActivityRecord, index: WorkSessionIndex): string {
  if (a.work_session_id) return a.work_session_id
  if (a.session_id) return index.get(a.session_id) ?? a.session_id
  return ''
}
