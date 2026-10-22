import type { ReviewScan, ReviewTool, ServerReviewResponse } from '@/types'

// Pure presentation rules for the review screen. The strings are the shared
// vocabulary of Spec 109 (fix-review-screen): the macOS app (ReviewPresentation
// in ReviewQueueView.swift) and `mcpproxy review show` use the same sentences.

export type ScanBannerSeverity = 'error' | 'warning' | 'success' | 'info'
export type ScanBannerAction = 'rescan' | 'scan-now' | 'none'

export interface ScanBanner {
  severity: ScanBannerSeverity
  text: string
  action: ScanBannerAction
}

function plural(count: number, one: string, many: string): string {
  return count === 1 ? one : many
}

/**
 * The scan line of the review screen. Returns null when the payload carries no
 * scan at all. The risk score is shown only for a scan that covers every
 * captured definition as it is now (`coverage: current`); a missing coverage
 * (an older core) reads as `none`.
 */
export function scanBanner(scan: ReviewScan | undefined | null, definitionsCaptured: boolean): ScanBanner | null {
  if (!scan) return null
  const coverage = definitionsCaptured ? (scan.coverage || 'none') : 'not_captured'
  switch (coverage) {
    case 'current': {
      const severity: ScanBannerSeverity = scan.verdict === 'dangerous' ? 'error' : scan.verdict === 'clean' ? 'success' : 'warning'
      const tools = scan.tools_scanned ?? 0
      return {
        severity,
        text: `Baseline scan: ${scan.verdict} · risk ${scan.risk_score ?? 0}/100 · covers all ${tools} ${plural(tools, 'tool', 'tools')}`,
        action: 'none',
      }
    }
    case 'stale': {
      const names = scan.unscanned_tools ?? []
      const count = names.length
      const what = count === 1 ? '1 tool definition changed or was added' : `${count} tool definitions changed or were added`
      const list = count > 0 ? ` (${names.join(', ')})` : ''
      return { severity: 'warning', text: `Scan out of date: ${what} after the last scan${list}. Last result: ${scan.verdict}.`, action: 'rescan' }
    }
    case 'not_captured':
      return { severity: 'warning', text: 'Scan not checked against tool definitions: they have not been captured yet.', action: 'none' }
    case 'tools_not_scanned':
      return { severity: 'warning', text: 'The last scan did not analyse tool definitions (0 exported).', action: 'rescan' }
    case 'scanning':
      return { severity: 'info', text: 'Scan in progress…', action: 'none' }
    default:
      return { severity: 'warning', text: 'Not scanned yet.', action: 'scan-now' }
  }
}

export interface ReviewHeadline {
  state: 'review' | 'approved'
  title: string
  subtitle: string
}

const needsReview = (tool: ReviewTool) => tool.approval_status === 'pending' || tool.approval_status === 'changed'

/** Heading and subtitle: a server that is not quarantined and has nothing pending reads as approved. */
export function reviewHeadline(review: ServerReviewResponse): ReviewHeadline {
  const name = review.server.name
  if (review.server.quarantined) {
    return { state: 'review', title: `Review ${name}`, subtitle: 'Review tool definitions before changing what agents can call.' }
  }
  const tools = review.tools
  const pending = tools.filter(needsReview).length
  const approvedUnblocked = tools.filter(t => t.approval_status === 'approved' && !t.disabled).length
  const blocked = tools.filter(t => t.disabled).length
  const readiness = review.server.readiness === 'disabled' ? 'Server disabled.'
    : review.server.readiness === 'connecting' ? 'Upstream connecting; retry after discovery.'
      : review.server.readiness === 'connected' ? 'Upstream connected; tool discovery may still be in progress.'
        : 'Approval does not confirm upstream readiness.'
  if (pending > 0) {
    return {
      state: 'review',
      title: `Review ${name}`,
      subtitle: `${pending} ${plural(pending, 'tool needs', 'tools need')} review · ${approvedUnblocked} approved and unblocked · ${blocked} blocked. ${readiness}`,
    }
  }
  if (tools.length === 0) {
    // Approved without seeing tools: still an approved server, just with nothing captured yet.
    return { state: 'approved', title: `${name} is approved`, subtitle: `No tool definitions have been captured yet. ${readiness}` }
  }
  return { state: 'approved', title: `${name} is approved`, subtitle: `${approvedUnblocked} approved and unblocked · ${blocked} blocked of ${tools.length}. ${readiness}` }
}

