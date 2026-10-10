import type { ReviewQueueRow } from '@/types'

// UX-04: the review queue lists every server that is owed a review, including
// disabled ones. Only enabled servers hold up a live agent, so those are the
// "active blockers" (the same population the Home attention list counts).
// A core that predates the `enabled` field omits it; that reads as enabled.
export function isActiveBlocker(row: ReviewQueueRow): boolean {
  return row.enabled !== false
}

export interface ReviewQueueSummary { total: number; active: number; deferred: number }

export function summarizeReviewQueue(rows: ReviewQueueRow[] | undefined): ReviewQueueSummary {
  const list = rows ?? []
  const active = list.filter(isActiveBlocker).length
  return { total: list.length, active, deferred: list.length - active }
}

export function reviewQueueScopeText(s: ReviewQueueSummary): string {
  if (s.total === 0) return 'Nothing is waiting for review.'
  const noun = s.total === 1 ? 'review' : 'reviews'
  const blockers = `${s.active} active ${s.active === 1 ? 'blocker' : 'blockers'}`
  if (s.deferred === 0) return `${s.total} ${noun}: ${blockers}.`
  return `${s.total} ${noun}: ${blockers}, ${s.deferred} on disabled ${s.deferred === 1 ? 'server' : 'servers'}.`
}

export type ReviewSort = 'name' | 'impact' | 'age'

export function sortReviewRows(rows: ReviewQueueRow[], sort: ReviewSort): ReviewQueueRow[] {
  const out = rows.slice()
  const byName = (a: ReviewQueueRow, b: ReviewQueueRow) => a.server.localeCompare(b.server)
  if (sort === 'impact') {
    const impact = (r: ReviewQueueRow) => (r.pending ?? 0) + (r.changed ?? 0) + (r.kind === 'server_review' ? (r.tools_captured ?? 0) : 0)
    // Active blockers first, then the most tools waiting.
    out.sort((a, b) => Number(isActiveBlocker(b)) - Number(isActiveBlocker(a)) || impact(b) - impact(a) || byName(a, b))
  } else if (sort === 'age') {
    // Oldest waiting first; rows with no timestamp last.
    const t = (r: ReviewQueueRow) => (r.since ? Date.parse(r.since) : Number.POSITIVE_INFINITY)
    out.sort((a, b) => (t(a) === t(b) ? byName(a, b) : t(a) - t(b)))
  } else {
    out.sort(byName)
  }
  return out
}
