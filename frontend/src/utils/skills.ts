// Spec 125 (skills over MCP, PR-4a): pure helpers for the Skills tab.
import type { SkillEntry, SkillFileInfo, SkillContentResponse, SkillsListResponse } from '@/types/api'

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

export interface LineOp { op: 'same' | 'add' | 'del'; text: string }
export interface LineDiffResult { tooLarge: boolean; ops: LineOp[] }

/** Largest middle section (lines a x lines b) the LCS table may have. */
export const LINE_DIFF_MAX_CELLS = 4_000_000

/**
 * Line diff of two texts. Common head and tail lines are peeled off first; the
 * rest uses an LCS table that is never built past maxCells, in which case the
 * result is tooLarge with no ops.
 */
export function lineDiff(a: string, b: string, maxCells = LINE_DIFF_MAX_CELLS): LineDiffResult {
  const x = a.split('\n'), y = b.split('\n')
  let head = 0
  while (head < x.length && head < y.length && x[head] === y[head]) head++
  let tail = 0
  while (tail < x.length - head && tail < y.length - head && x[x.length - 1 - tail] === y[y.length - 1 - tail]) tail++
  const mx = x.slice(head, x.length - tail), my = y.slice(head, y.length - tail)
  if ((mx.length + 1) * (my.length + 1) > maxCells) return { tooLarge: true, ops: [] }

  const w = my.length + 1
  const t = new Uint32Array((mx.length + 1) * w)
  for (let i = mx.length - 1; i >= 0; i--) {
    for (let j = my.length - 1; j >= 0; j--) {
      t[i * w + j] = mx[i] === my[j] ? t[(i + 1) * w + j + 1] + 1 : Math.max(t[(i + 1) * w + j], t[i * w + j + 1])
    }
  }
  const ops: LineOp[] = x.slice(0, head).map(text => ({ op: 'same', text }))
  let i = 0, j = 0
  while (i < mx.length && j < my.length) {
    if (mx[i] === my[j]) { ops.push({ op: 'same', text: mx[i] }); i++; j++ }
    else if (t[(i + 1) * w + j] >= t[i * w + j + 1]) ops.push({ op: 'del', text: mx[i++] })
    else ops.push({ op: 'add', text: my[j++] })
  }
  while (i < mx.length) ops.push({ op: 'del', text: mx[i++] })
  while (j < my.length) ops.push({ op: 'add', text: my[j++] })
  for (const text of x.slice(x.length - tail)) ops.push({ op: 'same', text })
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

/** The tab is offered when the feature is on and the server supports skills or has records. */
export function skillsTabVisible(listing: SkillsListResponse | undefined | null): boolean {
  return !!listing && listing.enabled && (listing.supported || (listing.skills?.length ?? 0) > 0)
}

/** Operator-facing words for an error code of the skills routes. */
export function skillErrorNotice(code: string | undefined, message: string | undefined): string {
  switch (code) {
    case 'review_out_of_date':
      return 'This skill changed since you opened it, so nothing was approved. The list was reloaded; review it again.'
    case 'server_replaced':
      return 'This server was removed and added again since you opened it, so nothing was changed. The list was reloaded.'
    case 'refresh_in_progress':
      return 'A refresh is already running for this server. The list updates when it finishes.'
    default:
      return message || 'The request failed'
  }
}