export type ToolControl = 'allow-toggle' | 'approve-reject' | 'approved' | 'blocked'

/** Which control a tool row gets: the quarantine checkbox, Approve/Reject, or a plain state. */
export function toolState(tool: ReviewTool, quarantined: boolean): ToolControl {
  if (quarantined) return 'allow-toggle'
  if (tool.approval_status === 'approved') return tool.disabled ? 'blocked' : 'approved'
  return 'approve-reject'
}

// ---------------------------------------------------------------------------
// Default selection (Spec 109 fix-review-defaults, D43). The core decides which
// tools start checked (`default_allowed`); these helpers only read it. A payload
// from an older core has no field, which reads as false: every tool starts
// unchecked, so a mismatched core fails closed. The macOS app
// (ReviewPresentation in ReviewQueueView.swift) uses the same sentences.
// ---------------------------------------------------------------------------

export const REVIEW_SELECTION_HINT = 'Only read-only tools with a clean scan start checked. Unchecked tools stay blocked after approval until you enable them on the Tools tab.'

/** What the user explicitly chose for one tool, with the payload they saw. */
export interface SelectionChoice {
  allowed: boolean
  tool: ReviewTool
}

/** The names that start checked: the core's `default_allowed`, a missing field counting as false. */
export function initialSelection(tools: ReviewTool[]): string[] {
  return tools.filter(t => t.default_allowed === true).map(t => t.name)
}

function sameValue(a: unknown, b: unknown): boolean {
  if (a === b) return true
  if (a === null || b === null || typeof a !== 'object' || typeof b !== 'object') return false
  if (Array.isArray(a) !== Array.isArray(b)) return false
  const left = a as Record<string, unknown>
  const right = b as Record<string, unknown>
  const keys = new Set([...Object.keys(left), ...Object.keys(right)])
  for (const key of keys) {
    if (!sameValue(left[key], right[key])) return false
  }
  return true
}

/**
 * The selection after a reload. An explicit uncheck always survives; an
 * explicit check survives only while the tool's payload is the one the user
 * saw (a changed definition, verdict or tier falls back to the default).
 */
export function mergeSelection(tools: ReviewTool[], choices: Map<string, SelectionChoice>): string[] {
  return tools.filter(tool => {
    const choice = choices.get(tool.name)
    if (!choice) return tool.default_allowed === true
    if (!choice.allowed) return false
    return sameValue(choice.tool, tool) ? true : tool.default_allowed === true
  }).map(t => t.name)
}

/** Primary approve button: the exact count, or the blind-approval wording when nothing is captured. */
export function approveLabel(selected: number, total: number, definitionsCaptured: boolean): string {
  if (!definitionsCaptured || total === 0) return 'Approve without seeing tools'
  return `Approve server (${selected} of ${total} ${plural(total, 'tool', 'tools')})`
}

/** Tooltip of the approve-everything action: it does not re-enable tools blocked earlier. */
export const APPROVE_ALL_HINT = 'Allows every pending or changed tool. Tools you blocked earlier on a re-quarantined server stay blocked.'

/** The explicit approve-everything action. */
export function approveAllLabel(total: number): string {
  return `Approve all (${total} ${plural(total, 'tool', 'tools')})`
}

// ---------------------------------------------------------------------------
// Scale controls (UX-03). Filtering and paging only decide which rows are
// DRAWN. The selection and the block list are always computed from the full
// tool list, so a filter, a page change or a refresh can never approve a tool
// the reviewer did not check, nor drop one they did.
// ---------------------------------------------------------------------------

export const REVIEW_PAGE_SIZES = [25, 50, 100] as const

export interface ReviewFilters {
  query: string
  tier: string // 'all' or a tier name
  state: string // 'all' | 'pending' | 'changed' | 'approved'
  selection: string // 'all' | 'allowed' | 'blocked'
}

export const NO_REVIEW_FILTERS: ReviewFilters = { query: '', tier: 'all', state: 'all', selection: 'all' }

