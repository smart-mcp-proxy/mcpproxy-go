// Spec 125 (skills over MCP, PR-4a): pure helpers for the Skills tab.
import type { SkillEntry, SkillFileInfo, SkillContentResponse, SkillsListResponse } from '@/types/api'

export function skillsChangeIncludesServer(event: Event, serverName: string): boolean {
  const payload = (event as CustomEvent).detail?.payload
  return payload?.server === serverName || (Array.isArray(payload?.servers) && payload.servers.includes(serverName))
}

export type SkillTone = 'error' | 'warning' | 'info' | 'success' | 'neutral'

/** The one state chip of a skill: blocked > unusable > changed > pending > approved. */
export function skillStateChip(entry: Pick<SkillEntry, 'status' | 'blocked'>): { label: string; tone: SkillTone } {
  if (entry.blocked) return { label: 'Blocked', tone: 'error' }
  switch (entry.status) {
    case 'unusable': return { label: 'Unusable', tone: 'neutral' }
    case 'changed': return { label: 'Changed', tone: 'warning' }
    case 'pending': return { label: 'Awaiting review', tone: 'info' }
    case 'approved': return { label: 'Approved', tone: 'success' }
    default: return { label: entry.status, tone: 'neutral' }
  }
}

import type { StatusTone } from '@/utils/healthTone'

/** Spec 134 FR-037: the status tone of each of the five state labels. Colour only; the words stay `skillStateChip`'s. */
export function skillStateTone(entry: Pick<SkillEntry, 'status' | 'blocked'>): StatusTone {
  if (entry.blocked) return 'error'
  switch (entry.status) {
    case 'approved': return 'ok'
    case 'pending': case 'changed': return 'review'
    case 'unusable': return 'off'
    default: return 'neutral'
  }
}

/** The one action of a skill row (FR-036). It opens the skill card, where the guarded decision is made. */
export function skillRowAction(entry: Pick<SkillEntry, 'status' | 'blocked'>): 'Review' | 'Review changes' | 'View' | 'Unblock' {
  if (entry.blocked) return 'Unblock'
  if (entry.status === 'pending') return 'Review'
  if (entry.status === 'changed') return 'Review changes'
  return 'View'
}

export type FileDiffState = 'added' | 'removed' | 'changed' | 'same'
export interface FileDiffRow {
  path: string
  state: FileDiffState
  before?: SkillFileInfo
  after?: SkillFileInfo
}

/** File-set diff of the approved copy against the current one, by path and digest. */
export function skillFileDiff(approved: SkillFileInfo[] | undefined, current: SkillFileInfo[]): FileDiffRow[] {
  const before = new Map((approved ?? []).map(f => [f.path, f]))
  const after = new Map(current.map(f => [f.path, f]))
  const rows: FileDiffRow[] = []
  for (const [path, a] of after) {
    const b = before.get(path)
    if (!b) rows.push({ path, state: 'added', after: a })
    else rows.push({ path, state: b.digest === a.digest ? 'same' : 'changed', before: b, after: a })
  }
  for (const [path, b] of before) if (!after.has(path)) rows.push({ path, state: 'removed', before: b })
  return rows.sort((x, y) => (x.path < y.path ? -1 : x.path > y.path ? 1 : 0))
}

/** `skip` stands for a run of unchanged lines that is not shown; its text says how many. */
export interface LineOp { op: 'same' | 'add' | 'del' | 'skip'; text: string }
export interface LineDiffResult { tooLarge: boolean; ops: LineOp[] }

/** Largest middle section (lines a x lines b) the LCS table may have. */
export const LINE_DIFF_MAX_CELLS = 4_000_000
/** Unchanged lines kept on each side of a change; longer unchanged runs collapse to one `skip`. */
export const LINE_DIFF_CONTEXT = 3
/** Most ops a diff may render (after collapsing); past it the result is tooLarge. */
export const LINE_DIFF_MAX_OPS = 5_000

/**
 * Line diff of two texts. Common head and tail lines are peeled off first; the
 * rest uses an LCS table that is never built past maxCells. The output is
 * bounded too: every unchanged run that is more than LINE_DIFF_CONTEXT lines longer than
 * the context it keeps (LINE_DIFF_CONTEXT on each side of a change) collapses to a "N unchanged lines" skip, and
 * a diff that still has more than maxOps ops is tooLarge with no ops. The
 * peeled head and tail are never expanded into one op per line.
 */
