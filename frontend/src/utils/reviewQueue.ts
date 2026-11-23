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

// Spec 125: held skills. The core sends skills_pending / skills_changed and
// skill_review rows to administrators only.
export const REVIEW_KIND_SKILL_REVIEW = 'skill_review'

export function heldSkillCount(row: ReviewQueueRow): number {
  return (row.skills_pending ?? 0) + (row.skills_changed ?? 0)
}

function heldSkillsText(row: ReviewQueueRow): string {
  const total = heldSkillCount(row)
  if (total === 0) return ''
  const changed = row.skills_changed ?? 0
  const head = `${total} ${total === 1 ? 'skill' : 'skills'} awaiting review`
  return changed > 0 ? `${head} · ${changed} changed` : head
}

// The one-line summary under a queue row's server name.
export function reviewQueueRowSubtitle(row: ReviewQueueRow): string {
  if (row.kind === 'server_review') return 'Server awaiting review'
  const skills = heldSkillsText(row)
  if (row.kind === REVIEW_KIND_SKILL_REVIEW) return skills
  const tools = `${row.pending ?? 0} new · ${row.changed ?? 0} changed`
  return skills ? `${tools} · ${skills}` : tools
}

// A queue action opens the Skills tab with the same scope as the queue.
export function serverSkillsReviewLink(server: string) {
  return { path: `/servers/${encodeURIComponent(server)}`, query: { tab: 'skills', state: 'needs-review' } }
}

// The row's Review link: a skill-only row opens the Skills tab, every other
// row the tool review (keeping a ?change= filter).
export function reviewQueueRowLink(row: ReviewQueueRow, change?: string) {
  if (row.kind === REVIEW_KIND_SKILL_REVIEW) return serverSkillsReviewLink(row.server)
  return { path: `/review/${encodeURIComponent(row.server)}`, query: { ...(change ? { change } : {}) } }
}

export type ReviewSort = 'name' | 'impact' | 'age'

export function sortReviewRows(rows: ReviewQueueRow[], sort: ReviewSort): ReviewQueueRow[] {
  const out = rows.slice()
  const byName = (a: ReviewQueueRow, b: ReviewQueueRow) => a.server.localeCompare(b.server)
  if (sort === 'impact') {
    const impact = (r: ReviewQueueRow) => (r.pending ?? 0) + (r.changed ?? 0) + heldSkillCount(r) + (r.kind === 'server_review' ? (r.tools_captured ?? 0) : 0)
    // Active blockers first, then the most tools and skills waiting.
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