export function hasActiveFilters(f: ReviewFilters): boolean {
  return f.query.trim() !== '' || f.tier !== 'all' || f.state !== 'all' || f.selection !== 'all'
}

/** Review-local filters over the FULL tool list; `allowed` is the full selection. */
export function filterReviewTools(tools: ReviewTool[], f: ReviewFilters, allowed: ReadonlySet<string>): ReviewTool[] {
  const q = f.query.trim().toLowerCase()
  return tools.filter(t => {
    if (q && !t.name.toLowerCase().includes(q) && !(t.description ?? '').toLowerCase().includes(q)) return false
    if (f.tier !== 'all' && t.tier !== f.tier) return false
    if (f.state !== 'all' && t.approval_status !== f.state) return false
    if (f.selection === 'allowed' && !allowed.has(t.name)) return false
    if (f.selection === 'blocked' && allowed.has(t.name)) return false
    return true
  })
}

/** Clamp a 1-based page into range for `total` rows. */
export function clampPage(page: number, total: number, pageSize: number): number {
  const pages = Math.max(1, Math.ceil(total / pageSize))
  return Math.min(Math.max(1, page), pages)
}

// ---------------------------------------------------------------------------
// Launch identity (UX-10). Only the backend-redacted review payload is shown.
// ---------------------------------------------------------------------------

const SAFE_WORD = /^[A-Za-z0-9_@%+=:,./-]+$/

