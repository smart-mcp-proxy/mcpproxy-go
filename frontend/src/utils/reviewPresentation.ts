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
  if (pending > 0) {
    return {
      state: 'review',
      title: `Review ${name}`,
      subtitle: `${pending} ${plural(pending, 'tool needs', 'tools need')} review. Agents cannot call ${plural(pending, 'it', 'them')} until approved.`,
    }
  }
  if (tools.length === 0) {
    // Approved without seeing tools: still an approved server, just with nothing captured yet.
    return { state: 'approved', title: `${name} is approved`, subtitle: 'No tool definitions have been captured yet. New or changed tools come back here for review.' }
  }
  const blocked = tools.filter(t => t.disabled).length
  const summary = `All ${tools.length} ${plural(tools.length, 'tool', 'tools')} approved${blocked > 0 ? ` (${blocked} blocked)` : ''}.`
  return { state: 'approved', title: `${name} is approved`, subtitle: `${summary} New or changed tools come back here for review.` }
}

export type ToolControl = 'allow-toggle' | 'approve-reject' | 'approved' | 'blocked'

/** Which control a tool row gets: the quarantine checkbox, Approve/Reject, or a plain state. */
export function toolState(tool: ReviewTool, quarantined: boolean): ToolControl {
  if (quarantined) return 'allow-toggle'
  if (tool.approval_status === 'approved') return tool.disabled ? 'blocked' : 'approved'
  return 'approve-reject'
}

// ---------------------------------------------------------------------------
// Default selection (Spec 109 fix-review-defaults, D41). The core decides which
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

/** The explicit approve-everything action. */
export function approveAllLabel(total: number): string {
  return `Approve all (${total} ${plural(total, 'tool', 'tools')})`
}