export function lineDiff(a: string, b: string, maxCells = LINE_DIFF_MAX_CELLS, maxOps = LINE_DIFF_MAX_OPS): LineDiffResult {
  const x = a.split('\n'), y = b.split('\n')
  let head = 0
  while (head < x.length && head < y.length && x[head] === y[head]) head++
  let tail = 0
  while (tail < x.length - head && tail < y.length - head && x[x.length - 1 - tail] === y[y.length - 1 - tail]) tail++
  const mx = x.slice(head, x.length - tail), my = y.slice(head, y.length - tail)
  if ((mx.length + 1) * (my.length + 1) > maxCells) return { tooLarge: true, ops: [] }
  // Every changed line is one op whatever the collapsing does.
  if (mx.length + my.length > maxOps) return { tooLarge: true, ops: [] }

  const w = my.length + 1
  const t = new Uint32Array((mx.length + 1) * w)
  for (let i = mx.length - 1; i >= 0; i--) {
    for (let j = my.length - 1; j >= 0; j--) {
      t[i * w + j] = mx[i] === my[j] ? t[(i + 1) * w + j + 1] + 1 : Math.max(t[(i + 1) * w + j], t[i * w + j + 1])
    }
  }
  const ops: LineOp[] = []
  // An unchanged run [from, to) of x: shown whole when short, else its edges
  // next to a change and one skip for the rest.
  const unchanged = (from: number, to: number, atStart: boolean, atEnd: boolean) => {
    const n = to - from
    const keepBefore = atStart ? 0 : LINE_DIFF_CONTEXT
    const keepAfter = atEnd ? 0 : LINE_DIFF_CONTEXT
    // A skip stands for more than LINE_DIFF_CONTEXT lines; a shorter run is shown whole.
    if (n <= keepBefore + keepAfter + LINE_DIFF_CONTEXT) { for (let k = from; k < to; k++) ops.push({ op: 'same', text: x[k] }); return }
    for (let k = from; k < from + keepBefore; k++) ops.push({ op: 'same', text: x[k] })
    ops.push({ op: 'skip', text: `${n - keepBefore - keepAfter} unchanged lines` })
    for (let k = to - keepAfter; k < to; k++) ops.push({ op: 'same', text: x[k] })
  }
  const changed = mx.length > 0 || my.length > 0
  // The middle, with its unchanged runs as x indexes for collapsing.
  let i = 0, j = 0
  let runStart = -1
  const flush = (end: number) => { if (runStart >= 0) { unchanged(runStart, end, false, false); runStart = -1 } }
  unchanged(0, head, true, !changed)
  while (i < mx.length && j < my.length) {
    if (mx[i] === my[j]) { if (runStart < 0) runStart = head + i; i++; j++ }
    else if (t[(i + 1) * w + j] >= t[i * w + j + 1]) { flush(head + i); ops.push({ op: 'del', text: mx[i++] }) }
    else { flush(head + i); ops.push({ op: 'add', text: my[j++] }) }
  }
  flush(head + i)
  while (i < mx.length) ops.push({ op: 'del', text: mx[i++] })
  while (j < my.length) ops.push({ op: 'add', text: my[j++] })
  if (changed) unchanged(x.length - tail, x.length, false, true)
  if (ops.length > maxOps) return { tooLarge: true, ops: [] }
  return { tooLarge: false, ops }
}

/**
 * The expected_hashes body of an approval: path -> the current_hash the reviewer
 * saw. A null-prototype object keeps a path called "__proto__" an own key.
 */
export function skillExpectedHashes(entries: Array<Pick<SkillEntry, 'path' | 'current_hash'>>): Record<string, string> {
  const out = Object.create(null) as Record<string, string>
  for (const e of entries) out[e.path] = e.current_hash
  return out
}

type PartResult = { success: boolean; data?: SkillContentResponse; error?: string; errorCode?: string }
export type WholeFileResult =
  | { success: true; text: string; capped: boolean }
  | { success: false; error: string; errorCode?: string }

/**
 * The client-side code of a part that does not belong to the version on screen:
 * its digest is not the one the listing names for that file and copy, or it
 * answers another offset. The text read so far is dropped, never joined.
 */
export const SKILL_CONTENT_CHANGED = 'content_changed'
export const SKILL_CONTENT_CHANGED_MESSAGE = 'This file changed; reload to review the new version.'

/** True when a part is the expected version (digest) at the expected offset. */
export function skillPartMatches(part: Pick<SkillContentResponse, 'digest' | 'offset'>, expectedDigest: string, expectedOffset: number): boolean {
  return !!expectedDigest && part.digest === expectedDigest && part.offset === expectedOffset
}

/**
 * Reads every part of a file by next_offset, stopping after maxParts. Every
 * part must carry expectedDigest (the digest the listing on screen names for
 * this file and copy) and answer the offset asked for; otherwise the result is
 * a SKILL_CONTENT_CHANGED failure with no text, so parts of two versions are
 * never joined.
 */