/** POSIX sh single-quote an argument unless it is made only of safe characters. */
export function posixQuote(word: string): string {
  if (word === '') return "''"
  if (SAFE_WORD.test(word)) return word
  return `'${word.replace(/'/g, `'\\''`)}'`
}

/** The copyable launch line: command and arguments, POSIX-quoted. */
export function formatLaunchCommand(command: string, args: string[] = []): string {
  return [command, ...args].map(posixQuote).join(' ')
}

// Spec 117 PR-2 (#1566): binding an approval to the reviewed definitions, the same rule as the
// macOS tray (ReviewPresentation.expectedHashes) and the CLI. The core approves only these
// fingerprints and answers 409 "out of date" when one changed or a tool appeared since.
// `tools` are the tools the approval covers; `review` (default `tools`) is the whole review on
// screen. Undefined (an unbound request) only when the review has tools and none carries a
// fingerprint, i.e. the core predates it; an empty review binds to the empty snapshot {}.
export function expectedHashes(tools: ReviewTool[], review: ReviewTool[] = tools): Record<string, string> | undefined {
  if (review.length > 0 && !review.some(t => !!t.current_hash)) return undefined
  // Null prototype: tool names are upstream-controlled, and a tool named "__proto__" written into
  // a plain {} hits the inherited setter and silently drops out of the JSON (Sol review r1).
  const out: Record<string, string> = Object.create(null)
  for (const tool of tools) if (tool.current_hash) out[tool.name] = tool.current_hash
  return out
}

/** A refusal because the review is out of date (Spec 117 FR-008): reload, never the force dialog. */
export function isStaleReviewError(message: string | undefined | null): boolean {
  return !!message && /out of date/i.test(message)
}

// ---------------------------------------------------------------------------
// Held tool changes (maintainer decision 2026-10-11, "Hold all changes, 1-click diff"). Every changed
// contract is held; the friction is reduced by a readable diff and one approval for the server's update.
// ---------------------------------------------------------------------------

export const HELD_CHANGE_DOCS = 'https://docs.mcpproxy.app/features/security-quarantine'
export const HELD_CHANGE_HINT = 'Changed tools stay blocked until you approve the update. To let a server you trust update its tools without review, set auto_approve_tool_changes: true on it.'

export type ChangeKind = 'added' | 'removed' | 'changed'
export interface ChangeLine { kind: ChangeKind; text: string; before?: string; after?: string }
export interface ChangeSection { section: 'description' | 'input_schema' | 'output_schema' | 'annotations'; title: string; lines: ChangeLine[] }

const HINTS = ['readOnlyHint', 'destructiveHint', 'idempotentHint', 'openWorldHint'] as const
const isObj = (v: unknown): v is Record<string, unknown> => !!v && typeof v === 'object' && !Array.isArray(v)
const same = (a: unknown, b: unknown) => JSON.stringify(a ?? null) === JSON.stringify(b ?? null)
const typeOf = (v: unknown) => isObj(v) && v.type !== undefined ? (Array.isArray(v.type) ? v.type.join('|') : String(v.type)) : 'any'

function schemaLines(before: unknown, after: unknown): ChangeLine[] {
  if (same(before, after)) return []
  const lines: ChangeLine[] = []
  const bp = isObj(before) && isObj(before.properties) ? before.properties : {}
  const ap = isObj(after) && isObj(after.properties) ? after.properties : {}
  const has = (o: Record<string, unknown>, k: string) => Object.prototype.hasOwnProperty.call(o, k)
  for (const key of Object.keys(ap)) if (!has(bp, key)) lines.push({ kind: 'added', text: `Parameter ${key} (${typeOf(ap[key])})` })
  for (const key of Object.keys(bp)) if (!has(ap, key)) lines.push({ kind: 'removed', text: `Parameter ${key} (${typeOf(bp[key])})` })
  for (const key of Object.keys(ap)) {
    if (!has(bp, key) || same(bp[key], ap[key])) continue
    lines.push({ kind: 'changed', text: typeOf(bp[key]) !== typeOf(ap[key]) ? `Parameter ${key} type ${typeOf(bp[key])} → ${typeOf(ap[key])}` : `Parameter ${key} changed` })
  }
  const breq = new Set<string>(isObj(before) && Array.isArray(before.required) ? before.required.map(String) : [])
  const areq = new Set<string>(isObj(after) && Array.isArray(after.required) ? after.required.map(String) : [])
  for (const key of areq) if (!breq.has(key)) lines.push({ kind: 'changed', text: `Parameter ${key} is now required` })
  for (const key of breq) if (!areq.has(key)) lines.push({ kind: 'changed', text: `Parameter ${key} is no longer required` })
  // Differences outside properties/required (additionalProperties, enum, ...) are reported independently,
  // so a widened contract is never hidden behind a property line.
  const rb = isObj(before) ? before : {}; const ra = isObj(after) ? after : {}
  for (const key of new Set([...Object.keys(rb), ...Object.keys(ra)])) {
    if (key === 'properties' || key === 'required') continue
    // An absent key and an explicit null are different contracts (const: null, default: null).
    if (has(rb, key) === has(ra, key) && same(rb[key], ra[key])) continue
    lines.push({ kind: 'changed', text: `Schema setting ${key} changed` })
  }
  // Nothing recognised at all (e.g. a non-object schema) must not vanish.
  if (lines.length === 0) lines.push({ kind: 'changed', text: 'Schema changed' })
  return lines
}

/** What changed between the approved definition and the current one, section by section; [] when nothing differs or no approved definition is stored. */
export function summarizeToolChange(tool: ReviewTool): ChangeSection[] {
  const prev = tool.previous
  if (!prev) return []
  const out: ChangeSection[] = []
  if (prev.description !== tool.description) out.push({ section: 'description', title: 'Description', lines: [{ kind: 'changed', text: 'Description changed', before: prev.description, after: tool.description }] })
  const input = schemaLines(prev.input_schema, tool.input_schema)
  if (input.length) out.push({ section: 'input_schema', title: 'Input schema', lines: input })
  const output = schemaLines(prev.output_schema, tool.output_schema)
  if (output.length) out.push({ section: 'output_schema', title: 'Output schema', lines: output })
  const pa = prev.annotations ?? {}; const ca = tool.annotations ?? {}
  const show = (v: unknown) => v === undefined || v === null ? 'not set' : String(v)
  const hints: ChangeLine[] = []
  for (const hint of HINTS) if (!same(pa[hint], ca[hint])) hints.push({ kind: 'changed', text: `${hint}: ${show(pa[hint])} → ${show(ca[hint])}` })
  if (hints.length) out.push({ section: 'annotations', title: 'Safety hints', lines: hints })
  return out
}

/** The tools held as changed since their approval, in review order. */
export function changedToolNames(tools: ReviewTool[]): string[] {
  return tools.filter(t => t.approval_status === 'changed').map(t => t.name)
}

export function approveUpdateLabel(count: number): string {
  return `Approve update (${count} changed ${count === 1 ? 'tool' : 'tools'})`
}