export async function readWholeSkillFile(fetchPart: (offset: number) => Promise<PartResult>, expectedDigest: string, maxParts = 200): Promise<WholeFileResult> {
  let text = ''
  let offset = 0
  for (let n = 0; n < maxParts; n++) {
    const res = await fetchPart(offset)
    if (!res.success || !res.data) return { success: false, error: res.error || 'Could not read the file', errorCode: res.errorCode }
    if (!skillPartMatches(res.data, expectedDigest, offset)) {
      return { success: false, error: SKILL_CONTENT_CHANGED_MESSAGE, errorCode: SKILL_CONTENT_CHANGED }
    }
    text += res.data.text
    const next = res.data.next_offset
    if (next === undefined || next === null) return { success: true, text, capped: false }
    if (next <= offset) return { success: false, error: 'The server returned a part that does not advance' }
    offset = next
  }
  return { success: true, text, capped: true }
}

/** The digest the listing names for one file of one copy, or undefined. */
export function skillFileDigest(entry: Pick<SkillEntry, 'files' | 'approved_files'>, file: string, copy: 'current' | 'approved'): string | undefined {
  const list = copy === 'approved' ? entry.approved_files : entry.files
  return list?.find(f => f.path === file)?.digest
}

/**
 * The tab is offered when the feature is on and the server supports skills or
 * has records. Only the administrator listing carries `supported`: a listing
 * without it (the scoped/agent-token shape, SkillsAgentListResponse, which has
 * no scan block either) is not offered.
 */
export function skillsTabVisible(listing: SkillsListResponse | undefined | null): boolean {
  if (!listing || typeof listing.supported !== 'boolean') return false
  return listing.enabled === true && (listing.supported || (listing.skills?.length ?? 0) > 0)
}

/**
 * The error code of a failed skills request. The listing and refresh routes send
 * error_code; approve/block/unblock answer 409 server_replaced through the shared
 * approval error writer, whose body has only `error` ("server_replaced: ..."),
 * like the tool approval routes. The code is then the message's prefix (the
 * only codes that writer puts first are read; any other message has no code).
 */
const MESSAGE_CODE = /^(server_replaced|review_out_of_date)\b(?=:|$)/
export function skillErrorCode(code: string | undefined, message: string | undefined): string | undefined {
  if (code) return code
  return message ? MESSAGE_CODE.exec(message)?.[1] : undefined
}

/** The request a notice answers: the refresh, or one of the writes. */
export type SkillAction = 'refresh' | 'approve' | 'block' | 'unblock'
const NOTHING_DONE: Record<SkillAction, string> = {
  refresh: 'nothing was refreshed', approve: 'nothing was approved', block: 'nothing was blocked', unblock: 'nothing was unblocked',
}

/** Operator-facing words for a failed skills request, worded for the action it refused. */
export function skillErrorNotice(code: string | undefined, message: string | undefined, action: SkillAction = 'refresh'): string {
  switch (skillErrorCode(code, message)) {
    case 'review_out_of_date':
      return 'This skill changed since you opened it, so nothing was approved. The list was reloaded; review it again.'
    case 'server_replaced':
      return `This server was removed and added again since you opened it, so ${NOTHING_DONE[action]}. The list was reloaded.`
    // The refresh codes of POST /skills/refresh (a concurrent refresh is joined, never refused).
    case 'refresh_superseded':
      return 'The server reconnected during the refresh, so its skills are read again after the reconnect. The list was reloaded.'
    case 'skills_not_supported':
      return 'This server does not announce the skills extension, so there is nothing to refresh. The list was reloaded.'
    case 'skills_disabled':
      return `Skills over MCP was turned off (enable_skills), so ${NOTHING_DONE[action]}. The list was reloaded.`
    default:
      return message || 'The request failed'
  }
}

/**
 * The notice after a successful approve, worded from the listing reloaded
 * after it (round-4 finding 3): an approved skill is not necessarily offered to
 * agents. `available` decides; the reason is the first one the listing shows.
 * A skill the listing does not explain (the server is disabled or quarantined,
 * which the listing does not carry) is "not offered yet" without a reason, and
 * no listing (the reload failed) or no such skill gives a neutral "Approved.".
 */
export function skillApprovedNotice(listing: SkillsListResponse | null | undefined, path: string): string {
  const s = listing?.skills?.find(k => k.path === path)
  if (!listing || !s) return 'Approved.'
  if (s.available) return 'Approved. Agents can use this skill now.'
  let reason = ''
  if (!listing.enabled) reason = 'Skills over MCP is turned off (enable_skills).'
  else if (listing.expose_skills === false) reason = 'this server is excluded from agents (expose_skills: false).'
  else if (s.blocked) reason = 'the skill is blocked.'
  else if (!s.listed) reason = 'the server no longer lists this skill.'
  else if (s.status !== 'approved') reason = `its state is now ${skillStateChip(s).label}.`
  return reason ? `Approved. Not offered to agents yet: ${reason}` : 'Approved. Not offered to agents yet.'
}
